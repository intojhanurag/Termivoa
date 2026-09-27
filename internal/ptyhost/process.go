package ptyhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"
)

const (
	DefaultColumns = 80
	DefaultRows    = 24
	MinColumns     = 20
	MaxColumns     = 400
	MinRows        = 5
	MaxRows        = 200

	terminationGracePeriod = 500 * time.Millisecond
)

var (
	ErrEmptyExecutable   = errors.New("ptyhost: executable is required")
	ErrInvalidDimensions = errors.New("ptyhost: dimensions are outside the supported range")
)

// Spec describes a command to start in a new pseudo-terminal. Args excludes
// the executable name. Env entries are applied after the inherited host
// environment, so the caller can set terminal-specific variables without
// accidentally dropping required OS variables such as SystemRoot on Windows.
type Spec struct {
	Executable string
	Args       []string
	Directory  string
	Env        []string
	Columns    int
	Rows       int
}

// Process is a command and its pseudo-terminal. There must be one reader of
// the PTY. Writes are serialized here so terminal responses and accepted user
// actions cannot interleave at the operating-system write boundary.
type Process struct {
	pty  xpty.Pty
	cmd  *exec.Cmd
	tree platformProcessTree

	done    chan struct{}
	waitErr error

	writeMu     sync.Mutex
	sizeMu      sync.Mutex
	terminateMu sync.Mutex
	closeOnce   sync.Once
	closeErr    error
}

// Start allocates a PTY and starts spec.Executable with spec.Args. The caller
// must keep ctx alive for the entire managed session. Canceling it terminates
// the managed process tree; viewer and request contexts must not be passed.
func Start(ctx context.Context, spec Spec) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("ptyhost: nil session context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ptyhost: session context: %w", err)
	}
	if spec.Executable == "" {
		return nil, ErrEmptyExecutable
	}
	if err := validateDimensions(spec.Columns, spec.Rows); err != nil {
		return nil, err
	}

	terminal, err := xpty.NewPty(spec.Columns, spec.Rows)
	if err != nil {
		return nil, fmt.Errorf("ptyhost: allocate PTY: %w", err)
	}

	cmd := exec.Command(spec.Executable, append([]string(nil), spec.Args...)...)
	cmd.Dir = spec.Directory
	cmd.Env = append(os.Environ(), spec.Env...)
	configureCommand(cmd)

	if err := terminal.Start(cmd); err != nil {
		_ = terminal.Close()
		return nil, fmt.Errorf("ptyhost: start command: %w", err)
	}

	tree, err := attachProcessTree(cmd)
	if err != nil {
		terminated, terminateErr := cleanupUnattachedProcess(cmd)
		if !terminated {
			cleanupErr := errors.Join(terminateErr, terminal.Close(), cmd.Process.Release())
			return nil, fmt.Errorf("ptyhost: isolate process tree; child termination unconfirmed: %w", errors.Join(err, cleanupErr))
		}
		cleanupErr := errors.Join(terminateErr, xpty.WaitProcess(context.Background(), cmd), terminal.Close())
		return nil, fmt.Errorf("ptyhost: isolate process tree: %w", errors.Join(err, cleanupErr))
	}

	p := &Process{
		pty:  terminal,
		cmd:  cmd,
		tree: tree,
		done: make(chan struct{}),
	}
	go p.reap()
	go p.watchSession(ctx)
	return p, nil
}

func validateDimensions(columns, rows int) error {
	if columns < MinColumns || columns > MaxColumns || rows < MinRows || rows > MaxRows {
		return fmt.Errorf("%w: columns=%d (want %d..%d), rows=%d (want %d..%d)",
			ErrInvalidDimensions, columns, MinColumns, MaxColumns, rows, MinRows, MaxRows)
	}
	return nil
}

func (p *Process) reap() {
	// xpty.WaitProcess intentionally ignores its context on Unix and delegates
	// to exec.Cmd.Wait. Session cancellation is therefore handled separately
	// by watchSession, which terminates the isolated process tree. On Windows,
	// using the helper is mandatory because exec.Cmd.Wait is not ConPTY-safe.
	p.waitErr = errors.Join(
		xpty.WaitProcess(context.Background(), p.cmd),
		closeProcessTree(p.tree),
	)
	close(p.done)
}

func (p *Process) watchSession(ctx context.Context) {
	select {
	case <-ctx.Done():
		terminationContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Terminate(terminationContext)
	case <-p.done:
	}
}

// PID returns the process identifier of the direct child.
func (p *Process) PID() int {
	return p.cmd.Process.Pid
}

// Read reads child output from the PTY. Exactly one goroutine should call Read.
func (p *Process) Read(data []byte) (int, error) {
	return p.pty.Read(data)
}

// Write serializes a complete write to the PTY. It keeps the writer lock while
// finishing short writes so bytes from separate terminal input events cannot
// interleave.
func (p *Process) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	total := 0
	for total < len(data) {
		written, err := p.pty.Write(data[total:])
		total += written
		if err != nil {
			return total, err
		}
		if written == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

// Resize changes the canonical PTY grid after validating the product limits.
func (p *Process) Resize(columns, rows int) error {
	if err := validateDimensions(columns, rows); err != nil {
		return err
	}
	p.sizeMu.Lock()
	defer p.sizeMu.Unlock()
	if err := p.pty.Resize(columns, rows); err != nil {
		return fmt.Errorf("ptyhost: resize PTY: %w", err)
	}
	return nil
}

// Size returns the current PTY dimensions.
func (p *Process) Size() (columns, rows int, err error) {
	p.sizeMu.Lock()
	defer p.sizeMu.Unlock()
	columns, rows, err = p.pty.Size()
	if err != nil {
		return 0, 0, fmt.Errorf("ptyhost: read PTY size: %w", err)
	}
	return columns, rows, nil
}

// Wait waits for the child to be reaped without changing its lifetime. A
// canceled wait context only stops this caller; it does not terminate the
// managed session.
func (p *Process) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ptyhost: nil wait context")
	}
	select {
	case <-p.done:
		return p.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Terminate ends the platform-managed process boundary. This is a Job Object
// on Windows. On Unix it is the PTY session the child leads: its process group
// and every other process in that session, such as an interactive shell's
// background jobs in their own process groups. Processes that called setsid
// have left the session and are never signaled. Terminate first sends a hangup
// and a termination request, then forcibly ends processes still in the boundary
// after a short grace period or when ctx expires. On Unix the grace period
// also covers session members that remain after the leader exits, whether the
// leader exited on request or on its own. Reaping continues in the background
// even if the caller's context expires.
func (p *Process) Terminate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ptyhost: nil termination context")
	}

	p.terminateMu.Lock()
	defer p.terminateMu.Unlock()

	select {
	case <-p.done:
		return ignoreProcessDone(cleanupExitedProcessTree(p.tree))
	default:
	}

	gracefulErr := ignoreProcessDone(terminateProcessTree(p.tree))
	timer := time.NewTimer(terminationGracePeriod)
	defer timer.Stop()

	select {
	case <-p.done:
		return errors.Join(gracefulErr, ignoreProcessDone(cleanupExitedProcessTree(p.tree)))
	case <-timer.C:
	case <-ctx.Done():
	}

	forceErr := ignoreProcessDone(forceTerminateProcessTree(p.tree))
	select {
	case <-p.done:
		return errors.Join(gracefulErr, forceErr)
	case <-ctx.Done():
		return errors.Join(ctx.Err(), gracefulErr, forceErr)
	}
}

func ignoreProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Close releases PTY handles. It does not terminate a live command; callers
// must explicitly terminate and wait before closing a live session.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.pty.Close()
	})
	return p.closeErr
}

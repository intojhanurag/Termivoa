package ptyhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
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
// the executable name. The child does not inherit the broker's whole
// environment: it receives hostEnvironment (the allowlisted host variables on
// Unix, the user's default environment on Windows), followed by Env. Callers therefore set terminal
// variables such as TERM in Env without dropping required OS variables such
// as SystemRoot on Windows.
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
	exited  chan struct{}
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
	cmd.Env = append(hostEnvironment(os.Environ()), spec.Env...)
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
		pty:    terminal,
		cmd:    cmd,
		tree:   tree,
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	go p.reap()
	go p.watchSession(ctx)
	return p, nil
}

// Host variables a managed session inherits. Everything else in the broker's
// environment, such as CLAUDE_CODE_*, TERM_PROGRAM, ITERM_SESSION_ID, VSCODE_*,
// TMUX, SHLVL, and PWD, describes the program or terminal that started the
// broker, not the new session. A macOS login shell rebuilds the rest from the
// user's profile. The Unix list is what launchd gives a user agent, without
// its XPC_* service markers, plus locale and time zone. Unix names are
// case-sensitive; LC_* is a prefix.
var unixInheritedNames = []string{
	"HOME", "USER", "LOGNAME", "SHELL", "PATH", "TMPDIR", "SSH_AUTH_SOCK",
	"__CF_USER_TEXT_ENCODING", "LANG", "LANGUAGE", "TZ",
}

// linuxInheritedNames are added on Linux. systemd gives the user manager the
// login's runtime directory and session bus, a desktop adds its display, and
// WSL adds its interop variables; without them systemctl --user, keyrings,
// xdg-open and running Windows programs from WSL fail inside a session. On
// macOS launchd provides these through other means.
var linuxInheritedNames = []string{
	"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "DISPLAY", "WAYLAND_DISPLAY",
	"WSL_DISTRO_NAME", "WSL_INTEROP", "WSLENV",
}

// Windows names are case-insensitive and stored upper case; PROCESSOR_* is a
// prefix. They are the per-machine and per-user variables Windows itself
// defines for a logon, which programs and shells need to locate the system,
// the user profile, and temporary storage. On Windows they fill gaps in, or
// replace when it is unavailable, the user's default environment block (see
// windowsEnvironment), which carries registry variables such as JAVA_HOME.
var windowsInheritedNames = []string{
	"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "PATHEXT",
	"TEMP", "TMP", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PUBLIC",
	"ALLUSERSPROFILE", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)",
	"PROGRAMW6432", "COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)",
	"COMMONPROGRAMW6432", "HOMEDRIVE", "HOMEPATH", "USERNAME", "USERDOMAIN",
	"COMPUTERNAME", "OS", "NUMBER_OF_PROCESSORS", "PSMODULEPATH",
}

// inheritedEnvironment returns the entries of environ that a managed session
// may inherit on goos, in their original order and spelling.
func inheritedEnvironment(environ []string, goos string) []string {
	names, prefix, windows := unixInheritedNames, "LC_", goos == "windows"
	if windows {
		names, prefix = windowsInheritedNames, "PROCESSOR_"
	}
	if goos == "linux" {
		names = slices.Concat(unixInheritedNames, linuxInheritedNames)
	}
	// Never nil: a nil exec.Cmd.Env (and conpty's Env) inherits everything.
	kept := []string{}
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			// Also drops Windows' hidden per-drive "=C:=C:\dir" entries.
			continue
		}
		if windows {
			name = strings.ToUpper(name)
		}
		if strings.HasPrefix(name, prefix) || slices.Contains(names, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// launcherVariablePrefixes name variables set by the program or terminal
// that started the broker. windowsEnvironment removes them even from the
// user's default environment block. Matching is case-insensitive.
var launcherVariablePrefixes = []string{
	"CLAUDECODE", "CLAUDE_CODE_", "TERM_PROGRAM", "TERM_SESSION_ID",
	"WT_SESSION", "WT_PROFILE_ID", "VSCODE_", "ITERM_", "TMUX",
}

// windowsEnvironment builds a Windows session environment. userDefault is the
// user's default environment block (CreateEnvironmentBlock without
// inheritance): the environment of a fresh logon, with system and user
// registry variables such as JAVA_HOME and the user Path. Launcher variables
// are removed from it. Allowlisted host variables it lacks are added. Without
// a block, the result is the allowlisted host environment. Never nil.
func windowsEnvironment(userDefault, host []string) []string {
	allowed := inheritedEnvironment(host, "windows")
	if len(userDefault) == 0 {
		return allowed
	}
	kept := []string{}
	seen := map[string]bool{}
	for _, entry := range userDefault {
		name, _, ok := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if !ok || name == "" || slices.ContainsFunc(launcherVariablePrefixes, func(prefix string) bool {
			return strings.HasPrefix(name, prefix)
		}) {
			continue
		}
		kept = append(kept, entry)
		seen[name] = true
	}
	for _, entry := range allowed {
		name, _, _ := strings.Cut(entry, "=")
		if !seen[strings.ToUpper(name)] {
			kept = append(kept, entry)
		}
	}
	return kept
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
	waitErr := xpty.WaitProcess(context.Background(), p.cmd)
	close(p.exited)
	p.waitErr = errors.Join(waitErr, closeProcessTree(p.tree))
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

// Exited reports whether the direct child has been reaped. Wait returns
// later: first the rest of the child's session is cleaned up, which on Unix
// can take terminationGracePeriod and longer on a busy computer.
func (p *Process) Exited() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
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

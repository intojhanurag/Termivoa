//go:build darwin

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
	"golang.org/x/sys/unix"
)

const outerGuardianMarker = "pterm-outer-tty-guardian"

type outerGuardianEvent struct {
	PID      int    `json:"pid"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}

// A shell normally remains the controlling-session leader when its attached
// program exits. Keep that owner alive so the parent can inspect the surviving
// slave's termios. Neither guardian control pipe carries terminal content.
func TestDesktopOuterTTYGuardian(t *testing.T) {
	marker := slices.Index(os.Args, outerGuardianMarker)
	if marker < 0 {
		t.Skip("helper subprocess only")
	}
	status := os.NewFile(3, "guardian-status")
	release := os.NewFile(4, "guardian-release")
	encoder := json.NewEncoder(status)
	command := exec.Command(os.Args[marker+1], os.Args[marker+2:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		_ = encoder.Encode(outerGuardianEvent{Error: err.Error()})
		os.Exit(3)
	}
	_ = encoder.Encode(outerGuardianEvent{PID: command.Process.Pid})
	err := command.Wait()
	result := outerGuardianEvent{ExitCode: command.ProcessState.ExitCode()}
	if err != nil {
		result.Error = err.Error()
	}
	_ = encoder.Encode(result)
	_, _ = io.CopyN(io.Discard, release, 1)
	os.Exit(0)
}

// The executable runs in a fresh outer PTY; the user's terminal is never put
// into raw mode. The managed child belongs to a separate disposable broker.
func TestPtermDesktopAttachNativeOuterTTY(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "pterm")
	build := exec.Command("go", "build", "-o", executable, "../../cmd/pterm")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.1")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build attach executable: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	runtimeDir, state := startLiveBroker(t, ctx)
	created := createLiveSession(t, ctx, runtimeDir)
	managed, err := state.Registry.Get(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := managed.Status(ctx)
	if err != nil || initial.PID <= 0 || initial.State != broker.SessionRunning {
		t.Fatalf("initial managed process: %#v, %v", initial, err)
	}
	boot := fmt.Sprintf("BOOT_QUERY_OK pid=%d", initial.PID)
	defer func() {
		if t.Failed() {
			// ctx has usually expired by now; the broker lives until cleanup.
			logContext, cancelLog := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelLog()
			frame, snapshotErr := managed.Snapshot(logContext)
			t.Logf("synthetic child state: %v\n%s", snapshotErr, trimScreen(liveScreenText(frame)))
		}
	}()
	waitLiveScreen(t, ctx, managed, boot)
	args := []string{"attach", "--dev", "--runtime-dir", runtimeDir}

	// An exact-fit observer gets the complete grid, never takes control, and
	// discards typing. A subsequent receipt proves ignored input never arrived.
	observer := startOuterAttach(t, ctx, "observer", executable, append(args, created.Session.ID), 80, 24)
	observer.waitText(t, ctx, boot, true)
	observer.assertRaw(t)
	observer.write(t, ctx, "IGNORED\r\x1d")
	observer.waitExit(t, ctx, false)
	observer.assertRestored(t, ctx)
	frame, err := managed.Snapshot(ctx)
	if err != nil || strings.Contains(liveScreenText(frame), "ACK") {
		t.Fatalf("observer sent child input: %v\n%s", err, liveScreenText(frame))
	}

	controlArgs := append(append([]string(nil), args...), "--control", created.Session.ID)
	controller := startOuterAttach(t, ctx, "controller", executable, controlArgs, 120, 40)
	// The old 80x24 frame can briefly display Control before its requested
	// resize snapshot starts. Require the footer on the final physical row:
	// the replacement grid has then been painted and acknowledged.
	controller.waitStatus(t, ctx, "Control |")
	waitOuterCanonicalSize(t, ctx, managed, 120, 39)
	controller.assertRaw(t)
	// Inject an outer terminal cursor response. It is not user input and must
	// not contaminate the child's next line or answer its own query twice.
	controller.write(t, ctx, "\x1b[7;9RCONTROL\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(1, "CONTROL"))
	assertLiveDisplay(t, ctx, managed, &liveDisplay{core: controller.display}, liveReceipt(1, "CONTROL"))
	controller.write(t, ctx, "\x1b[200~PASTE 界\x1b[201~\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(2, "\x1b[200~PASTE 界\x1b[201~"))
	controller.write(t, ctx, "QUERY\r")
	waitLiveScreen(t, ctx, managed, "TWO_VIEWER_QUERY_OK")
	controller.write(t, ctx, "\x16\x1d\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(4, "\x1d"))
	controller.write(t, ctx, "\x03\x04\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(5, "\x03\x04"))

	controller.resize(t, ctx, 60, 18)
	waitOuterCanonicalSize(t, ctx, managed, 60, 17)
	controller.waitStatus(t, ctx, "Control |")
	assertLiveDisplay(t, ctx, managed, &liveDisplay{core: controller.display}, "ACK05:")
	controller.write(t, ctx, "SHRUNK\r")
	waitOuterWrappedReceipt(t, ctx, managed, liveReceipt(6, "SHRUNK"))
	controller.resize(t, ctx, 120, 40)
	waitOuterCanonicalSize(t, ctx, managed, 120, 39)
	controller.waitStatus(t, ctx, "Control |")
	controller.write(t, ctx, "GROWN\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(7, "GROWN"))
	assertLiveDisplay(t, ctx, managed, &liveDisplay{core: controller.display}, liveReceipt(7, "GROWN"))

	// A second command explicitly takes control. The first becomes read-only;
	// detaching it must preserve both the child and the new controller.
	takeover := startOuterAttach(t, ctx, "takeover", executable, controlArgs, 120, 40)
	takeover.waitStatus(t, ctx, "Control |")
	controller.waitStatus(t, ctx, "Read-only |")
	controller.write(t, ctx, "STALE\r\x1d")
	controller.waitExit(t, ctx, false)
	controller.assertRestored(t, ctx)
	takeover.write(t, ctx, "TAKEOVER\r")
	waitLiveScreen(t, ctx, managed, liveReceipt(8, "TAKEOVER"))
	if err := takeover.target.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	takeover.waitExit(t, ctx, true)
	takeover.assertRestored(t, ctx)

	// Fresh observer attachment restores the current screen, not replayed raw
	// bytes or an automatically restarted shell. It does not reacquire a lease.
	reconnected := startOuterAttach(t, ctx, "reconnected", executable, append(args, created.Session.ID), 120, 40)
	reconnected.waitText(t, ctx, liveReceipt(8, "TAKEOVER"), true)
	reconnected.waitStatus(t, ctx, "Read-only |")
	reconnected.write(t, ctx, "\x1d")
	reconnected.waitExit(t, ctx, false)
	reconnected.assertRestored(t, ctx)
	final, err := managed.Status(ctx)
	if err != nil || final.PID != initial.PID || final.Generation != initial.Generation || final.State != broker.SessionRunning {
		t.Fatalf("attach/detach restarted or ended child: initial=%#v final=%#v error=%v", initial, final, err)
	}
}

type outerAttach struct {
	pty       *xpty.UnixPty
	command   *exec.Cmd
	target    *os.Process
	writer    *cli.FileTerminalWriter
	display   *terminal.Core
	displayMu sync.Mutex
	output    []byte // Tail of raw PTY output, guarded by displayMu.
	before    unix.Termios
	started   time.Time
	exited    chan struct{}
	result    outerGuardianEvent // Published by closing exited.
	exitedAt  time.Time          // Published by closing exited.
	replies   atomic.Int64
}

// outerOutputTail bounds the raw output kept for a failure log. It is large
// enough for a goroutine dump of the attach command.
const outerOutputTail = 64 * 1024

func startOuterAttach(t *testing.T, ctx context.Context, name, executable string, args []string, columns, rows int) *outerAttach {
	t.Helper()
	pty, err := xpty.NewUnixPty(columns, rows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pty.Close() })
	before, err := unix.IoctlGetTermios(int(pty.Slave().Fd()), unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := cli.NewFileConfirmationReader(pty.Master())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	writer, err := cli.NewFileTerminalWriter(pty.Master())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	// The display and its reader live until cleanup, not until ctx expires, so
	// a failure log can still show the screen and late process output.
	lifetime := context.WithoutCancel(ctx)
	display, err := terminal.New(lifetime, terminal.Config{Columns: columns, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	outer := &outerAttach{pty: pty, writer: writer, display: display, before: *before, exited: make(chan struct{})}
	repliesDone := make(chan struct{})
	go func() {
		defer close(repliesDone)
		for range display.Output() {
			outer.replies.Add(1) // Never forward these to the outer PTY or child.
		}
	}()
	readContext, stopReader := context.WithCancel(lifetime)
	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := reader.ReadContext(readContext, buffer)
			if n > 0 {
				outer.displayMu.Lock()
				outer.output = append(outer.output, buffer[:n]...)
				if len(outer.output) > outerOutputTail {
					outer.output = outer.output[len(outer.output)-outerOutputTail:]
				}
				writeErr := display.WriteOutput(readContext, buffer[:n])
				outer.displayMu.Unlock()
				if writeErr != nil {
					readDone <- writeErr
					return
				}
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()
	t.Cleanup(func() {
		stopReader()
		_ = reader.Close()
		if err := <-readDone; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("outer PTY reader: %v", err)
		}
		_ = display.Close()
		<-repliesDone
		if outer.replies.Load() != 0 {
			t.Errorf("normalized desktop drawing generated %d terminal replies", outer.replies.Load())
		}
	})
	statusRead, statusWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = statusRead.Close(); _ = statusWrite.Close() })
	releaseRead, releaseWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = releaseRead.Close(); _ = releaseWrite.Close() })
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helperArgs := []string{"-test.run=^TestDesktopOuterTTYGuardian$", "--", outerGuardianMarker, executable}
	outer.command = exec.Command(helper, append(helperArgs, args...)...)
	outer.command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	outer.command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	outer.command.ExtraFiles = []*os.File{statusWrite, releaseRead}
	if err := pty.Start(outer.command); err != nil {
		t.Fatal(err)
	}
	outer.started = time.Now()
	_ = statusWrite.Close()
	_ = releaseRead.Close()
	guardianDone := make(chan error, 1)
	go func() { guardianDone <- outer.command.Wait() }()
	started := make(chan outerGuardianEvent, 1)
	go func() {
		decoder := json.NewDecoder(statusRead)
		var event outerGuardianEvent
		if err := decoder.Decode(&event); err != nil {
			event.Error = err.Error()
		}
		started <- event
		if err := decoder.Decode(&outer.result); err != nil {
			outer.result = outerGuardianEvent{ExitCode: -1, Error: err.Error()}
		}
		outer.exitedAt = time.Now()
		close(outer.exited)
	}()
	t.Cleanup(func() {
		stopReader()
		defer func() {
			if outer.target != nil {
				_ = outer.target.Release()
			}
		}()
		select {
		case <-outer.exited:
		default:
			if outer.target != nil {
				_ = outer.target.Kill() // Only this fixture-owned command PID.
			} else {
				// The guardian may have started the child before reporting its
				// PID. Both belong to this fixture's new controlling-session group.
				_ = syscall.Kill(-outer.command.Process.Pid, syscall.SIGKILL)
			}
		}
		_ = releaseWrite.Close()
		select {
		case err := <-guardianDone:
			if err != nil {
				t.Errorf("outer guardian exit: %v", err)
			}
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-outer.command.Process.Pid, syscall.SIGKILL)
			t.Error("outer guardian did not exit after cleanup")
			select {
			case <-guardianDone:
			case <-time.After(3 * time.Second):
				t.Error("outer guardian did not join after owned process-group kill")
			}
		}
		_ = statusRead.Close()
		<-outer.exited
	})
	select {
	case event := <-started:
		if event.Error != "" || event.PID <= 0 {
			t.Fatalf("start attach below outer guardian: %#v", event)
		}
		outer.target, err = os.FindProcess(event.PID)
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("outer guardian startup: ", ctx.Err())
	}
	// Runs before the cleanups above kill the command and stop the reader.
	t.Cleanup(func() {
		if t.Failed() {
			outer.logFailure(t, name)
		}
	})
	return outer
}

// logFailure reports the attach command's exit status, its screen, and its
// raw output (stdout and stderr share the outer PTY). A command that is still
// running gets SIGQUIT, so the output ends with its goroutine dump.
func (outer *outerAttach) logFailure(t *testing.T, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running := true
	select {
	case <-outer.exited:
		running = false
		if outer.result.ExitCode == 0 && outer.result.Error == "" {
			t.Logf("%s attach exited cleanly %v after start", name, outer.exitedAt.Sub(outer.started).Round(time.Millisecond))
			return
		}
	default:
	}
	status := "exited"
	var frame terminal.Frame
	var err error
	if running {
		// Take the screen first: the attach turned line wrap off, so the
		// goroutine dump would overwrite the last column of every row.
		frame, err = outer.display.Snapshot(ctx)
		status = "was still running at the failure"
		if outer.target != nil && outer.target.Signal(syscall.SIGQUIT) == nil {
			status += "; sent SIGQUIT, then exited"
		}
	}
	select {
	case <-outer.exited:
		status += fmt.Sprintf(" %v after start: %#v", outer.exitedAt.Sub(outer.started).Round(time.Millisecond), outer.result)
	case <-ctx.Done():
		status += "; did not exit"
	}
	time.Sleep(200 * time.Millisecond) // Let the reader take output written just before exit.
	if !running {
		frame, err = outer.display.Snapshot(ctx)
	}
	outer.displayMu.Lock()
	output := visibleControls(outer.output)
	outer.displayMu.Unlock()
	t.Logf("%s attach %s\n--- %s screen (%v):\n%s--- %s raw output tail:\n%s\n--- end %s", name, status, name, err, trimScreen(liveScreenText(frame)), name, output, name)
}

// visibleControls shows raw terminal output in a log: ESC as \e, other C0
// controls and DEL as \xNN. Newlines and tabs stay; CRs before LF are dropped
// (output processing turns a CRLF written before raw mode into CR CR LF).
func visibleControls(data []byte) string {
	raw := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r\n", "\n")
	var text strings.Builder
	for _, b := range []byte(raw) {
		switch {
		case b == '\n' || b == '\t':
			text.WriteByte(b)
		case b == 0x1b:
			text.WriteString(`\e`)
		case b < 0x20 || b == 0x7f:
			fmt.Fprintf(&text, `\x%02x`, b)
		default:
			text.WriteByte(b)
		}
	}
	return text.String()
}

func (outer *outerAttach) write(t *testing.T, ctx context.Context, data string) {
	t.Helper()
	if n, err := outer.writer.WriteContext(ctx, []byte(data)); err != nil || n != len(data) {
		t.Fatalf("write synthetic outer input: %d/%d, %v", n, len(data), err)
	}
}

func (outer *outerAttach) resize(t *testing.T, ctx context.Context, columns, rows int) {
	t.Helper()
	// Model one physical terminal resize while the parser consumes its output.
	outer.displayMu.Lock()
	defer outer.displayMu.Unlock()
	if err := outer.pty.Resize(columns, rows); err != nil {
		t.Fatal(err)
	}
	if err := outer.display.Resize(ctx, columns, rows); err != nil {
		t.Fatal(err)
	}
}

func (outer *outerAttach) waitText(t *testing.T, ctx context.Context, expected string, alternate bool) {
	t.Helper()
	outer.waitDisplay(t, ctx, expected, alternate, false)
}

func (outer *outerAttach) waitStatus(t *testing.T, ctx context.Context, expected string) {
	t.Helper()
	outer.waitDisplay(t, ctx, expected, true, true)
}

func (outer *outerAttach) waitDisplay(t *testing.T, ctx context.Context, expected string, alternate, lastRow bool) {
	t.Helper()
	for {
		frame, err := outer.display.Snapshot(ctx)
		if err != nil {
			t.Fatalf("outer display snapshot while waiting for %q: %v", expected, err)
		}
		text := liveScreenText(frame)
		if lastRow {
			text = liveScreenText(terminal.Frame{Screen: frame.Screen[len(frame.Screen)-1:]})
		}
		if frame.Alternate == alternate && strings.Contains(text, expected) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("outer display missing %q (alternate=%t): %v\n%s", expected, alternate, ctx.Err(), liveScreenText(frame))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (outer *outerAttach) assertRaw(t *testing.T) {
	t.Helper()
	actual, err := unix.IoctlGetTermios(int(outer.pty.Slave().Fd()), unix.TIOCGETA)
	if err != nil || actual.Lflag&(unix.ECHO|unix.ICANON) != 0 {
		t.Fatalf("outer PTY did not enter raw mode: %#v, %v", actual, err)
	}
}

func (outer *outerAttach) waitExit(t *testing.T, ctx context.Context, canceled bool) {
	t.Helper()
	select {
	case <-outer.exited:
		if canceled {
			if outer.result.ExitCode != 1 {
				t.Fatalf("signal cancellation exit: %#v", outer.result)
			}
		} else if outer.result.ExitCode != 0 || outer.result.Error != "" {
			t.Fatalf("detach executable exit: %#v", outer.result)
		}
	case <-ctx.Done():
		t.Fatalf("attach executable did not exit: %v", ctx.Err())
	}
}

func (outer *outerAttach) assertRestored(t *testing.T, ctx context.Context) {
	t.Helper()
	outer.waitText(t, ctx, "Attachment closed; the managed session was not terminated.", false)
	after, err := unix.IoctlGetTermios(int(outer.pty.Slave().Fd()), unix.TIOCGETA)
	if err != nil || *after != outer.before {
		t.Fatalf("outer terminal modes not exactly restored: before=%#v after=%#v error=%v", outer.before, after, err)
	}
}

func waitOuterCanonicalSize(t *testing.T, ctx context.Context, managed broker.ManagedSession, columns, rows int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		frame, err := managed.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Columns == columns && frame.Rows == rows {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("canonical size = %dx%d, want %dx%d", frame.Columns, frame.Rows, columns, rows)
		case <-deadline.C:
			t.Fatalf("canonical size = %dx%d, want %dx%d", frame.Columns, frame.Rows, columns, rows)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitOuterWrappedReceipt(t *testing.T, ctx context.Context, managed broker.ManagedSession, expected string) {
	t.Helper()
	waitLiveCondition(t, ctx, "wrapped canonical receipt after shrink", func() bool {
		frame, err := managed.Snapshot(ctx)
		// A SHA-256 receipt is wider than the intentionally narrowed 60-column
		// terminal. Join normalized rows only for this synthetic receipt check.
		return err == nil && strings.Contains(strings.ReplaceAll(liveScreenText(frame), "\n", ""), expected)
	})
}

// trimScreen drops trailing blanks so a failure log shows only painted text.
func trimScreen(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

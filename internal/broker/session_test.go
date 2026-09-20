package broker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const testSessionID = "22222222-2222-4222-8222-222222222222"

func TestManagedSessionRuntime(t *testing.T) {
	fixture := buildSessionFixture(t)

	t.Run("query replies and final screen work without viewers", func(t *testing.T) {
		session := startFixtureSession(t, fixture, "query")
		defer session.Close()

		status := waitForSessionState(t, session, SessionExited)
		frame, err := session.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(screenText(frame), "QUERY_OK") {
			t.Fatalf("final screen = %q", screenText(frame))
		}
		if status.ExitCode == nil || *status.ExitCode != 0 {
			t.Fatalf("exit status = %#v", status)
		}
		time.Sleep(10 * time.Millisecond)
		again, err := session.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !again.EndedAt.Equal(status.EndedAt) {
			t.Fatalf("exit timestamp changed from %v to %v", status.EndedAt, again.EndedAt)
		}
	})

	t.Run("structured input reaches child in order", func(t *testing.T) {
		session := startFixtureSession(t, fixture, "echo")
		defer session.Close()
		waitForFrameText(t, session, "READY")

		grant := acquireFixtureControl(t, session)
		identity := fixtureIdentity(grant)
		if accepted, err := session.SubmitInput(context.Background(), identity, 1, InputAction{Kind: InputKindText, Text: "hello"}); err != nil || accepted.Duplicate {
			t.Fatalf("text acceptance = %#v, %v", accepted, err)
		}
		if _, err := session.SubmitInput(context.Background(), identity, 2, InputAction{Kind: InputKindKey, Key: protocol.Enter}); err != nil {
			t.Fatal(err)
		}
		frame := waitForFrameText(t, session, "ECHO_OK")
		if !strings.Contains(screenText(frame), `ECHO:"hello\n"`) {
			t.Fatalf("echo screen = %q", screenText(frame))
		}
		waitForSessionState(t, session, SessionExited)
	})

	t.Run("disconnect releases control but preserves process", func(t *testing.T) {
		session := startFixtureSession(t, fixture, "sleep")
		defer session.Close()
		waitForFrameText(t, session, "READY")
		before, err := session.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		grant := acquireFixtureControl(t, session)
		if err := session.Disconnect(context.Background(), grant.ConnectionID); err != nil {
			t.Fatal(err)
		}
		after, err := session.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if after.State != SessionRunning || after.PID != before.PID || after.Generation != before.Generation {
			t.Fatalf("disconnect changed process identity: before %#v, after %#v", before, after)
		}
		if _, err := session.SubmitInput(context.Background(), fixtureIdentity(grant), 1, InputAction{Kind: InputKindText, Text: "stale"}); !errors.Is(err, ErrNotController) {
			t.Fatalf("detached input error = %v", err)
		}
		if err := session.Terminate(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitForSessionState(t, session, SessionExited)
	})

	t.Run("only current controller can resize", func(t *testing.T) {
		session := startFixtureSession(t, fixture, "sleep")
		defer session.Close()
		grant := acquireFixtureControl(t, session)
		identity := fixtureIdentity(grant)
		stale := identity
		stale.Lease = "not-the-current-lease"
		if err := session.Resize(context.Background(), stale, 100, 30); !errors.Is(err, ErrStaleLease) {
			t.Fatalf("stale resize error = %v", err)
		}
		if err := session.Resize(context.Background(), identity, 100, 30); err != nil {
			t.Fatal(err)
		}
		frame, err := session.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		status, err := session.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Columns != 100 || frame.Rows != 30 || status.Columns != 100 || status.Rows != 30 {
			t.Fatalf("resize state = frame %dx%d, status %dx%d", frame.Columns, frame.Rows, status.Columns, status.Rows)
		}
		if err := session.Terminate(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitForSessionState(t, session, SessionExited)
	})
}

func TestSessionRejectsRequestContextAsLifetime(t *testing.T) {
	fixture := buildSessionFixture(t)
	session := startFixtureSession(t, fixture, "sleep")
	defer session.Close()

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Snapshot(requestContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %v", err)
	}
	status, err := session.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != SessionRunning {
		t.Fatalf("request cancellation ended session: %#v", status)
	}
	if err := session.Terminate(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, session, SessionExited)
}

func TestSessionValidatesQueueBoundsBeforeStartingProcess(t *testing.T) {
	_, err := StartSession(context.Background(), SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Executable: "unused", Columns: 80, Rows: 24},
		InputQueueCapacity: MaxInputQueueCapacity + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "input queue") {
		t.Fatalf("queue validation error = %v", err)
	}
}

func TestSessionRejectsCanceledBrokerContextBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := StartSession(ctx, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process: ptyhost.Spec{Executable: "must-not-run", Columns: 80, Rows: 24},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled broker context error = %v", err)
	}
}

func TestSessionShutdownWaitsBeforeClosingPTY(t *testing.T) {
	runtimeContext, cancel := context.WithCancel(context.Background())
	process := newInstrumentedProcess(nil)
	core := newStubTerminal()
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity,
		PTYOutputQueue:     DefaultPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !process.terminated.Load() || !process.waited.Load() || !process.closed.Load() {
		t.Fatalf("shutdown state: terminated=%v waited=%v closed=%v", process.terminated.Load(), process.waited.Load(), process.closed.Load())
	}
	if process.closeBeforeWait.Load() {
		t.Fatal("PTY closed before process Wait completed")
	}
	if !core.closed.Load() {
		t.Fatal("terminal core was not closed")
	}
}

func TestSessionReadFailureMarksLostAndTerminatesChild(t *testing.T) {
	runtimeContext, cancel := context.WithCancel(context.Background())
	process := newInstrumentedProcess(errors.New("synthetic PTY read failure"))
	core := newStubTerminal()
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity,
		PTYOutputQueue:     DefaultPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	status := waitForSessionState(t, session, SessionLost)
	deadline := time.Now().Add(time.Second)
	for !process.waited.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status.ExitCode != nil || !process.terminated.Load() || !process.waited.Load() {
		t.Fatalf("read failure status = %#v, terminated=%v waited=%v", status, process.terminated.Load(), process.waited.Load())
	}
}

func TestSessionCloseReturnsErrorWhenTerminationCannotComplete(t *testing.T) {
	runtimeContext, cancel := context.WithCancel(context.Background())
	process := newInstrumentedProcess(nil)
	process.terminateErr = errors.New("synthetic termination failure")
	core := newStubTerminal()
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity,
		PTYOutputQueue:     DefaultPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() { result <- session.Close() }()
	select {
	case closeErr := <-result:
		if !errors.Is(closeErr, ErrSessionShutdown) {
			t.Fatalf("close error = %v", closeErr)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("Close blocked after termination failure")
	}
	if process.closed.Load() {
		t.Fatal("live PTY was closed before process termination and Wait")
	}

	// Release the synthetic process so the actor can finish without leaking a
	// test goroutine. The previously reported shutdown error remains truthful.
	process.release()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not clean up after synthetic process exit")
	}
}

func TestSessionRejectsRequestQueuedImmediatelyBeforeFailedShutdown(t *testing.T) {
	runtimeContext, cancel := context.WithCancel(context.Background())
	process := newInstrumentedProcess(nil)
	process.terminateErr = errors.New("synthetic termination failure")
	core := newBlockingStubTerminal()
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity,
		PTYOutputQueue:     DefaultPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}

	snapshotResult := make(chan error, 1)
	go func() {
		_, snapshotErr := session.Snapshot(context.Background())
		snapshotResult <- snapshotErr
	}()
	select {
	case <-core.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("snapshot did not enter actor")
	}

	statusResult := make(chan error, 1)
	go func() {
		_, statusErr := session.Status(context.Background())
		statusResult <- statusErr
	}()
	deadline := time.Now().Add(time.Second)
	for len(session.requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(session.requests) == 0 {
		t.Fatal("status request was not queued")
	}

	closeResult := make(chan error, 1)
	go func() { closeResult <- session.Close() }()
	select {
	case <-session.shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown signal was not closed")
	}
	close(core.snapshotRelease)
	if err := <-snapshotResult; err != nil {
		t.Fatalf("accepted snapshot error = %v", err)
	}
	select {
	case statusErr := <-statusResult:
		if !errors.Is(statusErr, ErrSessionClosed) {
			t.Fatalf("queued status error = %v", statusErr)
		}
	case <-time.After(time.Second):
		t.Fatal("queued request hung during failed shutdown")
	}
	if closeErr := <-closeResult; !errors.Is(closeErr, ErrSessionShutdown) {
		t.Fatalf("close error = %v", closeErr)
	}

	process.release()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not clean up after synthetic process exit")
	}
}

func buildSessionFixture(t *testing.T) string {
	t.Helper()
	name := "ptychild"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", path, "../../tests/fixtures/ptychild")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build PTY fixture: %v\n%s", err, output)
	}
	return path
}

func startFixtureSession(t *testing.T, fixture, mode string) *Session {
	t.Helper()
	session, err := StartSession(context.Background(), SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process: ptyhost.Spec{
			Executable: fixture,
			Env:        []string{"PTERM_PTY_FIXTURE_MODE=" + mode},
			Columns:    80,
			Rows:       24,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func acquireFixtureControl(t *testing.T, session *Session) ControlGrant {
	t.Helper()
	grant, err := session.AcquireControl(context.Background(), testGeneration, "connection", "device")
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func fixtureIdentity(grant ControlGrant) MutationIdentity {
	return MutationIdentity{
		Generation: testGeneration, ConnectionID: grant.ConnectionID,
		DeviceID: grant.DeviceID, Lease: grant.Lease,
	}
}

func waitForFrameText(t *testing.T, session *Session, wanted string) terminal.Frame {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		frame, err := session.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(screenText(frame), wanted) {
			return frame
		}
		time.Sleep(10 * time.Millisecond)
	}
	frame, _ := session.Snapshot(context.Background())
	t.Fatalf("screen never contained %q: %q", wanted, screenText(frame))
	return terminal.Frame{}
}

func waitForSessionState(t *testing.T, session *Session, wanted SessionState) SessionStatus {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, err := session.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.State == wanted {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := session.Status(context.Background())
	t.Fatalf("session never reached %q: %#v", wanted, status)
	return SessionStatus{}
}

func screenText(frame terminal.Frame) string {
	var text strings.Builder
	for _, row := range frame.Screen {
		for _, cell := range row.Cells {
			if !cell.Continuation {
				text.WriteString(cell.Text)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

type instrumentedProcess struct {
	readErr         error
	terminateErr    error
	exited          chan struct{}
	exitOnce        sync.Once
	terminated      atomic.Bool
	waited          atomic.Bool
	closed          atomic.Bool
	closeBeforeWait atomic.Bool
}

func newInstrumentedProcess(readErr error) *instrumentedProcess {
	return &instrumentedProcess{readErr: readErr, exited: make(chan struct{})}
}

func (process *instrumentedProcess) PID() int { return 4242 }

func (process *instrumentedProcess) Read([]byte) (int, error) {
	if process.readErr != nil {
		return 0, process.readErr
	}
	<-process.exited
	return 0, io.EOF
}

func (process *instrumentedProcess) Write(data []byte) (int, error) { return len(data), nil }
func (process *instrumentedProcess) Resize(int, int) error          { return nil }

func (process *instrumentedProcess) Wait(context.Context) error {
	<-process.exited
	process.waited.Store(true)
	return nil
}

func (process *instrumentedProcess) Terminate(context.Context) error {
	if process.terminateErr != nil {
		return process.terminateErr
	}
	process.terminated.Store(true)
	process.release()
	return nil
}

func (process *instrumentedProcess) Close() error {
	if !process.waited.Load() {
		process.closeBeforeWait.Store(true)
	}
	process.closed.Store(true)
	process.release()
	return nil
}

func (process *instrumentedProcess) release() {
	process.exitOnce.Do(func() { close(process.exited) })
}

type stubTerminal struct {
	output          chan []byte
	closeOnce       sync.Once
	closed          atomic.Bool
	snapshotStarted chan struct{}
	snapshotRelease chan struct{}
	snapshotOnce    sync.Once
}

func newStubTerminal() *stubTerminal { return &stubTerminal{output: make(chan []byte)} }

func newBlockingStubTerminal() *stubTerminal {
	return &stubTerminal{
		output:          make(chan []byte),
		snapshotStarted: make(chan struct{}),
		snapshotRelease: make(chan struct{}),
	}
}

func (*stubTerminal) WriteOutput(context.Context, []byte) error { return nil }
func (*stubTerminal) Resize(context.Context, int, int) error    { return nil }
func (*stubTerminal) SendText(context.Context, string) error    { return nil }
func (*stubTerminal) Paste(context.Context, string) error       { return nil }
func (*stubTerminal) SendKey(context.Context, uv.KeyEvent) error {
	return nil
}
func (*stubTerminal) SendMouse(context.Context, uv.MouseEvent) error {
	return nil
}
func (terminalCore *stubTerminal) Snapshot(context.Context) (terminal.Frame, error) {
	if terminalCore.snapshotStarted != nil {
		terminalCore.snapshotOnce.Do(func() { close(terminalCore.snapshotStarted) })
		<-terminalCore.snapshotRelease
	}
	return terminal.Frame{Columns: 80, Rows: 24}, nil
}
func (terminalCore *stubTerminal) Output() <-chan []byte { return terminalCore.output }
func (terminalCore *stubTerminal) Close() error {
	terminalCore.closeOnce.Do(func() {
		terminalCore.closed.Store(true)
		close(terminalCore.output)
	})
	return nil
}

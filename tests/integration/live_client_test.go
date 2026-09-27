//go:build darwin || windows

package integration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/liveclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	liveProjectID   = "c1111111-1111-4111-8111-111111111111"
	liveProfileID   = "c2222222-2222-4222-8222-222222222222"
	liveOperationID = "c3333333-3333-4333-8333-333333333333"
	liveRequestID   = "c4444444-4444-4444-8444-444444444444"
	liveSeedBroker  = "c5555555-5555-4555-8555-555555555555"
	liveChildMarker = "pterm-live-fixture"
)

// The child is this test binary, launched through the real PTY adapter. Its
// receipts describe synthetic input only; no user shell, account, or TTY is used.
func TestLivePTYFixture(t *testing.T) {
	if !slices.Contains(os.Args, liveChildMarker) {
		t.Skip("helper subprocess only")
	}
	if err := runLivePTYFixture(); err != nil {
		fmt.Fprintln(os.Stderr, "FIXTURE_ERROR:", err)
		os.Exit(7)
	}
	os.Exit(0)
}

func runLivePTYFixture() error {
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
	input := bufio.NewReader(os.Stdin)
	query := func() error {
		if _, err := io.WriteString(os.Stdout, "\x1b7\x1b[3;7H\x1b[6n"); err != nil {
			return err
		}
		response := make([]byte, len("\x1b[3;7R"))
		if _, err := io.ReadFull(input, response); err != nil {
			return err
		}
		if string(response) != "\x1b[3;7R" {
			return errors.New("incorrect cursor-position reply")
		}
		_, err := io.WriteString(os.Stdout, "\x1b8")
		return err
	}
	if err := query(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\x1b[?2004h\x1b[2J\x1b[HBOOT_QUERY_OK pid=%d\r\n\x1b[1;38;2;100;216;232mGRID 界 e\u0301 👩‍💻\x1b[0m\r\n", os.Getpid())
	for number := 1; ; number++ {
		line, err := input.ReadString('\r')
		if err != nil {
			return err
		}
		line = strings.TrimSuffix(line, "\r")
		fmt.Fprintf(os.Stdout, "%s\r\n", liveReceipt(number, line))
		if line == "QUERY" {
			if err := query(); err != nil {
				return err
			}
			fmt.Fprint(os.Stdout, "TWO_VIEWER_QUERY_OK\r\n")
		}
	}
}

func liveReceipt(number int, value string) string {
	return fmt.Sprintf("ACK%02d:%x", number, sha256.Sum256([]byte(value)))
}

func TestLiveClientNativeBrokerControlInputResizeAndReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	runtimeDir, state := startLiveBroker(t, ctx)
	created := createLiveSession(t, ctx, runtimeDir)
	managed, err := state.Registry.Get(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := managed.Status(ctx)
	if err != nil || initial.PID <= 0 || initial.State != broker.SessionRunning {
		t.Fatalf("initial managed identity: %#v, %v", initial, err)
	}
	boot := fmt.Sprintf("BOOT_QUERY_OK pid=%d", initial.PID)
	waitLiveScreen(t, ctx, managed, boot) // Query response with no viewers.

	first, firstDisplay := connectLiveClient(t, ctx, runtimeDir, created.Session)
	if first.State().Controller {
		t.Fatal("attach automatically acquired control")
	}
	assertLiveDisplay(t, ctx, managed, firstDisplay, boot)
	if err := first.Send(ctx, desktop.TextAction{Text: "observer"}); !errors.Is(err, liveclient.ErrNotController) {
		t.Fatalf("observer input = %v", err)
	}
	if err := first.Resize(ctx, 100, 30); !errors.Is(err, liveclient.ErrNotController) {
		t.Fatalf("observer resize = %v", err)
	}
	if err := first.AcquireControl(ctx); err != nil {
		t.Fatal(err)
	}
	// Success requires the server to have processed snapshot.applied. This path
	// sends exact structured text, Tab, bracketed multiline paste, and Enter.
	sendLiveActions(t, ctx, first,
		desktop.TextAction{Text: "TEXT"}, desktop.KeyAction{Key: protocol.Tab},
		desktop.PasteAction{Text: "two\nlines"}, desktop.KeyAction{Key: protocol.Enter})
	receiptOne := liveReceipt(1, "TEXT\t\x1b[200~two\nlines\x1b[201~")
	assertLiveDisplay(t, ctx, managed, firstDisplay, receiptOne)

	second, secondDisplay := connectLiveClient(t, ctx, runtimeDir, created.Session)
	if second.State().Controller || !first.State().Controller {
		t.Fatal("second observer changed controller ownership")
	}
	assertLiveDisplay(t, ctx, managed, secondDisplay, receiptOne)
	if err := second.AcquireControl(ctx); err != nil {
		t.Fatal(err)
	}
	waitLiveState(t, ctx, first, func(state liveclient.State) bool { return !state.Controller })
	if err := first.Send(ctx, desktop.TextAction{Text: "stale"}); !errors.Is(err, liveclient.ErrNotController) {
		t.Fatalf("displaced controller input = %v", err)
	}
	if second.State().ControllerConnectionID == "" ||
		first.State().ControllerConnectionID != second.State().ControllerConnectionID {
		t.Fatal("viewers do not agree on the new controller connection")
	}
	sendLiveActions(t, ctx, second, desktop.TextAction{Text: "QUERY"}, desktop.KeyAction{Key: protocol.Enter})
	assertLiveDisplay(t, ctx, managed, secondDisplay, "TWO_VIEWER_QUERY_OK")
	assertLiveDisplay(t, ctx, managed, firstDisplay, liveReceipt(2, "QUERY"))
	// Any duplicate query reply contaminates this next receipt. Neither client
	// may answer child queries through its synthetic display or outer terminal.
	sendLiveActions(t, ctx, second, desktop.TextAction{Text: "AFTER_QUERY"}, desktop.KeyAction{Key: protocol.Enter})
	receiptThree := liveReceipt(3, "AFTER_QUERY")
	assertLiveDisplay(t, ctx, managed, secondDisplay, receiptThree)

	if err := second.Resize(ctx, 100, 30); err != nil {
		t.Fatal(err)
	}
	waitLiveCondition(t, ctx, "canonical resize", func() bool {
		status, err := managed.Status(ctx)
		return err == nil && status.Columns == 100 && status.Rows == 30
	})
	if err := second.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	assertLiveDisplay(t, ctx, managed, secondDisplay, receiptThree)
	assertLiveDisplay(t, ctx, managed, firstDisplay, receiptThree)
	if err := second.ReleaseControl(ctx); err != nil {
		t.Fatal(err)
	}
	waitLiveState(t, ctx, first, func(state liveclient.State) bool { return state.ControllerConnectionID == "" })
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Detach(ctx); err != nil {
		t.Fatal(err)
	}
	assertLiveIdentity(t, ctx, managed, initial)
	if err := second.Send(ctx, desktop.TextAction{Text: "offline"}); !errors.Is(err, liveclient.ErrClosed) {
		t.Fatalf("offline input = %v", err)
	}

	third, thirdDisplay := connectLiveClient(t, ctx, runtimeDir, created.Session)
	if third.State().Controller {
		t.Fatal("reconnect automatically reacquired control")
	}
	assertLiveDisplay(t, ctx, managed, thirdDisplay, receiptThree)
	assertLiveIdentity(t, ctx, managed, initial)
	if err := third.AcquireControl(ctx); err != nil {
		t.Fatal(err)
	}
	sendLiveActions(t, ctx, third, desktop.TextAction{Text: "RECONNECTED"}, desktop.KeyAction{Key: protocol.Enter})
	assertLiveDisplay(t, ctx, managed, thirdDisplay, liveReceipt(4, "RECONNECTED"))
	// Closing the active controller (without release) must revoke its lease and
	// preserve the process, just as detach did for the preceding observer.
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	fourth, fourthDisplay := connectLiveClient(t, ctx, runtimeDir, created.Session)
	waitLiveState(t, ctx, fourth, func(state liveclient.State) bool { return state.ControllerConnectionID == "" })
	assertLiveDisplay(t, ctx, managed, fourthDisplay, liveReceipt(4, "RECONNECTED"))
	assertLiveIdentity(t, ctx, managed, initial)
	if fourth.State().Controller {
		t.Fatal("final reconnect silently took control")
	}
}

func startLiveBroker(t *testing.T, parent context.Context) (string, host.BrokerReady) {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp" // Keep the user-owned Unix socket below its path limit.
	}
	root, err := os.MkdirTemp(base, "pterm-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir, runtimeDir := filepath.Join(root, "data"), filepath.Join(root, "runtime")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	persistence, err := store.Open(parent, filepath.Join(dataDir, "phone-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close() // Also covers a failed seed before the explicit close.
	now := time.Now().UTC()
	if _, err := persistence.BeginBrokerGeneration(parent, liveSeedBroker, now); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutProject(parent, store.Project{
		ID: liveProjectID, DisplayName: "Live fixture", CanonicalPath: dataDir, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutShellProfile(parent, store.ShellProfile{
		ID: liveProfileID, DisplayName: "Synthetic PTY", Platform: store.Platform(runtime.GOOS),
		ExecutablePath: executable, Arguments: []string{"-test.run=^TestLivePTYFixture$", "--", liveChildMarker},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(parent)
	ready, done := make(chan host.BrokerReady, 1), make(chan error, 1)
	go func() {
		done <- host.RunBroker(ctx, host.BrokerConfig{
			DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "live-integration", Profile: host.DevelopmentProfile,
			Ready: func(value host.BrokerReady) { ready <- value },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("broker shutdown: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("broker did not join after test cancellation")
		}
	})
	select {
	case state := <-ready:
		return runtimeDir, state
	case <-parent.Done():
		t.Fatal("broker startup: ", parent.Err())
		return "", host.BrokerReady{}
	}
}

func createLiveSession(t *testing.T, ctx context.Context, runtimeDir string) protocol.BrokerRPCSessionMutationResultPayload {
	t.Helper()
	connection, _, _, err := ipc.DialBroker(ctx, runtimeDir, protocol.CLI, "live-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: liveOperationID, ProjectID: liveProjectID, ShellProfileID: liveProfileID,
	})
	data, _ := json.Marshal(protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: protocol.SessionCreateRequest, RequestID: liveRequestID, Payload: payload,
	})
	if _, err := protocol.DecodeBrokerRPCRequest(data); err != nil {
		t.Fatal(err)
	}
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	if err := writer.WriteFrame(data); err != nil {
		t.Fatal(err)
	}
	data, err = reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	response, err := protocol.DecodeBrokerRPCResponse(data)
	if err != nil || response.Type != protocol.SessionCreateResult || response.RequestID != liveRequestID {
		t.Fatalf("create response: type=%s error=%v", response.Type, err)
	}
	result, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](response)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The synthetic display parses only the client's constrained drawing output.
// Its reply stream is drained into a test failure, NEVER into the PTY.
type liveDisplay struct{ core *terminal.Core }

func (display *liveDisplay) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := display.core.WriteOutput(ctx, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func connectLiveClient(t *testing.T, ctx context.Context, runtimeDir string, session protocol.BrokerRPCSessionRecord) (*liveclient.Client, *liveDisplay) {
	t.Helper()
	core, err := terminal.New(ctx, terminal.Config{Columns: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range core.Output() {
			t.Error("client drawing incorrectly generated a terminal query reply")
		}
	}()
	t.Cleanup(func() { _ = core.Close(); <-drained })
	display := &liveDisplay{core: core}
	client, err := liveclient.Dial(ctx, liveclient.Config{
		RuntimeDir: runtimeDir, ClientBuild: "live-integration", SessionID: session.ID,
		Generation: session.Generation, Output: display,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	return client, display
}

func sendLiveActions(t *testing.T, ctx context.Context, client *liveclient.Client, actions ...desktop.InputAction) {
	t.Helper()
	for _, action := range actions {
		if err := client.Send(ctx, action); err != nil {
			t.Fatalf("structured %T: %v", action, err)
		}
	}
}

func liveScreenText(frame terminal.Frame) string {
	var text strings.Builder
	for _, row := range frame.Screen {
		for _, cell := range row.Cells {
			text.WriteString(cell.Text)
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func waitLiveScreen(t *testing.T, ctx context.Context, session broker.ManagedSession, marker string) terminal.Frame {
	t.Helper()
	var frame terminal.Frame
	waitLiveCondition(t, ctx, "canonical synthetic receipt", func() bool {
		var err error
		frame, err = session.Snapshot(ctx)
		return err == nil && strings.Contains(liveScreenText(frame), marker)
	})
	return frame
}

func assertLiveDisplay(t *testing.T, ctx context.Context, session broker.ManagedSession, display *liveDisplay, marker string) {
	t.Helper()
	waitLiveCondition(t, ctx, "painted canonical rows and cursor", func() bool {
		// A marker can arrive at a PTY read boundary before its trailing CRLF.
		// Compare current immutable states on every poll, not a frozen partial
		// output frame whose cursor the display may already have advanced past.
		canonical, err := session.Snapshot(ctx)
		if err != nil || !strings.Contains(liveScreenText(canonical), marker) {
			return false
		}
		painted, err := display.core.ScreenSnapshot(ctx)
		if err != nil || painted.Cursor != canonical.Cursor {
			return false
		}
		for row := range canonical.Rows {
			for column := range canonical.Columns {
				if painted.Screen[row].Cells[column] != canonical.Screen[row].Cells[column] {
					return false
				}
			}
		}
		return true
	})
}

func assertLiveIdentity(t *testing.T, ctx context.Context, session broker.ManagedSession, initial broker.SessionStatus) {
	t.Helper()
	status, err := session.Status(ctx)
	if err != nil || status.PID != initial.PID || status.Generation != initial.Generation || status.State != broker.SessionRunning {
		t.Fatalf("viewer disconnect changed child identity: %#v, %v", status, err)
	}
}

func waitLiveState(t *testing.T, ctx context.Context, client *liveclient.Client, predicate func(liveclient.State) bool) {
	t.Helper()
	waitLiveCondition(t, ctx, "live client state", func() bool {
		if err := client.Err(); err != nil {
			t.Fatal(err)
		}
		return predicate(client.State())
	})
}

func waitLiveCondition(t *testing.T, ctx context.Context, name string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", name, ctx.Err())
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", name)
		case <-ticker.C:
		}
	}
}

// The seventeenth viewer is rejected by the real broker. WaitReady must end
// promptly with the broker's reason, not wait for a snapshot that never comes.
func TestLiveClientAttachRejectedAtViewerLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	runtimeDir, _ := startLiveBroker(t, ctx)
	created := createLiveSession(t, ctx, runtimeDir)
	for range broker.MaxViewersPerSession {
		connectLiveClient(t, ctx, runtimeDir, created.Session)
	}
	client, err := liveclient.Dial(ctx, liveclient.Config{
		RuntimeDir: runtimeDir, ClientBuild: "live-integration", SessionID: created.Session.ID,
		Generation: created.Session.Generation, Output: &liveDisplay{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	readyContext, cancelReady := context.WithTimeout(ctx, 3*time.Second)
	defer cancelReady()
	err = client.WaitReady(readyContext)
	remote, ok := errors.AsType[*liveclient.RemoteError](err)
	if !ok || remote.Code != protocol.LimitExceeded {
		t.Fatalf("attach beyond viewer limit = %v", err)
	}
}

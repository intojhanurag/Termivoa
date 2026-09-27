package liveclient

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func sizedSnapshot(t *testing.T, columns, height int, text string) (protocol.ScreenMetadata, []protocol.TerminalRow) {
	t.Helper()
	core, err := terminal.New(context.Background(), terminal.Config{Columns: columns, Rows: height})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := core.WriteOutput(context.Background(), []byte(text)); err != nil {
		t.Fatal(err)
	}
	frame, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	screen, rows, err := protocol.Snapshot(frame, frame.HistoryEpoch, frame.HistoryRevision)
	if err != nil {
		t.Fatal(err)
	}
	return screen, rows
}

func TestControllerShrinkRequestsFittingSnapshotWithoutClosing(t *testing.T) {
	var small atomic.Bool
	output := &bufferOutput{}
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(screen protocol.ScreenMetadata) error {
			if small.Load() && (screen.Columns > 20 || screen.Rows > 5) {
				return &ViewportResizeRequired{Columns: 20, Rows: 5}
			}
			return nil
		}
	})
	screen, rows := sizedSnapshot(t, 30, 6, "initial")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	waitState(t, client, func(state State) bool { return state.Ready })
	acquire(t, client, host, "1")
	small.Store(true)
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen, Rows: []protocol.ChangedRow{{Index: 0, Row: rows[0]}}})
	select {
	case <-client.Done():
		t.Fatalf("controller shrink closed the viewer instead of requesting a fitting snapshot: %v", client.Err())
	case request := <-host.requests:
		if request.Type != protocol.TerminalResize {
			t.Fatalf("shrink emitted %s", request.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("shrink did not request a fitting snapshot")
	}
	host.await(t, protocol.ScreenResync)
	before := output.text()
	if client.State().Ready || !client.State().Resizing {
		t.Fatal("resize did not disable input")
	}
	if err := client.Send(context.Background(), desktop.TextAction{Text: "must not queue"}); err != ErrNotReady {
		t.Fatalf("input while resizing: %v", err)
	}
	host.snapshot(t, "3", screen, rows) // Old in-flight grid must not be acknowledged.
	fitting, smallRows := sizedSnapshot(t, 20, 5, "fits")
	id := host.snapshot(t, "4", fitting, smallRows)
	ack, _ := protocol.DecodePayload[protocol.SnapshotAppliedPayload](host.await(t, protocol.SnapshotApplied))
	if ack.SnapshotID != id {
		t.Fatal("acknowledged stale oversized screen")
	}
	waitState(t, client, func(state State) bool { return state.Ready && !state.Resizing })
	if output.text() == before || !client.State().Controller {
		t.Fatal("did not resume fitting controller screen")
	}
}

func TestResizeDeadlineCannotBeExtendedByStaleSnapshots(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	if err := client.Resize(context.Background(), 30, 6); err != nil {
		t.Fatal(err)
	}
	host.await(t, protocol.TerminalResize)
	host.await(t, protocol.ScreenResync)
	// Keep production's fixed five-second policy, but shorten this instance's
	// immutable deadline so the deterministic no-convergence test is quick.
	client.mu.Lock()
	transition := *client.viewport
	transition.deadline = time.Now().Add(100 * time.Millisecond)
	client.viewport = &transition
	client.mu.Unlock()
	client.paintWake <- struct{}{}
	screen, rows := snapshot(t, "stale")
	host.snapshot(t, "2", screen, rows)
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("unfitting host never timed out")
	}
	if err := client.Err(); err == nil || !strings.Contains(err.Error(), "re-attach") {
		t.Fatalf("resize timeout lacks guidance: %v", err)
	}
	_ = client.Close()
	assertNoViewportAck(t, host)
}

func TestObserverViewportResizeRequestNeverMutatesSession(t *testing.T) {
	var small atomic.Bool
	output := &bufferOutput{}
	request := &ViewportResizeRequired{Columns: 20, Rows: 5}
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(protocol.ScreenMetadata) error {
			if small.Load() {
				return request
			}
			return nil
		}
	})
	screen, rows := sizedSnapshot(t, 30, 6, "observer")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	waitState(t, client, func(state State) bool { return state.Ready })
	small.Store(true)
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen, Rows: []protocol.ChangedRow{}})
	awaitViewportFailure(t, client, request)
	assertNoViewportAck(t, host)
}

type resizeRaceOutput struct {
	bufferOutput
	reject  atomic.Bool
	failure error
	written int
}

func (output *resizeRaceOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	if output.reject.Swap(false) {
		return output.written, output.failure
	}
	return output.bufferOutput.WriteContext(ctx, data)
}

func TestOnlyZeroBytePureViewportOutputFailureRecovers(t *testing.T) {
	for _, kind := range []string{"zero", "partial", "joined"} {
		t.Run(kind, func(t *testing.T) {
			request := &ViewportResizeRequired{Columns: 20, Rows: 5}
			failure := errors.New("native descriptor cleanup failed")
			output := &resizeRaceOutput{failure: request}
			if kind == "partial" {
				output.written = 1
			}
			if kind == "joined" {
				output.failure = errors.Join(request, failure)
			}
			client, host, _ := newClientFixture(t, output)
			screen, rows := sizedSnapshot(t, 30, 6, "initial")
			host.snapshot(t, "1", screen, rows)
			host.await(t, protocol.SnapshotApplied)
			waitState(t, client, func(state State) bool { return state.Ready })
			acquire(t, client, host, "1")
			output.reject.Store(true)
			host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen, Rows: []protocol.ChangedRow{}})
			if kind != "zero" {
				awaitViewportFailure(t, client, request)
				if kind == "joined" && !errors.Is(client.Err(), failure) {
					t.Fatal("cleanup failure was hidden")
				}
				assertNoViewportAck(t, host)
				return
			}
			host.await(t, protocol.TerminalResize)
			host.await(t, protocol.ScreenResync)
			fitting, smallRows := snapshot(t, "fits")
			host.snapshot(t, "3", fitting, smallRows)
			host.await(t, protocol.SnapshotApplied)
			waitState(t, client, func(state State) bool { return state.Ready && !state.Resizing })
		})
	}
}

func TestResizeTakeoverCanResumeAsObserverWithoutNewMutation(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	if err := client.Resize(context.Background(), 30, 6); err != nil {
		t.Fatal(err)
	}
	host.await(t, protocol.TerminalResize)
	host.await(t, protocol.ScreenResync)
	other := testOther
	host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{ControllerConnectionID: &other, LeaseEpoch: "2"})
	otherScreen, rows := sizedSnapshot(t, 40, 7, "other controller")
	host.snapshot(t, "2", otherScreen, rows)
	host.await(t, protocol.SnapshotApplied)
	waitState(t, client, func(state State) bool { return state.Ready && !state.Controller && !state.Resizing })
	if err := client.Send(context.Background(), desktop.TextAction{Text: "old controller"}); err != ErrNotController {
		t.Fatalf("stale input: %v", err)
	}
	select {
	case request := <-host.requests:
		t.Fatalf("unexpected mutation %s", request.Type)
	default:
	}
}

func TestViewportTargetsCoalesceAndCannotExtendTransition(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	if err := client.resizeViewport(context.Background(), 30, 6); err != nil {
		t.Fatal(err)
	}
	host.await(t, protocol.TerminalResize)
	host.await(t, protocol.ScreenResync)
	deadline := client.pendingViewport().deadline
	if err := client.resizeViewport(context.Background(), 30, 6); err != nil {
		t.Fatal(err)
	}
	select {
	case duplicate := <-host.requests:
		t.Fatalf("identical target repeated %s", duplicate.Type)
	default:
	}
	for columns := 31; columns < 38; columns++ {
		if err := client.resizeViewport(context.Background(), columns, 6); err != nil {
			t.Fatal(err)
		}
		host.await(t, protocol.TerminalResize)
		host.await(t, protocol.ScreenResync)
		if client.pendingViewport().deadline != deadline {
			t.Fatal("changed target extended resize deadline")
		}
	}
	if err := client.resizeViewport(context.Background(), 38, 6); err == nil || !strings.Contains(err.Error(), "re-attach") {
		t.Fatalf("unbounded target changes: %v", err)
	}
	select {
	case extra := <-host.requests:
		t.Fatalf("over-limit resize emitted %s", extra.Type)
	default:
	}
}

func TestResizePublicationCannotOvertakeOldSnapshotPaintAndAck(t *testing.T) {
	var armed atomic.Bool
	entered, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	defer resumeOnce.Do(func() { close(resume) })
	client, host, _ := newClientFixture(t, &bufferOutput{}, func(config *Config) {
		config.ValidateScreen = func(screen protocol.ScreenMetadata) error {
			if armed.Load() && screen.Columns == 20 {
				close(entered)
				<-resume
			}
			return nil
		}
	})
	screen, rows := snapshot(t, "initial")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	waitState(t, client, func(state State) bool { return state.Ready })
	acquire(t, client, host, "1")
	client.resizeGate <- struct{}{}
	var gateOnce sync.Once
	releaseGate := func() { gateOnce.Do(func() { <-client.resizeGate }) }
	defer releaseGate()
	resized := make(chan error, 1)
	go func() { resized <- client.Resize(context.Background(), 30, 6) }()
	// Admit the public resize before SnapshotBegin changes readiness. The
	// explicit gate supplies ordering; stack inspection only observes entry.
	deadline := time.Now().Add(time.Second)
	for {
		stack := make([]byte, 1<<18)
		n := runtime.Stack(stack, true)
		if strings.Contains(string(stack[:n]), "(*Client).resizeViewport(") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resize was not admitted")
		}
		runtime.Gosched()
	}
	armed.Store(true)
	staleID := host.snapshot(t, "2", screen, rows)
	select {
	case <-entered:
	case <-time.After(40 * time.Millisecond):
	}
	releaseGate()
	host.await(t, protocol.TerminalResize)
	host.await(t, protocol.ScreenResync)
	if err := <-resized; err != nil {
		t.Fatal(err)
	}
	resumeOnce.Do(func() { close(resume) })
	newScreen, newRows := sizedSnapshot(t, 30, 6, "new target")
	id := host.snapshot(t, "3", newScreen, newRows)
	ack, _ := protocol.DecodePayload[protocol.SnapshotAppliedPayload](host.await(t, protocol.SnapshotApplied))
	if ack.SnapshotID == staleID || ack.SnapshotID != id {
		t.Fatal("old snapshot was acknowledged after resize/resync abandoned it")
	}
	waitState(t, client, func(state State) bool { return state.Ready && !state.Resizing })
}

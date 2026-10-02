package broker

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const testThumbnailConnection = "55555555-5555-4555-8555-555555555555"

// timedViewerSink records when each screen patch reached the connection.
type timedViewerSink struct {
	recordingViewerSink
	mu      sync.Mutex
	patches []time.Time
}

func newTimedViewerSink() *timedViewerSink {
	return &timedViewerSink{recordingViewerSink: *newRecordingViewerSink()}
}

func (sink *timedViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	if envelope, err := protocol.DecodeHost(message); err == nil && envelope.Type == protocol.ScreenPatch {
		sink.mu.Lock()
		sink.patches = append(sink.patches, time.Now())
		sink.mu.Unlock()
	}
	return sink.recordingViewerSink.Send(ctx, message, drained)
}

func (sink *timedViewerSink) patchTimes() []time.Time {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]time.Time(nil), sink.patches...)
}

// attachApplied attaches a viewer and applies its initial snapshot.
func attachApplied(t *testing.T, session *Session, connectionID string, rate int, sink ViewerSink, records *recordingViewerSink) *ViewerSubscription {
	t.Helper()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: connectionID, DeviceID: testViewerDevice, Generation: testGeneration,
		Sink: sink, MaxUpdatesPerSecond: rate,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = viewer.Close() })
	begin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](waitForHostMessage(t, records, protocol.SnapshotBegin, 1))
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, records, protocol.SnapshotEnd, 1)
	revision, _ := strconv.ParseUint(begin.Revision, 10, 64)
	if err := viewer.SnapshotApplied(context.Background(), begin.SnapshotID, revision); err != nil {
		t.Fatal(err)
	}
	return viewer
}

// waitForPatchRevision waits until the newest patch reaches revision.
func waitForPatchRevision(t *testing.T, sink *recordingViewerSink, revision uint64, within time.Duration) []protocol.ScreenPatchPayload {
	t.Helper()
	want := strconv.FormatUint(revision, 10)
	deadline := time.Now().Add(within)
	for {
		patches := decodedPatches(t, sink)
		if len(patches) > 0 && patches[len(patches)-1].Revision == want {
			return patches
		}
		if time.Now().After(deadline) {
			t.Fatalf("viewer never reached revision %s", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Under a flood a thumbnail viewer gets at most its rate of frames, each patch
// chained from its last delivered revision, and it ends on the canonical
// screen. A normal viewer of the same session keeps its full rate.
func TestThumbnailViewerIsRateLimitedAndEndsOnCanonicalScreen(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	const rate = 5
	normal := newRecordingViewerSink()
	thumbnail := newTimedViewerSink()
	attachApplied(t, session, testViewerConnection, 0, normal, normal)
	attachApplied(t, session, testThumbnailConnection, rate, thumbnail, &thumbnail.recordingViewerSink)

	const changes = 300
	started := time.Now()
	for index := range changes {
		process.Emit([]byte("C" + strconv.Itoa(index)))
		time.Sleep(4 * time.Millisecond)
	}
	final := waitForTerminalRevision(t, session, changes)
	flood := time.Since(started)

	for _, sink := range []*recordingViewerSink{normal, &thumbnail.recordingViewerSink} {
		patches := waitForPatchRevision(t, sink, final.Revision, 3*time.Second)
		checkPatchChain(t, patches)
		if !patchContainsText(patches[len(patches)-1], "C"+strconv.Itoa(changes-1)) {
			t.Fatal("last patch does not show the canonical screen")
		}
		if snapshots := countHostMessages(t, sink, protocol.SnapshotBegin); snapshots != 1 {
			t.Fatalf("viewer received %d snapshots, want only the initial one", snapshots)
		}
	}

	times := thumbnail.patchTimes()
	// Reservations are at least 1/rate apart; allow one extra for send jitter.
	interval := time.Second / rate
	if limit := int(times[len(times)-1].Sub(times[0])/interval) + 2; len(times) > limit {
		t.Fatalf("thumbnail received %d patches in %v, want at most %d", len(times), times[len(times)-1].Sub(times[0]), limit)
	}
	if limit := int(flood.Seconds()*rate) + 2; len(times) > limit {
		t.Fatalf("thumbnail received %d patches during a %v flood, want at most %d", len(times), flood, limit)
	}
	normalPatches := len(decodedPatches(t, normal))
	t.Logf("flood=%v normal patches=%d thumbnail patches=%d", flood, normalPatches, len(times))
	if normalPatches < 3*len(times) {
		t.Fatalf("normal viewer received %d patches, thumbnail %d: the normal viewer was slowed", normalPatches, len(times))
	}
}

// Output that stops inside a throttled slot still paints when the next slot
// opens. Agent and folder scans also wake viewers in a live session, so
// TestThumbnailSkippedFrameArmsOneTrailingTimer proves the timer in isolation.
func TestThumbnailTrailingTimerPaintsFinalFrame(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	thumbnail := newRecordingViewerSink()
	attachApplied(t, session, testThumbnailConnection, 4, thumbnail, thumbnail)
	// The initial snapshot used the open slot, so this whole burst is throttled.
	for index := range 10 {
		process.Emit([]byte("B" + strconv.Itoa(index)))
	}
	final := waitForTerminalRevision(t, session, 10)
	patches := waitForPatchRevision(t, thumbnail, final.Revision, 700*time.Millisecond)
	checkPatchChain(t, patches)
	if len(patches) > 2 || !patchContainsText(patches[len(patches)-1], "B9") {
		t.Fatalf("thumbnail got %d patches ending at %#v, want the final frame in at most 2", len(patches), patches[len(patches)-1])
	}
	canonical, err := session.Snapshot(context.Background())
	if err != nil || patches[len(patches)-1].Revision != strconv.FormatUint(canonical.Revision, 10) {
		t.Fatalf("thumbnail ended at %s, canonical %d (%v)", patches[len(patches)-1].Revision, canonical.Revision, err)
	}
}

// Without an actor loop nothing else wakes the viewer, so only the trailing
// timer can deliver the skipped frame. One timer serves every skip, the next
// patch spans all skipped revisions, and detach stops the timer.
func TestThumbnailSkippedFrameArmsOneTrailingTimer(t *testing.T) {
	controller, err := NewController(testGeneration)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := NewInputInbox(controller, DefaultInputQueueCapacity)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &Session{generation: testGeneration, controller: controller, inbox: inbox, ctx: ctx, viewers: map[string]*viewerActorState{}}
	const interval = 100 * time.Millisecond
	viewerContext, viewerCancel := context.WithCancel(ctx)
	subscription := &ViewerSubscription{
		session: session, connectionID: testThumbnailConnection, frameInterval: interval,
		notify: make(chan struct{}, 1), detached: make(chan struct{}), ctx: viewerContext, cancel: viewerCancel,
	}
	publish := func(revision uint64) {
		session.published = &publishedFrame{frame: viewerFrame(revision, 20, 5, "R"+strconv.FormatUint(revision, 10))}
	}
	publish(0)
	viewer := &viewerActorState{subscription: subscription, base: session.published, clippedRows: make([]bool, 5)}
	session.viewers[testThumbnailConnection] = viewer
	reserve := func() viewerDeliveryPlan {
		t.Helper()
		plan, err := session.reserveViewerDelivery(testThumbnailConnection, subscription)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	publish(1)
	started := time.Now()
	if plan := reserve(); plan.kind != viewerDeliveryPatch || plan.target.frame.Revision != 1 {
		t.Fatalf("open slot plan = %#v", plan)
	}
	viewer.inFlight, viewer.base = nil, session.published
	publish(2)
	if plan := reserve(); plan.kind != viewerDeliveryNone || viewer.frameTimer == nil {
		t.Fatalf("closed slot plan = %#v, timer %v", plan, viewer.frameTimer)
	}
	timer := viewer.frameTimer
	publish(3)
	if plan := reserve(); plan.kind != viewerDeliveryNone || viewer.frameTimer != timer {
		t.Fatal("a second skip did not reuse the one trailing timer")
	}
	select {
	case <-subscription.notify:
	case <-time.After(time.Second):
		t.Fatal("trailing timer never woke the viewer")
	}
	if elapsed := time.Since(started); elapsed < interval {
		t.Fatalf("trailing timer fired after %v, before the %v slot", elapsed, interval)
	}
	if plan := reserve(); plan.kind != viewerDeliveryPatch || plan.base.frame.Revision != 1 || plan.target.frame.Revision != 3 {
		t.Fatalf("trailing plan = %#v, want patch 1 -> 3", plan)
	}
	viewer.inFlight, viewer.base = nil, session.published
	publish(4)
	if plan := reserve(); plan.kind != viewerDeliveryNone {
		t.Fatalf("closed slot plan = %#v", plan)
	}
	session.detachViewer(testThumbnailConnection, nil)
	if viewer.frameTimer.Stop() {
		t.Fatal("detach left the trailing timer armed")
	}
}

func TestThumbnailViewerCannotTakeControl(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer := attachApplied(t, session, testThumbnailConnection, 5, sink, sink)
	if _, err := session.AcquireControl(context.Background(), viewer); !errors.Is(err, ErrViewerReadOnly) {
		t.Fatalf("thumbnail acquire = %v, want %v", err, ErrViewerReadOnly)
	}
	for _, rate := range []int{-1, protocol.MaxThumbnailUpdatesPerSecond + 1} {
		if _, err := session.AttachViewer(context.Background(), ViewerConfig{
			ConnectionID: testViewerConnection, DeviceID: testViewerDevice, Generation: testGeneration,
			Sink: newRecordingViewerSink(), MaxUpdatesPerSecond: rate,
		}); err == nil {
			t.Fatalf("rate %d was accepted", rate)
		}
	}
}

// Over the session stream a thumbnail connection is refused control, input,
// answers and resize, even when it presents another connection's live lease.
func TestSessionStreamThumbnailIsReadOnly(t *testing.T) {
	fixture, _, process := newStreamFixture(t)
	full := startStreamClient(t, fixture.server, protocol.CLI)
	full.attach(t)
	grant := full.acquire(t)

	thumb := startStreamClient(t, fixture.server, protocol.CLI)
	thumb.await(t, protocol.Welcome, "")
	thumb.action(t, protocol.SessionAttach, protocol.SessionAttachPayload{Thumbnail: &protocol.ThumbnailMode{MaxUpdatesPerSecond: 5}})
	begin, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](thumb.await(t, protocol.SnapshotBegin, ""))
	thumb.await(t, protocol.SnapshotEnd, "")
	thumb.action(t, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})

	request := thumb.action(t, protocol.ControlAcquire, protocol.EmptyPayload{})
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](thumb.await(t, protocol.Error, request)); failure.Code != protocol.NotController {
		t.Fatalf("thumbnail control = %#v", failure)
	}
	request = thumb.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "x"})
	if rejected, _ := protocol.DecodePayload[protocol.InputRejectedPayload](thumb.await(t, protocol.InputRejected, request)); rejected.Code != protocol.NotController {
		t.Fatalf("thumbnail input = %#v", rejected)
	}
	request = thumb.action(t, protocol.InputAnswer, protocol.InputAnswerPayload{
		Lease: *grant.Lease, InputSequence: "1", QuestionID: "0123456789abcdef0123456789abcdef", OptionIndex: 0,
	})
	if rejected, _ := protocol.DecodePayload[protocol.InputRejectedPayload](thumb.await(t, protocol.InputRejected, request)); rejected.Code != protocol.NotController {
		t.Fatalf("thumbnail answer = %#v", rejected)
	}
	request = thumb.action(t, protocol.TerminalResize, protocol.TerminalResizePayload{Lease: *grant.Lease, Columns: 100, Rows: 30})
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](thumb.await(t, protocol.Error, request)); failure.Code != protocol.NotController {
		t.Fatalf("thumbnail resize = %#v", failure)
	}
	// The real controller is untouched.
	request = full.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "ok"})
	full.await(t, protocol.InputAccepted, request)
	waitStreamInput(t, process, "ok")
}

// Detach and session close stop the trailing timer; nothing is left running.
func TestThumbnailTimerLeavesNoGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	session, process := startViewerSession(t)
	sink := newRecordingViewerSink()
	viewer := attachApplied(t, session, testThumbnailConnection, 1, sink, sink)
	process.Emit([]byte("pending")) // throttled: arms the one-second timer
	waitForTerminalRevision(t, session, 1)
	time.Sleep(50 * time.Millisecond)
	if err := viewer.Close(); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			buffer := make([]byte, 1<<20)
			t.Fatalf("goroutines = %d, baseline %d\n%s", runtime.NumGoroutine(), baseline, buffer[:runtime.Stack(buffer, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

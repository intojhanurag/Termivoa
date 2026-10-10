package broker

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// Count every full screen copy, including the independent agent-state scans.
type countingViewerTerminal struct {
	*viewerTerminal
	captures atomic.Int64
}

func TestViewerCaptureDeadlineFollowsEligibility(t *testing.T) {
	dirty := time.Now()
	thumbnail := &viewerActorState{nextFrameAt: dirty.Add(time.Second)}
	normal := &viewerActorState{}
	session := &Session{captureAfter: dirty, viewers: map[string]*viewerActorState{"thumbnail": thumbnail}}
	if got := session.nextViewerCaptureAt(); !got.Equal(thumbnail.nextFrameAt) {
		t.Fatalf("thumbnail deadline = %v", got)
	}
	session.viewers["normal"] = normal
	if got := session.nextViewerCaptureAt(); !got.Equal(dirty) {
		t.Fatalf("new normal viewer deadline = %v", got)
	}
	normal.inFlight = &publishedFrame{}
	if got := session.nextViewerCaptureAt(); !got.Equal(thumbnail.nextFrameAt) {
		t.Fatalf("blocked normal deadline = %v", got)
	}
	thumbnail.awaiting = &viewerSnapshot{}
	if got := session.nextViewerCaptureAt(); !got.IsZero() {
		t.Fatalf("all blocked deadline = %v", got)
	}
	normal.inFlight = nil
	normal.noticeInFlight = &viewerNotice{}
	if got := session.nextViewerCaptureAt(); !got.IsZero() {
		t.Fatalf("blocked notice deadline = %v", got)
	}
	normal.noticeInFlight = nil
	normal.notices = []*viewerNotice{{}}
	if got := session.nextViewerCaptureAt(); !got.IsZero() {
		t.Fatalf("queued notice deadline = %v", got)
	}
	normal.notices = nil
	if got := session.nextViewerCaptureAt(); !got.Equal(dirty) {
		t.Fatalf("released normal deadline = %v", got)
	}
	delete(session.viewers, "normal")
	thumbnail.awaiting = nil
	if got := session.nextViewerCaptureAt(); !got.Equal(thumbnail.nextFrameAt) {
		t.Fatalf("detached normal deadline = %v", got)
	}
	delete(session.viewers, "thumbnail")
	if got := session.nextViewerCaptureAt(); !got.IsZero() {
		t.Fatalf("no viewers deadline = %v", got)
	}
}

func TestViewerSnapshotCatchupCapturesDirtyOutputOnce(t *testing.T) {
	core := &countingViewerTerminal{viewerTerminal: newViewerTerminal()}
	subscription := &ViewerSubscription{}
	session := &Session{ctx: context.Background(), terminal: core, viewers: map[string]*viewerActorState{
		"viewer": {subscription: subscription, inFlight: &publishedFrame{}, inFlightEpoch: 7},
	}}
	if err := core.WriteOutput(context.Background(), []byte("latest")); err != nil {
		t.Fatal(err)
	}
	session.captureAfter = time.Now().Add(framePublishInterval)
	if _, err := session.latestViewerFrame("viewer", subscription, 6); err == nil {
		t.Fatal("stale delivery accepted")
	}
	if core.captures.Load() != 0 {
		t.Fatal("stale delivery copied a screen")
	}
	for range 2 {
		frame, err := session.latestViewerFrame("viewer", subscription, 7)
		if err != nil || frame.frame.Revision != 1 || frame.frame.Screen[0].Cells[0].Text != "latest" {
			t.Fatalf("catch-up frame = %#v, error=%v", frame, err)
		}
	}
	if got := core.captures.Load(); got != 1 {
		t.Fatalf("catch-up captures=%d, want one", got)
	}
}

type blockedPatchViewerSink struct {
	recordingViewerSink
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (sink *blockedPatchViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	if envelope, err := protocol.DecodeHost(message); err == nil && envelope.Type == protocol.ScreenPatch {
		sink.once.Do(func() { close(sink.started) })
		select {
		case <-sink.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sink.recordingViewerSink.Send(ctx, message, drained)
}

func TestViewerCaptureStopsWhilePatchSinkIsBlocked(t *testing.T) {
	core := &countingViewerTerminal{viewerTerminal: newViewerTerminal()}
	session, process := startViewerSessionWithTerminal(t, core)
	defer session.Close()
	sink := &blockedPatchViewerSink{recordingViewerSink: *newRecordingViewerSink(), started: make(chan struct{}), release: make(chan struct{})}
	attachApplied(t, session, testViewerConnection, 0, sink, &sink.recordingViewerSink)
	process.Emit([]byte("first"))
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("patch did not start")
	}
	before, started := core.captures.Load(), time.Now()
	final := floodCaptureSession(t, session, process)
	elapsed, captures := time.Since(started), core.captures.Load()-before
	if captures > int64(elapsed/agentScanInterval)+2 {
		t.Fatalf("blocked patch copied %d screens in %v", captures, elapsed)
	}
	close(sink.release)
	patches := waitForPatchRevision(t, &sink.recordingViewerSink, final.Revision, time.Second)
	checkPatchChain(t, patches)
	if !patchContainsText(patches[len(patches)-1], "C299") {
		t.Fatal("released patch lost dirty final frame")
	}
}

func TestViewerCaptureDetachAndReattachGetsCurrentScreen(t *testing.T) {
	core := &countingViewerTerminal{viewerTerminal: newViewerTerminal()}
	session, process := startViewerSessionWithTerminal(t, core)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer := attachApplied(t, session, testViewerConnection, 5, sink, sink)
	if err := viewer.Close(); err != nil {
		t.Fatal(err)
	}
	before, started := core.captures.Load(), time.Now()
	final := floodCaptureSession(t, session, process)
	elapsed, captures := time.Since(started), core.captures.Load()-before
	if captures > int64(elapsed/agentScanInterval)+2 {
		t.Fatalf("detached viewer caused %d screen copies", captures)
	}
	replacement := newRecordingViewerSink()
	attachApplied(t, session, testViewerConnection, 5, replacement, replacement)
	begin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](waitForHostMessage(t, replacement, protocol.SnapshotBegin, 1))
	if err != nil || begin.Revision != strconv.FormatUint(final.Revision, 10) {
		t.Fatalf("reattach snapshot revision=%s error=%v", begin.Revision, err)
	}
}

func (core *countingViewerTerminal) ScreenSnapshot(ctx context.Context) (terminal.Frame, error) {
	core.captures.Add(1)
	return core.viewerTerminal.ScreenSnapshot(ctx)
}

func floodCaptureSession(t *testing.T, session *Session, process *viewerProcess) terminal.Frame {
	t.Helper()
	for index := range 300 {
		process.Emit([]byte("C" + strconv.Itoa(index)))
		time.Sleep(4 * time.Millisecond)
	}
	return waitForTerminalRevision(t, session, 300)
}

func TestViewerCaptureFollowsReadyViewerRate(t *testing.T) {
	for _, mode := range []string{"none", "thumbnail", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			core := &countingViewerTerminal{viewerTerminal: newViewerTerminal()}
			session, process := startViewerSessionWithTerminal(t, core)
			defer session.Close()
			var sinks []*recordingViewerSink
			if mode != "none" {
				sink := newRecordingViewerSink()
				attachApplied(t, session, testThumbnailConnection, 5, sink, sink)
				sinks = append(sinks, sink)
			}
			if mode == "mixed" {
				sink := newRecordingViewerSink()
				attachApplied(t, session, testViewerConnection, 0, sink, sink)
				sinks = append(sinks, sink)
			}
			before, started := core.captures.Load(), time.Now()
			final := floodCaptureSession(t, session, process)
			for _, sink := range sinks {
				patches := waitForPatchRevision(t, sink, final.Revision, time.Second)
				checkPatchChain(t, patches)
				if !patchContainsText(patches[len(patches)-1], "C299") {
					t.Fatal("viewer missed the final canonical screen")
				}
			}
			elapsed, captures := time.Since(started), core.captures.Load()-before
			t.Logf("mode=%s duration=%v full captures=%d captures/sec=%.1f", mode, elapsed, captures, float64(captures)/elapsed.Seconds())
			switch mode {
			case "none":
				if captures > int64(elapsed/agentScanInterval)+2 {
					t.Fatal("output without viewers copied publication screens")
				}
			case "thumbnail":
				// Five publication slots/sec plus independent agent scans and
				// two boundary slots. A post-capture rate cap fails this bound.
				limit := int64(elapsed/(time.Second/5) + elapsed/agentScanInterval + 2)
				if captures > limit {
					t.Fatalf("full captures=%d, thumbnail plus agent scan bound=%d", captures, limit)
				}
			case "mixed":
				if patches := len(decodedPatches(t, sinks[1])); patches < 20 {
					t.Fatalf("normal viewer slowed to thumbnail rate: %d patches", patches)
				}
			}
		})
	}
}

func TestViewerCaptureWaitsForSnapshotAcknowledgement(t *testing.T) {
	core := &countingViewerTerminal{viewerTerminal: newViewerTerminal()}
	session, process := startViewerSessionWithTerminal(t, core)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice, Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = viewer.Close() })
	begin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](waitForHostMessage(t, sink, protocol.SnapshotBegin, 1))
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	before, started := core.captures.Load(), time.Now()
	final := floodCaptureSession(t, session, process)
	elapsed, captures := time.Since(started), core.captures.Load()-before
	if captures > int64(elapsed/agentScanInterval)+2 {
		t.Fatalf("unacknowledged viewer copied %d screens in %v", captures, elapsed)
	}
	if len(decodedPatches(t, sink)) != 0 {
		t.Fatal("patch preceded snapshot acknowledgement")
	}
	revision, _ := strconv.ParseUint(begin.Revision, 10, 64)
	if err := viewer.SnapshotApplied(context.Background(), begin.SnapshotID, revision); err != nil {
		t.Fatal(err)
	}
	patches := waitForPatchRevision(t, sink, final.Revision, time.Second)
	checkPatchChain(t, patches)
	if !patchContainsText(patches[len(patches)-1], "C299") {
		t.Fatal("acknowledgement lost dirty final frame")
	}
}

// Measure the actual canonical grid copy avoided by deferring publication.
// This is per capture, not a wall-clock broker or agent-state benchmark.
func BenchmarkViewerPublicationCapture(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {200, 60}, {400, 200}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			ctx := context.Background()
			core, err := terminal.New(ctx, terminal.Config{Columns: size[0], Rows: size[1]})
			if err != nil {
				b.Fatal(err)
			}
			defer core.Close()
			session := &Session{ctx: ctx, terminal: core}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := session.capturePublished(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

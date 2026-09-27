package broker

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// slowViewerSink drains each message only after delay, like a connection
// whose reader has fallen behind.
type slowViewerSink struct {
	recordingViewerSink
	delay time.Duration
}

func (sink *slowViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	select {
	case <-time.After(sink.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return sink.recordingViewerSink.Send(ctx, message, drained)
}

// A slow viewer gets fewer patches than a fast one, each coalescing every
// change since its last delivered revision, and it still ends on the final
// revision. Neither viewer slows the output.
func TestSlowViewerReceivesCoalescedPatchesWithExactRevisions(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	fast := newRecordingViewerSink()
	slow := &slowViewerSink{recordingViewerSink: *newRecordingViewerSink(), delay: 100 * time.Millisecond}
	sinks := []*recordingViewerSink{fast, &slow.recordingViewerSink}
	for index, sink := range []ViewerSink{fast, slow} {
		viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
			ConnectionID: fmt.Sprintf("4444444%d-4444-4444-8444-444444444444", index), DeviceID: testViewerDevice,
			Generation: testGeneration, Sink: sink,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer viewer.Close()
		begin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](waitForHostMessage(t, sinks[index], protocol.SnapshotBegin, 1))
		if err != nil {
			t.Fatal(err)
		}
		waitForHostMessage(t, sinks[index], protocol.SnapshotEnd, 1)
		if err := viewer.SnapshotApplied(context.Background(), begin.SnapshotID, 0); err != nil {
			t.Fatal(err)
		}
	}

	const changes = 200
	started := time.Now()
	for index := range changes {
		process.Emit([]byte("C" + strconv.Itoa(index)))
		time.Sleep(5 * time.Millisecond)
	}
	final := waitForTerminalRevision(t, session, changes)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("output took %v; a slow viewer must not backpressure it", elapsed)
	}
	counts := make([]int, len(sinks))
	for index, sink := range sinks {
		deadline := time.Now().Add(5 * time.Second)
		for {
			patches := decodedPatches(t, sink)
			if len(patches) > 0 && patches[len(patches)-1].Revision == strconv.FormatUint(final.Revision, 10) {
				checkPatchChain(t, patches)
				if !patchContainsText(patches[len(patches)-1], "C199") {
					t.Fatal("last patch does not show the final output")
				}
				counts[index] = len(patches)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("viewer %d never reached revision %d", index, final.Revision)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if snapshots := countHostMessages(t, sink, protocol.SnapshotBegin); snapshots != 1 {
			t.Fatalf("viewer %d received %d snapshots, want only the initial one", index, snapshots)
		}
	}
	t.Logf("changes=%d fast patches=%d slow patches=%d", changes, counts[0], counts[1])
	if counts[1] >= counts[0]/2 {
		t.Fatalf("slow viewer received %d patches, fast %d: updates were not coalesced", counts[1], counts[0])
	}
}

func decodedPatches(t *testing.T, sink *recordingViewerSink) []protocol.ScreenPatchPayload {
	t.Helper()
	var patches []protocol.ScreenPatchPayload
	for _, message := range sink.snapshot() {
		envelope, err := protocol.DecodeHost(message)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Type != protocol.ScreenPatch {
			continue
		}
		patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](envelope)
		if err != nil {
			t.Fatal(err)
		}
		patches = append(patches, patch)
	}
	return patches
}

// checkPatchChain requires each patch to start at the previous revision,
// beginning at the applied snapshot revision 0, and to move forward.
func checkPatchChain(t *testing.T, patches []protocol.ScreenPatchPayload) {
	t.Helper()
	base := uint64(0)
	for index, patch := range patches {
		from, errFrom := strconv.ParseUint(patch.BaseRevision, 10, 64)
		to, errTo := strconv.ParseUint(patch.Revision, 10, 64)
		if errFrom != nil || errTo != nil || from != base || to <= from {
			t.Fatalf("patch %d = %s -> %s, want base %d and a later revision", index, patch.BaseRevision, patch.Revision, base)
		}
		base = to
	}
}

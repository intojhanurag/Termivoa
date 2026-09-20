package broker

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	testViewerConnection = "44444444-4444-4444-8444-444444444444"
	testViewerDevice     = "55555555-5555-4555-8555-555555555555"
)

func TestViewerSnapshotBoundaryAndExactPatchBase(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	beginPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)

	process.Emit([]byte("A"))
	waitForTerminalRevision(t, session, 1)
	time.Sleep(3 * framePublishInterval)
	if countHostMessages(t, sink, protocol.ScreenPatch) != 0 {
		t.Fatal("patch was published before snapshot.applied")
	}
	if err := viewer.SnapshotApplied(context.Background(), beginPayload.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}
	patchEnvelope := waitForHostMessage(t, sink, protocol.ScreenPatch, 1)
	patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](patchEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if patch.BaseRevision != "0" || patch.Revision != "1" {
		t.Fatalf("patch revisions = %s -> %s, want 0 -> 1", patch.BaseRevision, patch.Revision)
	}

	grant, err := session.AcquireControl(context.Background(), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resize(context.Background(), MutationIdentity{
		Generation: testGeneration, ConnectionID: testViewerConnection,
		DeviceID: testViewerDevice, Lease: grant.Lease,
	}, 30, 5); err != nil {
		t.Fatal(err)
	}
	resizedEnvelope := waitForHostMessage(t, sink, protocol.SnapshotBegin, 2)
	resized, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](resizedEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if resized.Screen.Columns != 30 || resized.Revision != "2" {
		t.Fatalf("resize snapshot = revision %s, columns %d", resized.Revision, resized.Screen.Columns)
	}
}

func TestHistoryResetSignalFollowsViewerBaseEpoch(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	beginPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	if err := viewer.SnapshotApplied(context.Background(), beginPayload.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}

	process.Emit([]byte("HISTORY_RESET"))
	patchEnvelope := waitForHostMessage(t, sink, protocol.ScreenPatch, 1)
	patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](patchEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.HistoryReset || patch.Screen.HistoryEpoch != "1" || patch.Screen.HistoryRevision != "0" {
		t.Fatalf("reset patch = %#v", patch)
	}

	process.Emit([]byte("ordinary"))
	nextEnvelope := waitForHostMessage(t, sink, protocol.ScreenPatch, 2)
	next, err := protocol.DecodePayload[protocol.ScreenPatchPayload](nextEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if next.HistoryReset {
		t.Fatalf("history reset repeated after viewer base advanced: %#v", next)
	}
}

func TestSessionHistoryReadChecksGenerationInsideActor(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	result, err := session.ReadHistory(context.Background(), testGeneration, terminal.HistoryReadRequest{
		Epoch: 0, Revision: 0, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || !result.Complete || result.Columns != 20 {
		t.Fatalf("history result = %#v", result)
	}
	if _, err := session.ReadHistory(context.Background(), "stale-generation", terminal.HistoryReadRequest{
		Limit: 1,
	}); !errors.Is(err, ErrGenerationChanged) {
		t.Fatalf("stale generation error = %v", err)
	}
}

func TestViewerResyncInterruptsSnapshotAndWrongAckForcesFreshSnapshot(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newGateViewerSink(2)
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	select {
	case <-sink.blocked:
	case <-time.After(time.Second):
		t.Fatal("snapshot row delivery did not block")
	}
	if err := viewer.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstBegin := waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotBegin, 1)
	firstPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](firstBegin)
	if err != nil {
		t.Fatal(err)
	}
	secondBegin := waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotBegin, 2)
	secondPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](secondBegin)
	if err != nil {
		t.Fatal(err)
	}
	if secondPayload.SnapshotID == firstPayload.SnapshotID {
		t.Fatal("resync reused an interrupted snapshot identity")
	}
	waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotEnd, 1)

	err = viewer.SnapshotApplied(context.Background(), firstPayload.SnapshotID, 0)
	if !errors.Is(err, ErrViewerSnapshotMismatch) {
		t.Fatalf("wrong acknowledgement error = %v", err)
	}
	thirdBegin := waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotBegin, 3)
	thirdPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](thirdBegin)
	if err != nil {
		t.Fatal(err)
	}
	if thirdPayload.SnapshotID == secondPayload.SnapshotID {
		t.Fatal("base mismatch did not create a fresh snapshot identity")
	}
}

func TestStalledViewerDisconnectsWithoutBackpressuringCanonicalState(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := blockingViewerSink{}
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SendTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 50; index++ {
		process.Emit([]byte("x"))
	}
	waitForTerminalRevision(t, session, 50)
	time.Sleep(2 * framePublishInterval)
	if pending := len(viewer.notify); pending != 1 {
		t.Fatalf("coalesced pending screen notifications = %d, want 1", pending)
	}
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("stalled viewer was not disconnected")
	}
	if !errors.Is(viewer.Err(), ErrViewerSlow) {
		t.Fatalf("viewer error = %v", viewer.Err())
	}
	viewer.mu.Lock()
	peak := viewer.budgetPeak
	viewer.mu.Unlock()
	if peak <= 0 || peak > MaxViewerQueueBytes {
		t.Fatalf("viewer budget peak = %d, limit %d", peak, MaxViewerQueueBytes)
	}

	frame, err := session.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.Revision < 50 {
		t.Fatalf("canonical revision = %d, want at least 50", frame.Revision)
	}
}

func TestAcceptedViewerMessageRemainsBudgetedUntilDownstreamDrain(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newDelayedDrainViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SendTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sink.accepted:
	case <-time.After(time.Second):
		t.Fatal("sink never accepted the first viewer message")
	}
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("accepted but undrained message did not disconnect the viewer")
	}
	if !errors.Is(viewer.Err(), ErrViewerSlow) {
		t.Fatalf("viewer error = %v", viewer.Err())
	}
	viewer.mu.Lock()
	usedBeforeDrain := viewer.budgetUsed
	viewer.mu.Unlock()
	if usedBeforeDrain <= 0 || usedBeforeDrain > MaxViewerQueueBytes {
		t.Fatalf("reserved downstream bytes = %d, limit %d", usedBeforeDrain, MaxViewerQueueBytes)
	}
	sink.drain()
	viewer.mu.Lock()
	usedAfterDrain := viewer.budgetUsed
	viewer.mu.Unlock()
	if usedAfterDrain != 0 {
		t.Fatalf("reserved downstream bytes after drain = %d, want 0", usedAfterDrain)
	}
}

func TestSnapshotAcknowledgementTimeoutReleasesViewerSlot(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SnapshotAckTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("unacknowledged snapshot did not time out")
	}
	if !errors.Is(viewer.Err(), ErrViewerSlow) || !errors.Is(viewer.Err(), ErrViewerSnapshotTimeout) {
		t.Fatalf("viewer timeout error = %v", viewer.Err())
	}

	replacement, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: newRecordingViewerSink(),
	})
	if err != nil {
		t.Fatalf("reattach after acknowledgement timeout: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotTimeoutSurvivesSaturatedPublicRequestQueue(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SnapshotAckTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	// Let the worker's completion request establish the actor-owned deadline
	// before deliberately blocking the next actor operation.
	time.Sleep(20 * time.Millisecond)
	grant, err := session.AcquireControl(context.Background(), viewer)
	if err != nil {
		t.Fatal(err)
	}
	resizeStarted, releaseResize := process.blockResize()
	resizeDone := make(chan error, 1)
	go func() {
		resizeDone <- session.Resize(context.Background(), MutationIdentity{
			Generation: testGeneration, ConnectionID: testViewerConnection,
			DeviceID: testViewerDevice, Lease: grant.Lease,
		}, 21, 5)
	}()
	<-resizeStarted
	fillPublicRequestQueue(t, session)
	process.release()
	time.Sleep(1100 * time.Millisecond)
	releaseResize()
	if err := <-resizeDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("actor-owned acknowledgement timeout was lost behind the saturated public queue")
	}
	if !errors.Is(viewer.Err(), ErrViewerSnapshotTimeout) {
		t.Fatalf("viewer timeout error = %v", viewer.Err())
	}
	select {
	case <-session.RetirementReady():
	case <-time.After(2 * time.Second):
		t.Fatal("unacknowledged viewer blocked session retirement after its deadline")
	}
}

func TestFailedViewerDetachSurvivesSaturatedPublicRequestQueue(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := newFailGateViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SendTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := session.AcquireControl(context.Background(), viewer)
	if err != nil {
		t.Fatal(err)
	}
	<-sink.started
	resizeStarted, releaseResize := process.blockResize()
	resizeDone := make(chan error, 1)
	go func() {
		resizeDone <- session.Resize(context.Background(), MutationIdentity{
			Generation: testGeneration, ConnectionID: testViewerConnection,
			DeviceID: testViewerDevice, Lease: grant.Lease,
		}, 21, 5)
	}()
	<-resizeStarted
	fillPublicRequestQueue(t, session)
	close(sink.fail)
	time.Sleep(1100 * time.Millisecond)
	releaseResize()
	if err := <-resizeDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("failed viewer did not detach through the internal lifecycle lane")
	}
	replacement, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: newRecordingViewerSink(),
	})
	if err != nil {
		t.Fatalf("failed viewer retained its slot after public queue saturation: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFinalPublishedFrameDrainsBeforeSessionRetirement(t *testing.T) {
	session, process := startViewerSession(t)
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	beginPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	if err := viewer.SnapshotApplied(context.Background(), beginPayload.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() {
		<-session.Ended()
		<-session.RetirementReady()
		closed <- session.Close()
	}()
	process.Emit([]byte("FINAL"))
	waitForTerminalRevision(t, session, 1)
	process.release()
	patchEnvelope := waitForHostMessage(t, sink, protocol.ScreenPatch, 1)
	patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](patchEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if !patchContainsText(patch, "FINAL") {
		t.Fatalf("final patch omitted FINAL: %#v", patch.Rows)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("session retirement did not complete after final patch drained")
	}
	<-viewer.Done()
}

func TestContentClippedPersistsAcrossPatchForUnchangedRow(t *testing.T) {
	core := newViewerTerminal()
	core.frame.Screen[1].Cells[0].Text = strings.Repeat("x", protocol.MaxEncodedTerminalRowBytes)
	session, process := startViewerSessionWithTerminal(t, core)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	beginPayload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	if !beginPayload.Screen.ContentClipped {
		t.Fatal("initial snapshot did not report clipped content")
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	if err := viewer.SnapshotApplied(context.Background(), beginPayload.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}
	process.Emit([]byte("changed-other-row"))
	waitForTerminalRevision(t, session, 1)
	patchEnvelope := waitForHostMessage(t, sink, protocol.ScreenPatch, 1)
	patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](patchEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.Screen.ContentClipped {
		t.Fatal("patch cleared contentClipped while the clipped row remained unchanged")
	}
}

func TestNoViewerOutputProgressesAndAttachGetsCurrentSnapshot(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	for index := 0; index < 25; index++ {
		process.Emit([]byte("n"))
	}
	waitForTerminalRevision(t, session, 25)

	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	payload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Revision != "25" {
		t.Fatalf("attach revision = %s, want 25", payload.Revision)
	}
}

func TestViewerCountIsBounded(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	viewers := make([]*ViewerSubscription, 0, MaxViewersPerSession)
	for index := 0; index < MaxViewersPerSession; index++ {
		connectionID := "44444444-4444-4444-8444-4444444444" + string("0123456789abcdef"[index:index+1]) + "0"
		viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
			ConnectionID: connectionID, DeviceID: testViewerDevice,
			Generation: testGeneration, Sink: newRecordingViewerSink(),
		})
		if err != nil {
			t.Fatalf("attach viewer %d: %v", index, err)
		}
		viewers = append(viewers, viewer)
	}
	defer func() {
		for _, viewer := range viewers {
			_ = viewer.Close()
		}
	}()
	_, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: "66666666-6666-4666-8666-666666666666", DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: newRecordingViewerSink(),
	})
	if !errors.Is(err, ErrViewerLimit) {
		t.Fatalf("viewer limit error = %v", err)
	}
}

func TestViewerCloseReleasesMatchingControllerLease(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: newRecordingViewerSink(),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := session.AcquireControl(context.Background(), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := viewer.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = session.SubmitInput(context.Background(), MutationIdentity{
		Generation: testGeneration, ConnectionID: testViewerConnection,
		DeviceID: testViewerDevice, Lease: grant.Lease,
	}, 1, InputAction{Kind: InputKindText, Text: "stale"})
	if !errors.Is(err, ErrNotController) {
		t.Fatalf("input after viewer close error = %v", err)
	}
}

func TestDetachedViewerHandleCannotMutateReplacementWithReusedConnectionID(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	oldSink := newRecordingViewerSink()
	oldViewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: oldSink,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldBeginEnvelope := waitForHostMessage(t, oldSink, protocol.SnapshotBegin, 1)
	oldBegin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](oldBeginEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, oldSink, protocol.SnapshotEnd, 1)
	if err := oldViewer.Close(); err != nil {
		t.Fatal(err)
	}

	replacementSink := newRecordingViewerSink()
	replacement, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: replacementSink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	replacementBeginEnvelope := waitForHostMessage(t, replacementSink, protocol.SnapshotBegin, 1)
	replacementBegin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](replacementBeginEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, replacementSink, protocol.SnapshotEnd, 1)
	if err := replacement.SnapshotApplied(context.Background(), replacementBegin.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}
	replacementGrant, err := session.AcquireControl(context.Background(), replacement)
	if err != nil {
		t.Fatalf("replacement acquire control: %v", err)
	}
	if _, err := session.AcquireControl(context.Background(), oldViewer); !errors.Is(err, ErrViewerMissing) {
		t.Fatalf("stale viewer acquire error = %v, want %v", err, ErrViewerMissing)
	}
	unattached := &ViewerSubscription{
		session: session, connectionID: testViewerConnection, deviceID: testViewerDevice,
	}
	if _, err := session.AcquireControl(context.Background(), unattached); !errors.Is(err, ErrViewerMissing) {
		t.Fatalf("unattached viewer acquire error = %v, want %v", err, ErrViewerMissing)
	}
	if _, err := session.AcquireControl(context.Background(), nil); !errors.Is(err, ErrViewerMissing) {
		t.Fatalf("nil viewer acquire error = %v, want %v", err, ErrViewerMissing)
	}
	if _, err := session.Heartbeat(context.Background(), MutationIdentity{
		Generation: testGeneration, ConnectionID: replacement.connectionID,
		DeviceID: replacement.deviceID, Lease: replacementGrant.Lease,
	}); err != nil {
		t.Fatalf("stale acquire disturbed replacement control: %v", err)
	}

	// Model a delayed duplicate teardown event from the old worker after the
	// connection ID has already been reused. The subscription incarnation, not
	// the display ID, decides which viewer can be detached.
	session.queueViewerDetach(oldViewer)
	if _, err := session.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := oldViewer.SnapshotApplied(context.Background(), oldBegin.SnapshotID, 0); !errors.Is(err, ErrViewerMissing) {
		t.Fatalf("stale snapshot acknowledgement error = %v, want %v", err, ErrViewerMissing)
	}
	if err := oldViewer.Resync(context.Background()); !errors.Is(err, ErrViewerMissing) {
		t.Fatalf("stale resync error = %v, want %v", err, ErrViewerMissing)
	}
	for _, request := range []sessionRequest{
		{kind: requestReserveViewerDelivery, connection: testViewerConnection, viewer: oldViewer},
		{kind: requestCompleteViewerDelivery, connection: testViewerConnection, viewer: oldViewer},
		{kind: requestRestartViewerSnapshot, connection: testViewerConnection, viewer: oldViewer},
		{kind: requestLatestViewerFrame, connection: testViewerConnection, viewer: oldViewer},
	} {
		if _, err := session.call(context.Background(), request); !errors.Is(err, ErrViewerMissing) {
			t.Fatalf("stale internal request %d error = %v, want %v", request.kind, err, ErrViewerMissing)
		}
	}

	process.Emit([]byte("replacement-still-patched"))
	waitForTerminalRevision(t, session, 1)
	patchEnvelope := waitForHostMessage(t, replacementSink, protocol.ScreenPatch, 1)
	patch, err := protocol.DecodePayload[protocol.ScreenPatchPayload](patchEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if patch.BaseRevision != "0" || patch.Revision != "1" {
		t.Fatalf("replacement patch revisions = %s -> %s, want 0 -> 1", patch.BaseRevision, patch.Revision)
	}
	time.Sleep(3 * framePublishInterval)
	if snapshots := countHostMessages(t, replacementSink, protocol.SnapshotBegin); snapshots != 1 {
		t.Fatalf("stale handle forced %d replacement snapshots, want 1", snapshots)
	}
}

func TestCurrentViewerCanExplicitlyTakeControl(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	first, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: newRecordingViewerSink(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: "66666666-6666-4666-8666-666666666666",
		DeviceID:     "77777777-7777-4777-8777-777777777777",
		Generation:   testGeneration, Sink: newRecordingViewerSink(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstGrant, err := session.AcquireControl(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	secondGrant, err := session.AcquireControl(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Heartbeat(context.Background(), MutationIdentity{
		Generation: testGeneration, ConnectionID: first.connectionID,
		DeviceID: first.deviceID, Lease: firstGrant.Lease,
	}); !errors.Is(err, ErrNotController) {
		t.Fatalf("replaced viewer heartbeat error = %v, want %v", err, ErrNotController)
	}
	if _, err := session.Heartbeat(context.Background(), MutationIdentity{
		Generation: testGeneration, ConnectionID: second.connectionID,
		DeviceID: second.deviceID, Lease: secondGrant.Lease,
	}); err != nil {
		t.Fatalf("current viewer heartbeat: %v", err)
	}
}

type recordingViewerSink struct {
	mu       sync.Mutex
	messages [][]byte
	changed  chan struct{}
}

func newRecordingViewerSink() *recordingViewerSink {
	return &recordingViewerSink{changed: make(chan struct{}, 1)}
}

func (sink *recordingViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sink.mu.Lock()
	sink.messages = append(sink.messages, append([]byte(nil), message...))
	sink.mu.Unlock()
	select {
	case sink.changed <- struct{}{}:
	default:
	}
	drained()
	return nil
}

func (sink *recordingViewerSink) snapshot() [][]byte {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	messages := make([][]byte, len(sink.messages))
	for index := range sink.messages {
		messages[index] = append([]byte(nil), sink.messages[index]...)
	}
	return messages
}

type gateViewerSink struct {
	recordingViewerSink
	blockAt int
	blocked chan struct{}
	once    sync.Once
}

func newGateViewerSink(blockAt int) *gateViewerSink {
	return &gateViewerSink{
		recordingViewerSink: *newRecordingViewerSink(), blockAt: blockAt, blocked: make(chan struct{}),
	}
}

func (sink *gateViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sink.mu.Lock()
	sink.messages = append(sink.messages, append([]byte(nil), message...))
	messageCount := len(sink.messages)
	sink.mu.Unlock()
	select {
	case sink.changed <- struct{}{}:
	default:
	}
	if messageCount != sink.blockAt {
		drained()
		return nil
	}
	sink.once.Do(func() { close(sink.blocked) })
	<-ctx.Done()
	return ctx.Err()
}

type blockingViewerSink struct{}

func (blockingViewerSink) Send(ctx context.Context, _ []byte, _ func()) error {
	<-ctx.Done()
	return ctx.Err()
}

type failGateViewerSink struct {
	started chan struct{}
	fail    chan struct{}
	once    sync.Once
}

func newFailGateViewerSink() *failGateViewerSink {
	return &failGateViewerSink{started: make(chan struct{}), fail: make(chan struct{})}
}

func (sink *failGateViewerSink) Send(ctx context.Context, _ []byte, _ func()) error {
	sink.once.Do(func() { close(sink.started) })
	select {
	case <-sink.fail:
		return errors.New("forced viewer sink failure")
	case <-ctx.Done():
		return ctx.Err()
	}
}

type delayedDrainViewerSink struct {
	accepted chan struct{}
	once     sync.Once
	drained  func()
	retained []byte
	mu       sync.Mutex
}

func newDelayedDrainViewerSink() *delayedDrainViewerSink {
	return &delayedDrainViewerSink{accepted: make(chan struct{})}
}

func (sink *delayedDrainViewerSink) Send(_ context.Context, message []byte, drained func()) error {
	sink.mu.Lock()
	sink.retained = append([]byte(nil), message...)
	sink.drained = drained
	sink.mu.Unlock()
	sink.once.Do(func() { close(sink.accepted) })
	return nil
}

func (sink *delayedDrainViewerSink) drain() {
	sink.mu.Lock()
	drained := sink.drained
	sink.drained = nil
	sink.retained = nil
	sink.mu.Unlock()
	if drained != nil {
		drained()
	}
}

func waitForHostMessage(t *testing.T, sink *recordingViewerSink, messageType protocol.MessageType, occurrence int) protocol.Envelope {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		count := 0
		for _, message := range sink.snapshot() {
			envelope, err := protocol.DecodeHost(message)
			if err != nil {
				t.Fatal(err)
			}
			if len(message) > protocol.MaxOutboundMessageBytes {
				t.Fatalf("outbound message = %d bytes, limit %d", len(message), protocol.MaxOutboundMessageBytes)
			}
			if envelope.Type == messageType {
				count++
				if count == occurrence {
					return envelope
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("never received %s occurrence %d", messageType, occurrence)
		}
		select {
		case <-sink.changed:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func countHostMessages(t *testing.T, sink *recordingViewerSink, messageType protocol.MessageType) int {
	t.Helper()
	count := 0
	for _, message := range sink.snapshot() {
		envelope, err := protocol.DecodeHost(message)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Type == messageType {
			count++
		}
	}
	return count
}

func fillPublicRequestQueue(t *testing.T, session *Session) {
	t.Helper()
	for index := 0; index < cap(session.requests); index++ {
		request := sessionRequest{kind: requestStatus, reply: make(chan sessionResponse, 1)}
		select {
		case session.requests <- request:
		default:
			t.Fatalf("public request queue filled after %d of %d requests", index, cap(session.requests))
		}
	}
}

func patchContainsText(patch protocol.ScreenPatchPayload, expected string) bool {
	for _, changed := range patch.Rows {
		for _, run := range changed.Row.Runs {
			for _, cell := range run.Cells {
				if strings.Contains(cell.Text, expected) {
					return true
				}
			}
		}
	}
	return false
}

func startViewerSession(t *testing.T) (*Session, *viewerProcess) {
	t.Helper()
	return startViewerSessionWithTerminal(t, newViewerTerminal())
}

func startViewerSessionWithTerminal(t *testing.T, core *viewerTerminal) (*Session, *viewerProcess) {
	t.Helper()
	runtimeContext, cancel := context.WithCancel(context.Background())
	process := newViewerProcess()
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process:            ptyhost.Spec{Columns: 20, Rows: 5},
		InputQueueCapacity: DefaultInputQueueCapacity, PTYOutputQueue: MaxPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	return session, process
}

func waitForTerminalRevision(t *testing.T, session *Session, revision uint64) terminal.Frame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame, err := session.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Revision >= revision {
			return frame
		}
		time.Sleep(time.Millisecond)
	}
	frame, _ := session.Snapshot(context.Background())
	t.Fatalf("terminal revision = %d, want at least %d", frame.Revision, revision)
	return terminal.Frame{}
}

type viewerProcess struct {
	reads         chan []byte
	exited        chan struct{}
	exitOnce      sync.Once
	resizeStarted chan struct{}
	resizeRelease chan struct{}
	resizeOnce    sync.Once
}

func newViewerProcess() *viewerProcess {
	return &viewerProcess{reads: make(chan []byte, 1024), exited: make(chan struct{})}
}

func (*viewerProcess) PID() int { return 5150 }
func (process *viewerProcess) Read(buffer []byte) (int, error) {
	select {
	case data := <-process.reads:
		return copy(buffer, data), nil
	case <-process.exited:
		return 0, io.EOF
	}
}
func (*viewerProcess) Write(data []byte) (int, error) { return len(data), nil }
func (process *viewerProcess) Resize(int, int) error {
	if process.resizeStarted != nil {
		process.resizeOnce.Do(func() { close(process.resizeStarted) })
		<-process.resizeRelease
	}
	return nil
}
func (process *viewerProcess) Wait(context.Context) error {
	<-process.exited
	return nil
}
func (process *viewerProcess) Terminate(context.Context) error { process.release(); return nil }
func (process *viewerProcess) Close() error                    { process.release(); return nil }
func (process *viewerProcess) release()                        { process.exitOnce.Do(func() { close(process.exited) }) }
func (process *viewerProcess) Emit(data []byte)                { process.reads <- append([]byte(nil), data...) }

func (process *viewerProcess) blockResize() (<-chan struct{}, func()) {
	process.resizeStarted = make(chan struct{})
	process.resizeRelease = make(chan struct{})
	var once sync.Once
	return process.resizeStarted, func() { once.Do(func() { close(process.resizeRelease) }) }
}

type viewerTerminal struct {
	frame     terminal.Frame
	output    chan []byte
	closeOnce sync.Once
}

func newViewerTerminal() *viewerTerminal {
	return &viewerTerminal{frame: viewerFrame(0, 20, 5, " "), output: make(chan []byte)}
}

func (core *viewerTerminal) WriteOutput(_ context.Context, data []byte) error {
	core.frame.Revision++
	text := string(data)
	if text == "HISTORY_RESET" {
		core.frame.HistoryEpoch++
		core.frame.HistoryRevision = 0
		text = "reset"
	}
	if text == "" {
		text = " "
	}
	core.frame.Screen[0].Cells[0].Text = text
	return nil
}
func (core *viewerTerminal) Resize(_ context.Context, columns, rows int) error {
	core.frame = viewerFrame(core.frame.Revision+1, columns, rows, " ")
	return nil
}
func (core *viewerTerminal) Snapshot(context.Context) (terminal.Frame, error) {
	return cloneViewerFrame(core.frame), nil
}
func (core *viewerTerminal) ScreenSnapshot(context.Context) (terminal.Frame, error) {
	return cloneViewerFrame(core.frame), nil
}
func (core *viewerTerminal) ReadHistory(_ context.Context, request terminal.HistoryReadRequest) (terminal.HistoryReadResult, error) {
	return terminal.HistoryReadResult{
		Changed: request.Epoch != core.frame.HistoryEpoch || request.Revision != core.frame.HistoryRevision,
		Epoch:   core.frame.HistoryEpoch, Revision: core.frame.HistoryRevision,
		Columns: core.frame.Columns, Offset: request.Offset, Complete: true,
	}, nil
}
func (*viewerTerminal) SendText(context.Context, string) error         { return nil }
func (*viewerTerminal) Paste(context.Context, string) error            { return nil }
func (*viewerTerminal) SendKey(context.Context, uv.KeyEvent) error     { return nil }
func (*viewerTerminal) SendMouse(context.Context, uv.MouseEvent) error { return nil }
func (core *viewerTerminal) Output() <-chan []byte                     { return core.output }
func (core *viewerTerminal) Close() error {
	core.closeOnce.Do(func() { close(core.output) })
	return nil
}

func viewerFrame(revision uint64, columns, rows int, text string) terminal.Frame {
	white := terminal.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := terminal.RGBA{A: 255}
	frame := terminal.Frame{
		Revision: revision, Columns: columns, Rows: rows, WidthMethod: "unicode-graphemes",
		Foreground: white, Background: black, CursorColor: white,
		Cursor: terminal.Cursor{Style: terminal.CursorBlock, Visible: true},
		Screen: make([]terminal.Row, rows),
	}
	for row := range frame.Screen {
		frame.Screen[row].Cells = make([]terminal.Cell, columns)
		for column := range frame.Screen[row].Cells {
			frame.Screen[row].Cells[column] = terminal.Cell{
				Text: " ", Width: 1, Foreground: white, Background: black,
			}
		}
	}
	frame.Screen[0].Cells[0].Text = text
	return frame
}

func cloneViewerFrame(frame terminal.Frame) terminal.Frame {
	cloned := frame
	cloned.Screen = make([]terminal.Row, len(frame.Screen))
	for index := range frame.Screen {
		cloned.Screen[index].Cells = append([]terminal.Cell(nil), frame.Screen[index].Cells...)
	}
	return cloned
}

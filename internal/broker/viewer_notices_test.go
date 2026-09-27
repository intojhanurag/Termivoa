package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	secondNoticeConnection = "66666666-6666-4666-8666-666666666666"
	firstControlRequest    = "77777777-7777-4777-8777-777777777777"
	secondControlRequest   = "88888888-8888-4888-8888-888888888888"
)

func TestViewerControlNoticesOrderSecretGrantsAndPublicTakeover(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	firstSink, secondSink := newRecordingViewerSink(), newRecordingViewerSink()
	first := attachNoticeViewer(t, session, testViewerConnection, firstSink)
	defer first.Close()
	second := attachNoticeViewer(t, session, secondNoticeConnection, secondSink)
	defer second.Close()
	for _, sink := range []*recordingViewerSink{firstSink, secondSink} {
		initial := controlNotice(t, waitForHostMessage(t, sink, protocol.ControlChanged, 1))
		if initial.ControllerConnectionID != nil || initial.Lease != nil || initial.LeaseEpoch != "0" {
			t.Fatalf("attach did not start as an observer: %#v", initial)
		}
		state, err := protocol.DecodePayload[protocol.SessionStatePayload](waitForHostMessage(t, sink, protocol.SessionState, 1))
		if err != nil || state.State != protocol.Running {
			t.Fatalf("initial state = %#v, error %v", state, err)
		}
	}
	ackNoticeSnapshot(t, first, firstSink)
	ackNoticeSnapshot(t, second, secondSink)
	firstGrant, err := session.AcquireControlForRequest(context.Background(), first, firstControlRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondGrant, err := session.AcquireControlForRequest(context.Background(), second, secondControlRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstOwn := waitForHostMessage(t, firstSink, protocol.ControlChanged, 2)
	firstPayload := controlNotice(t, firstOwn)
	if firstOwn.RequestID != firstControlRequest || firstPayload.Lease == nil || *firstPayload.Lease != firstGrant.Lease || firstPayload.LeaseEpoch != "1" {
		t.Fatalf("first correlated grant = %#v", firstPayload)
	}
	firstTakeover := controlNotice(t, waitForHostMessage(t, firstSink, protocol.ControlChanged, 3))
	if firstTakeover.Lease != nil || firstTakeover.LeaseEpoch != "2" || firstTakeover.ControllerConnectionID == nil || *firstTakeover.ControllerConnectionID != secondNoticeConnection {
		t.Fatalf("old holder takeover = %#v", firstTakeover)
	}
	secondObserver := controlNotice(t, waitForHostMessage(t, secondSink, protocol.ControlChanged, 2))
	if secondObserver.Lease != nil || secondObserver.LeaseEpoch != "1" {
		t.Fatalf("observer received another connection's secret: %#v", secondObserver)
	}
	secondOwn := waitForHostMessage(t, secondSink, protocol.ControlChanged, 3)
	secondPayload := controlNotice(t, secondOwn)
	if secondOwn.RequestID != secondControlRequest || secondPayload.Lease == nil || *secondPayload.Lease != secondGrant.Lease || secondPayload.LeaseEpoch != "2" {
		t.Fatalf("second correlated grant = %#v", secondPayload)
	}
	if err := session.ReleaseControl(context.Background(), fixtureIdentity(secondGrant)); err != nil {
		t.Fatal(err)
	}
	for _, sink := range []*recordingViewerSink{firstSink, secondSink} {
		released := controlNotice(t, waitForHostMessage(t, sink, protocol.ControlChanged, 4))
		if released.ControllerConnectionID != nil || released.Lease != nil || released.LeaseEpoch != "2" {
			t.Fatalf("release state = %#v", released)
		}
	}
}

func TestViewerIdleControllerExpiryIsBroadcastWithoutPolling(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newRecordingViewerSink()
	viewer := attachNoticeViewer(t, session, testViewerConnection, sink)
	defer viewer.Close()
	ackNoticeSnapshot(t, viewer, sink)
	grant, err := session.AcquireControlForRequest(context.Background(), viewer, firstControlRequest)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.ControlChanged, 2)
	// Shorten only the test lease, then one actor request refreshes the timer.
	// No client heartbeat, user input, status poll, or PTY output follows it.
	session.controller.mu.Lock()
	session.controller.expiresAt = time.Now().Add(25 * time.Millisecond)
	session.controller.mu.Unlock()
	if _, err := session.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	expired := controlNotice(t, waitForHostMessage(t, sink, protocol.ControlChanged, 3))
	if expired.ControllerConnectionID != nil || expired.Lease != nil || expired.LeaseEpoch != "1" {
		t.Fatalf("idle expiry = %#v", expired)
	}
	if _, err := session.SubmitInput(context.Background(), fixtureIdentity(grant), 1, InputAction{Kind: InputKindText, Text: "stale"}); !errors.Is(err, ErrNotController) {
		t.Fatalf("expired lease accepted input: %v", err)
	}
}

func TestViewerAcceptsSnapshotAckBeforeEndWriteCompletion(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := &earlySnapshotAckSink{
		recordingViewerSink: *newRecordingViewerSink(),
		ready:               make(chan struct{}), acked: make(chan error, 1), release: make(chan struct{}),
	}
	viewer := attachNoticeViewer(t, session, testViewerConnection, sink)
	defer viewer.Close()
	sink.viewer = viewer
	close(sink.ready)
	select {
	case err := <-sink.acked:
		if err != nil {
			t.Fatalf("acknowledgement raced snapshot completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot acknowledgement was not attempted")
	}
	// The early acknowledgement already covers the snapshot being sent.
	if _, err := session.AcquireControlForRequest(context.Background(), viewer, firstControlRequest); err != nil {
		t.Fatalf("early-applied viewer could not take control: %v", err)
	}
	process.Emit([]byte("AFTER"))
	waitForTerminalRevision(t, session, 1)
	if countHostMessages(t, &sink.recordingViewerSink, protocol.ScreenPatch) != 0 {
		t.Fatal("early acknowledgement unlocked a patch before snapshot.end drained")
	}
	close(sink.release)
	waitForHostMessage(t, &sink.recordingViewerSink, protocol.ScreenPatch, 1)
	if countHostMessages(t, &sink.recordingViewerSink, protocol.SnapshotBegin) != 1 {
		t.Fatal("legitimate early acknowledgement caused a redundant resync")
	}
}

func TestViewerFinalStateDrainsBeforeRetirement(t *testing.T) {
	session, process := startViewerSession(t)
	defer session.Close()
	sink := newNoticeGateSink(func(envelope protocol.Envelope) bool {
		if envelope.Type != protocol.SessionState {
			return false
		}
		payload, err := protocol.DecodePayload[protocol.SessionStatePayload](envelope)
		return err == nil && payload.State == protocol.Exited
	})
	viewer := attachNoticeViewer(t, session, testViewerConnection, sink)
	defer viewer.Close()
	defer sink.unblock()
	ackNoticeSnapshot(t, viewer, &sink.recordingViewerSink)
	process.release()
	select {
	case <-sink.blocked:
	case <-time.After(time.Second):
		t.Fatal("final session state was not delivered")
	}
	select {
	case <-session.Ended():
	default:
		t.Fatal("final lifecycle was not recorded before its notice")
	}
	select {
	case <-session.RetirementReady():
		t.Fatal("session retired before final state notice drained")
	default:
	}
	sink.unblock()
	select {
	case <-session.RetirementReady():
	case <-time.After(time.Second):
		t.Fatal("session did not retire after final state drained")
	}
}

func TestViewerNoticeFloodDisconnectsAndReleasesController(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	sink := newNoticeGateSink(func(envelope protocol.Envelope) bool { return envelope.Type == protocol.ControlChanged })
	viewer := attachNoticeViewer(t, session, testViewerConnection, sink)
	defer viewer.Close()
	defer sink.unblock()
	select {
	case <-sink.blocked:
	case <-time.After(time.Second):
		t.Fatal("initial control notice did not reach gate")
	}
	var acquireErr error
	// The gated viewer never receives its snapshot, so use the in-process
	// acquire; each grant still queues one control notice.
	for index := 0; index <= MaxViewerQueuedNotices; index++ {
		_, acquireErr = session.AcquireControl(context.Background(), viewer)
		if acquireErr != nil {
			break
		}
	}
	if !errors.Is(acquireErr, ErrViewerSlow) {
		t.Fatalf("notice overflow error = %v, want slow viewer", acquireErr)
	}
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("overflowed viewer did not disconnect")
	}
	if session.controller.State().ConnectionID != "" {
		t.Fatal("overflowed viewer retained its controller lease")
	}
	viewer.mu.Lock()
	used, peak := viewer.budgetUsed, viewer.budgetPeak
	viewer.mu.Unlock()
	if used != 0 || peak > MaxViewerQueueBytes {
		t.Fatalf("notice queue budget used=%d peak=%d", used, peak)
	}
}

func TestSnapshotTimeoutImmediatelyReleasesControllerAndBroadcasts(t *testing.T) {
	session, _ := startViewerSession(t)
	defer session.Close()
	observerSink := newRecordingViewerSink()
	observer := attachNoticeViewer(t, session, secondNoticeConnection, observerSink)
	defer observer.Close()
	ackNoticeSnapshot(t, observer, observerSink)
	sink := newRecordingViewerSink()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
		Generation: testGeneration, Sink: sink, SnapshotAckTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	grant, err := session.AcquireControl(context.Background(), viewer)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	select {
	case <-viewer.Done():
	case <-time.After(time.Second):
		t.Fatal("unacknowledged viewer did not expire")
	}
	released := controlNotice(t, waitForHostMessage(t, observerSink, protocol.ControlChanged, 3))
	if released.ControllerConnectionID != nil {
		t.Fatal("observer was not notified of failed holder's release")
	}
	if _, err := session.Heartbeat(context.Background(), fixtureIdentity(grant)); !errors.Is(err, ErrNotController) {
		t.Fatalf("timed-out viewer retained controller lease: %v", err)
	}
}

func attachNoticeViewer(t *testing.T, session *Session, connection string, sink ViewerSink) *ViewerSubscription {
	t.Helper()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: connection, DeviceID: testViewerDevice, Generation: testGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	return viewer
}

func ackNoticeSnapshot(t *testing.T, viewer *ViewerSubscription, sink *recordingViewerSink) {
	t.Helper()
	begin := waitForHostMessage(t, sink, protocol.SnapshotBegin, 1)
	payload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
	if err := viewer.SnapshotApplied(context.Background(), payload.SnapshotID, 0); err != nil {
		t.Fatal(err)
	}
}

func controlNotice(t *testing.T, envelope protocol.Envelope) protocol.ControlChangedPayload {
	t.Helper()
	payload, err := protocol.DecodePayload[protocol.ControlChangedPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type noticeGateSink struct {
	recordingViewerSink
	match       func(protocol.Envelope) bool
	blocked     chan struct{}
	release     chan struct{}
	once        sync.Once
	unblockOnce sync.Once
}

func newNoticeGateSink(match func(protocol.Envelope) bool) *noticeGateSink {
	return &noticeGateSink{
		recordingViewerSink: *newRecordingViewerSink(), match: match,
		blocked: make(chan struct{}), release: make(chan struct{}),
	}
}

func (sink *noticeGateSink) Send(ctx context.Context, message []byte, drained func()) error {
	envelope, err := protocol.DecodeHost(message)
	if err != nil {
		return err
	}
	block := false
	if sink.match(envelope) {
		sink.once.Do(func() { block = true; close(sink.blocked) })
	}
	if block {
		select {
		case <-sink.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sink.recordingViewerSink.Send(ctx, message, drained)
}

func (sink *noticeGateSink) unblock() { sink.unblockOnce.Do(func() { close(sink.release) }) }

type earlySnapshotAckSink struct {
	recordingViewerSink
	viewer  *ViewerSubscription
	ready   chan struct{}
	acked   chan error
	release chan struct{}
	once    sync.Once
}

func (sink *earlySnapshotAckSink) Send(ctx context.Context, message []byte, drained func()) error {
	envelope, err := protocol.DecodeHost(message)
	if err != nil {
		return err
	}
	if envelope.Type == protocol.SnapshotEnd {
		block := false
		sink.once.Do(func() { block = true })
		if block {
			select {
			case <-sink.ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			payload, err := protocol.DecodePayload[protocol.SnapshotEndPayload](envelope)
			if err != nil {
				return err
			}
			sink.acked <- sink.viewer.SnapshotApplied(ctx, payload.SnapshotID, 0)
			select {
			case <-sink.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return sink.recordingViewerSink.Send(ctx, message, drained)
}

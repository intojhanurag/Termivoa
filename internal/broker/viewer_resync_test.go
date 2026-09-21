package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestViewerActorResetCancelsOldDeliveryBeforePublishingReplacement(t *testing.T) {
	for _, kind := range []sessionRequestKind{requestViewerResync, requestViewerSnapshotApplied} {
		name := "resync"
		if kind == requestViewerSnapshotApplied {
			name = "wrong_snapshot_ack"
		}
		t.Run(name, func(t *testing.T) {
			session, _ := startViewerSession(t)
			defer session.Close()
			sink := newGateViewerSink(protocol.SnapshotRows)
			viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
				ConnectionID: testViewerConnection, DeviceID: testViewerDevice,
				Generation: testGeneration, Sink: sink,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer viewer.Close()
			select {
			case <-sink.blocked:
			case <-time.After(time.Second):
				t.Fatal("original snapshot rows did not block")
			}
			// A public caller may be descheduled after its actor reply. The
			// actor must already have canceled the obsolete delivery; it must
			// not rely on that caller later canceling an unscoped current send.
			_, err = session.call(context.Background(), sessionRequest{
				kind: kind, connection: viewer.connectionID, viewer: viewer,
				snapshotID: "66666666-6666-4666-8666-666666666666", revision: 0,
			})
			if kind == requestViewerSnapshotApplied {
				if !errors.Is(err, ErrViewerSnapshotMismatch) {
					t.Fatalf("wrong snapshot acknowledgement = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			begin := waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotBegin, 2)
			payload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
			if err != nil {
				t.Fatal(err)
			}
			waitForHostMessage(t, &sink.recordingViewerSink, protocol.SnapshotEnd, 1)
			if err := viewer.SnapshotApplied(context.Background(), payload.SnapshotID, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := session.AcquireControl(context.Background(), viewer); err != nil {
				t.Fatalf("replacement did not become controller-ready: %v", err)
			}
			if err := viewer.Close(); err != nil {
				t.Fatalf("replacement viewer close = %v", err)
			}
		})
	}
}

func TestViewerRepeatedPublicResyncDoesNotCancelItsReplacement(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "resync"
		if mismatch {
			name = "wrong_snapshot_ack"
		}
		t.Run(name, func(t *testing.T) {
			session, _ := startViewerSession(t)
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
			waitForHostMessage(t, sink, protocol.SnapshotEnd, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for iteration := 0; iteration < 1000; iteration++ {
				if mismatch {
					err = viewer.SnapshotApplied(ctx, "66666666-6666-4666-8666-666666666666", 0)
					if errors.Is(err, ErrViewerSnapshotMismatch) {
						err = nil
					}
				} else {
					err = viewer.Resync(ctx)
				}
				if err != nil {
					t.Fatalf("iteration %d resync failed: %v, viewer=%v", iteration, err, viewer.Err())
				}
				select {
				case <-viewer.Done():
					t.Fatalf("iteration %d closed viewer during resync: %v", iteration, viewer.Err())
				default:
				}
			}
		})
	}
}

func TestViewerResetRejectsLateDeliveryRegistration(t *testing.T) {
	for _, patch := range []bool{false, true} {
		name := "snapshot"
		if patch {
			name = "patch"
		}
		t.Run(name, func(t *testing.T) {
			// Run the actor-owned operations directly to place a reset exactly
			// after reservation and before the worker installs its cancellation.
			// No worker or running actor shares this fixture's state.
			subscription := &ViewerSubscription{
				connectionID: testViewerConnection, notify: make(chan struct{}, 1),
			}
			viewer := &viewerActorState{subscription: subscription, forceSnapshot: !patch}
			if patch {
				viewer.base = &publishedFrame{frame: viewerFrame(0, 20, 5, " ")}
			}
			session := &Session{
				published: &publishedFrame{frame: viewerFrame(1, 20, 5, "x")},
				viewers:   map[string]*viewerActorState{testViewerConnection: viewer},
			}
			plan, err := session.reserveViewerDelivery(testViewerConnection, subscription)
			if err != nil || plan.target == nil {
				t.Fatalf("reserve old delivery: %v", err)
			}
			if err := session.resyncViewer(testViewerConnection, subscription); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if subscription.setDeliveryCancel(plan.epoch, cancel) {
				t.Fatal("old reserved delivery registered after reset")
			}
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("old reserved delivery acquired an uncanceled context after reset")
			}
			replacement, err := session.reserveViewerDelivery(testViewerConnection, subscription)
			if err != nil || replacement.kind != viewerDeliverySnapshot {
				t.Fatalf("reserve replacement: kind=%v, error=%v", replacement.kind, err)
			}
			replacementContext, replacementCancel := context.WithCancel(context.Background())
			defer replacementCancel()
			if !subscription.setDeliveryCancel(replacement.epoch, replacementCancel) {
				t.Fatal("replacement registration was rejected")
			}
			if err := replacementContext.Err(); err != nil {
				t.Fatalf("replacement cancellation = %v", err)
			}
		})
	}
}

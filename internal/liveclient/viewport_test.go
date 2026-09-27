package liveclient

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestViewportFailureCannotTriggerResyncLoop(t *testing.T) {
	output := &bufferOutput{}
	rejected := fmt.Errorf("local display rejected frame: %w", desktop.ErrResyncRequired)
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(protocol.ScreenMetadata) error { return rejected }
	})
	screen, rows := snapshot(t, "must not paint")
	host.snapshot(t, "1", screen, rows)
	awaitViewportFailure(t, client, rejected)
	if output.text() != "" {
		t.Fatal("rejected snapshot was painted")
	}
	assertNoViewportAck(t, host)
}

func TestViewportRejectsCompleteSnapshotBeforePaintOrAck(t *testing.T) {
	output := &bufferOutput{}
	tooSmall := errors.New("outer terminal needs 20 columns and 5 rows")
	var calls atomic.Int64
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(screen protocol.ScreenMetadata) error {
			calls.Add(1)
			if screen.Columns != 20 || screen.Rows != 5 {
				return errors.New("wrong metadata")
			}
			return tooSmall
		}
	})
	screen, rows := snapshot(t, "must not paint")
	host.snapshot(t, "1", screen, rows)
	awaitViewportFailure(t, client, tooSmall)
	if calls.Load() != 1 || output.text() != "" {
		t.Fatalf("calls=%d output=%q", calls.Load(), output.text())
	}
	assertNoViewportAck(t, host)
}

func TestViewportIsCheckedAgainBeforeSnapshotEnd(t *testing.T) {
	output := &bufferOutput{}
	tooSmall := errors.New("outer terminal shrank while snapshot was in flight")
	var small atomic.Bool
	var calls atomic.Int64
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(protocol.ScreenMetadata) error {
			calls.Add(1)
			if small.Load() {
				return tooSmall
			}
			return nil
		}
	})
	screen, rows := snapshot(t, "snapshot")
	id, _ := newUUID()
	host.send(t, protocol.SnapshotBegin, "", protocol.SnapshotBeginPayload{SnapshotID: id, Revision: "1", Screen: screen})
	host.send(t, protocol.SnapshotRows, "", protocol.SnapshotRowsPayload{SnapshotID: id, Revision: "1", Rows: rows})
	small.Store(true)
	host.send(t, protocol.SnapshotEnd, "", protocol.SnapshotEndPayload{SnapshotID: id, Revision: "1", RowCount: screen.Rows})
	awaitViewportFailure(t, client, tooSmall)
	if calls.Load() != 1 || output.text() != "" {
		t.Fatalf("calls=%d output=%q", calls.Load(), output.text())
	}
	assertNoViewportAck(t, host)
}

func TestViewportRejectsPatchAfterOuterTerminalShrinks(t *testing.T) {
	output := &bufferOutput{}
	tooSmall := errors.New("outer terminal no longer fits canonical grid")
	var small atomic.Bool
	var calls atomic.Int64
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(protocol.ScreenMetadata) error {
			calls.Add(1)
			if small.Load() {
				return tooSmall
			}
			return nil
		}
	})
	screen, rows := snapshot(t, "initial")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	before := output.text()
	small.Store(true)
	next, changed := snapshot(t, "not painted")
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: next, Rows: []protocol.ChangedRow{{Index: 0, Row: changed[0]}}})
	awaitViewportFailure(t, client, tooSmall)
	if calls.Load() != 2 || output.text() != before {
		t.Fatalf("calls=%d rejected patch modified display", calls.Load())
	}
	assertNoViewportAck(t, host)
}

func TestViewportAllowsExactFitSnapshotsAndPatches(t *testing.T) {
	output := &bufferOutput{}
	var calls atomic.Int64
	client, host, _ := newClientFixture(t, output, func(config *Config) {
		config.ValidateScreen = func(screen protocol.ScreenMetadata) error {
			calls.Add(1)
			if screen.Columns > 20 || screen.Rows > 5 {
				return errors.New("grid exceeds viewport")
			}
			return nil
		}
	})
	screen, rows := snapshot(t, "initial")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	next, changed := snapshot(t, "updated")
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: next, Rows: []protocol.ChangedRow{{Index: 0, Row: changed[0]}}})
	for client.frames.DisplayedRevision() != "2" {
		select {
		case <-ctx.Done():
			t.Fatal("fitting patch was not painted")
		case <-time.After(time.Millisecond):
		}
	}
	if calls.Load() != 2 || client.Err() != nil || !client.State().Ready {
		t.Fatalf("calls=%d state=%+v err=%v", calls.Load(), client.State(), client.Err())
	}
}

func awaitViewportFailure(t *testing.T, client *Client, expected error) {
	t.Helper()
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("viewport failure did not close viewer")
	}
	if err := client.Close(); !errors.Is(err, expected) {
		t.Fatalf("close=%v want=%v", err, expected)
	}
	if client.State().Ready || client.State().Connected {
		t.Fatalf("rejected frame left viewer usable: %+v", client.State())
	}
}

func assertNoViewportAck(t *testing.T, host *fakeHost) {
	t.Helper()
	// Client.Close has joined its network workers, so no new requests can arrive.
	<-host.done
	select {
	case request := <-host.requests:
		t.Fatalf("rejected frame emitted %s", request.Type)
	default:
	}
}

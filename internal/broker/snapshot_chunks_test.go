package broker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type chunkTransfer struct {
	data    []byte
	drained func()
}
type chunkSink chan chunkTransfer

func (sink chunkSink) Send(ctx context.Context, data []byte, drained func()) error {
	select {
	case sink <- chunkTransfer{data: data, drained: drained}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSnapshotRowsSplitBelowWireLimit(t *testing.T) {
	core := newViewerTerminal()
	core.frame = viewerFrame(0, 200, 10, " ")
	for y := range core.frame.Screen {
		for x := range core.frame.Screen[y].Cells {
			// HTML-escaped printable content exercises JSON expansion, not only
			// raw text length. This is a synthetic transport stress cell.
			core.frame.Screen[y].Cells[x].Text = strings.Repeat("<", 140)
		}
	}
	session, _ := startViewerSessionWithTerminal(t, core)
	defer session.Close()
	sink := make(chunkSink)
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: testViewerConnection, DeviceID: testViewerDevice, Generation: testGeneration,
		Sink: sink, SendTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	rows, chunks := int64(0), 0
	for {
		select {
		case transfer := <-sink:
			if len(transfer.data) > protocol.MaxOutboundMessageBytes {
				t.Fatal("oversized snapshot message")
			}
			message, err := protocol.DecodeHost(transfer.data)
			transfer.drained()
			if err != nil {
				t.Fatal(err)
			}
			switch message.Type {
			case protocol.SnapshotRows:
				payload, err := protocol.DecodePayload[protocol.SnapshotRowsPayload](message)
				if err != nil {
					t.Fatal(err)
				}
				if payload.StartRow != rows {
					t.Fatal("snapshot rows are missing or duplicated")
				}
				rows += int64(len(payload.Rows))
				chunks++
			case protocol.SnapshotEnd:
				if rows != 10 || chunks < 2 {
					t.Fatalf("rows=%d chunks=%d", rows, chunks)
				}
				return
			}
		case <-viewer.Done():
			t.Fatalf("viewer stopped: %v", viewer.Err())
		case <-timer.C:
			t.Fatal("snapshot never completed")
		}
	}
}

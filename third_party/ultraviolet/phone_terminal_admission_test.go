package uv

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type admissionChunk struct {
	data  string
	epoch uint64
}

type admissionInput struct {
	chunks []admissionChunk
	epoch  atomic.Uint64
}

func (input *admissionInput) ReadContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(input.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := input.chunks[0]
	input.chunks = input.chunks[1:]
	input.epoch.Store(chunk.epoch)
	return copy(data, chunk.data), nil
}

func TestPhoneTerminalInputEpochTracksPartialSequencesAndPaste(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		chunks []admissionChunk
		epochs []uint64
	}{
		{"same CSI", []admissionChunk{{"\x1b[", 1}, {"A", 1}}, []uint64{1}},
		{"mixed CSI then fresh", []admissionChunk{{"\x1b[", 1}, {"A", 2}, {"z", 2}}, []uint64{0, 2}},
		{"same UTF8", []admissionChunk{{"\xf0\x9f", 7}, {"\x98\x80", 7}}, []uint64{7}},
		{"mixed UTF8", []admissionChunk{{"\xf0\x9f", 7}, {"\x98\x80", 8}}, []uint64{0}},
		{"same paste", []admissionChunk{{"\x1b[200~a", 1}, {"b\x1b[201~", 1}}, []uint64{1, 1, 1}},
		{"lease paste", []admissionChunk{{"\x1b[200~a", 1}, {"b\x1b[201~z", 2}}, []uint64{1, 0, 0, 2}},
		{"disabled paste", []admissionChunk{{"\x1b[200~a", 0}, {"b\x1b[201~z", 2}}, []uint64{0, 0, 0, 2}},
		{"mid-paste disabled", []admissionChunk{{"\x1b[200~a", 1}, {"b", 0}, {"c\x1b[201~z", 3}}, []uint64{1, 0, 0, 3}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := &admissionInput{chunks: fixture.chunks}
			reader := NewContextTerminalReader(source, "xterm-256color")
			reader.MaxPasteBytes, reader.MaxPendingBytes = 64*1024, 64*1024
			reader.EscTimeout = time.Hour
			reader.InputEpoch = source.epoch.Load
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			events := make(chan Event, 16)
			if err := reader.StreamEvents(ctx, events); err != nil {
				t.Fatal(err)
			}
			close(events)
			var index int
			for event := range events {
				tagged, ok := event.(InputEpochEvent)
				if !ok || index >= len(fixture.epochs) || tagged.Epoch != fixture.epochs[index] {
					t.Fatalf("event %d: %#v, expected epochs %v", index, event, fixture.epochs)
				}
				index++
			}
			if index != len(fixture.epochs) {
				t.Fatalf("got %d events, wanted %d", index, len(fixture.epochs))
			}
		})
	}
}

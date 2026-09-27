package desktop

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type boundedInputBytes struct{ *bytes.Reader }

func (input boundedInputBytes) ReadContext(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return input.Read(p)
}

func TestBoundedInputRejectsOversizedPasteAndPendingSequence(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
		want error
	}{
		{name: "paste", data: append([]byte("\x1b[200~"), bytes.Repeat([]byte("x"), protocol.MaxPasteBytes+1)...), want: uv.ErrPasteLimit},
		{name: "incomplete sequence", data: append([]byte("\x1b]52;c;"), bytes.Repeat([]byte("x"), MaxPendingInputBytes+1)...), want: uv.ErrPendingInputLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			streamer, err := NewBoundedInput(boundedInputBytes{bytes.NewReader(test.data)}, "xterm-256color")
			if err != nil {
				t.Fatal(err)
			}
			streamer.reader.EscTimeout = time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			events := make(chan uv.Event)
			done := make(chan error, 1)
			go func() { done <- streamer.StreamEvents(ctx, events); close(events) }()
			for event := range events {
				if _, harmless := event.(uv.PasteStartEvent); !harmless {
					t.Errorf("oversized input generated %T", event)
				}
			}
			if err := <-done; !errors.Is(err, test.want) {
				t.Fatalf("decoder error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestBoundedInputRequiresSourceAndContext(t *testing.T) {
	if _, err := NewBoundedInput(nil, "xterm-256color"); err == nil {
		t.Fatal("nil input accepted")
	}
	streamer, err := NewBoundedInput(boundedInputBytes{bytes.NewReader(nil)}, "xterm-256color")
	if err != nil {
		t.Fatal(err)
	}
	if err := streamer.StreamEvents(nil, make(chan uv.Event)); err == nil {
		t.Fatal("nil context accepted")
	}
}

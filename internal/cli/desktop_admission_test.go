package cli

import (
	"context"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
)

type desktopInputChunks chan []byte

func (chunks desktopInputChunks) ReadContext(ctx context.Context, data []byte) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case chunk := <-chunks:
		return copy(data, chunk), nil
	}
}

func TestDesktopSplitPasteCannotCrossAdmissionBoundary(t *testing.T) {
	for _, kind := range []string{"starts-syncing", "lease-takeover"} {
		t.Run(kind, func(t *testing.T) {
			client := desktopFake()
			client.state.Controller = true
			client.state.Ready = kind != "starts-syncing"
			display, _ := testDesktopDisplay(80, 24)
			transport := newDesktopTransport(context.Background(), client, display)
			defer transport.Close()
			chunks := make(desktopInputChunks)
			input, err := desktop.NewBoundedInput(chunks, "xterm-256color")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			events := make(chan uv.Event)
			done := make(chan error, 1)
			go func() { done <- input.StreamEvents(ctx, events) }()
			defer func() { cancel(); <-done }()
			controller := desktop.InputController{}
			chunks <- []byte("\x1b[200~stale ")
			event := <-events
			if _, ok := event.(uv.PasteStartEvent); !ok {
				t.Fatalf("first event: %T", event)
			}
			if _, err := controller.Handle(ctx, event, transport); err != nil {
				t.Fatal(err)
			}
			state := client.State()
			state.Ready = true
			if kind == "lease-takeover" {
				state.LeaseEpoch = "2"
			}
			client.setState(state)
			chunks <- []byte("paste\x1b[201~")
			for {
				event := <-events
				if _, err := controller.Handle(ctx, event, transport); err != nil {
					t.Fatal(err)
				}
				if _, ok := event.(uv.PasteEndEvent); ok {
					break
				}
			}
			if client.sent != 0 {
				t.Fatalf("stale partial paste crossed %s: %d child actions", kind, client.sent)
			}
		})
	}
}

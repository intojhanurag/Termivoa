package desktop

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

func startupTestTTY(t *testing.T, joined <-chan struct{}) *OuterTTY {
	t.Helper()
	return &OuterTTY{output: &bytes.Buffer{}, ops: ttyOperations{
		makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
		restore: func(uintptr, *term.State) error {
			select {
			case <-joined:
			default:
				t.Error("restoration raced startup decoder")
			}
			return nil
		},
	}}
}

func TestAttachmentStartupDrainsThenJoinsBeforeFreshInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	setupInput := make(chan struct{})
	joined := make(chan struct{})
	handoff := false
	transport := &attachmentTransport{recordingTransport: &recordingTransport{}}
	attachment := Attachment{
		TTY: startupTestTTY(t, joined),
		StartupEvents: eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
			defer close(joined)
			events <- uv.KeyPressEvent{Code: 'x', Text: "setup"}
			close(setupInput)
			<-ctx.Done()
			// The pinned decoder flushes events on cancellation. Discard both
			// the partial paste and local quote state; neither crosses handoff.
			events <- uv.PasteEvent{Content: "unfinished setup paste"}
			events <- uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}
			return ctx.Err()
		}),
		Connect: func(context.Context) (AttachmentTransport, error) { <-setupInput; return transport, nil },
		BeforeInput: func(context.Context) error {
			select {
			case <-joined:
			default:
				t.Error("native handoff before decoder join")
			}
			handoff = true
			return nil
		},
		Events: eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
			if !handoff {
				t.Error("live decoder before handoff")
			}
			events <- uv.KeyPressEvent{Code: 'y', Text: "live"}
			events <- uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}
			<-ctx.Done()
			return ctx.Err()
		}),
	}
	if err := attachment.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(transport.actions) != 1 || transport.actions[0] != (TextAction{Text: "live"}) {
		t.Fatalf("startup actions replayed: %#v", transport.actions)
	}
}

func TestAttachmentStartupDetachCancelsAndJoinsConnector(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "preserve-cleanup"}[cleanupFailure], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			joined := make(chan struct{})
			connectorJoined := make(chan struct{})
			failure := errors.New("native cleanup failed")
			attachment := Attachment{
				TTY: startupTestTTY(t, joined),
				StartupEvents: eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
					defer close(joined)
					events <- uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}
					<-ctx.Done()
					return ctx.Err()
				}),
				Connect: func(ctx context.Context) (AttachmentTransport, error) {
					defer close(connectorJoined)
					<-ctx.Done()
					if cleanupFailure {
						return nil, errors.Join(ctx.Err(), failure)
					}
					return nil, ctx.Err()
				},
				BeforeInput: func(context.Context) error { t.Error("handoff after detach"); return nil },
				Events:      eventStreamerFunc(func(context.Context, chan<- uv.Event) error { t.Error("live decoder after detach"); return nil }),
			}
			err := attachment.Run(ctx)
			if (!cleanupFailure && err != nil) || (cleanupFailure && !errors.Is(err, failure)) {
				t.Fatalf("startup detach error: %v", err)
			}
			select {
			case <-connectorJoined:
			default:
				t.Fatal("connector leaked")
			}
		})
	}
}

func TestAttachmentStartupHandoffFailureClosesTransport(t *testing.T) {
	joined := make(chan struct{})
	closed := false
	failure := errors.New("native discard failed")
	attachment := Attachment{
		TTY: startupTestTTY(t, joined),
		StartupEvents: eventStreamerFunc(func(ctx context.Context, _ chan<- uv.Event) error {
			defer close(joined)
			<-ctx.Done()
			return ctx.Err()
		}),
		Connect:     connectTransport(&attachmentTransport{recordingTransport: &recordingTransport{}, closeFn: func() error { closed = true; return nil }}),
		BeforeInput: func(context.Context) error { return failure },
		Events:      eventStreamerFunc(func(context.Context, chan<- uv.Event) error { t.Error("live input after failed handoff"); return nil }),
	}
	if err := attachment.Run(context.Background()); !errors.Is(err, failure) || !closed {
		t.Fatalf("failed handoff: %v, closed: %v", err, closed)
	}
}

func TestAttachmentStartupHandoffDetachClosesTransport(t *testing.T) {
	joined := make(chan struct{})
	closed := false
	attachment := Attachment{
		TTY: startupTestTTY(t, joined),
		StartupEvents: eventStreamerFunc(func(ctx context.Context, _ chan<- uv.Event) error {
			defer close(joined)
			<-ctx.Done()
			return ctx.Err()
		}),
		Connect:     connectTransport(&attachmentTransport{recordingTransport: &recordingTransport{}, closeFn: func() error { closed = true; return nil }}),
		BeforeInput: func(context.Context) error { return ErrDetached },
		Events:      eventStreamerFunc(func(context.Context, chan<- uv.Event) error { t.Error("live input after handoff detach"); return nil }),
	}
	if err := attachment.Run(context.Background()); err != nil || !closed {
		t.Fatalf("handoff detach: %v, closed: %v", err, closed)
	}
}

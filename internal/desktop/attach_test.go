package desktop

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

type eventStreamerFunc func(context.Context, chan<- uv.Event) error

func (streamer eventStreamerFunc) StreamEvents(ctx context.Context, events chan<- uv.Event) error {
	return streamer(ctx, events)
}

func TestAttachmentRestoresOuterTerminalAfterDetach(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	makeRawCalls := 0
	restoreCalls := 0
	tty := &OuterTTY{
		fd:     7,
		output: &output,
		ops: ttyOperations{
			makeRaw: func(fd uintptr) (*term.State, error) {
				makeRawCalls++
				if fd != 7 {
					t.Fatalf("raw fd = %d", fd)
				}
				return &term.State{}, nil
			},
			restore: func(fd uintptr, _ *term.State) error {
				restoreCalls++
				if fd != 7 {
					t.Fatalf("restore fd = %d", fd)
				}
				return nil
			},
		},
	}
	streamer := eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
		select {
		case events <- uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}:
		case <-ctx.Done():
		}
		<-ctx.Done()
		return nil
	})
	attachment := Attachment{TTY: tty, Events: streamer, Transport: &recordingTransport{}}
	if err := attachment.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if makeRawCalls != 1 || restoreCalls != 1 {
		t.Fatalf("raw/restore calls = %d/%d", makeRawCalls, restoreCalls)
	}
	if got := output.String(); got != enterOuterTerminal+restoreOuterTerminal {
		t.Fatalf("outer terminal lifecycle = %q", got)
	}
	if err := tty.Restore(); err != nil || restoreCalls != 1 {
		t.Fatalf("second restore = %v, calls %d", err, restoreCalls)
	}
}

func TestAttachmentRestoresAfterInputFailure(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	restoreCalls := 0
	tty := &OuterTTY{
		output: &output,
		ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error {
				restoreCalls++
				return nil
			},
		},
	}
	streamer := eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
		select {
		case events <- uv.KeyPressEvent{Code: 'x', Text: "x"}:
		case <-ctx.Done():
		}
		<-ctx.Done()
		return nil
	})
	want := errors.New("send failed")
	attachment := Attachment{TTY: tty, Events: streamer, Transport: &recordingTransport{err: want}}
	if err := attachment.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("attachment error = %v", err)
	}
	if restoreCalls != 1 || !strings.HasSuffix(output.String(), restoreOuterTerminal) {
		t.Fatalf("restore calls/output = %d/%q", restoreCalls, output.String())
	}
}

func TestOuterTTYRestoresRawModeWhenSetupWriteFails(t *testing.T) {
	t.Parallel()
	restoreCalls := 0
	tty := &OuterTTY{
		output: failingWriter{},
		ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error {
				restoreCalls++
				return nil
			},
		},
	}
	if err := tty.Enter(); err == nil {
		t.Fatal("setup write unexpectedly succeeded")
	}
	if restoreCalls != 1 {
		t.Fatalf("raw restore calls = %d", restoreCalls)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

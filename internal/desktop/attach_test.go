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

// The decoder deliberately stays alive until transport shutdown. Restoring
// terminal modes before both have stopped races a final read or screen write.
func TestAttachmentJoinsWorkersBeforeRestoring(t *testing.T) {
	t.Parallel()
	stopDecoder := make(chan struct{})
	decoderStopped := make(chan struct{})
	transport := &attachmentTransport{recordingTransport: &recordingTransport{}, closeFn: func() error {
		close(stopDecoder)
		return nil
	}}
	var output bytes.Buffer
	tty := &OuterTTY{output: &output, ops: ttyOperations{
		makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
		restore: func(uintptr, *term.State) error {
			select {
			case <-decoderStopped:
				return nil
			default:
				return errors.New("terminal restored while decoder was still running")
			}
		},
	}}
	streamer := eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
		defer close(decoderStopped)
		select {
		case events <- uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}:
		case <-ctx.Done():
		}
		<-ctx.Done()
		<-stopDecoder
		return nil
	})
	attachment := Attachment{TTY: tty, Events: streamer, Connect: connectTransport(transport)}
	if err := attachment.Run(context.Background()); err != nil {
		// Ensure a failing implementation does not leave the fixture running.
		select {
		case <-stopDecoder:
		default:
			close(stopDecoder)
		}
		<-decoderStopped
		t.Fatal(err)
	}
}

type attachmentTransport struct {
	*recordingTransport
	closeFn func() error
	done    <-chan struct{}
	err     error
}

func (transport *attachmentTransport) Close() error {
	if transport.closeFn != nil {
		return transport.closeFn()
	}
	return nil
}

func (transport *attachmentTransport) Done() <-chan struct{} { return transport.done }
func (transport *attachmentTransport) Err() error            { return transport.err }

func connectTransport(transport AttachmentTransport) func(context.Context) (AttachmentTransport, error) {
	return func(context.Context) (AttachmentTransport, error) { return transport, nil }
}

func TestAttachmentConnectsAfterEnteringTerminal(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	closed := false
	restored := false
	connectionDone := make(chan struct{})
	close(connectionDone)
	want := errors.New("broker disconnected")
	tty := &OuterTTY{output: &output, ops: ttyOperations{
		makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
		restore: func(uintptr, *term.State) error {
			restored = true
			if !closed {
				t.Error("terminal restored before the connection stopped painting")
			}
			return nil
		},
	}}
	attachment := Attachment{
		TTY: tty,
		Events: eventStreamerFunc(func(ctx context.Context, _ chan<- uv.Event) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		Connect: func(context.Context) (AttachmentTransport, error) {
			if !tty.rawActive || output.String() != enterOuterTerminal {
				t.Error("connection started before outer terminal entry")
			}
			return &attachmentTransport{
				recordingTransport: &recordingTransport{}, done: connectionDone, err: want,
				closeFn: func() error { closed = true; return nil },
			}, nil
		},
	}
	if err := attachment.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("disconnect error = %v, want %v", err, want)
	}
	if !restored {
		t.Fatal("outer terminal not restored after disconnect")
	}
}

func TestAttachmentRestoresAfterConnectionFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("connection refused")
	var output bytes.Buffer
	restored := false
	attachment := Attachment{
		TTY: &OuterTTY{output: &output, ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error { restored = true; return nil },
		}},
		Events: eventStreamerFunc(func(context.Context, chan<- uv.Event) error {
			t.Error("input started despite connection failure")
			return nil
		}),
		Connect: func(context.Context) (AttachmentTransport, error) { return nil, want },
	}
	if err := attachment.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("connect error = %v", err)
	}
	if !restored || output.String() != enterOuterTerminal+restoreOuterTerminal {
		t.Fatal("outer terminal not restored after connection failure")
	}
}

func TestAttachmentPreservesShutdownErrors(t *testing.T) {
	t.Parallel()
	readErr := errors.New("decode failed")
	closeErr := errors.New("close failed")
	restoreErr := errors.New("restore failed")
	var output bytes.Buffer
	attachment := Attachment{
		TTY: &OuterTTY{output: &output, ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error { return restoreErr },
		}},
		Events: eventStreamerFunc(func(context.Context, chan<- uv.Event) error {
			return errors.Join(context.Canceled, readErr)
		}),
		Connect: connectTransport(&attachmentTransport{
			recordingTransport: &recordingTransport{}, closeFn: func() error { return closeErr },
		}),
	}
	err := attachment.Run(context.Background())
	for _, want := range []error{readErr, closeErr, restoreErr} {
		if !errors.Is(err, want) {
			t.Errorf("shutdown error %v does not contain %v", err, want)
		}
	}
}

func TestAttachmentClosedEventChannelStopsInsteadOfSpinning(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	closed := false
	attachment := Attachment{
		TTY: &OuterTTY{output: &output, ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error { return nil },
		}},
		Events: eventStreamerFunc(func(ctx context.Context, events chan<- uv.Event) error {
			close(events)
			<-ctx.Done()
			return ctx.Err()
		}),
		Connect: connectTransport(&attachmentTransport{
			recordingTransport: &recordingTransport{}, closeFn: func() error { closed = true; return nil },
		}),
	}
	if err := attachment.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("closed event channel did not close connection")
	}
}

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
	attachment := Attachment{TTY: tty, Events: streamer, Connect: connectTransport(&attachmentTransport{recordingTransport: &recordingTransport{}})}
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
	attachment := Attachment{TTY: tty, Events: streamer, Connect: connectTransport(&attachmentTransport{recordingTransport: &recordingTransport{err: want}})}
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

func TestOuterTTYRetriesFailedRawRestore(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	restoreCalls := 0
	tty := &OuterTTY{
		output: &output,
		ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error {
				restoreCalls++
				if restoreCalls == 1 {
					return errors.New("temporary restore failure")
				}
				return nil
			},
		},
	}
	if err := tty.Enter(); err != nil {
		t.Fatal(err)
	}
	if err := tty.Restore(); err == nil {
		t.Fatal("first raw restore unexpectedly succeeded")
	}
	if err := tty.Restore(); err != nil {
		t.Fatalf("retry raw restore: %v", err)
	}
	if restoreCalls != 2 {
		t.Fatalf("raw restore calls = %d, want 2", restoreCalls)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestAttachmentReportsActivationFailureOnce(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	want := errors.New("status write failed")
	attachment := Attachment{
		TTY: &OuterTTY{output: &output, ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error { return nil },
		}},
		Events: eventStreamerFunc(func(ctx context.Context, _ chan<- uv.Event) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		Connect:  connectTransport(&attachmentTransport{recordingTransport: &recordingTransport{}}),
		Activate: func(context.Context) error { return want },
	}
	err := attachment.Run(context.Background())
	if !errors.Is(err, want) || strings.Count(err.Error(), want.Error()) != 1 {
		t.Fatalf("activation error = %v", err)
	}
}

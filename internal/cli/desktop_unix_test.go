//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestAttachDesktopFailedConnectionRestoresOwnedTTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	// Darwin exposes the kernel-maintained FWASWRITTEN bit after the first
	// successful write. Prime that bit before comparing mutable descriptor
	// flags; it is not a mode change made by the attachment.
	if _, err := slave.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	flags := confirmationFileFlags(t, slave)
	err = AttachDesktop(context.Background(), DesktopConfig{
		RuntimeDir: t.TempDir(), ClientBuild: "fixture", Input: slave, Output: slave,
		Session: protocol.BrokerRPCSessionRecord{ID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", State: protocol.Running},
	})
	if err == nil {
		t.Fatal("attachment unexpectedly connected without a broker")
	}
	after, stateErr := term.GetState(slave.Fd())
	if stateErr != nil || !reflect.DeepEqual(before, after) || confirmationFileFlags(t, slave) != flags {
		t.Fatalf("failed attach changed caller TTY or flags: %v", stateErr)
	}
	if _, err := slave.Write([]byte("caller still owns terminal")); err != nil {
		t.Fatalf("attachment closed caller terminal: %v", err)
	}
}

type startupBlockedOutput struct{ entered, resume chan struct{} }

func (output startupBlockedOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	if strings.Contains(string(data), "Control |") {
		close(output.entered)
		select {
		case <-output.resume:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return len(data), nil
}

type startupNativeTransport struct {
	*desktopFakeConnection
	actions   []desktop.InputAction
	admission *desktopAdmission
}

func (transport *startupNativeTransport) AdmissionEpoch() uint64 { return transport.admission.epoch() }
func (transport *startupNativeTransport) SendAdmitted(ctx context.Context, action desktop.InputAction, epoch uint64) error {
	if epoch == 0 || epoch != transport.AdmissionEpoch() {
		return nil
	}
	return transport.Send(ctx, action)
}

func (transport *startupNativeTransport) Send(_ context.Context, action desktop.InputAction) error {
	transport.actions = append(transport.actions, action)
	return nil
}

func TestDesktopStartupBlockedControlWriteNeverReplaysInput(t *testing.T) {
	for name, early := range map[string]string{
		"text":          "typed while still Syncing\r",
		"partial-paste": "\x1b[200~paste while still Syncing\r",
		"partial-csi":   "\x1b[",
	} {
		t.Run(name, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			before, err := term.GetState(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			reader, err := NewFileConfirmationReader(slave)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			startup, err := desktop.NewBoundedInput(reader, "xterm-256color")
			if err != nil {
				t.Fatal(err)
			}
			captured := make(chan uint64, 8)
			admission := &desktopAdmission{}
			live, err := desktop.NewAdmittedInput(reader, "xterm-256color", func() uint64 {
				epoch := admission.epoch()
				captured <- epoch
				return epoch
			})
			if err != nil {
				t.Fatal(err)
			}
			tty, err := desktop.NewOuterTTY(slave.Fd(), slave.Fd(), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			output := startupBlockedOutput{make(chan struct{}), make(chan struct{})}
			client := desktopFake()
			client.state.Controller = true
			admission.client = client
			transport := &startupNativeTransport{desktopFakeConnection: client, admission: admission}
			display := &desktopDisplay{writer: output, size: func() (int, int, error) { return 80, 25, nil }, client: client, controlPending: true,
				screen: &protocol.ScreenMetadata{Columns: 80, Rows: 24}}
			ready := desktopReadyBarrier{make(chan struct{}), make(chan struct{})}
			close(ready.ready)
			activated := make(chan struct{})
			attachment := desktop.Attachment{TTY: tty, Events: live, StartupEvents: startup,
				Connect: func(ctx context.Context) (desktop.AttachmentTransport, error) {
					if err := finishDesktopStartup(ctx, ready, display); err != nil {
						return nil, err
					}
					return transport, nil
				},
				BeforeInput: func(context.Context) error {
					return discardDesktopInput(reader.file.handle)
				},
				Activate: func(ctx context.Context) error {
					return display.activate(ctx, func() {
						admission.enabled.Store(true)
						close(activated)
					})
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- attachment.Run(ctx) }()
			select {
			case <-output.entered:
			case <-ctx.Done():
				t.Fatal("Control output did not reach barrier")
			}
			if _, err := master.Write([]byte(early)); err != nil {
				t.Fatal(err)
			}
			select {
			case epoch := <-captured:
				if epoch != 0 {
					t.Fatalf("blocked activation admitted epoch %d", epoch)
				}
			case <-ctx.Done():
				t.Fatal("startup decoder did not drain input during blocked output")
			}
			close(output.resume)
			select {
			case <-activated:
			case <-ctx.Done():
				t.Fatal("input activation did not complete")
			}
			if completion := map[string]string{"partial-paste": "\x1b[201~", "partial-csi": "A"}[name]; completion != "" {
				if _, err := master.Write([]byte(completion)); err != nil {
					t.Fatal(err)
				}
				select {
				case <-captured:
				case <-ctx.Done():
					t.Fatal("partial input completion not read")
				}
			}
			if _, err := master.Write([]byte("z\x1d")); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if len(transport.actions) != 1 || transport.actions[0] != (desktop.TextAction{Text: "z"}) {
				t.Fatalf("startup input/parser state replayed: %#v", transport.actions)
			}
			after, err := term.GetState(slave.Fd())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("startup did not restore caller terminal: %v", err)
			}
		})
	}
}

func TestDesktopStartupDiscardDoesNotReplayEarlyTTYInput(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	state, err := term.MakeRaw(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(slave.Fd(), state)
	reader, err := NewFileConfirmationReader(slave)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := master.Write([]byte("early command\r")); err != nil {
		t.Fatal(err)
	}
	if err := discardDesktopInput(reader.file.handle); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	var data [64]byte
	if n, err := reader.ReadContext(ctx, data[:]); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup input replayed: %q %v", data[:n], err)
	}
	if _, err := master.Write([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()
	if n, err := reader.ReadContext(readCtx, data[:]); err != nil || string(data[:n]) != "fresh" {
		t.Fatalf("fresh input lost: %q %v", data[:n], err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := term.GetState(slave.Fd()); err != nil {
		t.Fatalf("caller TTY closed: %v", err)
	}
}

// The startup decoder may be canceled after reading input and before turning
// it into events. A Ctrl+] it read must still detach at the handoff.
func TestDesktopStartupInputRecordsReadDetach(t *testing.T) {
	for _, test := range []struct {
		input  string
		detach bool
	}{{"typed", false}, {"typed\r\x1d", true}} {
		input, output, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		reader, err := NewFileConfirmationReader(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.WriteString(test.input); err != nil {
			t.Fatal(err)
		}
		startup := &startupDetachInput{reader: reader}
		var data [64]byte
		// The bytes read here are dropped, as a canceled decoder drops them.
		if _, err := startup.ReadContext(context.Background(), data[:]); err != nil {
			t.Fatal(err)
		}
		if startup.detach.Load() != test.detach {
			t.Fatalf("input %q: recorded detach = %t, want %t", test.input, !test.detach, test.detach)
		}
		_ = reader.Close()
		_ = input.Close()
		_ = output.Close()
	}
}

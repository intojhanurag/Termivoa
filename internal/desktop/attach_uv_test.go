package desktop

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

// Ultraviolet sends every decoded event with an unconditional channel send,
// including events remaining in a batch after the detach chord. A helper
// process lets a failing join be terminated without leaving blocked decoder
// goroutines in the parent test process. No real terminal modes are changed.
func TestAttachmentRealUltravioletDrainsEventsAfterDetach(t *testing.T) {
	const helper = "PTERM_DESKTOP_UV_DETACH_HELPER"
	if os.Getenv(helper) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestAttachmentRealUltravioletDrainsEventsAfterDetach$", "-test.v")
		command.Env = append(os.Environ(), helper+"=1")
		output, err := command.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("attachment did not join the real decoder after its detach event: %v\n%s", ctx.Err(), output)
		}
		if err != nil {
			t.Fatalf("real decoder helper failed: %v\n%s", err, output)
		}
		return
	}

	var output bytes.Buffer
	restored := false
	transport := &attachmentTransport{recordingTransport: &recordingTransport{}}
	attachment := Attachment{
		TTY: &OuterTTY{output: &output, ops: ttyOperations{
			makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
			restore: func(uintptr, *term.State) error { restored = true; return nil },
		}},
		Events:  uv.NewTerminalReader(bytes.NewReader([]byte{0x1d, 'x', 'y'}), "xterm-256color"),
		Connect: connectTransport(transport),
	}
	if err := attachment.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("outer terminal was not restored after decoder shutdown")
	}
	if len(transport.actions) != 0 {
		t.Fatal("events after detach were forwarded to the managed session")
	}
}

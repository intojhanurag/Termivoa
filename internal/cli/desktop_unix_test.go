//go:build darwin || linux

package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
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

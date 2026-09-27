//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestFileTerminalInputOutputAliasRestoresFlags(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	// Darwin exposes its kernel FWASWRITTEN bit through F_GETFL. Prime one
	// ordinary write before taking the exact flag baseline. Start blocking so
	// both adapters must exercise temporary O_NONBLOCK changes.
	if _, err := slave.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var primed [1]byte
	if _, err := io.ReadFull(master, primed[:]); err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(int(slave.Fd()), false); err != nil {
		t.Fatal(err)
	}
	beforeFlags := confirmationFileFlags(t, slave)
	reader, err := NewFileConfirmationReader(slave)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := NewFileTerminalWriter(slave)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// Both duplicates alias one file description. Input waits for a complete
	// line; output fills the PTY while the peer consumes nothing. Each adapter
	// must remain cancellable when the other restores the shared flags.
	readContext, cancelRead := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelRead()
	writeContext, cancelWrite := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelWrite()
	readResult := make(chan error, 1)
	writeResult := make(chan error, 1)
	go func() {
		var data [1]byte
		_, err := reader.ReadContext(readContext, data[:])
		readResult <- err
	}()
	go func() {
		_, err := writer.WriteContext(writeContext, bytes.Repeat([]byte("x"), 1<<20))
		writeResult <- err
	}()
	for _, result := range []<-chan error{readResult, writeResult} {
		select {
		case err := <-result:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("aliased descriptor operation returned %v", err)
			}
		case <-time.After(time.Second):
			_ = master.Close()
			t.Fatal("aliased terminal descriptor operation failed to cancel")
		}
	}
	if afterFlags := confirmationFileFlags(t, slave); afterFlags != beforeFlags {
		t.Fatalf("aliased adapters changed caller flags: %#x -> %#x", beforeFlags, afterFlags)
	}
}

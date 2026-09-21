//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestFileConfirmationPTYCancellationPreservesTTY(t *testing.T) {
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
	beforeFlags := confirmationFileFlags(t, slave)
	reader, err := NewFileConfirmationReader(slave)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		var data [1]byte
		_, err := reader.ReadContext(ctx, data[:])
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("PTY read returned %v", err)
		}
	case <-time.After(time.Second):
		_ = master.Close()
		<-result
		t.Fatal("PTY confirmation did not cancel promptly")
	}
	if afterFlags := confirmationFileFlags(t, slave); afterFlags != beforeFlags {
		t.Fatalf("cancellation changed original flags: %#x -> %#x", beforeFlags, afterFlags)
	}
	after, err := term.GetState(slave.Fd())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("confirmation changed terminal mode: err=%v", err)
	}
	if _, err := master.Write([]byte("yes\n")); err != nil {
		t.Fatal(err)
	}
	confirmer, err := NewLineConfirmer(reader, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := confirmer.Confirm(context.Background(), "Confirm? ")
	if err != nil || !accepted {
		t.Fatalf("fresh PTY confirmation failed: accepted=%v err=%v", accepted, err)
	}
	if afterFlags := confirmationFileFlags(t, slave); afterFlags != beforeFlags {
		t.Fatalf("successful read changed original flags: %#x -> %#x", beforeFlags, afterFlags)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := term.GetState(slave.Fd()); err != nil {
		t.Fatalf("Close invalidated caller terminal: %v", err)
	}
}

func TestFileConfirmationRestoresBlockingPipeFlags(t *testing.T) {
	var descriptors [2]int
	if err := unix.Pipe(descriptors[:]); err != nil {
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(descriptors[0]), "confirmation pipe")
	output := os.NewFile(uintptr(descriptors[1]), "confirmation writer")
	defer input.Close()
	defer output.Close()
	beforeFlags := confirmationFileFlags(t, input)
	if beforeFlags&unix.O_NONBLOCK != 0 {
		t.Fatal("fixture must start with blocking input")
	}
	reader, err := NewFileConfirmationReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	var data [1]byte
	if _, err := reader.ReadContext(ctx, data[:]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled pipe read returned %v", err)
	}
	if afterFlags := confirmationFileFlags(t, input); afterFlags != beforeFlags {
		t.Fatalf("pipe flags were not restored: %#x -> %#x", beforeFlags, afterFlags)
	}
}

func confirmationFileFlags(t *testing.T, source *os.File) int {
	t.Helper()
	connection, err := source.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var flagsErr error
	if err := connection.Control(func(fd uintptr) {
		flags, flagsErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if flagsErr != nil {
		t.Fatal(flagsErr)
	}
	return flags
}

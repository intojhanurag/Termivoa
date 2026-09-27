package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestFileConfirmationPipeCancellationAndOwnership(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	reader, err := NewFileConfirmationReader(input)
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
			t.Fatalf("blocked pipe returned %v, want context deadline", err)
		}
	case <-time.After(time.Second):
		_ = output.Close() // Release the test reader even if cancellation failed.
		<-result
		t.Fatal("blocked pipe did not cancel promptly")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
	// Closing/canceling the adapter must leave the caller's input descriptor
	// open, and cancellation must not create a background read of later input.
	if _, err := output.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	_ = output.Close()
	var data [1]byte
	if _, err := io.ReadFull(input, data[:]); err != nil || data[0] != 'y' {
		t.Fatalf("caller input unavailable or consumed: %q, %v", data[:], err)
	}
	if _, err := reader.ReadContext(context.Background(), data[:]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed adapter read returned %v", err)
	}
}

func TestFileConfirmationCloseInterruptsRead(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	reader, err := NewFileConfirmationReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	result := make(chan error, 1)
	go func() {
		var data [1]byte
		_, err := reader.ReadContext(context.Background(), data[:])
		result <- err
	}()
	// Close itself must finish while no input is available.
	closed := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		closed <- reader.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = output.Close()
		<-closed
		t.Fatal("Close did not interrupt the active pipe read")
	}
	if err := <-result; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("interrupted read returned %v", err)
	}
}

func TestFileConfirmationReadsPipeWithoutReadAhead(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader, err := NewFileConfirmationReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := output.Write([]byte("yes\nnext\n")); err != nil {
		t.Fatal(err)
	}
	_ = output.Close()
	var prompt bytes.Buffer
	confirmer, err := NewLineConfirmer(reader, &prompt)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := confirmer.Confirm(context.Background(), "Confirm? ")
	if err != nil || !accepted {
		t.Fatalf("pipe confirmation: accepted=%v err=%v", accepted, err)
	}
	remaining, err := io.ReadAll(input)
	if err != nil || string(remaining) != "next\n" {
		t.Fatalf("confirmation consumed subsequent input: %q err=%v", remaining, err)
	}
	var data [1]byte
	if _, err := reader.ReadContext(context.Background(), data[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("closed pipe returned %v, want EOF", err)
	}
}

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

type terminalWriteResult struct {
	count int
	err   error
}

func TestFileTerminalWriterCancelsBlockedPipe(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	writer, err := NewFileTerminalWriter(output)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result := make(chan terminalWriteResult, 1)
	go func() {
		n, err := writer.WriteContext(ctx, bytes.Repeat([]byte("x"), 1<<20))
		result <- terminalWriteResult{n, err}
	}()
	select {
	case result := <-result:
		if !errors.Is(result.err, context.DeadlineExceeded) || result.count < 0 || result.count >= 1<<20 {
			t.Fatalf("blocked output did not cancel with a bounded count: %+v", result)
		}
	case <-time.After(time.Second):
		_ = input.Close()
		<-result
		t.Fatal("blocked output did not cancel promptly")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Stat(); err != nil {
		t.Fatalf("writer closed caller-owned source: %v", err)
	}
	_ = output.Close()
	// The writer and its callback have joined, so draining to EOF cannot wait
	// on a leaked duplicate handle or a background writer.
	if _, err := io.Copy(io.Discard, input); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteContext(context.Background(), []byte("late")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed writer accepted output: %v", err)
	}
}

func TestFileTerminalWriterCloseJoinsBlockedWrite(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	writer, err := NewFileTerminalWriter(output)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	result := make(chan error, 1)
	go func() {
		_, err := writer.WriteContext(context.Background(), bytes.Repeat([]byte("x"), 1<<20))
		result <- err
	}()
	closed := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		closed <- writer.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = input.Close()
		<-closed
		t.Fatal("Close did not join a blocked output write")
	}
	if err := <-result; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write interrupted by Close returned %v", err)
	}
}

func TestFileTerminalWriterOwnsDuplicateAndWritesExactly(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	writer, err := NewFileTerminalWriter(output)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// The duplicate remains usable after the caller releases its own handle.
	_ = output.Close()
	want := []byte("synthetic terminal drawing")
	if n, err := writer.WriteContext(context.Background(), want); err != nil || n != len(want) {
		t.Fatalf("write count=%d err=%v", n, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(input)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("output=%q err=%v", got, err)
	}
}

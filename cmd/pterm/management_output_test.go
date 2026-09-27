package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestManagementOutputPreservesRegularFileRedirect(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output, err := newManagementOutput(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if output.owned != nil {
		t.Fatal("regular file incorrectly treated as interruptible terminal output")
	}
	if _, err := io.WriteString(output, "redirected\n"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if n, err := output.Write([]byte("late")); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("write after cancellation = %d, %v", n, err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "still open\n"); err != nil {
		t.Fatalf("caller file closed: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if actual, err := io.ReadAll(file); err != nil || string(actual) != "redirected\nstill open\n" {
		t.Fatalf("redirected output = %q, %v", actual, err)
	}
}

func TestManagementOutputCustomWriterAndNullSink(t *testing.T) {
	var buffer bytes.Buffer
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	for _, source := range []io.Writer{&buffer, null} {
		output, err := newManagementOutput(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if output.owned != nil {
			t.Fatal("nonterminal sink incorrectly treated as interruptible terminal output")
		}
		if n, err := output.Write([]byte("text")); err != nil || n != 4 {
			t.Fatalf("write = %d, %v", n, err)
		}
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if buffer.String() != "text" {
		t.Fatalf("custom output = %q", buffer.String())
	}
}

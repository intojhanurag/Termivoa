//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Console setup happens only in a disposable helper process. It never
// detaches the owner's shell or changes the parent process's console.
func TestFileConfirmationWindowsConsoleCancellation(t *testing.T) {
	const helper = "PTERM_CONFIRMATION_CONSOLE_HELPER"
	if os.Getenv(helper) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileConfirmationWindowsConsoleCancellation$", "-test.v")
		command.Env = append(os.Environ(), helper+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("console helper failed: %v\n%s", err, output)
		}
		return
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	_, _, _ = kernel.NewProc("FreeConsole").Call()
	if result, _, err := kernel.NewProc("AllocConsole").Call(); result == 0 {
		t.Fatalf("allocate fixture console: %v", err)
	}
	defer kernel.NewProc("FreeConsole").Call()
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var before uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &before); err != nil {
		t.Fatal(err)
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
		t.Fatalf("console cancellation returned %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	var after uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &after); err != nil || after != before {
		t.Fatalf("caller console changed or was closed: before=%#x after=%#x err=%v", before, after, err)
	}
}

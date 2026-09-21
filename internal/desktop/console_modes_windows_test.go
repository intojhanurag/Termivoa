//go:build windows

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every mutation happens in a disposable process with its own console. Never
// detach from, allocate over, or change modes/code pages on the parent console.
func TestOuterTTYWindowsIsolatedConsoleModes(t *testing.T) {
	const helper = "PTERM_DESKTOP_CONSOLE_MODES_HELPER"
	if os.Getenv(helper) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOuterTTYWindowsIsolatedConsoleModes$", "-test.v")
		command.Env = append(os.Environ(), helper+"=1")
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated Windows console fixture failed: %v\n%s", err, output)
		}
		return
	}
	assertExclusiveConsole(t)
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	original := captureNativeConsole(t, input, output)
	defer original.restore(t, input, output)

	// Tests run sequentially: code pages are console-wide, not handle-local.
	for _, testCase := range []string{"normal", "already_utf8_vt_wrap_off", "setup_partial_failure", "entry_write_failure", "restore_write_retry", "mode_restore_retry"} {
		t.Run(testCase, func(t *testing.T) {
			baseline := nativeConsoleState{
				inputCP: 437, outputCP: 850,
				inputMode: windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT |
					windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_EXTENDED_FLAGS,
				outputMode: windows.ENABLE_WRAP_AT_EOL_OUTPUT,
			}
			if testCase == "already_utf8_vt_wrap_off" {
				baseline.inputCP, baseline.outputCP = consoleUTF8, consoleUTF8
				baseline.outputMode = windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
			}
			baseline.restore(t, input, output)
			defer baseline.restore(t, input, output)
			writer := &nativeConsoleWriter{output: windows.Handle(output.Fd())}
			tty, err := NewOuterTTY(input.Fd(), output.Fd(), writer)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := tty.Restore(); err != nil {
					t.Errorf("final guard cleanup: %v", err)
				}
			}()
			modes := tty.modes.(*consoleModes)
			failure := errors.New("synthetic console failure after native state transition")
			switch testCase {
			case "setup_partial_failure":
				setter := modes.ops.setOutputMode
				fail := true
				modes.ops.setOutputMode = func(mode uint32) error {
					if err := setter(mode); err != nil {
						return err
					}
					if fail {
						fail = false
						return failure
					}
					return nil
				}
				if err := tty.Enter(); !errors.Is(err, failure) {
					t.Fatalf("partial setup error = %v", err)
				}
				if writer.calls != 0 {
					t.Fatal("failed setup emitted terminal drawing sequences")
				}
				assertNativeConsole(t, input, output, baseline)
				return
			case "entry_write_failure":
				writer.failOn, writer.failure = 1, failure
				if err := tty.Enter(); !errors.Is(err, failure) {
					t.Fatalf("entry write error = %v", err)
				}
				assertNativeConsole(t, input, output, baseline)
				return
			}

			if err := tty.Enter(); err != nil {
				t.Fatal(err)
			}
			active := baseline
			active.inputCP, active.outputCP = consoleUTF8, consoleUTF8
			active.inputMode &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
			active.inputMode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
			active.outputMode |= windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
			got := captureNativeConsole(t, input, output)
			// The entry sequence deliberately disables autowrap. Permit the
			// associated wrap flag to change during drawing, but require both
			// VT/processed bits and exact original flags after restoration.
			got.outputMode |= windows.ENABLE_WRAP_AT_EOL_OUTPUT
			active.outputMode |= windows.ENABLE_WRAP_AT_EOL_OUTPUT
			if got != active {
				t.Fatalf("active console state = %#v, want %#v", got, active)
			}
			if err := tty.Enter(); err == nil {
				t.Fatal("already active outer terminal was entered twice")
			}
			switch testCase {
			case "restore_write_retry":
				writer.failOn, writer.failure = writer.calls+1, failure
			case "mode_restore_retry":
				setter := modes.ops.setOutputCP
				fail := true
				modes.ops.setOutputCP = func(codePage uint32) error {
					if err := setter(codePage); err != nil {
						return err
					}
					if fail {
						fail = false
						return failure
					}
					return nil
				}
			}
			if testCase == "restore_write_retry" || testCase == "mode_restore_retry" {
				if err := tty.Restore(); !errors.Is(err, failure) {
					t.Fatalf("first restore error = %v", err)
				}
				if got := captureNativeConsole(t, input, output); got.inputMode != baseline.inputMode {
					t.Fatal("failed display restoration prevented raw-input restoration")
				}
				if err := tty.Enter(); err == nil {
					t.Fatal("outer terminal reentered with pending restoration")
				}
			}
			if err := tty.Restore(); err != nil {
				t.Fatal(err)
			}
			assertNativeConsole(t, input, output, baseline)
			if err := tty.Restore(); err != nil {
				t.Fatal("idempotent restoration:", err)
			}
			assertNativeConsole(t, input, output, baseline)
		})
	}
}

func assertExclusiveConsole(t *testing.T) {
	t.Helper()
	var processes [2]uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	count, _, err := proc.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
	if count != 1 || processes[0] != uint32(os.Getpid()) {
		t.Fatalf("refusing console mutation without an exclusive child console: count=%d error=%v", count, err)
	}
}

type nativeConsoleState struct {
	inputCP, outputCP, inputMode, outputMode uint32
}

func captureNativeConsole(t *testing.T, input, output *os.File) nativeConsoleState {
	t.Helper()
	var state nativeConsoleState
	var err error
	if state.inputCP, err = windows.GetConsoleCP(); err != nil {
		t.Fatal(err)
	}
	if state.outputCP, err = windows.GetConsoleOutputCP(); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &state.inputMode); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &state.outputMode); err != nil {
		t.Fatal(err)
	}
	return state
}

func (state nativeConsoleState) restore(t *testing.T, input, output *os.File) {
	t.Helper()
	for _, err := range []error{
		windows.SetConsoleMode(windows.Handle(input.Fd()), state.inputMode),
		windows.SetConsoleMode(windows.Handle(output.Fd()), state.outputMode),
		windows.SetConsoleCP(state.inputCP), windows.SetConsoleOutputCP(state.outputCP),
	} {
		if err != nil {
			t.Errorf("restore isolated fixture console: %v", err)
		}
	}
}

func assertNativeConsole(t *testing.T, input, output *os.File, want nativeConsoleState) {
	t.Helper()
	if got := captureNativeConsole(t, input, output); got != want {
		t.Fatalf("console state = %#v, want %#v", got, want)
	}
}

// Check the real console before every sequence, including a retried restore.
// WriteFile exercises byte-oriented console output rather than os.File's
// optional WriteConsoleW path. Failure injection never touches parent handles.
type nativeConsoleWriter struct {
	output        windows.Handle
	calls, failOn int
	failure       error
}

func (writer *nativeConsoleWriter) Write(data []byte) (int, error) {
	writer.calls++
	outputCP, err := windows.GetConsoleOutputCP()
	if err != nil {
		return 0, err
	}
	var mode uint32
	if err := windows.GetConsoleMode(writer.output, &mode); err != nil {
		return 0, err
	}
	if outputCP != consoleUTF8 || mode&(consoleProcessedOutput|consoleVTOutput) != consoleProcessedOutput|consoleVTOutput {
		return 0, fmt.Errorf("drawing attempted without UTF-8/VT: output=%d mode=%#x", outputCP, mode)
	}
	if writer.calls == writer.failOn {
		return 0, writer.failure
	}
	var written uint32
	err = windows.WriteFile(writer.output, data, &written, nil)
	return int(written), err
}

package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/charmbracelet/x/term"
)

type consoleFixture struct {
	input, output, mode uint32
	calls               []string
	fail                string
}

func (fixture *consoleFixture) ops() consoleModeOps {
	get := func(name string, value *uint32) func() (uint32, error) {
		return func() (uint32, error) {
			if fixture.fail == "get-"+name {
				return 0, errors.New("synthetic getter failure")
			}
			return *value, nil
		}
	}
	set := func(name string, value *uint32) func(uint32) error {
		return func(next uint32) error {
			fixture.calls = append(fixture.calls, fmt.Sprintf("%s=%d", name, next))
			// Simulate partial mutation even on failure: restoration must be
			// registered before the OS setter is called.
			*value = next
			if fixture.fail == name {
				fixture.fail = ""
				return errors.New("synthetic setter failure")
			}
			return nil
		}
	}
	return consoleModeOps{
		inputCP: get("input", &fixture.input), outputCP: get("output", &fixture.output), outputMode: get("mode", &fixture.mode),
		setInputCP: set("input", &fixture.input), setOutputCP: set("output", &fixture.output), setOutputMode: set("mode", &fixture.mode),
	}
}

func TestConsoleModesPreserveOriginalValuesAndRestoreReverseOrder(t *testing.T) {
	t.Parallel()
	fixture := &consoleFixture{input: 437, output: 932, mode: 2}
	guard := &consoleModes{ops: fixture.ops()}
	if err := guard.Enter(); err != nil {
		t.Fatal(err)
	}
	if fixture.input != consoleUTF8 || fixture.output != consoleUTF8 || fixture.mode != 7 {
		t.Fatalf("console not ready for UTF-8 drawing: %#v", fixture)
	}
	if err := guard.Enter(); err == nil {
		t.Fatal("entered active modes twice")
	}
	if err := guard.Restore(); err != nil {
		t.Fatal(err)
	}
	if fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
		t.Fatalf("original values lost: %#v", fixture)
	}
	want := []string{"input=65001", "output=65001", "mode=7", "mode=2", "output=932", "input=437"}
	if !reflect.DeepEqual(fixture.calls, want) {
		t.Fatalf("set/restore order = %v", fixture.calls)
	}
	if err := guard.Restore(); err != nil || !reflect.DeepEqual(fixture.calls, want) {
		t.Fatalf("idempotent restore changed modes: %v, %v", fixture.calls, err)
	}
}

func TestConsoleModesSetupFailuresRestoreAllTouchedValues(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"get-input", "get-output", "get-mode", "input", "output", "mode"} {
		t.Run(failure, func(t *testing.T) {
			fixture := &consoleFixture{input: 437, output: 932, mode: 2, fail: failure}
			guard := &consoleModes{ops: fixture.ops()}
			var output bytes.Buffer
			restored := 0
			tty := &OuterTTY{output: &output, modes: guard, ops: ttyOperations{
				makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
				restore: func(uintptr, *term.State) error { restored++; return nil },
			}}
			if err := tty.Enter(); err == nil {
				t.Fatal("setup failure was lost")
			}
			if output.Len() != 0 || restored != 1 || tty.rawActive || tty.modesActive {
				t.Fatalf("failed setup painted or left active terminal: output=%q, restored=%d", output.String(), restored)
			}
			if fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
				t.Fatalf("partial setup did not roll back: %#v", fixture)
			}
		})
	}
}

func TestConsoleModesRestoreContinuesAfterFailureAndCanRetry(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"input", "output", "mode"} {
		t.Run(failure, func(t *testing.T) {
			fixture := &consoleFixture{input: 437, output: 932, mode: 2}
			guard := &consoleModes{ops: fixture.ops()}
			if err := guard.Enter(); err != nil {
				t.Fatal(err)
			}
			fixture.fail = failure
			if err := guard.Restore(); err == nil {
				t.Fatal("restoration failure was suppressed")
			}
			if fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
				t.Fatal("one failure prevented other restoration attempts")
			}
			before := len(fixture.calls)
			if err := guard.Restore(); err != nil {
				t.Fatal(err)
			}
			if len(fixture.calls) != before+1 || guard.inputChanged || guard.outputChanged || guard.modeChanged {
				t.Fatalf("retry did not complete just the failed change: %v", fixture.calls)
			}
		})
	}
}

func TestConsoleAlreadyConfiguredStillRestoresDrawingMode(t *testing.T) {
	t.Parallel()
	fixture := &consoleFixture{input: consoleUTF8, output: consoleUTF8, mode: 7}
	guard := &consoleModes{ops: fixture.ops()}
	if err := guard.Enter(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) != 0 {
		t.Fatalf("already configured console changed during entry: %v", fixture.calls)
	}
	// Simulate an escape sequence changing an output-mode bit, without any
	// native setter having changed modes during setup.
	fixture.mode &^= 2
	if err := guard.Restore(); err != nil || !reflect.DeepEqual(fixture.calls, []string{"mode=7"}) || fixture.mode != 7 {
		t.Fatalf("drawing mode not restored or code pages unnecessarily rewritten: %v, %v", fixture.calls, err)
	}
}

type consoleCheckingWriter struct {
	t       *testing.T
	fixture *consoleFixture
	buffer  bytes.Buffer
	writes  int
	failAt  int
}

func (writer *consoleCheckingWriter) String() string { return writer.buffer.String() }

func (writer *consoleCheckingWriter) Write(data []byte) (int, error) {
	if writer.fixture.output != consoleUTF8 || writer.fixture.mode&consoleVTOutput == 0 {
		writer.t.Error("terminal sequence emitted before UTF-8/VT setup or after mode restoration")
	}
	writer.writes++
	if writer.writes == writer.failAt {
		return 0, errors.New("synthetic interrupted sequence")
	}
	return writer.buffer.Write(data)
}

func TestOuterTTYRestoresDrawingBeforeConsoleModes(t *testing.T) {
	t.Parallel()
	fixture := &consoleFixture{input: 437, output: 932, mode: 2}
	output := &consoleCheckingWriter{t: t, fixture: fixture}
	tty := &OuterTTY{output: output, modes: &consoleModes{ops: fixture.ops()}, ops: ttyOperations{
		makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
		restore: func(uintptr, *term.State) error { return nil },
	}}
	if err := tty.Enter(); err != nil {
		t.Fatal(err)
	}
	if err := tty.Restore(); err != nil {
		t.Fatal(err)
	}
	if output.String() != enterOuterTerminal+restoreOuterTerminal || fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
		t.Fatalf("incorrect complete restoration: %q, %#v", output.String(), fixture)
	}
}

func TestOuterTTYRestoreSequenceRetryReenablesConsoleDrawing(t *testing.T) {
	t.Parallel()
	fixture := &consoleFixture{input: 437, output: 932, mode: 2}
	output := &consoleCheckingWriter{t: t, fixture: fixture, failAt: 2}
	tty := &OuterTTY{output: output, modes: &consoleModes{ops: fixture.ops()}, ops: ttyOperations{
		makeRaw: func(uintptr) (*term.State, error) { return &term.State{}, nil },
		restore: func(uintptr, *term.State) error { return nil },
	}}
	if err := tty.Enter(); err != nil {
		t.Fatal(err)
	}
	if err := tty.Restore(); err == nil {
		t.Fatal("interrupted restoration succeeded")
	}
	if tty.rawActive || tty.modesActive || fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
		t.Fatal("failed sequence prevented native mode restoration")
	}
	if err := tty.Restore(); err != nil {
		t.Fatal(err)
	}
	if tty.displayActive || fixture.input != 437 || fixture.output != 932 || fixture.mode != 2 {
		t.Fatal("retry lost original modes or left pending drawing")
	}
	if output.String() != enterOuterTerminal+restoreOuterTerminal {
		t.Fatalf("retry drawing = %q", output.String())
	}
}

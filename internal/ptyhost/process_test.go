package ptyhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

var fixtureExecutable string

func TestMain(m *testing.M) {
	os.Exit(runPTYTests(m.Run))
}

const fixtureActivationTimeout = 10 * time.Second

func runPTYTests(run func() int) int {
	temporaryDirectory, err := os.MkdirTemp("", "pterm-ptyhost-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(temporaryDirectory)

	executableName := "ptychild"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	fixtureExecutable = filepath.Join(temporaryDirectory, executableName)

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "locate ptyhost test source")
		return 1
	}
	fixturePackage := filepath.Join(filepath.Dir(sourceFile), "..", "..", "tests", "fixtures", "ptychild")
	build := exec.Command("go", "build", "-o", fixtureExecutable, fixturePackage)
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build PTY fixture: %v\n%s", err, output)
		return 1
	}

	// A newly linked executable can still be waiting at _dyld_start on macOS
	// while concurrently launched test binaries are activating. Validate this
	// exact fixture once outside the PTY I/O deadline, with its own hard bound.
	// The probe uses no PTY, opens no project, and cannot spawn descendants.
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), fixtureActivationTimeout)
	err = activateFixture(ctx, fixtureExecutable)
	cancel()
	fmt.Fprintf(os.Stderr, "PTY fixture activation: %s (limit %s)\n", time.Since(started).Round(time.Millisecond), fixtureActivationTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "activate PTY fixture: %v\n", err)
		return 1
	}
	return run()
}

func activateFixture(ctx context.Context, executable string) error {
	command := exec.CommandContext(ctx, executable, "--pterm-fixture-activation")
	command.Env = append(os.Environ(), "PTERM_PTY_FIXTURE_MODE=args")
	return runFixtureActivation(ctx, command)
}

func runFixtureActivation(ctx context.Context, command *exec.Cmd) error {
	output, err := command.Output()
	if err != nil {
		return errors.Join(ctx.Err(), err)
	}
	var result struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(output, &result); err != nil || len(result.Args) != 1 || result.Args[0] != "--pterm-fixture-activation" {
		return errors.New("fixture activation returned an unexpected response")
	}
	return nil
}

func TestFixtureActivationFailureDoesNotBecomeReadinessSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), fixtureActivationTimeout)
	defer cancel()
	wrongResponse := exec.CommandContext(ctx, fixtureExecutable, "wrong-activation-marker")
	wrongResponse.Env = append(os.Environ(), "PTERM_PTY_FIXTURE_MODE=args")
	if err := runFixtureActivation(ctx, wrongResponse); err == nil {
		t.Fatal("activation accepted an incorrect response")
	}

	// A safe synthetic sleep models native activation that never completes.
	// The separate activation gate must fail and reap it, not retry or enter
	// any PTY lifecycle test after its deadline.
	blocked, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	sleeper := exec.CommandContext(blocked, fixtureExecutable)
	sleeper.Env = append(os.Environ(), "PTERM_PTY_FIXTURE_MODE=sleep")
	started := time.Now()
	if err := runFixtureActivation(blocked, sleeper); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked activation = %v", err)
	}
	if sleeper.Process != nil && sleeper.ProcessState == nil {
		t.Fatal("activation returned without reaping its timed-out child")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("activation cancellation took %s", elapsed)
	}
}

func TestFixtureActivationValidatesExactExecutable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), fixtureActivationTimeout)
	defer cancel()
	if err := activateFixture(ctx, fixtureExecutable); err != nil {
		t.Fatal(err)
	}
	if err := activateFixture(ctx, filepath.Join(t.TempDir(), "missing-fixture")); err == nil {
		t.Fatal("activation accepted a missing executable")
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := activateFixture(canceled, fixtureExecutable); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled activation = %v", err)
	}
}

func TestStartReadWriteResizeAndWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	process := startFixture(t, ctx, "echo")
	defer process.Close()

	output := readUntil(t, process, "READY", 2*time.Second)
	if !strings.Contains(output, "READY") {
		t.Fatalf("fixture did not become ready: %q", output)
	}

	if _, err := process.Write(append([]byte("hello, PTY"), fixtureEnter...)); err != nil {
		t.Fatalf("write: %v", err)
	}
	output += readUntil(t, process, "ECHO_OK", 2*time.Second)
	if !strings.Contains(output, `ECHO:"hello, PTY`) {
		t.Fatalf("fixture did not echo input: %q", output)
	}

	if err := process.Resize(120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	columns, rows, err := process.Size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if columns != 120 || rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", columns, rows)
	}

	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func TestStartPreservesArgumentBoundaries(t *testing.T) {
	want := []string{"path with spaces", "unicode-文件", "$(not-executed)", "; rm -rf never"}
	process, err := Start(context.Background(), Spec{
		Executable: fixtureExecutable,
		Args:       want,
		Env:        []string{"PTERM_PTY_FIXTURE_MODE=args"},
		Columns:    DefaultColumns,
		Rows:       DefaultRows,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer process.Close()

	line := readJSONLine(t, process, 2*time.Second)
	var result struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(line, &result); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	if fmt.Sprint(result.Args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", result.Args, want)
	}
	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestWaitReportsExitStatus(t *testing.T) {
	process := startFixtureWithEnv(t, context.Background(), "exit", "PTERM_PTY_FIXTURE_EXIT=7")
	defer process.Close()

	err := process.Wait(context.Background())
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("wait error = %T %v, want *exec.ExitError", err, err)
	}
	if exitError.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exitError.ExitCode())
	}
}

func TestTerminateAfterNaturalExitIsNoop(t *testing.T) {
	process := startFixture(t, context.Background(), "args")
	defer process.Close()
	_ = readJSONLine(t, process, 2*time.Second)
	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}

	terminationContext, cancelTermination := context.WithTimeout(context.Background(), time.Second)
	defer cancelTermination()
	if err := process.Terminate(terminationContext); err != nil {
		t.Fatalf("terminate exited process: %v", err)
	}
}

func TestWaitContextDoesNotTerminateSession(t *testing.T) {
	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	process := startFixture(t, sessionContext, "sleep")
	defer process.Close()
	readUntil(t, process, "READY", 2*time.Second)

	waitContext, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if err := process.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
	select {
	case <-process.done:
		t.Fatal("wait context terminated session")
	default:
	}

	cancelSession()
	waitForExit(t, process)
}

func TestInvalidStartAndResize(t *testing.T) {
	for _, test := range []struct {
		name    string
		columns int
		rows    int
	}{
		{name: "too few columns", columns: MinColumns - 1, rows: DefaultRows},
		{name: "too many columns", columns: MaxColumns + 1, rows: DefaultRows},
		{name: "too few rows", columns: DefaultColumns, rows: MinRows - 1},
		{name: "too many rows", columns: DefaultColumns, rows: MaxRows + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Start(context.Background(), Spec{
				Executable: fixtureExecutable,
				Columns:    test.columns,
				Rows:       test.rows,
			})
			if !errors.Is(err, ErrInvalidDimensions) {
				t.Fatalf("start error = %v, want ErrInvalidDimensions", err)
			}
		})
	}

	if _, err := Start(context.Background(), Spec{Columns: DefaultColumns, Rows: DefaultRows}); !errors.Is(err, ErrEmptyExecutable) {
		t.Fatalf("empty executable error = %v, want ErrEmptyExecutable", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Start(canceled, Spec{Executable: fixtureExecutable, Columns: DefaultColumns, Rows: DefaultRows}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start error = %v, want context.Canceled", err)
	}

	process := startFixture(t, context.Background(), "sleep")
	defer func() {
		terminationContext, cancelTermination := context.WithTimeout(context.Background(), time.Second)
		defer cancelTermination()
		_ = process.Terminate(terminationContext)
		_ = process.Close()
	}()
	if err := process.Resize(MinColumns-1, DefaultRows); !errors.Is(err, ErrInvalidDimensions) {
		t.Fatalf("resize error = %v, want ErrInvalidDimensions", err)
	}
}

func TestConcurrentResizeAndSize(t *testing.T) {
	process := startFixture(t, context.Background(), "sleep")
	defer func() {
		terminationContext, cancelTermination := context.WithTimeout(context.Background(), time.Second)
		defer cancelTermination()
		_ = process.Terminate(terminationContext)
		_ = process.Close()
	}()
	readUntil(t, process, "READY", 2*time.Second)

	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := range 25 {
				columns := 80 + (worker+iteration)%20
				rows := 24 + (worker+iteration)%10
				if err := process.Resize(columns, rows); err != nil {
					t.Errorf("resize: %v", err)
					return
				}
				if _, _, err := process.Size(); err != nil {
					t.Errorf("size: %v", err)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestTerminalQueriesRoundTripWithoutViewer(t *testing.T) {
	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	process := startFixture(t, sessionContext, "query")
	defer process.Close()

	core, err := terminal.New(sessionContext, terminal.Config{Columns: DefaultColumns, Rows: DefaultRows})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	var responseMu sync.Mutex
	var responses bytes.Buffer
	writerDone := make(chan error, 1)
	go func() {
		for output := range core.Output() {
			responseMu.Lock()
			_, _ = responses.Write(output)
			responseMu.Unlock()
			if _, err := process.Write(output); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	var observedMu sync.Mutex
	var observed bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 128)
		for {
			count, readErr := process.Read(buffer)
			if count > 0 {
				observedMu.Lock()
				observed.Write(buffer[:count])
				seenSuccess := bytes.Contains(observed.Bytes(), []byte("QUERY_OK"))
				observedMu.Unlock()
				if err := core.WriteOutput(context.Background(), buffer[:count]); err != nil {
					readDone <- err
					return
				}
				if seenSuccess {
					readDone <- nil
					return
				}
			}
			if readErr != nil {
				readDone <- fmt.Errorf("read query fixture before success: %w (output %q)", readErr, observed.Bytes())
				return
			}
		}
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		observedMu.Lock()
		observedBytes := append([]byte(nil), observed.Bytes()...)
		observedMu.Unlock()
		responseMu.Lock()
		responseBytes := append([]byte(nil), responses.Bytes()...)
		responseMu.Unlock()
		t.Fatalf("terminal query fixture timed out (child output %q, terminal responses %x)", observedBytes, responseBytes)
	}
	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("query fixture exit: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writerDone:
		if err != nil && !errors.Is(err, os.ErrClosed) {
			t.Fatalf("response writer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("response writer did not stop")
	}
}

func startFixture(t *testing.T, ctx context.Context, mode string) *Process {
	t.Helper()
	return startFixtureWithEnv(t, ctx, mode)
}

func startFixtureWithEnv(t *testing.T, ctx context.Context, mode string, environment ...string) *Process {
	t.Helper()
	process, err := Start(ctx, Spec{
		Executable: fixtureExecutable,
		Env:        append([]string{"PTERM_PTY_FIXTURE_MODE=" + mode}, environment...),
		Columns:    DefaultColumns,
		Rows:       DefaultRows,
	})
	if err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	return process
}

func readUntil(t *testing.T, process *Process, marker string, timeout time.Duration) string {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	resultChannel := make(chan result, 1)
	go func() {
		var output bytes.Buffer
		buffer := make([]byte, 256)
		for !strings.Contains(output.String(), marker) {
			read, err := process.Read(buffer)
			if read > 0 {
				_, _ = output.Write(buffer[:read])
			}
			if err != nil {
				resultChannel <- result{data: output.Bytes(), err: err}
				return
			}
		}
		resultChannel <- result{data: output.Bytes()}
	}()

	select {
	case result := <-resultChannel:
		if result.err != nil {
			t.Fatalf("read before %q: %v (output %q)", marker, result.err, result.data)
		}
		return string(result.data)
	case <-time.After(timeout):
		_ = process.Close()
		t.Fatalf("timed out waiting for %q", marker)
		return ""
	}
}

func readJSONLine(t *testing.T, process *Process, timeout time.Duration) []byte {
	t.Helper()
	type result struct {
		line []byte
		err  error
	}
	resultChannel := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(process).ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if start := bytes.IndexByte(line, '{'); start >= 0 {
			line = line[start:]
		}
		resultChannel <- result{line: line, err: err}
	}()
	select {
	case result := <-resultChannel:
		if result.err != nil {
			t.Fatalf("read JSON: %v", result.err)
		}
		return result.line
	case <-time.After(timeout):
		_ = process.Close()
		t.Fatal("timed out reading JSON")
		return nil
	}
}

func waitForExit(t *testing.T, process *Process) {
	t.Helper()
	waitContext, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWait()
	if err := process.Wait(waitContext); err == nil {
		t.Fatal("canceled process exited successfully; want signal exit")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("canceled process was not reaped")
	}
}

func readDescendantPID(t *testing.T, process *Process) int {
	t.Helper()
	output := readUntil(t, process, "DESCENDANT=", 2*time.Second)
	line := output[strings.Index(output, "DESCENDANT=")+len("DESCENDANT="):]
	line = strings.TrimSpace(strings.SplitN(line, "\n", 2)[0])
	descendantPID, err := strconv.Atoi(strings.TrimSuffix(line, "\r"))
	if err != nil {
		t.Fatalf("parse descendant PID from %q: %v", output, err)
	}
	return descendantPID
}

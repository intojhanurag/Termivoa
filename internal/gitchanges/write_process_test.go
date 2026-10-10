//go:build darwin || linux || windows

package gitchanges

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A real, controlled process tree also exercises Windows Job ownership where
// a shell hook fixture would depend on which git shell the owner installed.
func TestWriteProcessFixture(t *testing.T) {
	mode := os.Getenv("PTERM_WRITE_PROCESS_FIXTURE")
	if mode == "" || os.Args[len(os.Args)-1] != "--pterm-write-process-fixture" {
		return
	}
	dir := os.Getenv("PTERM_WRITE_PROCESS_DIRECTORY")
	if err := os.WriteFile(filepath.Join(dir, mode+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	next := map[string]string{"parent": "child", "child": "leaf"}[mode]
	if next != "" {
		child := writeFixtureCommand(context.Background(), dir, next)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func writeFixtureCommand(ctx context.Context, dir, mode string) *exec.Cmd {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWriteProcessFixture$", "--", "--pterm-write-process-fixture")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PTERM_WRITE_PROCESS_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "PTERM_WRITE_PROCESS_FIXTURE="+mode, "PTERM_WRITE_PROCESS_DIRECTORY="+dir)
	command.WaitDelay = time.Second
	return command
}

func TestWriteProcessCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			dir := t.TempDir()
			// Keep an unrelated process alive to catch a group/job targeting mistake.
			controlDir := t.TempDir()
			control := writeFixtureCommand(context.Background(), controlDir, "leaf")
			if err := control.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Process.Kill(); _ = control.Wait() }()
			controlPID := waitWriteFixturePID(t, controlDir, "leaf")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			command := writeFixtureCommand(ctx, dir, "parent")
			done := make(chan error, 1)
			go func() { done <- runWriteCommand(command) }()
			var pids []int
			defer func() {
				for _, pid := range pids {
					if process, err := os.FindProcess(pid); err == nil {
						_ = process.Kill()
					}
				}
			}()
			for _, mode := range []string{"parent", "child", "leaf"} {
				pids = append(pids, waitWriteFixturePID(t, dir, mode))
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled write returned success")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled write exceeded bound")
			}
			for _, pid := range pids {
				assertWriteProcessGone(t, pid)
			}
			pids = nil
			if writeProcessGone(controlPID) {
				t.Fatal("cleanup ended unrelated process")
			}
		})
	}
}

func TestWriteProcessFailedAndCanceledStart(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWriteCommand(writeFixtureCommand(canceled, t.TempDir(), "parent")); err == nil {
		t.Fatal("canceled start succeeded")
	}
	command := exec.CommandContext(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err := runWriteCommand(command); err == nil {
		t.Fatal("missing command started")
	}
}

func waitWriteFixturePID(t *testing.T, dir, mode string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(dir, mode+".pid"))
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 1 {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture did not publish %s PID", mode)
	return 0
}

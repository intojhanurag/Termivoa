//go:build windows

package ptyhost

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// This native test intentionally starts the descendant immediately. Start
// creates the ConPTY child suspended, assigns its Job Object, and only then
// resumes it, so no descendant can escape between process creation and Job
// enrollment.
func TestWindowsJobCleansDescendantsWithoutShutdownDeadlock(t *testing.T) {
	process := startFixture(t, context.Background(), "tree")
	descendantPID := readDescendantPID(t, process)

	terminationContext, cancelTermination := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelTermination()
	if err := process.Terminate(terminationContext); err != nil {
		_ = process.Close()
		t.Fatalf("terminate process tree: %v", err)
	}
	waitForExit(t, process)

	closeResult := make(chan error, 1)
	go func() { closeResult <- process.Close() }()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("close ConPTY: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing ConPTY deadlocked after process termination")
	}

	assertWindowsProcessGone(t, uint32(descendantPID))
}

func assertWindowsProcessGone(t *testing.T, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return
		}
		if err == nil {
			status, waitErr := windows.WaitForSingleObject(process, 0)
			_ = windows.CloseHandle(process)
			if waitErr == nil && status == windows.WAIT_OBJECT_0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d remains after Job Object termination: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Needs native Windows: it reads the real user default environment block.
// Cross-compiled only on macOS (go test -c).
func TestHostEnvironmentUsesUserDefaultBlockWithoutLauncherVariables(t *testing.T) {
	t.Setenv("CLAUDE_CODE_X", "leak")
	t.Setenv("WT_SESSION", "leak")
	t.Setenv("PTERM_LAUNCHER_ONLY", "leak")

	names := map[string]bool{}
	for _, entry := range hostEnvironment(os.Environ()) {
		name, _, _ := strings.Cut(entry, "=")
		names[strings.ToUpper(name)] = true
	}
	for _, name := range []string{"SYSTEMROOT", "PATH", "USERPROFILE", "TEMP"} {
		if !names[name] {
			t.Errorf("session environment lacks %s", name)
		}
	}
	for _, name := range []string{"CLAUDE_CODE_X", "WT_SESSION", "PTERM_LAUNCHER_ONLY"} {
		if names[name] {
			t.Errorf("session environment inherited launcher variable %s", name)
		}
	}
}

//go:build windows

package ptyhost

import (
	"context"
	"errors"
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

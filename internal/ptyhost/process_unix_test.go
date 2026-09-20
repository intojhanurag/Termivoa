//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ptyhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestChildOwnsSessionAndControllingTerminal(t *testing.T) {
	process := startFixture(t, context.Background(), "session")
	defer process.Close()

	line := readJSONLine(t, process, 2*time.Second)
	var info struct {
		PID            int  `json:"pid"`
		SessionID      int  `json:"sessionId"`
		ProcessGroupID int  `json:"processGroupId"`
		ControllingTTY bool `json:"controllingTty"`
	}
	if err := json.Unmarshal(line, &info); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	if !info.ControllingTTY {
		t.Fatal("child has no controlling terminal")
	}
	if info.SessionID != info.PID {
		t.Fatalf("session ID = %d, want child PID %d", info.SessionID, info.PID)
	}
	if info.ProcessGroupID != info.PID {
		t.Fatalf("process group ID = %d, want child PID %d", info.ProcessGroupID, info.PID)
	}
	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestSessionCancellationCleansProcessGroup(t *testing.T) {
	sessionContext, cancelSession := context.WithCancel(context.Background())
	process := startFixture(t, sessionContext, "tree")
	defer process.Close()

	descendantPID := readDescendantPID(t, process)

	cancelSession()
	waitForExit(t, process)

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(descendantPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d remains after session cancellation: %v", descendantPID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNaturalLeaderExitCleansDescendants(t *testing.T) {
	process := startFixture(t, context.Background(), "orphan")
	defer process.Close()

	descendantPID := readDescendantPID(t, process)
	if err := process.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	assertProcessGone(t, descendantPID)
}

func TestExplicitTerminateCleansProcess(t *testing.T) {
	process := startFixture(t, context.Background(), "sleep")
	defer process.Close()
	readUntil(t, process, "READY", 2*time.Second)

	terminationContext, cancelTermination := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelTermination()
	if err := process.Terminate(terminationContext); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	waitForExit(t, process)
	if err := syscall.Kill(process.PID(), 0); err == nil || errors.Is(err, os.ErrPermission) {
		t.Fatalf("process %d remains after termination: %v", process.PID(), err)
	}
}

func TestTerminateDoesNotKillUnrelatedSession(t *testing.T) {
	first := startFixture(t, context.Background(), "sleep")
	defer first.Close()
	second := startFixture(t, context.Background(), "sleep")
	defer second.Close()
	readUntil(t, first, "READY", 2*time.Second)
	readUntil(t, second, "READY", 2*time.Second)

	terminationContext, cancelTermination := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelTermination()
	if err := first.Terminate(terminationContext); err != nil {
		t.Fatalf("terminate first session: %v", err)
	}
	waitForExit(t, first)

	select {
	case <-second.done:
		t.Fatal("terminating one session also terminated an unrelated session")
	default:
	}

	if err := second.Terminate(terminationContext); err != nil {
		t.Fatalf("terminate second session: %v", err)
	}
	waitForExit(t, second)
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d remains after session ended: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ptyhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// An interactive shell puts each background job in its own process group and
// ignores SIGTERM. Terminate must still end those jobs, the same way closing a
// real terminal window hangs up the shell and its jobs.
func TestTerminateHangsUpInteractiveShellJobs(t *testing.T) {
	for _, shell := range []struct {
		path string
		args []string
	}{
		{"/bin/zsh", []string{"-f", "-i"}},
		{"/bin/bash", []string{"--norc", "--noprofile", "-i"}},
	} {
		t.Run(filepath.Base(shell.path), func(t *testing.T) {
			if _, err := os.Stat(shell.path); err != nil {
				t.Skipf("%s is not installed", shell.path)
			}
			process, err := Start(context.Background(), Spec{
				Executable: shell.path, Args: shell.args, Directory: t.TempDir(),
				Env:     []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "},
				Columns: 80, Rows: 24,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			if _, err := process.Write([]byte(`sleep 300 & echo JOB=$!=E""ND` + "\n")); err != nil {
				t.Fatal(err)
			}
			output := readUntil(t, process, "=END\r\n", 5*time.Second)
			match := regexp.MustCompile(`JOB=(\d+)=END`).FindStringSubmatch(output)
			if match == nil {
				t.Fatalf("no job pid in %q", output)
			}
			jobPID, _ := strconv.Atoi(match[1])
			defer syscall.Kill(jobPID, syscall.SIGKILL)
			// The broker always drains output; an exiting shell otherwise waits
			// for its unread terminal output.
			go func() {
				buffer := make([]byte, 4096)
				for {
					if _, err := process.Read(buffer); err != nil {
						return
					}
				}
			}()

			terminationContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := process.Terminate(terminationContext); err != nil {
				t.Fatalf("terminate: %v", err)
			}
			assertProcessGone(t, jobPID)
		})
	}
}

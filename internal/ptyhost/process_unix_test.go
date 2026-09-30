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
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
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

// A background job that traps hangup and needs a moment to clean up must get
// the grace period, both when the session is terminated and when the user
// ends the shell with exit. The job runs in its own process group, so only the
// session boundary reaches it.
func TestSessionJobsGetGracePeriod(t *testing.T) {
	const shell = "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("%s is not installed", shell)
	}
	for _, ending := range []string{"terminate", "exit"} {
		t.Run(ending, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "cleaned")
			process, err := Start(context.Background(), Spec{
				Executable: shell, Args: []string{"--norc", "--noprofile", "-i"}, Directory: t.TempDir(),
				Env:     []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "},
				Columns: 80, Rows: 24,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			// The job starts with the default hangup action. It signals through
			// the FIFO once its trap is set, and only then does the shell print
			// the job PID, so the test never ends the session before the trap.
			armed := filepath.Join(filepath.Dir(marker), "armed")
			job := `mkfifo ` + armed + `; (trap 'sleep 0.1; echo ok > ` + marker + `; exit 0' HUP TERM; echo > ` + armed + `; while :; do sleep 0.05; done) & read < ` + armed + `; echo JOB=$!=E""ND` + "\n"
			if _, err := process.Write([]byte(job)); err != nil {
				t.Fatal(err)
			}
			output := readUntil(t, process, "=END\r\n", 5*time.Second)
			match := regexp.MustCompile(`JOB=(\d+)=END`).FindStringSubmatch(output)
			if match == nil {
				t.Fatalf("no job pid in %q", output)
			}
			jobPID, _ := strconv.Atoi(match[1])
			defer syscall.Kill(jobPID, syscall.SIGKILL)
			go func() {
				buffer := make([]byte, 4096)
				for {
					if _, err := process.Read(buffer); err != nil {
						return
					}
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if ending == "terminate" {
				if err := process.Terminate(ctx); err != nil {
					t.Fatalf("terminate: %v", err)
				}
			} else {
				if _, err := process.Write([]byte("exit\n")); err != nil {
					t.Fatal(err)
				}
				if err := process.Wait(ctx); err != nil {
					t.Fatalf("wait: %v", err)
				}
			}
			assertProcessGone(t, jobPID)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("background job was killed before its hangup cleanup finished: %v", err)
			}
		})
	}
}

func TestChildDoesNotInheritLaunchingProgramEnvironment(t *testing.T) {
	home := t.TempDir()
	for name, value := range map[string]string{
		"HOME": home, "PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8",
		"CLAUDE_CODE_X": "leak", "CLAUDECODE": "1", "TERM_PROGRAM": "iTerm.app",
		"ITERM_SESSION_ID": "w0t0p0", "VSCODE_GIT_IPC_HANDLE": "/tmp/leak", "TERM": "dumb",
	} {
		t.Setenv(name, value)
	}

	process, err := Start(context.Background(), Spec{
		Executable: "/bin/sh",
		Args:       []string{"-c", "env; echo PTERM_ENV_END"},
		Env:        []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
		Columns:    DefaultColumns,
		Rows:       DefaultRows,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer process.Close()

	child := map[string]string{}
	for _, line := range strings.Split(readUntil(t, process, "PTERM_ENV_END", 5*time.Second), "\n") {
		if name, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			child[name] = value
		}
	}
	for name, want := range map[string]string{
		"HOME": home, "PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8",
		"TERM": "xterm-256color", "COLORTERM": "truecolor",
	} {
		if child[name] != want {
			t.Errorf("child %s = %q, want %q", name, child[name], want)
		}
	}
	for name := range child {
		if strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "VSCODE_") ||
			name == "TERM_PROGRAM" || name == "ITERM_SESSION_ID" {
			t.Errorf("child inherited %s", name) // never print values: tokens
		}
	}
	_ = process.Wait(context.Background())
}

// A session leader that exits after Close must not free its terminal before
// macOS revokes it, or the revoke can kill the next session's PTY (#100).
func TestCloseKeepsSlaveOpenUntilChildIsReaped(t *testing.T) {
	process := startFixture(t, context.Background(), "sleep")
	slave := process.pty.(*xpty.UnixPty).Slave()
	if err := process.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Closing the master hangs up the child, which may exit and be reaped
	// before this check; then Close correctly released the slave.
	if _, err := slave.Stat(); errors.Is(err, os.ErrClosed) && !process.Exited() {
		t.Fatal("slave closed before the child was reaped")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = process.Terminate(ctx)
	_ = process.Wait(ctx)
	for _, err := slave.Stat(); !errors.Is(err, os.ErrClosed); _, err = slave.Stat() {
		if ctx.Err() != nil {
			t.Fatal("slave still open after the child was reaped")
		}
		time.Sleep(time.Millisecond)
	}
}

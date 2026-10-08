//go:build darwin || linux

package gitchanges

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real git runs this hook; neither git nor the runner under test is mocked.
// Closing its pipes proves success cannot come from WaitDelay closing a pipe.
func TestWriteCancellationEndsHookDescendants(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "inherited pipes", true: "closed pipes"}[closed], func(t *testing.T) {
			dir := writableRepo(t)
			script := "#!/bin/sh\n"
			if closed {
				script += "exec >/dev/null 2>&1\n"
			}
			script += "printf '%s\\n' $$ > hook.pid\nsh -c 'printf \"%s\\n\" $$ > child.pid; sleep 30 & printf \"%s\\n\" \"$!\" > sleeper.pid; wait' &\nwait\n"
			if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := runWrite(ctx, dir, "commit", "--allow-empty", "-m", "fixture"); done <- err }()
			var pids []int
			defer func() {
				for _, pid := range pids {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}()
			for _, name := range []string{"hook.pid", "child.pid", "sleeper.pid"} {
				pids = append(pids, waitWritePID(t, dir, name))
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled git reported success")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("cancel did not finish within bound")
			}
			for _, pid := range pids {
				assertWriteProcessGone(t, pid)
			}
			pids = nil
		})
	}
}

func waitWritePID(t *testing.T, dir, name string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 1 {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture did not publish %s", name)
	return 0
}

func assertWriteProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writeProcessGone(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hook descendant PID %d survived canceled git", pid)
}

func writeProcessGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	// A dead Linux orphan may await init's reap; it cannot execute or mutate.
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if _, rest, ok := strings.Cut(string(data), ") "); ok && strings.HasPrefix(rest, "Z ") {
			return true
		}
	}
	return false
}

func TestWorktreeCancellationStopsHookBeforeRollback(t *testing.T) {
	dir := writableRepo(t)
	root := t.TempDir()
	t.Setenv("PTERM_WRITE_HOOK_PIDS", dir)
	script := "#!/bin/sh\nprintf '%s\\n' $$ > \"$PTERM_WRITE_HOOK_PIDS/hook.pid\"\nsh -c 'printf \"%s\\n\" $$ > \"$PTERM_WRITE_HOOK_PIDS/child.pid\"; sleep 30 & printf \"%s\\n\" \"$!\" > \"$PTERM_WRITE_HOOK_PIDS/sleeper.pid\"; wait' &\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "post-checkout"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := CreateWorktree(ctx, dir, root, "cancel-fixture", "main"); done <- err }()
	var pids []int
	defer func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	for _, name := range []string{"hook.pid", "child.pid", "sleeper.pid"} {
		pids = append(pids, waitWritePID(t, dir, name))
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled worktree reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rollback did not finish within bound")
	}
	for _, pid := range pids {
		assertWriteProcessGone(t, pid)
	}
	pids = nil
	if _, err := os.Stat(filepath.Join(root, filepath.Base(dir), "cancel-fixture")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree left after rollback: %v", err)
	}
	if got := output(t, dir, "branch", "--list", BranchPrefix+"cancel-fixture"); got != "" {
		t.Fatalf("new branch left after rollback: %s", got)
	}
}

func TestWriteSuccessfulHookKeepsEnvironmentAndCommit(t *testing.T) {
	dir := writableRepo(t)
	t.Setenv("PTERM_WRITE_HOOK_VALUE", "fixture-value")
	script := "#!/bin/sh\nprintf '%s' \"$PTERM_WRITE_HOOK_VALUE\" > successful-hook\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := runWrite(context.Background(), dir, "commit", "--allow-empty", "-m", "successful fixture"); err != nil {
		t.Fatalf("success hook: %v %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "successful-hook"))
	if err != nil || string(data) != "fixture-value" {
		t.Fatalf("hook environment/result = %q, %v", data, err)
	}
	if got := output(t, dir, "log", "-1", "--format=%s"); got != "successful fixture" {
		t.Fatalf("commit did not finish: %s", got)
	}
}

//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/platform"
)

func processGroup(t *testing.T, pid int) int {
	t.Helper()
	group, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

// TestUpDevRunsIndependentProcessGroups builds the real binary, starts the
// harness in a temporary development profile, kills the gateway's whole
// process group and proves the broker survives; Ctrl+C then stops both.
func TestUpDevRunsIndependentProcessGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pterm binary")
	}
	root, err := os.MkdirTemp("/tmp", "pterm-up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := filepath.Join(root, "pterm")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build pterm: %v\n%s", err, output)
	}
	runtimeDir := filepath.Join(root, "runtime")
	dataDir := filepath.Join(root, "data")
	up := exec.Command(binary, "up", "--dev", "--data-dir", dataDir, "--runtime-dir", runtimeDir, "--listen", freeLoopbackAddress(t))
	// Its own HOME and no herdr on PATH: the broker never runs the owner's
	// herdr and never dials its socket (docs/herdr-provider.md, S16). It may
	// still stat a herdr at a fixed system path; with no socket under this
	// HOME that herdr is "not running" and is never started.
	up.Env = append(os.Environ(), "HOME="+root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	stdout, err := up.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	pids := map[string]int{}
	t.Cleanup(func() {
		// The children run in their own process groups, so killing the
		// harness alone would orphan them when a check fails early.
		_ = up.Process.Kill()
		_, _ = up.Process.Wait()
		for _, pid := range pids {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})

	lines := bufio.NewScanner(stdout)
	var url string
	for url == "" && lines.Scan() {
		var name string
		var pid int
		if n, _ := fmt.Sscanf(lines.Text(), "%s started (pid %d)", &name, &pid); n == 2 {
			pids[name] = pid
		}
		if strings.HasPrefix(lines.Text(), "Development gateway: ") {
			url = strings.TrimPrefix(lines.Text(), "Development gateway: ")
		}
	}
	if url == "" || pids["broker"] == 0 || pids["gateway"] == 0 {
		t.Fatalf("harness did not report both processes and a URL: %v %q", pids, url)
	}
	if pids["broker"] == pids["gateway"] || processGroup(t, pids["broker"]) == processGroup(t, pids["gateway"]) {
		t.Fatal("broker and gateway share a process group")
	}

	if err := syscall.Kill(-pids["gateway"], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for lines.Scan() && (strings.HasPrefix(lines.Text(), "Press Ctrl+C") || strings.HasPrefix(lines.Text(), "Pair a browser: ")) {
	}
	if !strings.HasPrefix(lines.Text(), "gateway exited:") {
		t.Fatalf("harness did not report the gateway exit: %q", lines.Text())
	}
	if _, reachable, _ := activeSessions(context.Background(), platform.Paths{DataDir: dataDir, RuntimeDir: runtimeDir}); !reachable {
		t.Fatal("killing the gateway's process group stopped the broker")
	}

	if err := up.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := up.Wait(); err != nil {
		t.Fatalf("harness exit after Ctrl+C: %v", err)
	}
	if _, reachable, _ := activeSessions(context.Background(), platform.Paths{DataDir: dataDir, RuntimeDir: runtimeDir}); reachable {
		t.Fatal("broker still running after Ctrl+C")
	}
}

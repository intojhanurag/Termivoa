package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

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
	up := exec.Command(binary, "up", "--dev", "--data-dir", filepath.Join(root, "data"), "--runtime-dir", runtimeDir, "--listen", freeLoopbackAddress(t))
	stdout, err := up.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = up.Process.Kill(); _, _ = up.Process.Wait() })

	lines := bufio.NewScanner(stdout)
	pids := map[string]int{}
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
	for lines.Scan() && strings.HasPrefix(lines.Text(), "Press Ctrl+C") {
	}
	if !strings.HasPrefix(lines.Text(), "gateway exited:") {
		t.Fatalf("harness did not report the gateway exit: %q", lines.Text())
	}
	if _, reachable, _ := activeSessions(context.Background(), runtimeDir); !reachable {
		t.Fatal("killing the gateway's process group stopped the broker")
	}

	if err := up.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := up.Wait(); err != nil {
		t.Fatalf("harness exit after Ctrl+C: %v", err)
	}
	if _, reachable, _ := activeSessions(context.Background(), runtimeDir); reachable {
		t.Fatal("broker still running after Ctrl+C")
	}
}

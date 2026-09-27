//go:build darwin || linux

package ptyhost

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the listening dev server. The session leader
// listens itself and starts a copy that detaches with setsid, as a coding
// agent may do with a dev server.
func init() {
	mode := os.Getenv("PTERM_LISTEN_HELPER")
	if mode == "" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if mode == "detached" {
		fmt.Printf("%d:%d\n", os.Getpid(), port)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	executable, _ := os.Executable()
	detached := exec.Command(executable)
	detached.Env = []string{"PTERM_LISTEN_HELPER=detached"}
	detached.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, _ := detached.StdoutPipe()
	if detached.Start() != nil {
		os.Exit(2)
	}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	fmt.Printf("PORTS=%d:%s=END\n", port, strings.TrimSpace(line))
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestListeningPortsReportsOnlySessionTree(t *testing.T) {
	outside, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer outside.Close()
	outsidePort := uint16(outside.Addr().(*net.TCPAddr).Port)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := Start(context.Background(), Spec{
		Executable: executable, Env: []string{"PTERM_LISTEN_HELPER=leader"},
		Columns: DefaultColumns, Rows: DefaultRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	output := readUntil(t, process, "=END", 5*time.Second)
	match := regexp.MustCompile(`PORTS=(\d+):(\d+):(\d+)=END`).FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("no ports in %q", output)
	}
	leaderPort, _ := strconv.Atoi(match[1])
	detachedPID, _ := strconv.Atoi(match[2])
	detachedPort, _ := strconv.Atoi(match[3])
	defer syscall.Kill(detachedPID, syscall.SIGKILL)

	want := []PortTarget{{uint16(leaderPort), "127.0.0.1"}, {uint16(detachedPort), "127.0.0.1"}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ports, err := process.ListeningPorts(ctx)
		cancel()
		if err != nil {
			t.Fatalf("ListeningPorts: %v", err)
		}
		if slices.ContainsFunc(ports, func(target PortTarget) bool { return target.Port == outsidePort }) {
			t.Fatalf("reported port %d owned by the test process: %v", outsidePort, ports)
		}
		if slices.Contains(ports, want[0]) && slices.Contains(ports, want[1]) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ports = %v, want %v", ports, want)
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := process.Terminate(ctx); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	_ = process.Wait(ctx)
	if ports, err := process.ListeningPorts(ctx); ports != nil || err != nil {
		t.Fatalf("after exit ListeningPorts = %v, %v; want nil, nil", ports, err)
	}
}

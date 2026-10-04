//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// TestFakeHerdr drives the built binary the way the broker does: the socket
// through the herdr provider, state changes through control.sock, and the
// attach client in a PTY.
func TestFakeHerdr(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fakeherdr")
	}
	root, err := os.MkdirTemp("/tmp", "fakeherdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary, err := herdrtest.Build(filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	folder := filepath.Join(home, ".config", "herdr")
	snapshot, err := os.ReadFile(filepath.Join("..", "..", "internal", "herdr", "testdata", "snapshot-0.8.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := herdrtest.Scenario(folder, false, []string{"claude", "codex"}, snapshot); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(binary, "server")
	server.Env = []string{"HOME=" + home}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		_ = server.Wait()
	})
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(folder, "control.sock")); return err == nil })

	config := herdr.Config{BinaryPaths: []string{binary}, Home: home}
	status := herdr.Inspect(context.Background(), config)
	if status.State != protocol.HTTPProviderStateReady || status.Version != "0.8.2" || status.AttachAny ||
		strings.Join(status.AgentKinds, ",") != "claude,codex" {
		t.Fatalf("inspect = %+v", status)
	}
	if err := herdrtest.Control(folder, map[string]any{"op": "status", "pane": "w3:p1", "status": "blocked"}); err != nil {
		t.Fatal(err)
	}
	provider := herdr.New(config, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go provider.Run(ctx)
	waitFor(t, func() bool {
		groups := provider.Snapshot().Groups
		return len(groups) == 3 && groups[0].Items[1].State == "blocked"
	})

	// attach starts the provider's argv in a PTY and keeps reading it: a
	// client with unread output cannot exit on macOS.
	type client struct {
		process *ptyhost.Process
		mu      sync.Mutex
		text    strings.Builder
	}
	attach := func(target string) *client {
		t.Helper()
		found, err := provider.Attach(target)
		if err != nil {
			t.Fatal(err)
		}
		process, err := ptyhost.Start(context.Background(), ptyhost.Spec{
			Executable: found.Argv[0], Args: found.Argv[1:], Env: found.Env, Directory: root, Columns: 45, Rows: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = process.Close() })
		started := &client{process: process}
		go func() {
			buffer := make([]byte, 4096)
			for {
				count, err := process.Read(buffer)
				started.mu.Lock()
				started.text.Write(buffer[:count])
				started.mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		return started
	}
	output := func(started *client, want string) {
		t.Helper()
		waitFor(t, func() bool {
			started.mu.Lock()
			defer started.mu.Unlock()
			return strings.Contains(started.text.String(), want)
		})
	}
	wait := func(started *client) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return started.process.Wait(ctx)
	}
	first := attach("w1:p1.term_65d0031602b701")
	output(first, "FAKE-HERDR w1:p1 45x20")
	second := attach("w1:p1.term_65d0031602b701")
	output(second, "already has an attached client")
	if err := wait(second); err == nil {
		t.Fatal("a second attach exits 0")
	}
	if _, err := first.process.Write([]byte("hi\x02\x02!\x02x")); err != nil {
		t.Fatal(err)
	}
	// ctrl+b ctrl+b passes one ctrl+b; ctrl+b x drops the ctrl+b.
	waitFor(t, func() bool { return inputHex(readLog(t, folder), "w1:p1") == "6869022178" })
	if _, err := first.process.Write([]byte("\x02q")); err != nil {
		t.Fatal(err)
	}
	if err := wait(first); err != nil {
		t.Fatalf("ctrl+b q: %v", err)
	}
	if log := readLog(t, folder); !strings.Contains(log, "attach w1:p1 exit 0 detach") {
		t.Fatalf("log:\n%s", log)
	}

	// A closed pane ends the attach with exit 1.
	third := attach("w3:p1.term_65d0031602b703")
	output(third, "FAKE-HERDR w3:p1")
	if err := herdrtest.Control(folder, map[string]any{"op": "close", "pane": "w3:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := wait(third); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("closed pane: %v", err)
	}
	waitFor(t, func() bool { return !provider.Has("w3:p1.term_65d0031602b703") })
}

// TestProbeOnAHerdrWithoutTerminal: a herdr without the terminal command
// answers `terminal attach --help` with its general help and exit 0, and
// its default command starts the UI. The probe must read "cannot attach any
// pane" and never start the UI (it runs with no terminal; under a developer's
// terminal this also proves it has no controlling terminal).
func TestProbeOnAHerdrWithoutTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fakeherdr")
	}
	root, err := os.MkdirTemp("/tmp", "fakeherdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary, err := herdrtest.Build(filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	folder := filepath.Join(home, ".config", "herdr")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	scenario := `{"version":"0.8.2","protocol":20,"noTerminal":true,"kinds":["claude"],` +
		`"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[],"agents":[]}}`
	if err := os.WriteFile(filepath.Join(folder, "scenario.json"), []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(binary, "server")
	server.Env = []string{"HOME=" + home}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		_ = server.Wait()
	})
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(folder, "control.sock")); return err == nil })
	status := herdr.Inspect(context.Background(), herdr.Config{BinaryPaths: []string{binary}, Home: home})
	if status.State != protocol.HTTPProviderStateReady || status.AttachAny {
		t.Fatalf("inspect = %+v", status)
	}
	if log := readLog(t, folder); strings.Contains(log, "ui started") {
		t.Fatalf("the probe started the herdr UI:\n%s", log)
	}
}

// inputHex joins the hex input the attach client of pane logged.
func inputHex(log, pane string) string {
	var joined strings.Builder
	for _, line := range strings.Split(log, "\n") {
		if hex, ok := strings.CutPrefix(line, "input "+pane+" "); ok {
			joined.WriteString(hex)
		}
	}
	return joined.String()
}

func readLog(t *testing.T, folder string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(folder, "fake.log"))
	return string(data)
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

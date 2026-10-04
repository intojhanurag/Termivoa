//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
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

// rawCall sends one request to the fake's socket and returns herdr's error
// code ("" for a result).
func rawCall(t *testing.T, folder, method string, params map[string]any) string {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(folder, "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, _ := json.Marshal(map[string]any{"id": "1", "method": method, "params": params})
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	return reply.Error.Code
}

// TestFakeHerdrManage drives what docs/herdr-manage.md uses through the
// provider: workspace.create, "+" on a git workspace without worktrees
// (pane cwd; worktree.create gives the source a worktree record), tab.create,
// worktree.remove (dirty by scenario) and workspace.close as herdr 0.8.2 and
// as 0.9.3.
func TestFakeHerdrManage(t *testing.T) {
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
	repo := filepath.Join(root, "shop")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, groupCloseRequired := range []bool{false, true} {
		t.Run(map[bool]string{false: "herdr 0.8.2", true: "herdr 0.9.3"}[groupCloseRequired], func(t *testing.T) {
			home := filepath.Join(root, map[bool]string{false: "a", true: "b"}[groupCloseRequired])
			folder := filepath.Join(home, ".config", "herdr")
			if err := os.MkdirAll(folder, 0o700); err != nil {
				t.Fatal(err)
			}
			scenario, _ := json.Marshal(map[string]any{"attachAny": true, "dirty": []string{"w2"}, "groupCloseRequired": groupCloseRequired})
			if err := os.WriteFile(filepath.Join(folder, "scenario.json"), scenario, 0o600); err != nil {
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
			// herdr refuses a second server for a running session.
			second := exec.Command(binary, "server")
			second.Env = []string{"HOME=" + home}
			if output, err := second.CombinedOutput(); err == nil || !strings.Contains(string(output), "herdr server is already running") {
				t.Fatalf("second server = %v %s", err, output)
			}

			provider := herdr.New(herdr.Config{BinaryPaths: []string{binary}, Home: home}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go provider.Run(ctx)
			waitFor(t, func() bool { return provider.Snapshot().State == protocol.HTTPProviderStateReady })
			text := func(value string) *string { return &value }
			manage := func(request protocol.HTTPProviderManageRequest) protocol.HTTPProviderManageResponse {
				t.Helper()
				response, err := provider.Manage(ctx, request, herdr.Project{Folder: repo, Name: "shop"})
				if err != nil {
					t.Fatalf("%s: %v", request.Action, err)
				}
				return response
			}
			made := manage(protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace})
			if made.ItemID == nil || *made.ItemID != "w1" || made.TargetID == nil {
				t.Fatalf("new workspace = %+v", made)
			}
			if _, err := provider.Attach(*made.TargetID); err != nil {
				t.Fatalf("the new workspace's terminal: %v", err)
			}
			// The pane's cwd is in git: "+" without a worktree record.
			groups := provider.Snapshot().Groups
			if len(groups) != 1 || groups[0].ID != "ws-w1" || !groups[0].CanCreate {
				t.Fatalf("groups = %+v", groups)
			}
			created, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: "ws-w1", Name: "silver-meadow"})
			if err != nil || created.ItemID != "w2" {
				t.Fatalf("create = %+v %v", created, err)
			}
			waitFor(t, func() bool {
				groups := provider.Snapshot().Groups
				return len(groups) == 1 && strings.HasPrefix(groups[0].ID, "g") && len(groups[0].Items) == 2 &&
					groups[0].Items[0].Linked == nil && groups[0].Items[1].Linked != nil
			})
			tab := manage(protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w2")})
			if tab.TargetID == nil || *tab.TargetID == created.TargetID {
				t.Fatalf("new tab = %+v", tab)
			}
			if items := provider.Snapshot().Groups[0].Items; len(items[1].Targets) != 2 {
				t.Fatalf("targets after the new tab = %+v", items[1].Targets)
			}
			// w2 is dirty by the scenario: nothing is removed without force, and the provider never forces.
			remove := protocol.HTTPProviderManageRequest{Action: protocol.RemoveItem, ItemID: text("w2"), Name: text("silver-meadow")}
			if dirty := manage(remove); dirty.Problem == nil || *dirty.Problem != protocol.Uncommitted {
				t.Fatalf("dirty remove = %+v", dirty)
			}
			if code := rawCall(t, folder, "worktree.remove", map[string]any{"workspace_id": "w1", "force": false}); code != "not_linked_worktree" {
				t.Fatalf("remove a main checkout = %q", code)
			}
			if code := rawCall(t, folder, "workspace.close", map[string]any{"workspace_id": "w77"}); code != "workspace_not_found" {
				t.Fatalf("close an unknown workspace = %q", code)
			}
			// Closing the main checkout: 0.9.3 refuses while a worktree is open; 0.8.2 closes both.
			code := rawCall(t, folder, "workspace.close", map[string]any{"workspace_id": "w1"})
			if groupCloseRequired {
				if code != "workspace_group_close_required" {
					t.Fatalf("close a main checkout = %q", code)
				}
				if code := rawCall(t, folder, "worktree.remove", map[string]any{"workspace_id": "w2", "force": true}); code != "" {
					t.Fatalf("forced remove = %q", code)
				}
				waitFor(t, func() bool {
					groups := provider.Snapshot().Groups
					return len(groups) == 1 && len(groups[0].Items) == 1
				})
				manage(protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: text("w1"), Name: text("shop")})
			} else if code != "" {
				t.Fatalf("close a main checkout = %q", code)
			}
			waitFor(t, func() bool { return len(provider.Snapshot().Groups) == 0 })
			if log := readLog(t, folder); !strings.Contains(log, "workspace.create shop "+repo) || !strings.Contains(log, "tab.create w2") {
				t.Fatalf("log:\n%s", log)
			}
		})
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

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var update = flag.Bool("update", false, "rewrite testdata/*.want.json")

// tempRoot is a short private folder under /tmp: a Unix socket path must
// stay under 104 bytes on macOS.
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "herdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func startFake(t *testing.T, home string) *herdrtest.Server {
	t.Helper()
	server, err := herdrtest.Listen(filepath.Join(home, ".config", "herdr", "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	return server
}

func fakeBinary(t *testing.T, root string, attachAny bool) string {
	t.Helper()
	path, err := herdrtest.Script(filepath.Join(root, "bin"), attachAny, "pi, claude, codex, Bad Kind, gemini")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeSnapshot(t *testing.T, data []byte) wireSnapshot {
	t.Helper()
	var snapshot wireSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

var fixedNow = time.Date(2026, 10, 4, 9, 51, 2, 0, time.UTC)

func TestMapSnapshotFixtures(t *testing.T) {
	for _, name := range []string{"snapshot-0.8.2", "snapshot-0.9.3"} {
		t.Run(name, func(t *testing.T) {
			result := mapSnapshot(decodeSnapshot(t, readFixture(t, name+".json")), name == "snapshot-0.9.3", fixedNow, nil)
			got, err := json.MarshalIndent(result.response, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.ValidateProviderSnapshot(result.response); err != nil {
				t.Fatalf("mapped snapshot breaks the contract: %v", err)
			}
			for _, secret := range []string{"TITLE-MARKER", "/home/demo", "repo_key", ".git"} {
				if strings.Contains(string(got), secret) {
					t.Fatalf("mapped snapshot leaks %q:\n%s", secret, got)
				}
			}
			wantPath := filepath.Join("testdata", name+".want.json")
			if *update {
				if err := os.WriteFile(wantPath, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := readFixture(t, name+".want.json"); string(want) != string(got)+"\n" {
				t.Fatalf("mapping changed (go test -run %s -update):\n%s", t.Name(), got)
			}
		})
	}
}

func TestMapRulesAndHostView(t *testing.T) {
	result := mapSnapshot(decodeSnapshot(t, readFixture(t, "snapshot-0.9.3.json")), false, fixedNow, nil)
	groups := result.response.Groups
	if len(groups) != 3 || groups[0].Name != "shop" || groups[1].Name != "notes" || groups[2].Name != "api" {
		t.Fatalf("groups: %+v", groups)
	}
	shop := groups[0]
	if shop.State != stateBlocked || !shop.CanCreate || len(shop.Items) != 2 || shop.Items[0].ID != "w1" || shop.Items[1].ID != "w3" {
		t.Fatalf("shop: %+v", shop)
	}
	main := shop.Items[0]
	if len(main.Targets) != 2 || main.Targets[1].Label != "logs" || main.Targets[1].State != stateNone ||
		main.Targets[1].CanOpen || main.Targets[1].ID != "w1:p3.term_65d0031602b712" {
		t.Fatalf("tab without agent: %+v", main.Targets)
	}
	if main.OpenTarget == nil || *main.OpenTarget != "w1:p1.term_65d0031602b701" {
		t.Fatalf("openTarget: %v", main.OpenTarget)
	}
	if notes := groups[1]; notes.ID != "ws-w2" || notes.CanCreate || notes.State != stateNone || notes.Items[0].OpenTarget != nil {
		t.Fatalf("notes: %+v", notes)
	}
	// A newer word is unknown; the roll-up still prefers idle over unknown.
	api := groups[2]
	if api.Items[1].State != stateUnknown || api.State != stateIdle {
		t.Fatalf("api: %+v", api)
	}
	found := result.targets["w3:p1.term_65d0031602b703"]
	if !found.agent || found.pane != "w3:p1" || found.directory != "/home/demo/.herdr/worktrees/shop/silver-meadow" ||
		found.group != "shop" || found.item != "silver-meadow" {
		t.Fatalf("host target: %+v", found)
	}
	if ref := result.groups[shop.ID]; !ref.git || ref.workspace != "w1" {
		t.Fatalf("group ref: %+v", ref)
	}
	if result.kinds["claude"] != 3 || result.kinds["codex"] != 1 {
		t.Fatalf("kinds: %v", result.kinds)
	}
	// stateSince keeps the first time a state was seen.
	later := mapSnapshot(decodeSnapshot(t, readFixture(t, "snapshot-0.9.3.json")), false, fixedNow.Add(time.Minute), result.since)
	if got := *later.response.Groups[0].Items[0].StateSince; got != "2026-10-04T09:51:02Z" {
		t.Fatalf("stateSince moved: %s", got)
	}
}

func TestMapCapsAndBodyBudget(t *testing.T) {
	build := func(count, tabCount int, label string, status func(int) string) wireSnapshot {
		var workspaces, tabs, panes, layouts, agents []map[string]any
		for index := 1; index <= count; index++ {
			ws := fmt.Sprintf("w%d", index)
			workspaces = append(workspaces, map[string]any{"workspace_id": ws, "label": fmt.Sprintf("%s%d", label, index), "number": index, "active_tab_id": ws + ":t1"})
			for number := 1; number <= tabCount; number++ {
				tab, pane, terminal := fmt.Sprintf("%s:t%d", ws, number), fmt.Sprintf("%s:p%d", ws, number), fmt.Sprintf("term_%d_%d", index, number)
				tabs = append(tabs, map[string]any{"tab_id": tab, "workspace_id": ws, "label": label, "number": number})
				panes = append(panes, map[string]any{"pane_id": pane, "tab_id": tab, "terminal_id": terminal})
				layouts = append(layouts, map[string]any{"tab_id": tab, "focused_pane_id": pane})
				agents = append(agents, map[string]any{"pane_id": pane, "tab_id": tab, "terminal_id": terminal, "agent": "claude", "agent_status": status(index)})
			}
		}
		data, _ := json.Marshal(map[string]any{"workspaces": workspaces, "tabs": tabs, "panes": panes, "layouts": layouts, "agents": agents})
		return decodeSnapshot(t, data)
	}
	many := mapSnapshot(build(70, 1, "notes", func(int) string { return "idle" }), true, fixedNow, nil)
	if len(many.response.Groups) != maxGroups || many.response.Truncated == nil || !*many.response.Truncated {
		t.Fatalf("70 groups: %d truncated=%v", len(many.response.Groups), many.response.Truncated)
	}
	// Long names push the body over 256 KiB: whole groups go, least urgent
	// first, and the blocked one stays.
	long := strings.Repeat("ž", 79)
	big := mapSnapshot(build(60, 20, long, func(index int) string {
		if index == 60 {
			return "blocked"
		}
		return "idle"
	}), true, fixedNow, nil)
	body, _ := json.Marshal(big.response)
	if len(body) > maxBodyBytes || big.response.Truncated == nil || len(big.response.Groups) >= 60 {
		t.Fatalf("budget: %d bytes, %d groups", len(body), len(big.response.Groups))
	}
	last := big.response.Groups[len(big.response.Groups)-1]
	if last.State != stateBlocked {
		t.Fatalf("the blocked group was dropped: %+v", last)
	}
	for key := range big.targets {
		if !slices.ContainsFunc(big.response.Groups, func(group protocol.HTTPProviderGroup) bool {
			return slices.ContainsFunc(group.Items, func(item protocol.HTTPProviderItem) bool {
				return slices.ContainsFunc(item.Targets, func(wire protocol.HTTPProviderTarget) bool { return wire.ID == key })
			})
		}) {
			t.Fatalf("host keeps target %s of a dropped group", key)
		}
	}
}

func TestClean(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"shop", "shop"},
		{"  silver \t\n meadow  ", "silver meadow"},
		{"evil\u202egnp.exe", "evilgnp.exe"},   // bidi override (Cf)
		{"zero\u200bwidth\ufeff", "zerowidth"}, // invisible (Cf)
		{"bell\x07\x1b[31mred", "bell[31mred"}, // C0 controls (Cc)
		{"c1\u0085\u009bx", "c1x"},             // C1 controls (Cc)
		{"\u200b\u202e", "fallback"},           // nothing left
		{strings.Repeat("a", 100), strings.Repeat("a", 80)},
	} {
		if got := Clean(test.in, maxNameRunes, "fallback"); got != test.want {
			t.Errorf("Clean(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	if got := Clean(strings.Repeat("😀", 70), maxNameRunes, ""); len(got) > maxNameBytes {
		t.Fatalf("Clean kept %d bytes", len(got))
	}
	if got := Title("shop", "silver-meadow", 32); got != "herdr · shop · silver-meadow" {
		t.Fatal(got)
	}
	if got := Title("notes", "notes", 32); got != "herdr · notes" {
		t.Fatal(got)
	}
}

func TestParseKinds(t *testing.T) {
	help := []byte("Options:\n      --kind <KIND>\n          [possible values: pi, claude, codex, Bad Kind, claude, mastracode]\n")
	if got := parseKinds(help); !reflect.DeepEqual(got, []string{"pi", "claude", "codex", "mastracode"}) {
		t.Fatal(got)
	}
	if parseKinds([]byte("no list here")) != nil {
		t.Fatal("kinds without a list")
	}
	if got := agentKinds([]string{"pi", "claude", "codex", "gemini"}, map[string]int{"codex": 1, "claude": 3, "gemini": 1, "amp": 9}); !reflect.DeepEqual(got, []string{"claude", "codex", "gemini"}) {
		t.Fatal(got)
	}
}

func newTestProvider(t *testing.T, attachAny bool) (*Provider, *herdrtest.Server, *[]Change) {
	t.Helper()
	root := tempRoot(t)
	home := filepath.Join(root, "home")
	binary := fakeBinary(t, root, attachAny)
	server := startFake(t, home)
	var changes []Change
	provider := New(Config{BinaryPaths: []string{binary}, Home: home}, func(change Change) { changes = append(changes, change) })
	return provider, server, &changes
}

func TestPollStates(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	ctx := context.Background()
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	provider.step(ctx)
	info, installed := provider.Info()
	if !installed || info.State != protocol.HTTPProviderStateReady || info.Version == nil || *info.Version != "0.8.2" ||
		!info.CanCreate || !reflect.DeepEqual(info.AgentKinds, []string{"claude", "codex"}) {
		t.Fatalf("ready info: %+v installed=%v", info, installed)
	}
	if err := protocol.ValidateProviderInfo(info); err != nil {
		t.Fatal(err)
	}
	if got := provider.Snapshot(); got.State != protocol.HTTPProviderStateReady || len(got.Groups) != 3 {
		t.Fatalf("snapshot: %+v", got)
	}

	// A snapshot without its arrays: unsupported.
	server.Snapshot([]byte(`{"version":"0.8.2","protocol":20,"workspaces":[]}`))
	provider.step(ctx)
	if info, _ := provider.Info(); info.State != protocol.Unsupported || len(provider.Snapshot().Groups) != 0 {
		t.Fatalf("missing arrays: %+v", info)
	}

	// The server stops: notRunning at the next poll.
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	provider.retryAt = time.Time{}
	provider.step(ctx)
	server.Stop()
	provider.step(ctx)
	if info, installed := provider.Info(); !installed || info.State != protocol.NotRunning || provider.Has("w1:p1.term_65d0031602b701") {
		t.Fatalf("stopped: %+v", info)
	}

	// Turned off: no socket call at all.
	mustStart(t, server)
	server.Calls()
	provider.config.Enabled = func() bool { return false }
	provider.step(ctx)
	if info, installed := provider.Info(); !installed || info.State != protocol.Off || len(server.Calls()) != 0 {
		t.Fatalf("off: %+v", info)
	}
}

func TestPollOldProtocolAndMissingBinary(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Version("0.7.0", 19)
	provider.step(context.Background())
	if info, _ := provider.Info(); info.State != protocol.Unsupported || *info.Version != "0.7.0" {
		t.Fatalf("old: %+v", info)
	}

	root := tempRoot(t)
	missing := New(Config{BinaryPaths: []string{filepath.Join(root, "bin", "herdr")}, Home: root}, nil)
	missing.step(context.Background())
	if _, installed := missing.Info(); installed {
		t.Fatal("a missing binary lists a provider")
	}
}

func TestHungSocketTurnsNotRunningAfterThreeMisses(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	ctx := context.Background()
	provider.step(ctx)
	server.Hang(true)
	for miss := 1; miss <= missLimit; miss++ {
		started := time.Now()
		provider.step(ctx)
		if elapsed := time.Since(started); elapsed > callTimeout+500*time.Millisecond {
			t.Fatalf("poll took %v", elapsed)
		}
		state := provider.Snapshot().State
		if miss < missLimit && state != protocol.HTTPProviderStateReady || miss == missLimit && state != protocol.NotRunning {
			t.Fatalf("after %d misses: %s", miss, state)
		}
	}
}

func TestSocketChecks(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	ctx := context.Background()
	if _, err := provider.ping(ctx); err != nil {
		t.Fatalf("a private socket is refused: %v", err)
	}
	if err := os.Chmod(server.Path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ping(ctx); err == nil {
		t.Fatal("a socket other users can open is accepted")
	}
	if err := os.Chmod(server.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Dir(server.Path)
	if err := os.Chmod(folder, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ping(ctx); err == nil {
		t.Fatal("a group-writable socket folder is accepted")
	}
	if err := os.Chmod(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// A writable folder higher up the chain (finding 11).
	config := filepath.Dir(folder)
	if err := os.Chmod(config, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ping(ctx); err == nil {
		t.Fatal("a world-writable parent folder is accepted")
	}
	if err := os.Chmod(config, 0o700); err != nil {
		t.Fatal(err)
	}
	server.Stop()
	if err := os.WriteFile(server.Path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ping(ctx); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("a regular file is dialed: %v", err)
	}
}

func TestReplyLimits(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Handle("session.snapshot", func(json.RawMessage) (any, *herdrtest.Error) {
		return strings.Repeat("x", maxLineBytes), nil
	})
	if _, err := provider.readSnapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("a 4 MiB reply is read: %v", err)
	}
	server.Handle("session.snapshot", func(json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "boom", Message: "/home/demo/private path"}
	})
	_, err := provider.readSnapshot(context.Background())
	var failure *callError
	if !errors.As(err, &failure) || strings.Contains(err.Error(), "/home/demo") {
		t.Fatalf("herdr error text leaks: %v", err)
	}
}

func TestAttachArgv(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	provider.step(context.Background())
	attach, err := provider.Attach("w1:p1.term_65d0031602b701")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attach.Argv, []string{provider.binary, "agent", "attach", "w1:p1"}) ||
		attach.Directory != "/home/demo/shop" || attach.Title != "herdr · shop" || attach.Key != "w1:p1.term_65d0031602b701" {
		t.Fatalf("agent attach: %+v", attach)
	}
	wantEnv := []string{"HOME=" + provider.config.Home, "PATH=", "HERDR_SOCKET_PATH=" + server.Path, "TERM=xterm-256color", "COLORTERM=truecolor"}
	if !reflect.DeepEqual(attach.Env, wantEnv) {
		t.Fatalf("env: %v", attach.Env)
	}
	// Without `terminal attach`, a tab without an agent cannot open.
	if _, err := provider.Attach("w1:p3.term_65d0031602b712"); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("no-agent tab on an old herdr: %v", err)
	}
	if _, err := provider.Attach("w1:p1.term_000000000000"); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("a target with another terminal ID: %v", err)
	}

	withAny, server2, _ := newTestProvider(t, true)
	server2.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	withAny.config.Session = "lab"
	withAny.step(context.Background())
	// The lab session's socket is elsewhere: the provider is not running.
	if _, err := withAny.Attach("w1:p3.term_65d0031602b712"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("attach while not running: %v", err)
	}
	withAny.config.Session = ""
	withAny.retryAt = time.Time{}
	withAny.step(context.Background())
	withAny.config.Session = "lab"
	attach, err = withAny.Attach("w1:p3.term_65d0031602b712")
	if err != nil || !reflect.DeepEqual(attach.Argv, []string{withAny.binary, "--session", "lab", "terminal", "attach", "term_65d0031602b712"}) {
		t.Fatalf("terminal attach: %+v %v", attach, err)
	}
}

func agentSnapshot(t *testing.T, states ...string) []byte {
	t.Helper()
	var workspaces, tabs, panes, layouts, agents []map[string]any
	for index, state := range states {
		number := index + 1
		ws, tab, pane, terminal := fmt.Sprintf("w%d", number), fmt.Sprintf("w%d:t1", number), fmt.Sprintf("w%d:p1", number), fmt.Sprintf("term_%d", number)
		workspaces = append(workspaces, map[string]any{"workspace_id": ws, "label": fmt.Sprintf("item%d", number), "number": number, "active_tab_id": tab,
			"worktree": map[string]any{"repo_key": "/r/.git", "repo_name": "shop", "checkout_path": "/r", "is_linked_worktree": number > 1}})
		tabs = append(tabs, map[string]any{"tab_id": tab, "workspace_id": ws, "label": "1", "number": 1})
		panes = append(panes, map[string]any{"pane_id": pane, "tab_id": tab, "terminal_id": terminal, "title": "SECRET-TITLE"})
		layouts = append(layouts, map[string]any{"tab_id": tab, "focused_pane_id": pane})
		if state != "" {
			agents = append(agents, map[string]any{"pane_id": pane, "tab_id": tab, "terminal_id": terminal, "agent": "claude", "agent_status": state, "title": "SECRET-TITLE"})
		}
	}
	data, _ := json.Marshal(map[string]any{"version": "0.8.2", "protocol": 20, "workspaces": workspaces, "tabs": tabs, "panes": panes, "layouts": layouts, "agents": agents})
	return data
}

func TestChangesFollowPushRules(t *testing.T) {
	provider, server, changes := newTestProvider(t, false)
	ctx := context.Background()
	clock := fixedNow
	provider.now = func() time.Time { return clock }
	poll := func(states ...string) {
		server.Snapshot(agentSnapshot(t, states...))
		provider.step(ctx)
		clock = clock.Add(pollEvery)
	}
	// Baseline: an agent already blocked or done at start never pushes.
	poll("blocked", "done", "working", "working")
	poll("blocked", "done", "working", "working")
	if len(*changes) != 0 {
		t.Fatalf("baseline pushed: %+v", *changes)
	}
	// Blocked: only after two polls in a row, once.
	poll("idle", "done", "blocked", "working")
	if len(*changes) != 0 {
		t.Fatalf("one blocked poll pushed: %+v", *changes)
	}
	poll("idle", "done", "blocked", "working")
	poll("idle", "done", "blocked", "working")
	if len(*changes) != 1 || (*changes)[0].Key != "w3:p1.term_3" || (*changes)[0].To != "blocked" ||
		(*changes)[0].Group != "shop" || (*changes)[0].Item != "item3" || (*changes)[0].Agent != "claude" {
		t.Fatalf("blocked: %+v", *changes)
	}
	// Finished: working for 20 s or more, then done or idle.
	*changes = nil
	clock = clock.Add(20 * time.Second)
	poll("working", "done", "working", "idle")
	if len(*changes) != 1 || (*changes)[0].Key != "w4:p1.term_4" || (*changes)[0].To != "done" || (*changes)[0].Worked < doneAfter {
		t.Fatalf("finished via idle: %+v", *changes)
	}
	// Under 20 s of work: no push.
	*changes = nil
	poll("done", "done", "working", "idle")
	if len(*changes) != 0 {
		t.Fatalf("short work pushed: %+v", *changes)
	}
	// Rediscovery drops all state: a restarted herdr is a new baseline.
	server.Stop()
	provider.step(ctx)
	mustStart(t, server)
	provider.retryAt = time.Time{}
	clock = clock.Add(time.Minute)
	poll("blocked", "blocked", "blocked", "done")
	poll("blocked", "blocked", "blocked", "done")
	if len(*changes) != 0 {
		t.Fatalf("rediscovery pushed a burst: %+v", *changes)
	}
	for _, change := range *changes {
		if strings.Contains(fmt.Sprint(change), "SECRET") {
			t.Fatal("a change carries a title")
		}
	}
}

func TestFindBinary(t *testing.T) {
	root := tempRoot(t)
	onPath := fakeBinary(t, root, false)
	if got, _ := FindBinary(Config{PathEnv: "relative:" + filepath.Dir(onPath), Home: root}); got != onPath {
		t.Fatalf("PATH lookup: %q", got)
	}
	if err := os.Chmod(onPath, 0o777); err != nil {
		t.Fatal(err)
	}
	got, refused := FindBinary(Config{BinaryPaths: []string{onPath}, Home: root})
	if got != "" || len(refused) != 1 || !strings.Contains(refused[0], "writable") {
		t.Fatalf("a world-writable herdr is used: %q %v", got, refused)
	}
}

// TestGuardRefusesRealHerdr is design S16: no test config may name the real
// home, a real herdr path, or a PATH outside a temp folder.
func TestGuardRefusesRealHerdr(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := tempRoot(t)
	for _, config := range []Config{
		{Home: home},
		{Home: root, BinaryPaths: []string{"/opt/homebrew/bin/herdr"}},
		{Home: root, BinaryPaths: []string{"/opt/homebrew/Cellar/herdr/0.8.2/bin/herdr"}},
		{Home: root, BinaryPaths: []string{"/usr/local/bin/herdr"}},
		{Home: root, BinaryPaths: []string{filepath.Join(home, ".cargo", "bin", "herdr")}},
		{Home: root, PathEnv: "/opt/homebrew/bin:/usr/bin"},
		{Home: ""},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New accepted %+v", config)
				}
			}()
			New(config, nil)
		}()
	}
}

func mustStart(t *testing.T, server *herdrtest.Server) {
	t.Helper()
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectAndCustomPrefix(t *testing.T) {
	root := tempRoot(t)
	home := filepath.Join(root, "home")
	binary := fakeBinary(t, root, true)
	server := startFake(t, home)
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	config := Config{BinaryPaths: []string{binary}, Home: home}
	status := Inspect(context.Background(), config)
	if status.Binary != binary || status.State != protocol.HTTPProviderStateReady || status.Version != "0.8.2" ||
		status.Protocol != 20 || !status.CanCreate || !status.AttachAny || status.CustomPrefix {
		t.Fatalf("status = %+v", status)
	}
	configFile := filepath.Join(home, ".config", "herdr", "config.toml")
	for text, want := range map[string]bool{
		"[keys]\n# prefix = \"ctrl+a\"\n":             false,
		"[keys]\nprefix = \"ctrl+b\" # default\n":     false,
		"[ui]\nprefix = \"ctrl+a\"\n":                 false,
		"[keys]\nhelp = \"prefix+?\"\nprefix = 'f12'": true,
	} {
		if err := os.WriteFile(configFile, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Inspect(context.Background(), config).CustomPrefix; got != want {
			t.Errorf("%q: custom prefix = %v", text, got)
		}
	}
	server.Stop()
	if status := Inspect(context.Background(), config); status.State != protocol.NotRunning || status.Binary == "" {
		t.Fatalf("stopped = %+v", status)
	}
}

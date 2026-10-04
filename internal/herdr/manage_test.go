//go:build !windows

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// manageWorld is a ready provider on the 0.8.2 fixture whose fake herdr
// records the manage calls. reply, when set, is herdr's error for the next call.
type manageWorld struct {
	provider *Provider
	server   *herdrtest.Server
	mu       sync.Mutex
	calls    []string
	reply    *herdrtest.Error
	logs     []string
}

func newManageWorld(t *testing.T, attachAny bool) *manageWorld {
	t.Helper()
	provider, server, _ := newTestProvider(t, attachAny)
	world := &manageWorld{provider: provider, server: server}
	provider.logf = func(format string, values ...any) {
		world.mu.Lock()
		world.logs = append(world.logs, fmt.Sprintf(format, values...))
		world.mu.Unlock()
	}
	base := string(readFixture(t, "snapshot-0.8.2.json"))
	server.Snapshot([]byte(base))
	for _, method := range []string{"workspace.close", "worktree.remove", "tab.create", "workspace.create"} {
		server.Handle(method, func(params json.RawMessage) (any, *herdrtest.Error) {
			world.mu.Lock()
			defer world.mu.Unlock()
			world.calls = append(world.calls, method+" "+string(params))
			if world.reply != nil {
				return nil, world.reply
			}
			// herdr adds the new tab's pane: the next snapshot has it.
			snapshot := strings.Replace(base, "\"tabs\":[\n", "\"tabs\":[\n"+`{"label":"9","number":9,"tab_id":"w3:t9","workspace_id":"w3"},`, 1)
			snapshot = strings.Replace(snapshot, "\"panes\":[\n", "\"panes\":[\n"+`{"pane_id":"w3:p9","tab_id":"w3:t9","terminal_id":"term_9","workspace_id":"w3"},`, 1)
			server.Snapshot([]byte(snapshot))
			return map[string]any{"workspace": map[string]any{"workspace_id": "w9"},
				"root_pane": map[string]any{"pane_id": "w3:p9", "terminal_id": "term_9", "cwd": "/home/demo"}}, nil
		})
	}
	runProvider(t, provider)
	waitState(t, provider, protocol.HTTPProviderStateReady)
	return world
}

func runProvider(t *testing.T, provider *Provider) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); provider.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func waitState(t *testing.T, provider *Provider, state protocol.HTTPProviderState) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); provider.Snapshot().State != state; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want %s", provider.Snapshot().State, state)
		}
	}
}

func (world *manageWorld) take() []string {
	world.mu.Lock()
	defer world.mu.Unlock()
	calls := world.calls
	world.calls = nil
	return calls
}

func manageRequest(action protocol.HTTPProviderManageAction, item, name string) protocol.HTTPProviderManageRequest {
	request := protocol.HTTPProviderManageRequest{Action: action}
	if item != "" {
		request.ItemID = &item
	}
	if name != "" {
		request.Name = &name
	}
	return request
}

func TestManageItemActions(t *testing.T) {
	world := newManageWorld(t, false)
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		request protocol.HTTPProviderManageRequest
		herdr   *herdrtest.Error
		want    error
		call    string // "" when herdr must not be called
		problem bool
	}{
		{"close a linked worktree", manageRequest(protocol.CloseItem, "w3", "silver-meadow"), nil, nil, `workspace.close {"workspace_id":"w3"}`, false},
		{"close a workspace outside git", manageRequest(protocol.CloseItem, "w2", "notes"), nil, nil, `workspace.close {"workspace_id":"w2"}`, false},
		{"close under another name", manageRequest(protocol.CloseItem, "w3", "shop"), nil, ErrRenamed, "", false},
		{"close an unknown item", manageRequest(protocol.CloseItem, "w77", "x"), nil, ErrTargetMissing, "", false},
		{"close a main checkout with open worktrees", manageRequest(protocol.CloseItem, "w1", "shop"), nil, ErrGroupOpen, "", false},
		{"herdr 0.9.3 refuses the close", manageRequest(protocol.CloseItem, "w2", "notes"),
			&herdrtest.Error{Code: "workspace_group_close_required", Message: "x"}, ErrGroupOpen, "workspace.close", false},
		{"herdr lost the workspace", manageRequest(protocol.CloseItem, "w2", "notes"),
			&herdrtest.Error{Code: "workspace_not_found", Message: "x"}, ErrTargetMissing, "workspace.close", false},
		{"remove a linked worktree", manageRequest(protocol.RemoveItem, "w3", "silver-meadow"), nil, nil, `worktree.remove {"force":false,"workspace_id":"w3"}`, false},
		{"remove under another name", manageRequest(protocol.RemoveItem, "w3", "other"), nil, ErrRenamed, "", false},
		{"remove a main checkout", manageRequest(protocol.RemoveItem, "w1", "shop"), nil, ErrNotLinked, "", false},
		{"remove outside git", manageRequest(protocol.RemoveItem, "w2", "notes"), nil, ErrNotLinked, "", false},
		{"remove with uncommitted changes", manageRequest(protocol.RemoveItem, "w3", "silver-meadow"),
			&herdrtest.Error{Code: "dirty_worktree_requires_force", Message: "fatal: '/home/demo/secret' contains modified or untracked files"}, nil, "worktree.remove", true},
		{"herdr fails the remove", manageRequest(protocol.RemoveItem, "w3", "silver-meadow"),
			&herdrtest.Error{Code: "worktree_remove_failed", Message: "fatal: /home/demo/secret is locked"}, ErrManage, "worktree.remove", false},
		{"herdr says it is not linked", manageRequest(protocol.RemoveItem, "w3", "silver-meadow"),
			&herdrtest.Error{Code: "not_linked_worktree", Message: "x"}, ErrNotLinked, "worktree.remove", false},
		{"new tab", manageRequest(protocol.NewTab, "w3", ""), nil, nil, `tab.create {"focus":false,"workspace_id":"w3"}`, false},
		{"new tab in an unknown item", manageRequest(protocol.NewTab, "w77", ""), nil, ErrTargetMissing, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			world.mu.Lock()
			world.reply, world.logs = test.herdr, nil
			world.mu.Unlock()
			response, err := world.provider.Manage(ctx, test.request, Project{})
			calls := world.take()
			if !errors.Is(err, test.want) || (err == nil) != (test.want == nil) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.call == "" && len(calls) != 0 || test.call != "" && (len(calls) != 1 || !strings.HasPrefix(calls[0], test.call)) {
				t.Fatalf("herdr calls = %v, want %q", calls, test.call)
			}
			if (response.Problem != nil) != test.problem || test.problem && *response.Problem != protocol.Uncommitted {
				t.Fatalf("problem = %v", response.Problem)
			}
			if err == nil && (response.ItemID == nil || *response.ItemID != *test.request.ItemID) {
				t.Fatalf("itemId = %v", response.ItemID)
			}
			// This herdr cannot attach a terminal without an agent: no targetId.
			if response.TargetID != nil {
				t.Fatalf("targetId = %s without terminal attach", *response.TargetID)
			}
			world.mu.Lock()
			defer world.mu.Unlock()
			if test.herdr != nil {
				if len(world.logs) != 1 || world.logs[0] != fmt.Sprintf("herdr: %s failed: %s", test.request.Action, test.herdr.Code) {
					t.Fatalf("log = %q", world.logs)
				}
			}
		})
	}
}

func TestManageNewTabAndWorkspaceOpenAtOnce(t *testing.T) {
	world := newManageWorld(t, true)
	ctx := context.Background()
	response, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w3", ""), Project{})
	if err != nil || response.TargetID == nil || *response.TargetID != "w3:p9.term_9" || *response.ItemID != "w3" {
		t.Fatalf("new tab = %+v %v", response, err)
	}
	// The answer came after a poll that has the new terminal: it opens at once.
	if _, err := world.provider.Attach(*response.TargetID); err != nil {
		t.Fatalf("attach the new tab: %v", err)
	}
	world.take()
	project := Project{Folder: "/tmp/shop", Name: "My\u202e shop\n"}
	response, err = world.provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace}, project)
	calls := world.take()
	if err != nil || response.TargetID == nil || *response.ItemID != "w9" ||
		len(calls) != 1 || calls[0] != `workspace.create {"cwd":"/tmp/shop","focus":false,"label":"My shop"}` {
		t.Fatalf("new workspace = %+v %v %v", response, err, calls)
	}
	// A terminal ID that is not a handle (it would be a CLI flag) is never answered.
	world.server.Handle("tab.create", func(json.RawMessage) (any, *herdrtest.Error) {
		return map[string]any{"root_pane": map[string]any{"pane_id": "w3:p9", "terminal_id": "--takeover"}}, nil
	})
	if response, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w3", ""), Project{}); err != nil || response.TargetID != nil {
		t.Fatalf("bad terminal id = %+v %v", response, err)
	}
}

func TestManageCaps(t *testing.T) {
	world := newManageWorld(t, true)
	ctx := context.Background()
	var workspaces, tabs, panes []string
	for index := range maxGroups {
		workspaces = append(workspaces, fmt.Sprintf(`{"workspace_id":"w%d","label":"n%d","number":%d,"active_tab_id":"w%d:t0"}`, index, index, index, index))
	}
	for index := range maxTargets {
		tabs = append(tabs, fmt.Sprintf(`{"tab_id":"w0:t%d","workspace_id":"w0","label":"%d","number":%d}`, index, index, index))
		panes = append(panes, fmt.Sprintf(`{"pane_id":"w0:p%d","tab_id":"w0:t%d","terminal_id":"term_%d","workspace_id":"w0"}`, index, index, index))
	}
	world.server.Snapshot([]byte(fmt.Sprintf(`{"version":"0.8.2","protocol":20,"workspaces":[%s],"tabs":[%s],"panes":[%s],"layouts":[],"agents":[]}`,
		strings.Join(workspaces, ","), strings.Join(tabs, ","), strings.Join(panes, ","))))
	if _, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w0", ""), Project{}); !errors.Is(err, ErrFull) {
		t.Fatalf("33rd terminal = %v", err)
	}
	if _, err := world.provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace}, Project{Folder: "/tmp/x", Name: "x"}); !errors.Is(err, ErrFull) {
		t.Fatalf("65th group = %v", err)
	}
	if _, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w1", ""), Project{}); err != nil {
		t.Fatalf("a tab in a small item = %v", err)
	}
	if calls := world.take(); len(calls) != 1 {
		t.Fatalf("herdr calls = %v", calls)
	}
}

func TestManageAndCreateShareOneLock(t *testing.T) {
	world := newManageWorld(t, false)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	world.server.Handle("workspace.close", func(json.RawMessage) (any, *herdrtest.Error) {
		close(entered)
		<-release
		return map[string]any{}, nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := world.provider.Manage(ctx, manageRequest(protocol.CloseItem, "w2", "notes"), Project{})
		done <- err
	}()
	<-entered
	if _, err := world.provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "a"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("create during manage = %v", err)
	}
	if _, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w3", ""), Project{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("manage during manage = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Not ready: every item action is refused before herdr is asked.
	world.server.Stop()
	waitState(t, world.provider, protocol.NotRunning)
	if _, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w3", ""), Project{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("manage while not running = %v", err)
	}
}

func TestInRepository(t *testing.T) {
	root, err := filepath.EvalSymlinks(tempRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	make := func(parts ...string) string {
		path := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	repo := make("repo")
	make("repo", ".git")
	deep := make("repo", "a", "b")
	plain := make("plain")
	// A linked worktree has a .git file, not a folder.
	tree := make("tree")
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(deep, link); err != nil {
		t.Fatal(err)
	}
	levels := []string{"repo"}
	for range gitMaxLevels {
		levels = append(levels, "d")
	}
	tooDeep := make(levels...)
	for _, test := range []struct {
		name    string
		folders []string
		want    bool
	}{
		{"the repository", []string{repo}, true},
		{"a folder in it", []string{deep}, true},
		{"a .git file", []string{tree}, true},
		{"through a link", []string{link}, true},
		{"not git", []string{plain}, false},
		{"a later pane is in git", []string{plain, repo}, true},
		{"missing", []string{filepath.Join(root, "none")}, false},
		{"relative", []string{"repo"}, false},
		{"a file", []string{filepath.Join(tree, ".git")}, false},
		{"32 levels up and no more", []string{tooDeep}, false},
		{"31 levels up", []string{filepath.Dir(tooDeep)}, true},
	} {
		if got := inRepository(test.folders); got != test.want {
			t.Errorf("%s = %t", test.name, got)
		}
	}
	// The walk stops at a folder that is not this user's: /tmp's own .git would not count.
	stop, _ := os.Stat(filepath.Join(repo, "a"))
	saved := ownFolder
	ownFolder = func(info os.FileInfo) bool { return !os.SameFile(info, stop) }
	defer func() { ownFolder = saved }()
	if inRepository([]string{deep}) {
		t.Fatal("the walk passed a folder of another user")
	}
	if !inRepository([]string{repo}) {
		t.Fatal("an owned repository is refused")
	}
}

const gitSnapshot = `{"version":"0.8.2","protocol":20,"workspaces":[
{"workspace_id":"w1","label":"site","number":1,"active_tab_id":"w1:t1"},
{"workspace_id":"w2","label":"notes","number":2,"active_tab_id":"w2:t1"}],
"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"1","number":1},{"tab_id":"w2:t1","workspace_id":"w2","label":"1","number":1}],
"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","terminal_id":"term_1","workspace_id":"w1","cwd":"/git"},
{"pane_id":"w2:p1","tab_id":"w2:t1","terminal_id":"term_2","workspace_id":"w2","cwd":"/slow"}],
"layouts":[],"agents":[]}`

func TestGitWorkspacesHaveATimeLimit(t *testing.T) {
	provider, _, _ := newTestProvider(t, false)
	release := make(chan struct{})
	var slow atomic.Int32
	saved := findRepository
	findRepository = func(folders []string) bool {
		if folders[0] == "/slow" {
			slow.Add(1)
			<-release
		}
		return true
	}
	defer func() { findRepository = saved }()
	snapshot := decodeSnapshot(t, []byte(gitSnapshot))
	started := time.Now()
	git := provider.gitWorkspaces(snapshot, fixedNow)
	if elapsed := time.Since(started); elapsed > 2*gitCheckLimit || !git["w1"] || git["w2"] {
		t.Fatalf("git = %v after %v", git, elapsed)
	}
	// Not tried again for 60 s, and not while the old check still hangs.
	for _, later := range []time.Duration{time.Second, gitCheckRetry - time.Second, gitCheckRetry + time.Second} {
		started = time.Now()
		if git := provider.gitWorkspaces(snapshot, fixedNow.Add(later)); git["w2"] || time.Since(started) > gitCheckLimit/2 {
			t.Fatalf("after %v: %v", later, git)
		}
	}
	if slow.Load() != 1 {
		t.Fatalf("checks of the hung folder = %d", slow.Load())
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	if git := provider.gitWorkspaces(snapshot, fixedNow.Add(gitCheckRetry+2*time.Second)); git["w2"] {
		t.Fatal("the back-off ended early")
	}
	if git := provider.gitWorkspaces(snapshot, fixedNow.Add(3*gitCheckRetry)); !git["w2"] || slow.Load() != 2 {
		t.Fatalf("after the back-off: %v, %d checks", git, slow.Load())
	}
	// A workspace that left the snapshot leaves no state behind.
	provider.gitWorkspaces(decodeSnapshot(t, []byte(`{"workspaces":[],"tabs":[],"panes":[],"layouts":[],"agents":[]}`)), fixedNow)
	if len(provider.gitChecks) != 0 {
		t.Fatalf("checks kept: %v", provider.gitChecks)
	}
}

// A git workspace that herdr gave no worktree record gets "+", and create
// finds the new item although its group ID changed (ws-w1 to g<hash>).
func TestCreateOnAGitWorkspaceWithoutWorktrees(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	repo := filepath.Join(tempRoot(t), "site")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := strings.Replace(gitSnapshot, `"/git"`, `"`+repo+`"`, 1)
	server.Snapshot([]byte(base))
	runProvider(t, provider)
	waitState(t, provider, protocol.HTTPProviderStateReady)
	groups := provider.Snapshot().Groups
	if len(groups) != 2 || groups[0].ID != "ws-w1" || !groups[0].CanCreate || groups[1].CanCreate || groups[0].Items[0].Linked != nil {
		t.Fatalf("groups = %+v", groups)
	}
	if _, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{GroupID: "ws-w2", Name: "a"}); !errors.Is(err, ErrNotGit) {
		t.Fatalf("create outside git = %v", err)
	}
	saved := createTimeouts
	createTimeouts.worktree, createTimeouts.refresh = 300*time.Millisecond, time.Second
	defer func() { createTimeouts = saved }()
	var asked map[string]any
	server.Handle("worktree.create", func(params json.RawMessage) (any, *herdrtest.Error) {
		_ = json.Unmarshal(params, &asked)
		tree := `"worktree":{"repo_key":"` + repo + `/.git","repo_name":"site","checkout_path":"` + repo + `"`
		after := strings.Replace(base, `"active_tab_id":"w1:t1"}`, `"active_tab_id":"w1:t1",`+tree+`,"is_linked_worktree":false}},
{"workspace_id":"w3","label":"brave-fox","number":3,"active_tab_id":"w3:t1",`+tree+`,"is_linked_worktree":true}}`, 1)
		after = strings.Replace(after, `"tabs":[`, `"tabs":[{"tab_id":"w3:t1","workspace_id":"w3","label":"1","number":1},`, 1)
		after = strings.Replace(after, `"panes":[`, `"panes":[{"pane_id":"w3:p1","tab_id":"w3:t1","terminal_id":"term_3","workspace_id":"w3"},`, 1)
		server.Snapshot([]byte(after))
		time.Sleep(500 * time.Millisecond) // herdr made it, but the answer is late
		return nil, &herdrtest.Error{Code: "late", Message: "late"}
	})
	created, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{GroupID: "ws-w1", Name: "brave-fox"})
	if err != nil || created.ItemID != "w3" || created.TargetID != "w3:p1.term_3" || asked["workspace_id"] != "w1" {
		t.Fatalf("create = %+v %v (asked %v)", created, err, asked)
	}
	if groups := provider.Snapshot().Groups; len(groups) != 2 || !strings.HasPrefix(groups[0].ID, "g") || len(groups[0].Items) != 2 ||
		groups[0].Items[1].Linked == nil || groups[0].Items[0].Linked != nil {
		t.Fatalf("groups after create = %+v", groups)
	}
}

func TestNotInstalledCanInstallAndCanStart(t *testing.T) {
	root := tempRoot(t)
	home, binary := filepath.Join(root, "home"), filepath.Join(root, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := New(Config{BinaryPaths: []string{binary}, Home: home}, nil)
	provider.logf = func(string, ...any) {}
	now := fixedNow
	provider.now = func() time.Time { return now }
	ctx := context.Background()
	flags := func() (protocol.HTTPProviderState, bool, bool) {
		info, installed := provider.Info()
		if installed != (info.State != protocol.NotInstalled) {
			t.Fatalf("installed = %t in state %s", installed, info.State)
		}
		return info.State, info.CanInstall != nil && *info.CanInstall, info.CanStart != nil && *info.CanStart
	}
	installable := (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
	provider.step(ctx)
	if state, install, start := flags(); state != protocol.NotInstalled || install != installable || start {
		t.Fatalf("no binary = %s %t %t", state, install, start)
	}
	// A herdr that Termivoa refuses (others can write it): an install would not help.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binary, 0o777); err != nil {
		t.Fatal(err)
	}
	now = now.Add(lookupEvery)
	provider.step(ctx)
	if state, install, start := flags(); state != protocol.NotInstalled || install || start {
		t.Fatalf("refused binary = %s %t %t", state, install, start)
	}
	// Fixed, but the next lookup is 30 s away: only "look again now" finds it at once.
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	provider.step(ctx)
	if state, _, _ := flags(); state != protocol.NotInstalled {
		t.Fatalf("before the lookup timer = %s", state)
	}
	provider.LookAgain()
	provider.step(ctx)
	if state, install, start := flags(); state != protocol.NotRunning || install || !start {
		t.Fatalf("installed, no server = %s %t %t", state, install, start)
	}
	// A socket that Termivoa refuses: a start would not help.
	server := startFake(t, home)
	if err := os.Chmod(server.Path, 0o666); err != nil {
		t.Fatal(err)
	}
	provider.LookAgain()
	provider.step(ctx)
	if state, _, start := flags(); state != protocol.NotRunning || start {
		t.Fatalf("refused socket = %s %t", state, start)
	}
	if err := os.Chmod(server.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	// The retry is 5 s away; the flag clears it.
	provider.step(ctx)
	if state, _, _ := flags(); state != protocol.NotRunning {
		t.Fatalf("before the retry timer = %s", state)
	}
	provider.LookAgain()
	provider.step(ctx)
	if state, install, start := flags(); state != protocol.HTTPProviderStateReady || install || start {
		t.Fatalf("ready = %s %t %t", state, install, start)
	}
	// Uninstalled while running: notInstalled again.
	server.Stop()
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	provider.step(ctx)
	if state, install, _ := flags(); state != protocol.NotInstalled || install != installable {
		t.Fatalf("uninstalled = %s %t", state, install)
	}
}

// startScript replaces the provider's herdr with a script whose `server`
// writes its arguments, environment and process group to log and stays.
func startScript(t *testing.T, provider *Provider, name, body string) (path, log string) {
	t.Helper()
	folder := filepath.Dir(provider.config.BinaryPaths[0])
	path, log = filepath.Join(folder, name), filepath.Join(folder, name+".log")
	script := "#!/bin/sh\nLOG='" + log + "'\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

const serverScript = `case "$*" in
"server") SEEN=$(/usr/bin/env); PATH=/usr/bin:/bin
  { echo "argv=$*"; echo "pgid=$(ps -o pgid= -p $$ | tr -d ' ')"; echo "tty=$(ps -o tty= -p $$ | tr -d ' ')"; echo "$SEEN"; echo end; } >> "$LOG"; exec sleep 3;;
"terminal attach --help") echo 'Usage: herdr terminal attach [OPTIONS] <TERMINAL_ID>'; exit 0;;
esac
exit 2
`

func readLog(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestStartServer(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	provider.config.PathEnv = filepath.Dir(provider.config.BinaryPaths[0])
	provider.logf = func(string, ...any) {}
	_, log := startScript(t, provider, "herdr", serverScript)
	t.Setenv("CLAUDE_CODE_SECRET", "1")
	t.Setenv("LC_TEST", "kept")
	saved, savedRun := manageTimeouts, systemdRun
	manageTimeouts.ready = 3 * time.Second
	systemdRun = func() string { return "" }
	defer func() { manageTimeouts, systemdRun = saved, savedRun }()
	runProvider(t, provider)
	waitState(t, provider, protocol.HTTPProviderStateReady)
	ctx := context.Background()
	start := protocol.HTTPProviderManageRequest{Action: protocol.StartServer}
	// A running herdr is never started again.
	if _, err := provider.Manage(ctx, start, Project{}); !errors.Is(err, ErrNotStartable) || readLog(log) != "" {
		t.Fatalf("start while ready = %v %q", err, readLog(log))
	}
	server.Stop()
	waitState(t, provider, protocol.NotRunning)
	if info, _ := provider.Info(); info.CanStart == nil {
		t.Fatal("canStart is not set")
	}
	// The poll loop's retry is up to 5 s away: the start must not wait for it.
	done := make(chan error, 1)
	began := time.Now()
	go func() {
		_, err := provider.Manage(ctx, start, Project{})
		done <- err
	}()
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(readLog(log), "end\n"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the server was not started: %q", readLog(log))
		}
	}
	mustStart(t, server) // the server comes up
	if err := <-done; err != nil || time.Since(began) > 2*time.Second {
		t.Fatalf("start = %v after %v", err, time.Since(began))
	}
	text := readLog(log)
	for _, want := range []string{
		"argv=server\n", "\nHOME=" + provider.config.Home + "\n", "\nPATH=" + provider.config.PathEnv + "\n",
		"\nHERDR_SOCKET_PATH=" + provider.config.socketPath() + "\n", "\nTERM=xterm-256color\n", "\nLC_TEST=kept\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the server's start lacks %q:\n%s", want, text)
		}
	}
	// Only the allowlist: nothing of the program that started the broker.
	if strings.Contains(text, "CLAUDE_CODE_SECRET") || strings.Count(text, "\nHOME=") != 1 || strings.Count(text, "\nPATH=") != 1 {
		t.Fatalf("the server's environment:\n%s", text)
	}
	// Its own session: not the broker's process group, no terminal.
	if strings.Contains(text, fmt.Sprintf("pgid=%d\n", syscall.Getpgrp())) || !strings.Contains(text, "tty=?") {
		t.Fatalf("the server is not in its own session:\n%s", text)
	}
	// The child it started still lives and the server is gone again: no second child.
	server.Stop()
	waitState(t, provider, protocol.NotRunning)
	if _, err := provider.Manage(ctx, start, Project{}); !errors.Is(err, ErrNoServer) || strings.Count(readLog(log), "argv=") != 1 {
		t.Fatalf("second start = %v:\n%s", err, readLog(log))
	}
}

func TestStartServerThatNeverAnswers(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	provider.logf = func(string, ...any) {}
	_, log := startScript(t, provider, "herdr", serverScript)
	scope, scopeLog := startScript(t, provider, "systemd-run", `echo "$*" >> "$LOG"; exit 1`+"\n")
	saved, savedRun := manageTimeouts, systemdRun
	manageTimeouts.ready, manageTimeouts.gone = 2*time.Second, 100*time.Millisecond
	systemdRun = func() string { return scope }
	defer func() { manageTimeouts, systemdRun = saved, savedRun }()
	server.Stop()
	runProvider(t, provider)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if info, _ := provider.Info(); info.CanStart != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never startable")
		}
	}
	began := time.Now()
	_, err := provider.Manage(context.Background(), protocol.HTTPProviderManageRequest{Action: protocol.StartServer}, Project{})
	if elapsed := time.Since(began); !errors.Is(err, ErrNoServer) || elapsed < 2*time.Second || elapsed > 6*time.Second {
		t.Fatalf("start = %v after %v", err, elapsed)
	}
	// The systemd scope ended without a server: herdr was started plainly, once.
	binary := provider.config.BinaryPaths[0]
	if got := readLog(scopeLog); got != "--user --scope --quiet --collect -- "+binary+" server\n" {
		t.Fatalf("systemd-run argv = %q", got)
	}
	if strings.Count(readLog(log), "argv=server\n") != 1 {
		t.Fatalf("plain starts:\n%s", readLog(log))
	}
}

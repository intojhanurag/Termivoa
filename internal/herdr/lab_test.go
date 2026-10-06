//go:build !windows

package herdr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// lab is an isolated real herdr: a copy of the installed binary, a temp
// HOME and its own named session (docs/herdr-provider.md, section 15). It
// never reads the owner's HOME and stops only its own session.
type lab struct {
	t       *testing.T
	root    string
	binary  string
	session string
	env     []string
}

// TestLab runs only with PTERM_HERDR_LAB=1 (and PTERM_HERDR_LAB_BINARY to
// pick a herdr other than the one on PATH).
func TestLab(t *testing.T) {
	if os.Getenv("PTERM_HERDR_LAB") != "1" {
		t.Skip("opt-in: PTERM_HERDR_LAB=1 go test ./internal/herdr -run Lab")
	}
	lab := startLab(t)
	ctx := context.Background()
	repo := lab.repo("shop")
	notes := filepath.Join(lab.root, "notes")
	if err := os.MkdirAll(notes, 0o700); err != nil {
		t.Fatal(err)
	}
	lab.run("workspace", "create", "--cwd", repo, "--label", "shop", "--no-focus")
	lab.run("workspace", "create", "--cwd", notes, "--label", "notes", "--no-focus")
	lab.run("worktree", "create", "--workspace", "w1", "--branch", "silver-meadow", "--no-focus")
	lab.run("pane", "report-agent", "w1:p1", "--source", "lab", "--agent", "claude", "--state", "blocked")
	lab.run("pane", "report-agent", "w3:p1", "--source", "lab", "--agent", "codex", "--state", "working")

	var mu sync.Mutex
	var changes []Change
	provider := New(Config{BinaryPaths: []string{lab.binary}, Home: lab.root, Session: lab.session}, func(change Change) {
		mu.Lock()
		changes = append(changes, change)
		mu.Unlock()
	})
	provider.step(ctx)
	info, installed := provider.Info()
	if !installed || info.State != protocol.HTTPProviderStateReady || info.Version == nil || len(provider.caps.kinds) == 0 {
		t.Fatalf("info = %+v, kinds %v", info, provider.caps.kinds)
	}
	// The probe reads `terminal attach --help` text (0.8.2 has the command).
	if !provider.caps.attachAny {
		t.Fatal("the probe missed terminal attach")
	}
	snapshot := provider.Snapshot()
	if err := protocol.ValidateProviderSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 2 || snapshot.Groups[0].Name != "shop" || len(snapshot.Groups[0].Items) != 2 ||
		snapshot.Groups[0].State != "blocked" || snapshot.Groups[0].Items[1].State != "working" || snapshot.Groups[1].Name != "notes" {
		t.Fatalf("mapping: %+v", snapshot)
	}
	// idle reported to a headless server is done (unseen).
	lab.run("pane", "report-agent", "w3:p1", "--source", "lab", "--agent", "codex", "--state", "idle")
	provider.step(ctx)
	if state := provider.Snapshot().Groups[0].Items[1].State; state != "done" {
		t.Fatalf("idle on a headless server = %s", state)
	}

	target := *provider.Snapshot().Groups[0].Items[0].OpenTarget
	first := lab.attach(provider, target)
	time.Sleep(time.Second)
	// The attach gives the pane its own size.
	first.write("stty size\r")
	lab.waitPane("w1:p1", "20 45")
	// A second direct attach is refused (never --takeover).
	second := lab.attach(provider, target)
	if err := second.wait(); err == nil || !strings.Contains(second.output(), "already has an attached client") {
		t.Fatalf("second attach: %v %q", err, second.output())
	}
	// ctrl+b ctrl+b (what the broker sends for one ctrl+b) reaches the
	// program as one literal ctrl+b.
	first.write("cat -v\r")
	time.Sleep(300 * time.Millisecond)
	first.write("\x02\x02\r")
	lab.waitPane("w1:p1", "^B")
	first.write("\x03")
	// ctrl+b q detaches with exit 0.
	first.write("\x02q")
	if err := first.wait(); err != nil {
		t.Fatalf("detach: %v", err)
	}

	// M2 create, without an agent: the lab starts no real agent program.
	group := provider.Snapshot().Groups[0].ID
	created, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: group, Name: "lab-tree"})
	if err != nil || created.AgentStarted || created.Problem != nil {
		t.Fatalf("create = %+v %v", created, err)
	}
	provider.step(ctx)
	if _, err := provider.Attach(created.TargetID); err != nil && !errors.Is(err, ErrTargetMissing) {
		t.Fatal(err)
	}
	found := false
	for _, item := range provider.Snapshot().Groups[0].Items {
		found = found || item.ID == created.ItemID && item.Name == "lab-tree"
	}
	if !found {
		t.Fatalf("new worktree not in the snapshot: %+v", provider.Snapshot().Groups[0])
	}
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: group, Name: "lab-tree"}); !errors.Is(err, ErrExists) {
		t.Fatalf("second create = %v", err)
	}
	notesGroup := provider.Snapshot().Groups[1].ID
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: notesGroup, Name: "x"}); !errors.Is(err, ErrNotGit) {
		t.Fatalf("create outside git = %v", err)
	}
	lab.manage(provider)
}

// manage is docs/herdr-manage.md on the real herdr: A1 to A6, that the
// server stays up with no workspace, and $PATH in a pane of a server that
// the provider started. From here the poll loop runs; nothing calls step.
func (lab *lab) manage(provider *Provider) {
	t := lab.t
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); provider.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	text := func(value string) *string { return &value }
	if info, _ := provider.Info(); info.Version != nil {
		t.Logf("manage on herdr %s", *info.Version)
	}
	// group waits for a poll that shows the group named name (nil want: any) and returns it.
	group := func(name string, want func(protocol.HTTPProviderGroup) bool) protocol.HTTPProviderGroup {
		t.Helper()
		var found protocol.HTTPProviderGroup
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			provider.Poll()
			for _, candidate := range provider.Snapshot().Groups {
				if candidate.Name == name && (want == nil || want(candidate)) {
					return candidate
				}
				if candidate.Name == name {
					found = candidate
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("group %q never matched: %+v (all: %+v)", name, found, provider.Snapshot().Groups)
			}
		}
	}
	manage := func(request protocol.HTTPProviderManageRequest, project Project) protocol.HTTPProviderManageResponse {
		t.Helper()
		response, err := provider.Manage(ctx, request, project)
		if err != nil {
			t.Fatalf("%s: %v", request.Action, err)
		}
		return response
	}
	// checkout is the folder of a workspace, from herdr's own record.
	checkout := func(item string) string {
		t.Helper()
		snapshot, err := provider.readSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, workspace := range records(snapshot.Workspaces, func(record wireWorkspace) string { return record.ID }) {
			if workspace.ID == item && workspace.Worktree != nil {
				return workspace.Worktree.CheckoutPath
			}
		}
		t.Fatalf("no checkout path for %s", item)
		return ""
	}

	// A1: a git workspace that herdr gave no worktree record has "+"; one outside git has none.
	site := lab.repo("site")
	if err := os.WriteFile(filepath.Join(site, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lab.git("-C", site, "add", ".gitignore")
	lab.git("-C", site, "-c", "user.email=lab@example.com", "-c", "user.name=lab", "commit", "-q", "-m", "ignore")
	lab.run("workspace", "create", "--cwd", site, "--label", "site", "--no-focus")
	before := group("site", func(found protocol.HTTPProviderGroup) bool { return found.CanCreate })
	if !strings.HasPrefix(before.ID, "ws-") || before.Items[0].Linked != nil {
		t.Fatalf("a git workspace without worktrees = %+v", before)
	}
	if notes := group("notes", nil); notes.CanCreate {
		t.Fatalf("a workspace outside git can create: %+v", notes)
	}
	// A1: "+" works there, and the group moves from ws-<id> to g<hash>.
	created, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: before.ID, Name: "clean-tree"})
	if err != nil {
		t.Fatalf("create on a git workspace without worktrees = %v", err)
	}
	after := group("site", func(found protocol.HTTPProviderGroup) bool { return len(found.Items) == 2 })
	if !strings.HasPrefix(after.ID, "g") || after.Items[0].Linked != nil || after.Items[1].Linked == nil || after.Items[1].ID != created.ItemID {
		t.Fatalf("after the first worktree = %+v", after)
	}

	// A1: which folder counts when a pane changed folder. A workspace made outside git whose shell then moved into a repository.
	moved := filepath.Join(lab.root, "moved")
	if err := os.MkdirAll(moved, 0o700); err != nil {
		t.Fatal(err)
	}
	made := manage(protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace}, Project{Folder: moved, Name: "moved"})
	if made.ItemID == nil || made.TargetID == nil {
		t.Fatalf("new workspace = %+v", made)
	}
	shell := lab.attach(provider, *made.TargetID)
	time.Sleep(time.Second)
	shell.write("cd " + site + "\r")
	time.Sleep(2 * time.Second)
	shell.write("\x02q")
	_ = shell.wait()
	time.Sleep(3 * time.Second)
	// The moved workspace has no worktree record yet: its own group, named after it.
	movedGroup := group("moved", nil)
	_, err = provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: movedGroup.ID, Name: "moved-tree"})
	// herdr 0.8.2: the pane's cwd follows its shell, the host sees the repository there, and worktree.create uses it.
	t.Logf("a pane that moved into a repository: group %q, host canCreate=%t, herdr's worktree.create error=%v", movedGroup.ID, movedGroup.CanCreate, err)
	if movedGroup.CanCreate != (err == nil) {
		t.Fatalf("the host and herdr disagree about the moved pane: canCreate=%t, create=%v", movedGroup.CanCreate, err)
	}

	// A2: a new tab opens at once.
	tab := manage(protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: &after.Items[0].ID}, Project{})
	if tab.TargetID == nil {
		t.Fatalf("new tab = %+v", tab)
	}
	if _, err := provider.Attach(*tab.TargetID); err != nil {
		t.Fatalf("the new tab cannot open: %v", err)
	}

	// A3: close. A main checkout with an open worktree is refused by the host; herdr is not asked.
	main := after.Items[0]
	if _, err := provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: &main.ID, Name: &main.Name}, Project{}); !errors.Is(err, ErrGroupOpen) {
		t.Fatalf("close a main checkout with a worktree = %v", err)
	}
	if _, err := provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: &main.ID, Name: text("other")}, Project{}); !errors.Is(err, ErrRenamed) {
		t.Fatalf("close under another name = %v", err)
	}

	// A4: remove. Uncommitted work is never deleted; a clean worktree goes, files that git ignores go with it, the branch stays.
	tree := checkout(created.ItemID)
	if err := os.WriteFile(filepath.Join(tree, "work.txt"), []byte("not committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remove := protocol.HTTPProviderManageRequest{Action: protocol.RemoveItem, ItemID: &created.ItemID, Name: text("clean-tree")}
	if dirty := manage(remove, Project{}); dirty.Problem == nil || *dirty.Problem != protocol.Uncommitted {
		t.Fatalf("remove with uncommitted work = %+v", dirty)
	}
	if _, err := os.Stat(filepath.Join(tree, "work.txt")); err != nil {
		t.Fatalf("uncommitted work was touched: %v", err)
	}
	if err := os.Remove(filepath.Join(tree, "work.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if clean := manage(remove, Project{}); clean.Problem != nil {
		t.Fatalf("remove a clean worktree = %+v", clean)
	}
	group("site", func(found protocol.HTTPProviderGroup) bool {
		return !slices.ContainsFunc(found.Items, func(item protocol.HTTPProviderItem) bool { return item.ID == created.ItemID })
	})
	if _, err := os.Stat(tree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree folder (with an ignored .env) stayed: %v", err)
	}
	if branches := lab.git("-C", site, "branch", "--list", "clean-tree"); !strings.Contains(branches, "clean-tree") {
		t.Fatalf("the branch is gone: %q", branches)
	}
	if _, err := provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.RemoveItem, ItemID: &main.ID, Name: &main.Name}, Project{}); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("remove a main checkout = %v", err)
	}

	// A3 and "the server stays up with no workspace": close everything, worktrees before their main checkout.
	for round := 0; len(provider.Snapshot().Groups) > 0; round++ {
		if round == 20 {
			t.Fatalf("workspaces stayed: %+v", provider.Snapshot().Groups)
		}
		for _, found := range provider.Snapshot().Groups {
			// Last item first. herdr 0.9.3 refuses to close the workspace that owns a repository's
			// worktrees while another workspace of that repository is open: then the other one goes first.
			closed := false
			for index := len(found.Items) - 1; index >= 0 && !closed; index-- {
				item := found.Items[index]
				_, err := provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: &item.ID, Name: &item.Name}, Project{})
				t.Logf("close %s %q (linked %v, %d items in the group): %v", item.ID, item.Name, item.Linked != nil, len(found.Items), err)
				if closed = err == nil; !closed && !errors.Is(err, ErrGroupOpen) {
					t.Fatalf("closeItem: %v", err)
				}
			}
			if !closed {
				// herdr 0.9.3: two workspaces at one repository's main checkout close only as a group
				// (close_group), which Termivoa never sends (a close ends one workspace). The lab does,
				// to go on; docs/herdr-manage.md, section 13.
				if len(found.Items) < 2 || found.Items[0].Linked != nil || found.Items[1].Linked != nil {
					t.Fatalf("no workspace of group %s can be closed: %+v", found.ID, found.Items)
				}
				if err := provider.call(ctx, manageTimeouts.call, "workspace.close",
					map[string]any{"workspace_id": found.Items[0].ID, "close_group": true}, &struct{}{}); err != nil {
					t.Fatalf("close_group: %v", err)
				}
				t.Logf("herdr closes group %s only as a group", found.ID)
			}
		}
		time.Sleep(300 * time.Millisecond)
		provider.Poll()
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	if state := provider.Snapshot().State; state != protocol.HTTPProviderStateReady {
		t.Fatalf("the server left with its last workspace: %s", state)
	}
	if _, err := os.Stat(filepath.Join(lab.root, "shop")); err != nil {
		t.Fatalf("close deleted a folder: %v", err)
	}

	// A6: stop the lab's server; the provider starts it again (isolated HOME, the lab's session).
	lab.stop()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if info, _ := provider.Info(); info.State == protocol.NotRunning && info.CanStart != nil {
			break
		}
		if time.Now().After(deadline) {
			info, _ := provider.Info()
			t.Fatalf("after the stop = %+v", info)
		}
	}
	// git too: herdr runs it for the worktree calls of section 14 below.
	provider.config.PathEnv = filepath.Dir(lab.binary) + ":/usr/bin:/bin"
	began := time.Now()
	manage(protocol.HTTPProviderManageRequest{Action: protocol.StartServer}, Project{})
	t.Logf("startServer answered ready after %v", time.Since(began).Round(10*time.Millisecond))
	if info, _ := provider.Info(); info.State != protocol.HTTPProviderStateReady {
		t.Fatalf("after the start = %+v", info)
	}
	if _, err := provider.Manage(ctx, protocol.HTTPProviderManageRequest{Action: protocol.StartServer}, Project{}); !errors.Is(err, ErrNotStartable) {
		t.Fatalf("start while running = %v", err)
	}
	// A pane of that server has the PATH the provider gave it.
	opened := manage(protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace}, Project{Folder: moved, Name: "started"})
	if opened.TargetID == nil {
		t.Fatalf("new workspace in the started server = %+v", opened)
	}
	client := lab.attach(provider, *opened.TargetID)
	time.Sleep(time.Second)
	// To a file: the 45-column pane wraps a long PATH.
	seen := filepath.Join(lab.root, "pane-env.txt")
	client.write("echo \"PATH=$PATH HOME=$HOME\" > " + seen + "\r")
	var env string
	for deadline := time.Now().Add(10 * time.Second); !strings.HasSuffix(env, "\n"); time.Sleep(100 * time.Millisecond) {
		data, _ := os.ReadFile(seen)
		if env = string(data); time.Now().After(deadline) {
			t.Fatalf("the pane never wrote its environment: %q", env)
		}
	}
	t.Logf("a pane of the started server: %s", strings.TrimSpace(env))
	path, home, _ := strings.Cut(strings.TrimSpace(env), " HOME=")
	if !slices.Contains(filepath.SplitList(strings.TrimPrefix(path, "PATH=")), filepath.Dir(lab.binary)) || home != lab.root {
		t.Fatalf("the pane's environment is not the provider's: %s", env)
	}
	client.write("\x02q")
	_ = client.wait()

	// Section 14, B2: a linked worktree whose workspace is closed shows as closed, and openItem opens it again.
	manage(protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace}, Project{Folder: site, Name: "site"})
	again := group("site", func(found protocol.HTTPProviderGroup) bool { return found.CanCreate })
	branch, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: again.ID, Name: "again-tree"})
	if err != nil {
		t.Fatalf("create again-tree = %v", err)
	}
	group("site", func(found protocol.HTTPProviderGroup) bool { return len(found.Items) == 2 })
	// Worktrees made by hand with git: one the phone may open, and three it may not (detached, a name
	// outside the create rules, a folder that is gone).
	for _, args := range [][]string{{"-b", "by-hand", "by-hand"}, {"--detach", "detached"}, {"-b", "Odd_Name", "odd"}, {"-b", "gone-tree", "gone"}} {
		args[len(args)-1] = filepath.Join(lab.root, args[len(args)-1])
		lab.git(append([]string{"-C", site, "worktree", "add", "-q"}, args...)...)
	}
	if err := os.RemoveAll(filepath.Join(lab.root, "gone")); err != nil {
		t.Fatal(err)
	}
	manage(protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: &branch.ItemID, Name: text("again-tree")}, Project{})
	began = time.Now()
	again = group("site", func(found protocol.HTTPProviderGroup) bool { return slices.Contains(found.Closed, "again-tree") })
	t.Logf("closed %v after the close: %v", time.Since(began).Round(10*time.Millisecond), again.Closed)
	// clean-tree was removed (its branch stays, its folder is gone): not closed, like the other three.
	for _, name := range []string{"clean-tree", "Odd_Name", "gone-tree", "master", "main", ""} {
		if slices.Contains(again.Closed, name) {
			t.Fatalf("%q is in closed: %v", name, again.Closed)
		}
	}
	if !slices.Contains(again.Closed, "by-hand") || !slices.IsSorted(again.Closed) {
		t.Fatalf("closed = %v", again.Closed)
	}
	if err := protocol.ValidateProviderSnapshot(provider.Snapshot()); err != nil {
		t.Fatal(err)
	}
	open := protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: &again.ID, Worktree: text("again-tree")}
	opened = manage(open, Project{})
	if opened.ItemID == nil || opened.TargetID == nil {
		t.Fatalf("openItem = %+v", opened)
	}
	if _, err := provider.Attach(*opened.TargetID); err != nil {
		t.Fatalf("the opened worktree cannot open: %v", err)
	}
	// It is an item again (a linked one), and not closed.
	group("site", func(found protocol.HTTPProviderGroup) bool {
		return !slices.Contains(found.Closed, "again-tree") && slices.ContainsFunc(found.Items, func(item protocol.HTTPProviderItem) bool {
			return item.ID == *opened.ItemID && item.Name == "again-tree" && item.Linked != nil
		})
	})
	// herdr's own second open answers the open workspace (already_open) and makes none.
	var second struct {
		AlreadyOpen bool `json:"already_open"`
		Workspace   struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
	}
	if err := provider.call(ctx, manageTimeouts.call, "worktree.open",
		map[string]any{"workspace_id": again.Items[0].ID, "branch": "again-tree", "focus": false}, &second); err != nil || !second.AlreadyOpen || second.Workspace.ID != *opened.ItemID {
		t.Fatalf("herdr's second worktree.open = %+v %v", second, err)
	}
	// The phone can name only what herdr lists as closed now.
	for _, name := range []string{"again-tree", "Odd_Name", "gone-tree", "clean-tree", "no-such"} {
		open.Worktree = &name
		if _, err := provider.Manage(ctx, open, Project{}); !errors.Is(err, ErrTargetMissing) {
			t.Fatalf("openItem %q = %v", name, err)
		}
	}
	open.Worktree = text("by-hand")
	if hand := manage(open, Project{}); hand.ItemID == nil || hand.TargetID == nil {
		t.Fatalf("openItem for a worktree made by hand = %+v", hand)
	}

	// Section 14, B1: a new tab with an agent. An agent must run in this herdr for its kind to be offered.
	// The lab has no agent program. herdr 0.8.2 types the kind's command into the tab's shell and answers
	// "started" at once (launch_pending); the prompt then fails (agent_not_ready): promptFailed. A herdr
	// that fails the start answers agentFailed. Either way the tab is made and the answer has a problem.
	pane, _, _ := strings.Cut(*opened.TargetID, ".")
	lab.run("pane", "report-agent", pane, "--source", "lab", "--agent", "claude", "--state", "idle")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if info, _ := provider.Info(); slices.Contains(info.AgentKinds, "claude") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claude is not offered")
		}
	}
	agentTab := protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: opened.ItemID, Agent: text("claude"), Prompt: text("hello")}
	for round := range 2 { // herdr takes an agent name once: the second tab needs another
		began = time.Now()
		tab := manage(agentTab, Project{})
		if tab.AgentStarted == nil || tab.AgentProblem == nil || *tab.AgentStarted != (*tab.AgentProblem == protocol.PromptFailed) {
			t.Fatalf("agent tab %d = %+v", round, tab)
		}
		t.Logf("agent tab %d after %v: agentStarted=%t agentProblem=%q targetId=%t", round, time.Since(began).Round(10*time.Millisecond), *tab.AgentStarted, *tab.AgentProblem, tab.TargetID != nil)
		if tab.TargetID == nil {
			t.Fatalf("agent tab %d has no terminal: %+v", round, tab)
		}
		if _, err := provider.Attach(*tab.TargetID); err != nil {
			t.Fatalf("agent tab %d cannot open: %v", round, err)
		}
	}
	agentTab.Agent = text("codex") // a start kind of herdr, but no codex runs here
	if _, err := provider.Manage(ctx, agentTab, Project{}); !errors.Is(err, ErrKind) {
		t.Fatalf("an agent that is not offered = %v", err)
	}
}

// stop stops the lab's own session, as its cleanup does.
func (lab *lab) stop() {
	lab.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop := exec.CommandContext(ctx, lab.binary, "session", "stop", lab.session)
	stop.Env, stop.Dir = lab.env, lab.root
	if output, err := stop.CombinedOutput(); err != nil {
		lab.t.Fatalf("session stop: %v\n%s", err, output)
	}
}

func (lab *lab) git(args ...string) string {
	lab.t.Helper()
	command := exec.Command("git", args...)
	command.Env = lab.env
	output, err := command.CombinedOutput()
	if err != nil {
		lab.t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func startLab(t *testing.T) *lab {
	t.Helper()
	source := os.Getenv("PTERM_HERDR_LAB_BINARY")
	if source == "" {
		found, err := exec.LookPath("herdr")
		if err != nil {
			t.Skip("no herdr on PATH")
		}
		source = found
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "termivoa-lab-")
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := os.UserHomeDir(); strings.HasPrefix(root, real) {
		t.Fatal("the lab HOME must not be inside the real home")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".zshrc"), []byte("PROMPT='%# '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No background calls to herdr.dev from the lab.
	if err := os.MkdirAll(filepath.Join(root, ".config", "herdr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".config", "herdr", "config.toml"),
		[]byte("[update]\nversion_check = false\nmanifest_check = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	lab := &lab{
		t: t, root: root, binary: binary, session: "termivoa-lab-" + hex.EncodeToString(suffix),
		env: []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "SHELL=/bin/zsh", "TERM=xterm-256color", "LANG=en_US.UTF-8"},
	}
	server := exec.Command(binary, "--session", lab.session, "server")
	server.Env, server.Dir = lab.env, root
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, binary, "session", "stop", lab.session)
		stop.Env, stop.Dir = lab.env, root
		_ = stop.Run()
		done := make(chan struct{})
		go func() { _ = server.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = server.Process.Kill()
		}
		_ = os.RemoveAll(root)
	})
	socket := filepath.Join(root, ".config", "herdr", "sessions", lab.session, "herdr.sock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return lab
		}
		if time.Now().After(deadline) {
			t.Fatal("lab server did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (lab *lab) run(args ...string) string {
	lab.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, lab.binary, append([]string{"--session", lab.session}, args...)...)
	command.Env, command.Dir = lab.env, lab.root
	output, err := command.CombinedOutput()
	if err != nil {
		lab.t.Fatalf("herdr %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func (lab *lab) repo(name string) string {
	lab.t.Helper()
	repo := filepath.Join(lab.root, name)
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.email=lab@example.com", "-c", "user.name=lab", "commit", "-q", "--allow-empty", "-m", "init"}} {
		command := exec.Command("git", args...)
		command.Env = lab.env
		if output, err := command.CombinedOutput(); err != nil {
			lab.t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo
}

func (lab *lab) waitPane(pane, want string) {
	lab.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(lab.run("pane", "read", pane), want) {
		if time.Now().After(deadline) {
			lab.t.Fatalf("pane %s never showed %q:\n%s", pane, want, lab.run("pane", "read", pane))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type labClient struct {
	t       *testing.T
	process *ptyhost.Process
	mu      sync.Mutex
	text    strings.Builder
}

// attach starts the provider's argv in a 45x20 PTY, as a provider session
// does, and keeps draining it.
func (lab *lab) attach(provider *Provider, target string) *labClient {
	lab.t.Helper()
	found, err := provider.Attach(target)
	if err != nil {
		lab.t.Fatal(err)
	}
	process, err := ptyhost.Start(context.Background(), ptyhost.Spec{
		Executable: found.Argv[0], Args: found.Argv[1:], Env: found.Env, Directory: lab.root, Columns: 45, Rows: 20,
	})
	if err != nil {
		lab.t.Fatal(err)
	}
	lab.t.Cleanup(func() { _ = process.Close() })
	client := &labClient{t: lab.t, process: process}
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := process.Read(buffer)
			client.mu.Lock()
			client.text.Write(buffer[:count])
			client.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return client
}

func (client *labClient) write(text string) {
	client.t.Helper()
	if _, err := client.process.Write([]byte(text)); err != nil {
		client.t.Fatal(err)
	}
}

func (client *labClient) output() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.text.String()
}

func (client *labClient) wait() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.process.Wait(ctx)
}

//go:build !windows

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// treeList is herdr's worktree.list answer for the shop repository of the
// 0.8.2 fixture: only old-idea and fix/login are closed worktrees that the
// phone may open.
const treeList = `{"type":"worktree_list","source":{"repo_root":"/home/demo/shop"},"worktrees":[
{"path":"/home/demo/shop","branch":"main","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":false,"open_workspace_id":"w1","label":"shop"},
{"path":"/home/demo/.herdr/worktrees/shop/silver-meadow","branch":"silver-meadow","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"open_workspace_id":"w3","label":"shop"},
{"path":"/home/demo/secret/old-idea","branch":"old-idea","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/login","branch":"fix/login","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"open_workspace_id":null,"label":"shop"},
{"path":"/home/demo/secret/gone","branch":"gone-tree","is_bare":false,"is_detached":false,"is_prunable":true,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/det","is_bare":false,"is_detached":true,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/det2","branch":"det-named","is_bare":false,"is_detached":true,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/bare","branch":"bare-tree","is_bare":true,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/odd","branch":"Odd_Name","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/flag","branch":"--force","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"label":"shop"},
{"path":"/home/demo/secret/main2","branch":"not-linked","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":false,"label":"shop"}
]}`

// listWorld answers worktree.list from text (by workspace) and counts the calls.
type listWorld struct {
	mu    sync.Mutex
	text  map[string]string // by workspace ID; "" is herdr's error
	asked []string
	wait  time.Duration
}

func (world *listWorld) serve(server *herdrtest.Server) {
	server.Handle("worktree.list", func(params json.RawMessage) (any, *herdrtest.Error) {
		var request struct {
			Workspace string `json:"workspace_id"`
		}
		_ = json.Unmarshal(params, &request)
		world.mu.Lock()
		world.asked = append(world.asked, request.Workspace)
		text, wait := world.text[request.Workspace], world.wait
		world.mu.Unlock()
		time.Sleep(wait)
		if text == "" {
			return nil, &herdrtest.Error{Code: "not_git_worktree", Message: "fatal: /home/demo/secret is not a work tree"}
		}
		return json.RawMessage(text), nil
	})
}

func (world *listWorld) take() []string {
	world.mu.Lock()
	defer world.mu.Unlock()
	asked := world.asked
	world.asked = nil
	return asked
}

func closedOf(provider *Provider, group string) []string {
	for _, found := range provider.Snapshot().Groups {
		if found.ID == group {
			return found.Closed
		}
	}
	return nil
}

func TestClosedWorktreesAreListedAndFiltered(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Snapshot(readFixture(t, "snapshot-0.8.2.json"))
	world := &listWorld{text: map[string]string{"w1": treeList}}
	world.serve(server)
	ctx := context.Background()
	provider.step(ctx) // the first turn finds the groups
	if asked := world.take(); len(asked) != 0 || closedOf(provider, shopGroup) != nil {
		t.Fatalf("first turn asked %v", asked)
	}
	provider.step(ctx)
	// Each git group is asked with its main workspace; the group outside git (w2) is never asked.
	asked := world.take()
	if len(asked) != 2 || !(asked[0] == "w1" && asked[1] == "w4" || asked[0] == "w4" && asked[1] == "w1") {
		t.Fatalf("asked %v", asked)
	}
	// Linked, no workspace, not prunable, bare or detached, a create name; sorted.
	if got := closedOf(provider, shopGroup); !reflect.DeepEqual(got, []string{"fix/login", "old-idea"}) {
		t.Fatalf("closed = %v", got)
	}
	snapshot := provider.Snapshot()
	if err := protocol.ValidateProviderSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	// Nothing of herdr's paths or text leaves the host.
	if data, _ := json.Marshal(snapshot); strings.Contains(string(data), "secret") || strings.Contains(string(data), "/home/demo") {
		t.Fatalf("a path left the host: %s", data)
	}
	// herdr's error for the api repository: no list, and nothing else changes.
	for _, group := range snapshot.Groups {
		if group.ID != shopGroup && group.Closed != nil {
			t.Fatalf("group %s closed = %v", group.ID, group.Closed)
		}
	}

	// At most 16 names.
	var many []string
	for index := range 40 {
		many = append(many, fmt.Sprintf(`{"path":"/x/%d","branch":"tree-%02d","is_bare":false,"is_detached":false,"is_prunable":false,"is_linked_worktree":true,"label":"shop"}`, 39-index, 39-index))
	}
	world.mu.Lock()
	world.text["w1"] = `{"worktrees":[` + strings.Join(many, ",") + `]}`
	world.mu.Unlock()
	now := time.Now().Add(closedEvery)
	provider.now = func() time.Time { return now }
	provider.step(ctx)
	if got := closedOf(provider, shopGroup); len(got) != maxClosed || got[0] != "tree-00" || got[15] != "tree-15" {
		t.Fatalf("closed = %v", got)
	}
}

// gitGroups is a snapshot with count repositories, one workspace each.
func gitGroups(count int) []byte {
	var workspaces []string
	for index := range count {
		workspaces = append(workspaces, fmt.Sprintf(`{"workspace_id":"w%d","label":"r%d","number":%d,"worktree":{"repo_key":"/r%d/.git","repo_name":"r%d","checkout_path":"/r%d","is_linked_worktree":false}}`,
			index, index, index, index, index, index))
	}
	return []byte(`{"version":"0.8.2","protocol":20,"workspaces":[` + strings.Join(workspaces, ",") + `],"tabs":[],"panes":[],"layouts":[],"agents":[]}`)
}

func TestClosedWorktreesBounds(t *testing.T) {
	provider, server, _ := newTestProvider(t, false)
	server.Snapshot(gitGroups(6))
	world := &listWorld{text: map[string]string{}}
	for index := range 7 {
		world.text[fmt.Sprintf("w%d", index)] = `{"worktrees":[{"branch":"old-idea","is_linked_worktree":true}]}`
	}
	world.serve(server)
	ctx := context.Background()
	now := fixedNow
	provider.now = func() time.Time { return now }
	turn := func(after time.Duration, want int, why string) {
		t.Helper()
		now = now.Add(after)
		provider.step(ctx)
		if asked := world.take(); len(asked) != want {
			t.Fatalf("%s: asked %v, want %d calls", why, asked, want)
		}
	}
	listed := func() int {
		count := 0
		for _, group := range provider.Snapshot().Groups {
			count += len(group.Closed)
		}
		return count
	}
	turn(0, 0, "the first turn has no groups yet")
	turn(pollEvery, closedPerTurn, "at most 4 groups in one turn")
	turn(pollEvery, 2, "the other 2 groups in the next turn")
	if listed() != 6 {
		t.Fatalf("closed lists = %d", listed())
	}
	turn(pollEvery, 0, "no group twice in 10 s")
	turn(pollEvery, 0, "no group twice in 10 s")
	// The results stay between polls.
	if listed() != 6 {
		t.Fatalf("closed lists were not kept: %d", listed())
	}
	turn(closedEvery-3*pollEvery, closedPerTurn, "the first 4 again after 10 s")
	turn(pollEvery, 2, "the other 2 after their 10 s")

	// A workspace came or left: every list may be old. Each is asked again, 4 in one turn, the
	// longest wait first; a list stays on screen until its turn.
	server.Snapshot(gitGroups(7))
	turn(pollEvery, closedPerTurn, "after a change of workspaces")
	if listed() != 6 {
		t.Fatalf("lists that were not asked again yet went away: %d", listed())
	}
	turn(pollEvery, 3, "the rest, with the new group")
	// A group that left takes its list along.
	server.Snapshot(gitGroups(1))
	turn(pollEvery, 1, "one group stays")
	turn(pollEvery, 0, "nothing new")
	if len(provider.closed) != 1 || listed() != 1 {
		t.Fatalf("lists kept: %v", provider.closed)
	}

	// A slow herdr costs a turn about one second, not one second for each group.
	server.Snapshot(gitGroups(6))
	turn(pollEvery, 1, "the change is seen; the new groups are known a turn later")
	world.mu.Lock()
	world.wait = 3 * time.Second
	world.mu.Unlock()
	now = now.Add(pollEvery)
	began := time.Now()
	provider.step(ctx)
	if elapsed := time.Since(began); elapsed < callTimeout || elapsed > callTimeout+800*time.Millisecond {
		t.Fatalf("a turn with 4 hung lists took %v", elapsed)
	}
	if provider.Snapshot().State != protocol.HTTPProviderStateReady || listed() != 1 {
		t.Fatalf("after a hung list: %s, %d lists", provider.Snapshot().State, listed())
	}
}

func TestMapSnapshotPutsClosedInItsGroup(t *testing.T) {
	closed := map[string][]string{shopGroup: {"fix/login", "old-idea"}, "gone": {"x"}}
	result := mapSnapshot(decodeSnapshot(t, readFixture(t, "snapshot-0.8.2.json")), false, fixedNow, nil, nil, closed)
	for _, group := range result.response.Groups {
		if want := closed[group.ID]; !reflect.DeepEqual(group.Closed, want) {
			t.Fatalf("group %s closed = %v, want %v", group.ID, group.Closed, want)
		}
	}
}

// openWorld is manageWorld with closed worktrees in the shop repository.
func newOpenWorld(t *testing.T, attachAny bool) (*manageWorld, *listWorld) {
	t.Helper()
	world := newManageWorld(t, attachAny)
	lists := &listWorld{text: map[string]string{"w1": treeList}}
	lists.serve(world.server)
	return world, lists
}

func openRequest(group, name string) protocol.HTTPProviderManageRequest {
	return protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: &group, Worktree: &name}
}

func TestManageOpenItem(t *testing.T) {
	world, lists := newOpenWorld(t, false)
	ctx := context.Background()
	apiGroup := world.provider.Snapshot().Groups[2].ID // herdr refuses its worktree.list
	for _, test := range []struct {
		name    string
		request protocol.HTTPProviderManageRequest
		herdr   *herdrtest.Error
		want    error
		call    string
	}{
		{"a closed worktree", openRequest(shopGroup, "old-idea"), nil, nil, `worktree.open {"branch":"old-idea","focus":false,"workspace_id":"w1"}`},
		{"a name with a slash", openRequest(shopGroup, "fix/login"), nil, nil, `worktree.open {"branch":"fix/login","focus":false,"workspace_id":"w1"}`},
		{"an open worktree", openRequest(shopGroup, "silver-meadow"), nil, ErrTargetMissing, ""},
		{"a name herdr does not list", openRequest(shopGroup, "ghost"), nil, ErrTargetMissing, ""},
		{"a prunable worktree", openRequest(shopGroup, "gone-tree"), nil, ErrTargetMissing, ""},
		{"a detached worktree", openRequest(shopGroup, "det-named"), nil, ErrTargetMissing, ""},
		{"the main checkout", openRequest(shopGroup, "main"), nil, ErrTargetMissing, ""},
		{"an unknown group", openRequest("gffffffffffff", "old-idea"), nil, ErrTargetMissing, ""},
		{"a group whose list herdr refuses", openRequest(apiGroup, "old-idea"), nil, ErrManage, ""},
		{"a group outside git", openRequest("ws-w2", "old-idea"), nil, ErrManage, ""},
		{"herdr lost the worktree", openRequest(shopGroup, "old-idea"), &herdrtest.Error{Code: "worktree_not_found", Message: "x"}, ErrTargetMissing, "worktree.open"},
		{"herdr fails the open", openRequest(shopGroup, "old-idea"), &herdrtest.Error{Code: "worktree_open_failed", Message: "fatal: /home/demo/secret"}, ErrManage, "worktree.open"},
	} {
		t.Run(test.name, func(t *testing.T) {
			world.mu.Lock()
			world.reply, world.logs = test.herdr, nil
			world.mu.Unlock()
			lists.take()
			response, err := world.provider.Manage(ctx, test.request, Project{})
			calls := world.take()
			if !errors.Is(err, test.want) || (err == nil) != (test.want == nil) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.call == "" && len(calls) != 0 || test.call != "" && (len(calls) != 1 || !strings.HasPrefix(calls[0], test.call)) {
				t.Fatalf("herdr calls = %v, want %q", calls, test.call)
			}
			// The item is the workspace herdr opened. This herdr cannot attach a terminal without an agent: no targetId.
			if err == nil && (response.ItemID == nil || *response.ItemID != "w9" || response.TargetID != nil) {
				t.Fatalf("response = %+v", response)
			}
			world.mu.Lock()
			defer world.mu.Unlock()
			if test.herdr != nil && (len(world.logs) != 1 || world.logs[0] != "herdr: openItem failed: "+test.herdr.Code) {
				t.Fatalf("log = %q", world.logs)
			}
			if strings.Contains(strings.Join(world.logs, "\n"), "/home/demo") {
				t.Fatalf("a path in the log: %q", world.logs)
			}
		})
	}
}

func TestManageOpenItemOpensAtOnceAndTakesAlreadyOpen(t *testing.T) {
	world, lists := newOpenWorld(t, true)
	ctx := context.Background()
	lists.take()
	response, err := world.provider.Manage(ctx, openRequest(shopGroup, "old-idea"), Project{})
	if err != nil || response.ItemID == nil || *response.ItemID != "w9" || response.TargetID == nil || *response.TargetID != "w3:p9.term_9" {
		t.Fatalf("open = %+v %v", response, err)
	}
	if _, err := world.provider.Attach(*response.TargetID); err != nil {
		t.Fatalf("attach the opened worktree: %v", err)
	}
	// The list is read again for the action: the cached one is not trusted.
	if asked := lists.take(); len(asked) == 0 || asked[0] != "w1" {
		t.Fatalf("worktree.list calls = %v", asked)
	}
	// Another client opened it between the list and the open: herdr answers the open workspace.
	world.server.Handle("worktree.open", func(json.RawMessage) (any, *herdrtest.Error) {
		return json.RawMessage(`{"type":"worktree_opened","already_open":true,"workspace":{"workspace_id":"w3"},
			"root_pane":{"pane_id":"w3:p1","terminal_id":"term_65d0031602b703"}}`), nil
	})
	response, err = world.provider.Manage(ctx, openRequest(shopGroup, "old-idea"), Project{})
	if err != nil || *response.ItemID != "w3" || *response.TargetID != "w3:p1.term_65d0031602b703" {
		t.Fatalf("already open = %+v %v", response, err)
	}
	// It shares the lock of create and manage.
	entered, release := make(chan struct{}), make(chan struct{})
	world.server.Handle("worktree.open", func(json.RawMessage) (any, *herdrtest.Error) {
		close(entered)
		<-release
		return map[string]any{}, nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := world.provider.Manage(ctx, openRequest(shopGroup, "old-idea"), Project{})
		done <- err
	}()
	<-entered
	if _, err := world.provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "a"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("create during an open = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManageOpenItemInAFullGroup(t *testing.T) {
	world, lists := newOpenWorld(t, true)
	var workspaces []string
	for index := range maxItems {
		workspaces = append(workspaces, fmt.Sprintf(`{"workspace_id":"w%d","label":"n%d","number":%d,"worktree":{"repo_key":"/home/demo/shop/.git","repo_name":"shop","checkout_path":"/x/%d","is_linked_worktree":%t}}`,
			index+1, index, index+1, index, index > 0))
	}
	world.server.Snapshot([]byte(`{"version":"0.8.2","protocol":20,"workspaces":[` + strings.Join(workspaces, ",") + `],"tabs":[],"panes":[],"layouts":[],"agents":[]}`))
	lists.take()
	if _, err := world.provider.Manage(context.Background(), openRequest(shopGroup, "old-idea"), Project{}); !errors.Is(err, ErrFull) {
		t.Fatalf("a 65th item = %v", err)
	}
	if calls := world.take(); len(calls) != 0 {
		t.Fatalf("herdr calls = %v", calls)
	}
}

// agentWorld is manageWorld whose herdr starts agents in the new tab's pane (w3:p9).
func newAgentWorld(t *testing.T, attachAny bool) (*manageWorld, *createWorld) {
	t.Helper()
	world := newManageWorld(t, attachAny)
	agents := &createWorld{server: world.server, calls: map[string]map[string]any{}}
	for _, method := range []string{"agent.start", "agent.prompt"} {
		world.server.Handle(method, func(params json.RawMessage) (any, *herdrtest.Error) {
			var values map[string]any
			_ = json.Unmarshal(params, &values)
			agents.mu.Lock()
			agents.calls[method] = values
			agents.mu.Unlock()
			world.mu.Lock()
			world.calls = append(world.calls, method)
			world.mu.Unlock()
			if method == "agent.start" {
				// The pane of the new tab now has an agent.
				snapshot := string(readFixture(t, "snapshot-0.8.2.json"))
				snapshot = strings.Replace(snapshot, "\"tabs\":[\n", "\"tabs\":[\n"+`{"label":"9","number":9,"tab_id":"w3:t9","workspace_id":"w3"},`, 1)
				snapshot = strings.Replace(snapshot, "\"panes\":[\n", "\"panes\":[\n"+`{"pane_id":"w3:p9","tab_id":"w3:t9","terminal_id":"term_9","workspace_id":"w3"},`, 1)
				snapshot = strings.Replace(snapshot, "\"agents\":[\n", "\"agents\":[\n"+`{"agent":"claude","agent_status":"idle","pane_id":"w3:p9","tab_id":"w3:t9","terminal_id":"term_9","workspace_id":"w3"},`, 1)
				world.server.Snapshot([]byte(snapshot))
			}
			return map[string]any{}, nil
		})
	}
	return world, agents
}

func agentTab(agent string, prompt *string) protocol.HTTPProviderManageRequest {
	request := manageRequest(protocol.NewTab, "w3", "")
	request.Agent, request.Prompt = &agent, prompt
	return request
}

func TestManageNewTabWithAnAgent(t *testing.T) {
	// A herdr without terminal attach: the new tab opens only because its agent started.
	world, agents := newAgentWorld(t, false)
	ctx := context.Background()
	prompt := "fix the flaky test\n\tthen run it"
	response, err := world.provider.Manage(ctx, agentTab("claude", &prompt), Project{})
	if err != nil || *response.ItemID != "w3" || response.AgentStarted == nil || !*response.AgentStarted || response.AgentProblem != nil ||
		response.TargetID == nil || *response.TargetID != "w3:p9.term_9" {
		t.Fatalf("new tab with an agent = %+v %v", response, err)
	}
	if err := protocol.ValidateProviderManageResponse(response); err != nil {
		t.Fatal(err)
	}
	if calls := world.take(); len(calls) != 3 || !strings.HasPrefix(calls[0], "tab.create") || calls[1] != "agent.start" || calls[2] != "agent.prompt" {
		t.Fatalf("herdr calls = %v", calls)
	}
	// Create's call, in the pane of the new tab. herdr takes each agent name once: the pane is in it.
	if call := agents.call("agent.start"); call["kind"] != "claude" || call["name"] != "claude-w3-p9" || call["pane_id"] != "w3:p9" || call["timeout_ms"] != float64(agentReadyMs) {
		t.Fatalf("agent.start %v", call)
	}
	if call := agents.call("agent.prompt"); call["target"] != "w3:p9" || call["text"] != prompt {
		t.Fatalf("agent.prompt %v", call)
	}
	if _, err := world.provider.Attach(*response.TargetID); err != nil {
		t.Fatalf("attach the agent tab: %v", err)
	}

	// gemini is a start kind but runs nowhere in this herdr (create's rule): nothing is made.
	if _, err := world.provider.Manage(ctx, agentTab("gemini", nil), Project{}); !errors.Is(err, ErrKind) {
		t.Fatalf("a kind that is not offered = %v", err)
	}
	if calls := world.take(); len(calls) != 0 {
		t.Fatalf("herdr calls = %v", calls)
	}
	// Without an agent the answer says nothing about one.
	if response, err := world.provider.Manage(ctx, manageRequest(protocol.NewTab, "w3", ""), Project{}); err != nil || response.AgentStarted != nil || response.AgentProblem != nil {
		t.Fatalf("a shell tab = %+v %v", response, err)
	}
}

func TestManageNewTabAgentProblems(t *testing.T) {
	prompt := "go"
	for _, test := range []struct {
		name          string
		attachAny     bool
		start, prompt *herdrtest.Error
		started       bool
		problem       protocol.HTTPProviderCreateProblem
		target        bool
	}{
		{"the agent fails", false, &herdrtest.Error{Code: "agent_start_failed", Message: "no claude on PATH"}, nil, false, protocol.AgentFailed, false},
		{"the agent fails, a herdr that opens shells", true, &herdrtest.Error{Code: "agent_start_failed"}, nil, false, protocol.AgentFailed, true},
		// The agent runs and waits at a question: its pane can be attached, as in create.
		{"the agent is not ready", false, &herdrtest.Error{Code: "agent_not_ready"}, nil, false, protocol.AgentNotReady, true},
		{"the prompt fails", false, nil, &herdrtest.Error{Code: "agent_not_ready"}, true, protocol.PromptFailed, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			world, _ := newAgentWorld(t, test.attachAny)
			if test.start != nil {
				world.server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) { return nil, test.start })
			}
			if test.prompt != nil {
				world.server.Handle("agent.prompt", func(json.RawMessage) (any, *herdrtest.Error) { return nil, test.prompt })
			}
			response, err := world.provider.Manage(context.Background(), agentTab("claude", &prompt), Project{})
			// The tab exists either way.
			if err != nil || *response.ItemID != "w3" || response.AgentStarted == nil || *response.AgentStarted != test.started ||
				response.AgentProblem == nil || *response.AgentProblem != test.problem || (response.TargetID != nil) != test.target {
				t.Fatalf("response = %+v %v", response, err)
			}
			if calls := world.take(); len(calls) == 0 || !strings.HasPrefix(calls[0], "tab.create") {
				t.Fatalf("herdr calls = %v", calls)
			}
		})
	}
}

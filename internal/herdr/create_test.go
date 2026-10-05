package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// createWorld serves the 0.8.2 fixture and answers the M2 methods like
// herdr: worktree.create adds w9 to the snapshot.
type createWorld struct {
	mu     sync.Mutex
	server *herdrtest.Server
	base   string
	calls  map[string]map[string]any
}

const newWorkspace = `{"active_tab_id":"w9:t1","label":"brave-fox","number":9,"workspace_id":"w9","worktree":{"checkout_path":"/home/demo/.herdr/worktrees/shop/brave-fox","is_linked_worktree":true,"repo_key":"/home/demo/shop/.git","repo_name":"shop","repo_root":"/home/demo/shop"}}`

func newCreateWorld(t *testing.T, server *herdrtest.Server) *createWorld {
	world := &createWorld{server: server, base: string(readFixture(t, "snapshot-0.8.2.json")), calls: map[string]map[string]any{}}
	server.Snapshot([]byte(world.base))
	record := func(method string, params json.RawMessage) {
		var values map[string]any
		_ = json.Unmarshal(params, &values)
		world.mu.Lock()
		world.calls[method] = values
		world.mu.Unlock()
	}
	server.Handle("worktree.create", func(params json.RawMessage) (any, *herdrtest.Error) {
		record("worktree.create", params)
		world.add(false)
		return map[string]any{"type": "worktree_created", "workspace": json.RawMessage(newWorkspace),
			"root_pane": map[string]any{"pane_id": "w9:p1", "terminal_id": "term_9"}}, nil
	})
	server.Handle("agent.start", func(params json.RawMessage) (any, *herdrtest.Error) {
		record("agent.start", params)
		world.add(true)
		return map[string]any{"type": "agent_started"}, nil
	})
	server.Handle("agent.prompt", func(params json.RawMessage) (any, *herdrtest.Error) {
		record("agent.prompt", params)
		return map[string]any{"type": "agent_prompted"}, nil
	})
	return world
}

// add puts w9 (and with agent, a claude in it) into the served snapshot.
func (world *createWorld) add(agent bool) {
	insert := func(snapshot, list, record string) string {
		return strings.Replace(snapshot, "\""+list+"\":[\n", "\""+list+"\":[\n"+record+",", 1)
	}
	snapshot := insert(world.base, "workspaces", newWorkspace)
	snapshot = insert(snapshot, "tabs", `{"label":"1","number":1,"tab_id":"w9:t1","workspace_id":"w9"}`)
	snapshot = insert(snapshot, "panes", `{"pane_id":"w9:p1","tab_id":"w9:t1","terminal_id":"term_9","workspace_id":"w9"}`)
	snapshot = insert(snapshot, "layouts", `{"tab_id":"w9:t1","focused_pane_id":"w9:p1"}`)
	if agent {
		snapshot = insert(snapshot, "agents", `{"agent":"claude","agent_status":"idle","pane_id":"w9:p1","tab_id":"w9:t1","terminal_id":"term_9","workspace_id":"w9"}`)
	}
	world.server.Snapshot([]byte(snapshot))
}

func (world *createWorld) call(method string) map[string]any {
	world.mu.Lock()
	defer world.mu.Unlock()
	return world.calls[method]
}

const shopGroup = "g7d0f77ccdcfd"

func readyProvider(t *testing.T) (*Provider, *herdrtest.Server, *createWorld) {
	t.Helper()
	provider, server, _ := newTestProvider(t, false)
	world := newCreateWorld(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); provider.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for deadline := time.Now().Add(5 * time.Second); provider.Snapshot().State != protocol.HTTPProviderStateReady; {
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return provider, server, world
}

// A poll that ran between worktree.create and agent.start cached the new
// pane without its agent, which herdr 0.8.2 without terminal attach cannot
// open. Create must answer only after a read that saw the agent.
func TestCreateAnswersAfterAReadThatSawTheAgent(t *testing.T) {
	provider, server, world := readyProvider(t)
	server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			provider.Poll()
			provider.mu.RLock()
			_, cached := provider.view.targets["w9:p1.term_9"]
			provider.mu.RUnlock()
			if cached {
				break
			}
		}
		world.add(true)
		return map[string]any{"type": "agent_started"}, nil
	})
	agent := "claude"
	response, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent})
	if err != nil || !response.AgentStarted {
		t.Fatalf("create = %+v %v", response, err)
	}
	if _, err := provider.Attach(response.TargetID); err != nil {
		t.Fatalf("attach right after create: %v", err)
	}
}

func TestCreateMakesAWorktreeAndStartsAnAgent(t *testing.T) {
	provider, _, world := readyProvider(t)
	agent, prompt := "claude", "fix the flaky test\n\tthen run it"
	response, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{
		GroupID: shopGroup, Name: "brave-fox", Agent: &agent, Prompt: &prompt,
	})
	if err != nil || response.ItemID != "w9" || response.TargetID != "w9:p1.term_9" || !response.AgentStarted || response.Problem != nil {
		t.Fatalf("create = %+v %v", response, err)
	}
	if err := protocol.ValidateProviderCreateResponse(response); err != nil {
		t.Fatal(err)
	}
	if call := world.call("worktree.create"); call["workspace_id"] != "w1" || call["branch"] != "brave-fox" || call["focus"] != false {
		t.Fatalf("worktree.create %v", call)
	}
	if call := world.call("agent.start"); call["kind"] != "claude" || call["name"] != "claude-w9-p1" || call["pane_id"] != "w9:p1" || call["timeout_ms"] != float64(agentReadyMs) {
		t.Fatalf("agent.start %v", call)
	}
	if call := world.call("agent.prompt"); call["target"] != "w9:p1" || call["text"] != prompt {
		t.Fatalf("agent.prompt %v", call)
	}
	// The new terminal is in the cache when create answers: open works at once.
	if _, err := provider.Attach(response.TargetID); err != nil {
		t.Fatalf("attach after create: %v", err)
	}
}

func TestCreateRefusalsAndHerdrErrors(t *testing.T) {
	provider, server, _ := readyProvider(t)
	ctx := context.Background()
	codex, missing := "codex", "gemini"
	for _, test := range []struct {
		request protocol.HTTPProviderCreateRequest
		want    error
	}{
		{protocol.HTTPProviderCreateRequest{GroupID: "gffffffffffff", Name: "a"}, ErrTargetMissing},
		{protocol.HTTPProviderCreateRequest{GroupID: "ws-w2", Name: "a"}, ErrNotGit},
		// gemini is a start kind but runs nowhere in this herdr.
		{protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "a", Agent: &missing}, ErrKind},
	} {
		if _, err := provider.Create(ctx, test.request); !errors.Is(err, test.want) {
			t.Fatalf("%+v = %v", test.request, err)
		}
	}
	for code, want := range map[string]error{
		"not_git_worktree":       ErrNotGit,
		"worktree_create_failed": ErrExists,
		"git_failed":             ErrWorktree,
	} {
		server.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
			message := "fatal: '/home/demo/.herdr/worktrees/shop/a' already exists"
			if code == "git_failed" {
				message = "fatal: /home/demo/private"
			}
			return nil, &herdrtest.Error{Code: code, Message: message}
		})
		_, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "a"})
		if !errors.Is(err, want) || strings.Contains(err.Error(), "/home/demo") {
			t.Fatalf("%s = %v", code, err)
		}
	}
	// One create at a time.
	release := make(chan struct{})
	server.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		<-release
		return nil, &herdrtest.Error{Code: "git_failed"}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "slow", Agent: &codex})
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "b"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second create = %v", err)
	}
	close(release)
	<-done
}

func TestCreateAgentProblems(t *testing.T) {
	provider, server, _ := readyProvider(t)
	ctx := context.Background()
	agent, prompt := "claude", "go"
	server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "agent_start_failed", Message: "no claude on PATH"}
	})
	response, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent})
	if err != nil || response.AgentStarted || response.Problem == nil || *response.Problem != protocol.AgentFailed || response.ItemID != "w9" {
		t.Fatalf("agent failed = %+v %v", response, err)
	}
	server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) { return map[string]any{}, nil })
	server.Handle("agent.prompt", func(json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "agent_not_found"}
	})
	response, err = provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent, Prompt: &prompt})
	if err != nil || !response.AgentStarted || response.Problem == nil || *response.Problem != protocol.PromptFailed {
		t.Fatalf("prompt failed = %+v %v", response, err)
	}
}

// After a step times out, create reads the snapshot again and answers with
// what exists.
func TestCreateRecheckAfterTimeout(t *testing.T) {
	saved := createTimeouts
	t.Cleanup(func() { createTimeouts = saved })
	createTimeouts.worktree, createTimeouts.agent = 300*time.Millisecond, 300*time.Millisecond
	provider, server, world := readyProvider(t)
	ctx := context.Background()
	agent := "claude"
	server.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		world.add(false) // herdr made it, but answers too late
		time.Sleep(time.Second)
		return nil, nil
	})
	response, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent})
	if err != nil || response.ItemID != "w9" || response.TargetID != "w9:p1.term_9" || response.AgentStarted ||
		*response.Problem != protocol.AgentFailed {
		t.Fatalf("worktree timeout = %+v %v", response, err)
	}
	server.Snapshot([]byte(world.base))
	server.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		time.Sleep(time.Second) // nothing made
		return nil, nil
	})
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox"}); !errors.Is(err, ErrWorktree) {
		t.Fatalf("timeout without a worktree = %v", err)
	}

	newCreateWorld(t, server)
	server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) {
		world.add(true) // the agent runs, but is not ready in time
		time.Sleep(time.Second)
		return nil, nil
	})
	response, err = provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent})
	if err != nil || response.AgentStarted || *response.Problem != protocol.AgentNotReady {
		t.Fatalf("agent timeout = %+v %v", response, err)
	}
}

func TestAgentNameKeepsThePane(t *testing.T) {
	long := strings.Repeat("k", 32)
	for _, test := range []struct{ kind, pane, want string }{
		{"claude", "w3:p2", "claude-w3-p2"},
		{long, "w1:p1", strings.Repeat("k", 26) + "-w1-p1"},
		{long, "w1:p2", strings.Repeat("k", 26) + "-w1-p2"},
	} {
		got := agentName(test.kind, test.pane)
		if got != test.want || len(got) > maxAgentName || strings.HasSuffix(got, "-") {
			t.Errorf("agentName(%q, %q) = %q", test.kind, test.pane, got)
		}
	}
	// Two panes whose IDs differ only at the end never share a name.
	if a, b := agentName("claude", "workspace-0123456789abcdefghij:pane-1"), agentName("claude", "workspace-0123456789abcdefghij:pane-2"); a == b {
		t.Fatalf("two panes share the name %q", a)
	}
}

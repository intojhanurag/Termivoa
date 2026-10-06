package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	server.Handle("agent.get", agentGet(readyAgent))
	return world
}

// herdr's agent record (agent.get) at three moments of a launch: the command is typed and no program shows yet,
// the program shows and herdr does not take a prompt yet, and ready.
const (
	typedAgent = `{"agent":null,"agent_status":"unknown","launch_pending":true}`
	shownAgent = `{"agent":"claude","agent_status":"unknown","launch_pending":true}`
	readyAgent = `{"agent":"claude","agent_status":"idle","interactive_ready":true}`
)

// agentGet answers agent.get with each record in turn; the last one stays.
func agentGet(records ...string) herdrtest.Handler {
	var mu sync.Mutex
	return func(json.RawMessage) (any, *herdrtest.Error) {
		mu.Lock()
		defer mu.Unlock()
		record := records[0]
		if len(records) > 1 {
			records = records[1:]
		}
		return map[string]any{"type": "agent_info", "agent": json.RawMessage(record)}, nil
	}
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

// herdr answers agent.start at once with launch_pending, and refuses a prompt
// until the agent is ready (issue 192). It also says "started" when the
// agent's program is missing (issue 193). startAgent waits on herdr's own
// record of the agent.
func TestStartAgentWaitsForHerdrsRecord(t *testing.T) {
	saved := createTimeouts
	t.Cleanup(func() { createTimeouts = saved })
	createTimeouts.look = 10 * time.Millisecond
	// A case that ends by itself has a step limit it never meets, also on a loaded computer; one that
	// ends at the step limit has a short one.
	const never, short = time.Minute, time.Second
	prompt := "go"
	blocked := `{"agent":"claude","agent_status":"blocked","launch_pending":true}`
	refuses := func(code string) herdrtest.Handler {
		return func(json.RawMessage) (any, *herdrtest.Error) {
			return nil, &herdrtest.Error{Code: code, Message: "agent target w9:p1"}
		}
	}
	for _, test := range []struct {
		name    string
		limit   time.Duration
		get     herdrtest.Handler
		prompt  *string
		started bool
		problem protocol.HTTPProviderCreateProblem // "": none
		sent    bool                               // the prompt reached herdr
	}{
		{"the prompt waits until herdr takes it", never, agentGet(typedAgent, shownAgent, shownAgent, readyAgent), &prompt, true, "", true},
		{"herdr's launch limit ended for a program that shows: it takes the prompt", never, agentGet(shownAgent, `{"agent":"claude","agent_status":"unknown"}`), &prompt, true, "", true},
		{"without a prompt, a program that shows is enough", never, agentGet(typedAgent, shownAgent), nil, true, "", false},
		// A slow start (a heavy shell, a loaded computer): many looks pass before the program shows.
		{"the program shows late", never, agentGet(append(slices.Repeat([]string{typedAgent}, 60), readyAgent)...), &prompt, true, "", true},
		{"the program never shows", short, agentGet(typedAgent), &prompt, false, protocol.AgentFailed, false},
		{"the program never shows, no prompt", short, agentGet(typedAgent), nil, false, protocol.AgentFailed, false},
		{"herdr dropped the launch", never, refuses("agent_not_found"), &prompt, false, protocol.AgentFailed, false},
		{"the agent waits at a question", never, agentGet(shownAgent, blocked), &prompt, false, protocol.AgentNotReady, false},
		// No prompt was to be sent: an agent that is there is started, also at a question.
		{"the agent waits at a question, no prompt", never, agentGet(typedAgent, blocked), nil, true, "", false},
		{"the agent is not ready in time", short, agentGet(shownAgent), &prompt, true, protocol.PromptFailed, false},
		// A herdr whose agent.get answers another error cannot say: started, and the prompt is tried once.
		{"herdr has no agent.get", never, refuses("invalid_request"), nil, true, "", false},
		{"herdr has no agent.get: the prompt is tried once", never, refuses("invalid_request"), &prompt, true, protocol.PromptFailed, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, server, world := readyProvider(t)
			var mu sync.Mutex
			pending := true
			server.Handle("agent.start", func(json.RawMessage) (any, *herdrtest.Error) {
				world.add(true)
				return map[string]any{"type": "agent_started", "agent": json.RawMessage(typedAgent)}, nil
			})
			server.Handle("agent.get", func(params json.RawMessage) (any, *herdrtest.Error) {
				result, failure := test.get(params)
				if failure == nil {
					mu.Lock()
					pending = strings.Contains(string(result.(map[string]any)["agent"].(json.RawMessage)), "launch_pending")
					mu.Unlock()
				}
				return result, failure
			})
			sent := false
			server.Handle("agent.prompt", func(json.RawMessage) (any, *herdrtest.Error) {
				mu.Lock()
				defer mu.Unlock()
				if pending {
					return nil, &herdrtest.Error{Code: "agent_not_ready", Message: "agent w9:p1 is not an active named agent"}
				}
				sent = true
				return map[string]any{"type": "agent_prompted"}, nil
			})
			agent := "claude"
			createTimeouts.agent = test.limit
			began := time.Now()
			response, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent, Prompt: test.prompt})
			if err != nil || response.AgentStarted != test.started || (response.Problem == nil) != (test.problem == "") ||
				response.Problem != nil && *response.Problem != test.problem {
				t.Fatalf("create = %+v %v", response, err)
			}
			if err := protocol.ValidateProviderCreateResponse(response); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if sent != test.sent {
				t.Fatalf("prompt sent = %t", sent)
			}
			// Inside the step limit, and the 3 s wait for the list after it.
			if took := time.Since(began); took > min(test.limit, 10*time.Second)+createTimeouts.refresh+time.Second {
				t.Fatalf("took %v", took)
			}
		})
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

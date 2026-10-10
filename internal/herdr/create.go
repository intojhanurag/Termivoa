package herdr

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Create errors. Their text is Termivoa's; herdr's and git's text is used to
// classify only, never returned.
var (
	ErrBusy     = errors.New("herdr: another create is running")
	ErrKind     = errors.New("herdr: agent kind is not offered")
	ErrNotGit   = errors.New("herdr: group is not in git")
	ErrExists   = errors.New("herdr: worktree or branch exists")
	ErrWorktree = errors.New("herdr: worktree create failed")
)

// createTimeouts are design S6's step limits. agent is agent.start and the
// wait for the agent after it; look is the time between two looks at a
// starting agent. A test seam.
var createTimeouts = struct{ worktree, agent, look, prompt, refresh time.Duration }{
	worktree: 15 * time.Second, agent: 20 * time.Second, look: 200 * time.Millisecond, prompt: 5 * time.Second, refresh: 3 * time.Second,
}

// agentReadyMs is herdr's own limit for a launch: after it herdr drops a
// launch whose program never showed (agent.get then answers agent_not_found),
// and takes prompts for one that did. It ends before the 20 s step limit, so
// it is also how long a program may take to show. maxAgentName is herdr's
// limit for an agent name.
const (
	agentReadyMs = 15000
	maxAgentName = 32
)

// Create is milestone M2: a worktree in a group's repository
// (worktree.create from its main checkout), then optionally an agent
// (agent.start) and a first prompt (agent.prompt). One runs at a time. ctx
// must be broker-owned, never a request's: a phone that leaves does not cut
// a create short. After a step times out, Create reads the snapshot again
// and answers with what exists.
func (provider *Provider) Create(ctx context.Context, request protocol.HTTPProviderCreateRequest) (protocol.HTTPProviderCreateResponse, error) {
	var response protocol.HTTPProviderCreateResponse
	if !provider.create.TryLock() {
		return response, refused(ErrBusy)
	}
	defer provider.create.Unlock()
	provider.mu.RLock()
	current := provider.view
	provider.mu.RUnlock()
	if current.state != protocol.HTTPProviderStateReady {
		return response, refused(ErrUnavailable)
	}
	group, ok := current.groups[request.GroupID]
	switch {
	case !ok:
		return response, refused(ErrTargetMissing)
	case !group.git:
		return response, refused(ErrNotGit)
	case request.Agent != nil && !slices.Contains(current.kinds, *request.Agent):
		return response, refused(ErrKind)
	}

	var created struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			PaneID     string `json:"pane_id"`
			TerminalID string `json:"terminal_id"`
		} `json:"root_pane"`
	}
	// The items before the call: after a timeout only a new one can be the worktree this call made.
	before := map[string]bool{}
	for _, existing := range current.response.Groups {
		for _, item := range existing.Items {
			before[item.ID] = true
		}
	}
	err := provider.call(ctx, createTimeouts.worktree, "worktree.create", map[string]any{
		"workspace_id": group.workspace, "branch": request.Name, "focus": false,
	}, &created)
	var failure *callError
	switch {
	case timedOut(err):
		item, found := provider.findItem(ctx, request.Name, before)
		if !found {
			return response, ErrWorktree
		}
		response.ItemID, response.TargetID = item.ID, firstTarget(item)
		if request.Agent != nil {
			response.Problem = problem(protocol.AgentFailed)
		}
		if err := validCreated(response); err != nil {
			return response, err
		}
		provider.settle(ctx, createTimeouts.refresh, response.TargetID)
		return response, nil
	case errors.As(err, &failure) && failure.code == "not_git_worktree":
		return response, ErrNotGit
	case errors.As(err, &failure) && strings.Contains(failure.message, "already exists"):
		return response, ErrExists
	case err != nil:
		return response, ErrWorktree
	}
	response.ItemID = created.Workspace.ID
	response.TargetID = created.RootPane.PaneID + "." + created.RootPane.TerminalID
	if err := validCreated(response); err != nil {
		return response, err
	}
	// Answer once a snapshot read after the last step has the new terminal.
	defer provider.settle(ctx, createTimeouts.refresh, response.TargetID)
	if request.Agent == nil {
		return response, nil
	}

	response.AgentStarted, response.Problem = provider.startAgent(ctx, created.RootPane.PaneID, response.TargetID, *request.Agent, request.Prompt)
	return response, nil
}

var notAgentName = regexp.MustCompile(`[^a-z0-9]+`)

// agentName is the herdr name of an agent: its kind and its pane, at most
// maxAgentName characters. The pane part tells two agents of one kind apart,
// so a cut takes from the kind and keeps the pane's end.
func agentName(kind, pane string) string {
	tail := strings.Trim(notAgentName.ReplaceAllString(strings.ToLower(pane), "-"), "-")
	tail = tail[max(0, len(tail)-(maxAgentName-2)):]
	return kind[:min(len(kind), maxAgentName-1-len(tail))] + "-" + tail
}

// startAgent starts an agent of kind in pane (agent.start) and then sends
// prompt, if any (agent.prompt, 5 s): the part that Create and Manage's newTab
// share. targetID is the pane's terminal. A nil problem: all worked.
// herdr refuses a name that another agent has (agent_name_taken), so the
// name is the kind and the pane: "claude-w3-p2".
//
// herdr (0.8.2 and 0.9.3) answers agent.start at once, with launch_pending:
// it has typed the kind's command into the pane's shell, no more. While the
// launch is pending it refuses a prompt (agent_not_ready), and it says
// "started" also when the program is missing. So startAgent then asks herdr
// (agent.get) until the agent's program shows in the pane, and before a
// prompt until herdr takes input for it; agent.start and this wait share the
// 20 s step limit. A launch that herdr dropped (its own limit, agentReadyMs:
// the program never showed, or ended), or a program that does not show within
// the step limit, is agentFailed. With a prompt, an agent at a question is
// agentNotReady, and one that is not ready for the prompt in time is
// promptFailed. Without a prompt, a program that shows is "started". A herdr
// that refuses agent.get with another code cannot say: the agent counts as
// started, and the prompt is tried once.
func (provider *Provider) startAgent(ctx context.Context, pane, targetID, kind string, prompt *string) (started bool, _ *protocol.HTTPProviderCreateProblem) {
	deadline := time.Now().Add(createTimeouts.agent)
	err := provider.call(ctx, createTimeouts.agent, "agent.start", map[string]any{
		"name": agentName(kind, pane), "kind": kind, "pane_id": pane, "timeout_ms": agentReadyMs,
	}, &struct{}{})
	var failure *callError
	if err != nil {
		if timedOut(err) && provider.hasAgent(ctx, targetID) ||
			errors.As(err, &failure) && (strings.Contains(failure.code, "timeout") || strings.Contains(failure.code, "not_ready")) {
			return false, problem(protocol.AgentNotReady)
		}
		return false, problem(protocol.AgentFailed)
	}
	// The agent is there: send the prompt, if any.
	send := func() (bool, *protocol.HTTPProviderCreateProblem) {
		if prompt == nil {
			return true, nil
		}
		if err := provider.call(ctx, createTimeouts.prompt, "agent.prompt", map[string]any{"target": pane, "text": *prompt}, &struct{}{}); err != nil {
			return true, problem(protocol.PromptFailed)
		}
		return true, nil
	}
	for seen := false; ; time.Sleep(createTimeouts.look) {
		var got struct {
			Agent struct {
				Agent   string `json:"agent"` // the program herdr sees in the pane; empty until it shows
				Status  string `json:"agent_status"`
				Pending bool   `json:"launch_pending"`
			} `json:"agent"`
		}
		err := provider.call(ctx, min(callTimeout, time.Until(deadline)), "agent.get", map[string]any{"target": pane}, &got)
		switch {
		case errors.As(err, &failure) && (failure.code == "agent_not_found" || failure.code == "agent_not_running"):
			return false, problem(protocol.AgentFailed) // herdr dropped the launch: the program ended, or never showed
		case errors.As(err, &failure):
			return send() // this herdr has no such agent.get: as before the wait existed
		case err == nil && got.Agent.Agent != "" && prompt == nil:
			return true, nil // also one that waits at a question: no prompt was to be sent
		case err == nil && got.Agent.Agent != "" && got.Agent.Status == stateBlocked:
			return false, problem(protocol.AgentNotReady)
		case err == nil && got.Agent.Agent != "":
			seen = true
			if !got.Agent.Pending {
				return send()
			}
		}
		if time.Until(deadline) <= createTimeouts.look || ctx.Err() != nil {
			if !seen {
				return false, problem(protocol.AgentFailed)
			}
			return true, problem(protocol.PromptFailed)
		}
	}
}

func problem(value protocol.HTTPProviderCreateProblem) *protocol.HTTPProviderCreateProblem {
	return &value
}

func validCreated(response protocol.HTTPProviderCreateResponse) error {
	if !handlePattern.MatchString(response.ItemID) || !handlePattern.MatchString(response.TargetID) {
		return ErrWorktree
	}
	return nil
}

func firstTarget(item protocol.HTTPProviderItem) string {
	if item.OpenTarget != nil {
		return *item.OpenTarget
	}
	if len(item.Targets) > 0 {
		return item.Targets[0].ID
	}
	return ""
}

// readMapped reads and maps one snapshot outside the poll loop; it changes
// no loop state.
func (provider *Provider) readMapped(ctx context.Context) (mapped, bool) {
	snapshot, err := provider.readSnapshot(ctx)
	if err != nil {
		return mapped{}, false
	}
	provider.mu.RLock()
	attachAny := provider.view.caps.attachAny
	provider.mu.RUnlock()
	return mapSnapshot(snapshot, attachAny, provider.now(), nil, nil, nil), true
}

// findItem looks for the item a timed-out worktree.create made: herdr names
// the new workspace after the branch. It looks in every group: the first
// worktree of a repository moves its group from ws-<id> to g<hash>.
func (provider *Provider) findItem(ctx context.Context, name string, before map[string]bool) (protocol.HTTPProviderItem, bool) {
	result, ok := provider.readMapped(ctx)
	if !ok {
		return protocol.HTTPProviderItem{}, false
	}
	for _, group := range result.response.Groups {
		for _, item := range group.Items {
			if item.Name == name && !before[item.ID] {
				return item, true
			}
		}
	}
	return protocol.HTTPProviderItem{}, false
}

func (provider *Provider) hasAgent(ctx context.Context, targetID string) bool {
	result, ok := provider.readMapped(ctx)
	return ok && result.targets[targetID].agent
}

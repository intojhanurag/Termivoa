package herdr

import (
	"context"
	"errors"
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

// createTimeouts are design S6's step limits. A test seam.
var createTimeouts = struct{ worktree, agent, prompt, refresh time.Duration }{
	worktree: 15 * time.Second, agent: 20 * time.Second, prompt: 5 * time.Second, refresh: 3 * time.Second,
}

// agentReadyMs is herdr's own readiness wait for agent.start; it answers
// before the 20 s step limit.
const agentReadyMs = 15000

// Create is milestone M2: a worktree in a group's repository
// (worktree.create from its main checkout), then optionally an agent
// (agent.start) and a first prompt (agent.prompt). One runs at a time. ctx
// must be broker-owned, never a request's: a phone that leaves does not cut
// a create short. After a step times out, Create reads the snapshot again
// and answers with what exists.
func (provider *Provider) Create(ctx context.Context, request protocol.HTTPProviderCreateRequest) (protocol.HTTPProviderCreateResponse, error) {
	var response protocol.HTTPProviderCreateResponse
	if !provider.create.TryLock() {
		return response, ErrBusy
	}
	defer provider.create.Unlock()
	provider.mu.RLock()
	current := provider.view
	provider.mu.RUnlock()
	if current.state != protocol.HTTPProviderStateReady {
		return response, ErrUnavailable
	}
	group, ok := current.groups[request.GroupID]
	switch {
	case !ok:
		return response, ErrTargetMissing
	case !group.git:
		return response, ErrNotGit
	case request.Agent != nil && !slices.Contains(current.kinds, *request.Agent):
		return response, ErrKind
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
	// The group's items before the call: after a timeout only a new one can be the worktree this call made.
	before := map[string]bool{}
	provider.mu.RLock()
	for _, existing := range provider.view.response.Groups {
		if existing.ID == request.GroupID {
			for _, item := range existing.Items {
				before[item.ID] = true
			}
		}
	}
	provider.mu.RUnlock()
	err := provider.call(ctx, createTimeouts.worktree, "worktree.create", map[string]any{
		"workspace_id": group.workspace, "branch": request.Name, "focus": false,
	}, &created)
	var failure *callError
	switch {
	case timedOut(err):
		item, found := provider.findItem(ctx, request.GroupID, request.Name, before)
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

	err = provider.call(ctx, createTimeouts.agent, "agent.start", map[string]any{
		"name": *request.Agent, "kind": *request.Agent, "pane_id": created.RootPane.PaneID, "timeout_ms": agentReadyMs,
	}, &struct{}{})
	if err != nil {
		response.Problem = problem(protocol.AgentFailed)
		if timedOut(err) && provider.hasAgent(ctx, response.TargetID) ||
			errors.As(err, &failure) && (strings.Contains(failure.code, "timeout") || strings.Contains(failure.code, "not_ready")) {
			response.Problem = problem(protocol.AgentNotReady)
		}
		return response, nil
	}
	response.AgentStarted = true
	if request.Prompt != nil {
		if err := provider.call(ctx, createTimeouts.prompt, "agent.prompt", map[string]any{
			"target": created.RootPane.PaneID, "text": *request.Prompt,
		}, &struct{}{}); err != nil {
			response.Problem = problem(protocol.PromptFailed)
		}
	}
	return response, nil
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
	return mapSnapshot(snapshot, attachAny, provider.now(), nil), true
}

// findItem looks for the item a timed-out worktree.create made: herdr names
// the new workspace after the branch.
func (provider *Provider) findItem(ctx context.Context, groupID, name string, before map[string]bool) (protocol.HTTPProviderItem, bool) {
	result, ok := provider.readMapped(ctx)
	if !ok {
		return protocol.HTTPProviderItem{}, false
	}
	for _, group := range result.response.Groups {
		if group.ID != groupID {
			continue
		}
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

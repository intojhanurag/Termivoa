package herdr

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	maxClosed     = 16               // names in one group's closed list
	closedPerTurn = 4                // worktree.list calls in one poll turn
	closedEvery   = 10 * time.Second // between two worktree.list calls for one group
)

// closedTrees is one group's closed worktrees, kept between polls.
type closedTrees struct {
	names []string
	at    time.Time // when herdr was asked; zero: ask at the next turn
}

// listClosed asks herdr for the worktrees of workspace's repository and
// returns the ones that openItem can open (docs/herdr-manage.md, section 14):
// a linked worktree with no workspace, not prunable, bare or detached, whose
// branch passes the rules of a create name; sorted. Only the branch names are
// kept: herdr's paths and text stay in this function.
func (provider *Provider) listClosed(ctx context.Context, timeout time.Duration, workspace string) ([]string, error) {
	var result struct {
		Worktrees []struct {
			Branch   string `json:"branch"`
			Linked   bool   `json:"is_linked_worktree"`
			Prunable bool   `json:"is_prunable"`
			Bare     bool   `json:"is_bare"`
			Detached bool   `json:"is_detached"`
			Open     string `json:"open_workspace_id"`
		} `json:"worktrees"`
	}
	if err := provider.call(ctx, timeout, "worktree.list", map[string]any{"workspace_id": workspace}, &result); err != nil {
		return nil, err
	}
	var names []string
	for _, tree := range result.Worktrees {
		if tree.Linked && tree.Open == "" && !tree.Prunable && !tree.Bare && !tree.Detached && protocol.ValidateProviderBranch(tree.Branch) == nil {
			names = append(names, tree.Branch)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// closedWorktrees is the poll part of design B2: the closed worktrees of each
// git group of the last turn, by group ID, for mapSnapshot. A group is asked
// again at most every 10 s, and at most 4 groups in one turn, all at once
// with the 1 s limit of a poll call: a slow herdr costs a turn one second.
// A list that herdr did not give is empty until the next try. A group that
// left takes its list along. When the set of workspaces changed, every list
// is old (a worktree was opened or closed): all are dropped and asked again.
// Poll loop only.
func (provider *Provider) closedWorktrees(ctx context.Context, snapshot wireSnapshot, now time.Time) map[string][]string {
	var ids []string
	for _, workspace := range records(snapshot.Workspaces, func(record wireWorkspace) string { return record.ID }) {
		ids = append(ids, workspace.ID)
	}
	slices.Sort(ids)
	// ponytail: one change drops every group's list; with more than 4 git groups the other groups'
	// closed rows come back a few turns later. Drop only the changed group's list if that shows.
	if key := strings.Join(ids, " "); key != provider.closedFor {
		provider.closed, provider.closedFor = nil, key
	}
	groups := provider.view.groups
	kept := map[string]closedTrees{}
	var due []string
	for id, group := range groups {
		// Only a group whose workspace is still there: a group of the last turn can be gone now.
		if _, here := slices.BinarySearch(ids, group.workspace); !group.git || !here {
			continue
		}
		kept[id] = provider.closed[id]
		if !now.Before(kept[id].at.Add(closedEvery)) {
			due = append(due, id)
		}
	}
	// The longest wait first.
	slices.SortFunc(due, func(a, b string) int {
		if order := kept[a].at.Compare(kept[b].at); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
	due = due[:min(len(due), closedPerTurn)]
	lists := make([][]string, len(due))
	var wait sync.WaitGroup
	for index, id := range due {
		wait.Go(func() { lists[index], _ = provider.listClosed(ctx, callTimeout, groups[id].workspace) })
	}
	wait.Wait()
	for index, id := range due {
		kept[id] = closedTrees{names: lists[index][:min(len(lists[index]), maxClosed)], at: now}
	}
	provider.closed = kept
	closed := map[string][]string{}
	for id, list := range kept {
		closed[id] = list.names
	}
	return closed
}

package herdr

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// track is the push state of one agent target across polls.
type track struct {
	state        string
	blockedSince time.Time     // first poll of this blocked spell
	blockedSent  bool          // this spell pushed (or the baseline saw it)
	workingAt    time.Time     // the latest poll that saw it working; zero when it is not
	worked       time.Duration // observed work in this turn, without blocked time
}

// apply publishes a mapped snapshot and emits the push transitions of
// section 7: blocked for at least 2 s (by time, not by polls: a create or
// the Integrations switch forces polls 20 ms apart), finished (done or idle) after
// at least 20 s of observed work; the first snapshot after discovery is a
// baseline without pushes.
func (provider *Provider) apply(ctx context.Context, snapshot wireSnapshot, now time.Time) {
	result := mapSnapshot(snapshot, provider.caps.attachAny, now, provider.since,
		provider.gitWorkspaces(snapshot, now), provider.closedWorktrees(ctx, snapshot, now))
	provider.since = result.since
	// A restart of the same herdr version between two polls: every terminal ID is new. A new baseline, no push burst.
	// Panes, not targets: a tab's target moves between its panes without a restart.
	if previous := provider.view.live; len(previous) > 0 && len(result.live) > 0 {
		survived := false
		for key := range result.live {
			if _, survived = previous[key]; survived {
				break
			}
		}
		if !survived {
			provider.baseline = true
		}
	}
	var changes []Change
	tracks := make(map[string]*track, len(result.targets))
	for key, found := range result.targets {
		if !found.agent {
			continue
		}
		current := provider.tracks[key]
		if current == nil {
			current = &track{state: stateNone}
			if provider.baseline {
				current.state = found.state
				if found.state == stateBlocked {
					current.blockedSince, current.blockedSent = now, true
				}
			}
		}
		tracks[key] = current
		from := current.state
		if found.state == stateBlocked {
			if current.blockedSince.IsZero() {
				current.blockedSince = now
			}
			if !current.blockedSent && now.Sub(current.blockedSince) >= blockedFor {
				current.blockedSent = true
				changes = append(changes, Change{Key: key, Group: found.group, Item: found.item, Agent: found.kind, From: from, To: stateBlocked})
			}
		} else {
			current.blockedSince, current.blockedSent = time.Time{}, false
		}
		// Time between two polls counts as work only when the earlier one saw it working.
		if !current.workingAt.IsZero() {
			current.worked += now.Sub(current.workingAt)
		}
		current.workingAt = time.Time{}
		switch found.state {
		case stateWorking:
			current.workingAt = now
		case stateDone, stateIdle:
			if current.worked >= doneAfter {
				changes = append(changes, Change{Key: key, Group: found.group, Item: found.item, Agent: found.kind, From: from, To: stateDone, Worked: current.worked})
			}
			current.worked = 0
		}
		current.state = found.state
	}
	// An empty snapshot keeps the baseline: a restarted herdr can bring its panes back a poll later.
	provider.tracks, provider.baseline = tracks, len(result.live) == 0
	kinds := agentKinds(provider.caps.kinds, result.kinds)
	provider.publish(func(current *view) {
		current.installed, current.binary = true, provider.binary
		current.state, current.caps = result.response.State, provider.caps
		current.response, current.targets, current.groups, current.kinds = result.response, result.targets, result.groups, kinds
		current.live = result.live
	})
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(a.Key, b.Key) })
	for _, change := range changes {
		provider.onChange(change)
	}
}

// agentKinds is design Q2: kinds herdr can start that already run in this
// herdr, most used first (ties by name), at most 16.
func agentKinds(startable []string, running map[string]int) []string {
	var kinds []string
	for _, kind := range startable {
		if running[kind] > 0 {
			kinds = append(kinds, kind)
		}
	}
	slices.SortStableFunc(kinds, func(a, b string) int {
		if order := cmp.Compare(running[b], running[a]); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
	return kinds[:min(len(kinds), maxAgentKinds)]
}

package herdr

import (
	"cmp"
	"slices"
	"time"
)

// track is the push state of one agent target across polls.
type track struct {
	state        string
	blocked      int       // polls in a row it was blocked
	workingSince time.Time // first poll of this turn that saw it working
}

// apply publishes a mapped snapshot and emits the push transitions of
// section 7: blocked after two polls in a row, finished (done or idle) after
// at least 20 s of observed work; the first snapshot after discovery is a
// baseline without pushes.
func (provider *Provider) apply(snapshot wireSnapshot, now time.Time) {
	result := mapSnapshot(snapshot, provider.caps.attachAny, now, provider.since)
	provider.since = result.since
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
					current.blocked = blockedPolls
				}
			}
		}
		tracks[key] = current
		from := current.state
		if found.state == stateBlocked {
			if current.blocked++; current.blocked == blockedPolls {
				changes = append(changes, Change{Key: key, Group: found.group, Item: found.item, Agent: found.kind, From: from, To: stateBlocked})
			}
		} else {
			current.blocked = 0
		}
		switch found.state {
		case stateWorking:
			if current.workingSince.IsZero() {
				current.workingSince = now
			}
		case stateDone, stateIdle:
			if worked := now.Sub(current.workingSince); !current.workingSince.IsZero() && worked >= doneAfter {
				changes = append(changes, Change{Key: key, Group: found.group, Item: found.item, Agent: found.kind, From: from, To: stateDone, Worked: worked})
			}
			current.workingSince = time.Time{}
		}
		current.state = found.state
	}
	provider.tracks, provider.baseline = tracks, false
	kinds := agentKinds(provider.caps.kinds, result.kinds)
	provider.publish(func(current *view) {
		current.installed, current.binary = true, provider.binary
		current.state, current.caps = result.response.State, provider.caps
		current.response, current.targets, current.groups, current.kinds = result.response, result.targets, result.groups, kinds
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

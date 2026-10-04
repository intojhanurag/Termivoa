package herdr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	maxGroups      = 64
	maxItems       = 64
	maxTargets     = 32
	maxBodyBytes   = 256 << 10
	maxNameRunes   = 80
	maxNameBytes   = 240
	maxAgentKinds  = 16
	stateNone      = "none"
	stateUnknown   = "unknown"
	stateBlocked   = "blocked"
	stateDone      = "done"
	stateWorking   = "working"
	stateIdle      = "idle"
	groupHashBytes = 6 // 12 hex digits
)

// rank is the roll-up order, most urgent first.
var rank = map[string]int{stateBlocked: 0, stateDone: 1, stateWorking: 2, stateIdle: 3, stateUnknown: 4, stateNone: 5}

var handlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)

// wireSnapshot is session.snapshot's result.snapshot. Arrays stay raw so one
// bad record is dropped instead of failing the whole snapshot.
type wireSnapshot struct {
	Version    string             `json:"version"`
	Protocol   int                `json:"protocol"`
	Workspaces *[]json.RawMessage `json:"workspaces"`
	Tabs       *[]json.RawMessage `json:"tabs"`
	Panes      *[]json.RawMessage `json:"panes"`
	Layouts    *[]json.RawMessage `json:"layouts"`
	Agents     *[]json.RawMessage `json:"agents"`
}

func (snapshot wireSnapshot) complete() bool {
	return snapshot.Workspaces != nil && snapshot.Tabs != nil && snapshot.Panes != nil &&
		snapshot.Layouts != nil && snapshot.Agents != nil
}

type wireWorkspace struct {
	ID          string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	ActiveTabID string `json:"active_tab_id"`
	Worktree    *struct {
		RepoKey      string `json:"repo_key"`
		RepoName     string `json:"repo_name"`
		CheckoutPath string `json:"checkout_path"`
		Linked       bool   `json:"is_linked_worktree"`
	} `json:"worktree"`
}

type wireTab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

type wirePane struct {
	ID         string `json:"pane_id"`
	TabID      string `json:"tab_id"`
	TerminalID string `json:"terminal_id"`
}

type wireLayout struct {
	TabID       string `json:"tab_id"`
	FocusedPane string `json:"focused_pane_id"`
}

type wireAgent struct {
	PaneID     string `json:"pane_id"`
	TerminalID string `json:"terminal_id"`
	TabID      string `json:"tab_id"`
	Agent      string `json:"agent"`
	Status     string `json:"agent_status"`
}

// records decodes each raw record, dropping any that does not decode or has
// no valid ID.
func records[T any](raw *[]json.RawMessage, id func(T) string) []T {
	var kept []T
	for _, message := range *raw {
		var record T
		if json.Unmarshal(message, &record) == nil && handlePattern.MatchString(id(record)) {
			kept = append(kept, record)
		}
	}
	return kept
}

// target is the host-only view of one wire target: what Attach and pushes need.
type target struct {
	pane, terminal string
	agent          bool
	kind           string
	state          string
	canOpen        bool
	group, item    string // cleaned names
	directory      string // checkout path (host only)
}

// groupRef is the host-only view of one group: what Create needs.
type groupRef struct {
	git       bool
	workspace string // the workspace worktree.create runs in
	name      string
}

type stateStamp struct {
	state string
	since time.Time
}

type mapped struct {
	response protocol.HTTPProviderSnapshotResponse
	targets  map[string]target
	groups   map[string]groupRef
	kinds    map[string]int
	since    map[string]stateStamp
	// live is every pane ID + "." + terminal ID herdr has, also panes that
	// are no target: an open provider session lives as long as its pane.
	live map[string]struct{}
}

// mapSnapshot is section 5 of the design: groups by repository, items by
// workspace, a target per agent and per tab without one, roll-ups, caps and
// the body budget. since carries each item's first-seen state across polls.
func mapSnapshot(snapshot wireSnapshot, attachAny bool, now time.Time, since map[string]stateStamp) mapped {
	workspaces := records(snapshot.Workspaces, func(record wireWorkspace) string { return record.ID })
	tabs := records(snapshot.Tabs, func(record wireTab) string { return record.ID })
	panes := records(snapshot.Panes, func(record wirePane) string { return record.ID })
	layouts := records(snapshot.Layouts, func(record wireLayout) string { return record.TabID })
	agents := records(snapshot.Agents, func(record wireAgent) string { return record.PaneID })

	tabsOf := map[string][]wireTab{}
	for _, tab := range tabs {
		tabsOf[tab.WorkspaceID] = append(tabsOf[tab.WorkspaceID], tab)
	}
	panesOf, terminals, live := map[string][]wirePane{}, map[string]string{}, map[string]struct{}{}
	for _, pane := range panes {
		panesOf[pane.TabID] = append(panesOf[pane.TabID], pane)
		terminals[pane.ID] = pane.TerminalID
		live[pane.ID+"."+pane.TerminalID] = struct{}{}
	}
	focused := map[string]string{}
	for _, layout := range layouts {
		focused[layout.TabID] = layout.FocusedPane
	}
	agentsOf := map[string][]wireAgent{}
	for _, agent := range agents {
		if agent.TerminalID == "" {
			agent.TerminalID = terminals[agent.PaneID]
		}
		agentsOf[agent.TabID] = append(agentsOf[agent.TabID], agent)
	}
	slices.SortStableFunc(workspaces, func(a, b wireWorkspace) int { return a.Number - b.Number })

	result := mapped{
		targets: map[string]target{}, groups: map[string]groupRef{}, kinds: map[string]int{},
		since: map[string]stateStamp{}, live: live,
	}
	truncated := false
	type groupBuild struct {
		group   protocol.HTTPProviderGroup
		ref     groupRef
		linked  []int // per item: 0 main checkout, else the workspace number
		targets []string
	}
	var order []string
	builds := map[string]*groupBuild{}
	for _, workspace := range workspaces {
		groupID, groupName, git, linked, directory := "ws-"+workspace.ID, workspace.Label, false, workspace.Number, ""
		if tree := workspace.Worktree; tree != nil && tree.RepoKey != "" {
			sum := sha256.Sum256([]byte(tree.RepoKey))
			groupID, groupName, git, directory = "g"+hex.EncodeToString(sum[:groupHashBytes]), tree.RepoName, true, tree.CheckoutPath
			if !tree.Linked {
				linked = 0
			}
		}
		if !handlePattern.MatchString(groupID) {
			continue
		}
		build := builds[groupID]
		if build == nil {
			build = &groupBuild{
				group: protocol.HTTPProviderGroup{ID: groupID, Name: Clean(groupName, maxNameRunes, "workspace "+strconv.Itoa(workspace.Number)), CanCreate: git},
				ref:   groupRef{git: git, workspace: workspace.ID},
			}
			build.ref.name = build.group.Name
			builds[groupID] = build
			order = append(order, groupID)
		}
		if len(build.group.Items) == maxItems {
			truncated = true
			continue
		}
		item := protocol.HTTPProviderItem{
			ID: workspace.ID, Name: Clean(workspace.Label, maxNameRunes, "workspace "+strconv.Itoa(workspace.Number)),
			Targets: []protocol.HTTPProviderTarget{},
		}
		var keys []string
		workspaceTabs := tabsOf[workspace.ID]
		slices.SortStableFunc(workspaceTabs, func(a, b wireTab) int { return a.Number - b.Number })
		activeTarget := ""
		for _, tab := range workspaceTabs {
			label := Clean(tab.Label, maxNameRunes, strconv.Itoa(tab.Number))
			var tabTargets []protocol.HTTPProviderTarget
			for _, agent := range agentsOf[tab.ID] {
				wire := protocol.HTTPProviderTarget{
					ID: agent.PaneID + "." + agent.TerminalID, Label: label, State: agentWord(agent.Status), CanOpen: true,
				}
				// A program in a pane can report any text as its agent name: only a kind word leaves the host (S4, S5).
				kind := ""
				if kindPattern.MatchString(agent.Agent) {
					kind = agent.Agent
					wire.Agent = &kind
					result.kinds[kind]++
				}
				if handlePattern.MatchString(wire.ID) && handlePattern.MatchString(agent.TerminalID) {
					tabTargets = append(tabTargets, wire)
					result.targets[wire.ID] = target{
						pane: agent.PaneID, terminal: agent.TerminalID, agent: true, kind: kind, state: wire.State,
						canOpen: true, group: build.group.Name, item: item.Name, directory: directory,
					}
				}
			}
			if len(tabTargets) == 0 {
				pane := focused[tab.ID]
				if _, ok := terminals[pane]; !ok && len(panesOf[tab.ID]) > 0 {
					pane = panesOf[tab.ID][0].ID
				}
				// The terminal ID becomes a CLI argument on its own (herdr's
				// parser takes no "--"), so it must pass the pattern alone: an
				// ID like "--takeover" is dropped.
				if terminal, ok := terminals[pane]; ok && handlePattern.MatchString(terminal) && handlePattern.MatchString(pane+"."+terminal) {
					wire := protocol.HTTPProviderTarget{ID: pane + "." + terminal, Label: label, State: stateNone, CanOpen: attachAny}
					tabTargets = append(tabTargets, wire)
					result.targets[wire.ID] = target{
						pane: pane, terminal: terminal, state: stateNone, canOpen: attachAny,
						group: build.group.Name, item: item.Name, directory: directory,
					}
				}
			}
			for _, wire := range tabTargets {
				if len(item.Targets) == maxTargets {
					truncated = true
					delete(result.targets, wire.ID)
					continue
				}
				item.Targets = append(item.Targets, wire)
				keys = append(keys, wire.ID)
				if tab.ID == workspace.ActiveTabID && activeTarget == "" && wire.CanOpen {
					activeTarget = wire.ID
				}
			}
		}
		item.State = rollItem(item.Targets)
		if open := openTarget(item.Targets, activeTarget); open != "" {
			item.OpenTarget = &open
		}
		stamp, seen := since[item.ID]
		if !seen || stamp.state != item.State {
			stamp = stateStamp{state: item.State, since: now}
		}
		result.since[item.ID] = stamp
		stampText := stamp.since.UTC().Format(time.RFC3339)
		item.StateSince = &stampText
		if git && linked == 0 {
			build.ref.workspace = workspace.ID
		}
		build.group.Items = append(build.group.Items, item)
		build.linked = append(build.linked, linked)
		build.targets = append(build.targets, keys...)
	}

	groups := make([]protocol.HTTPProviderGroup, 0, len(order))
	var targetsOf [][]string
	for _, id := range order {
		build := builds[id]
		// Main checkout first, then linked worktrees by herdr's number.
		indexes := make([]int, len(build.group.Items))
		for index := range indexes {
			indexes[index] = index
		}
		slices.SortStableFunc(indexes, func(a, b int) int { return build.linked[a] - build.linked[b] })
		items := make([]protocol.HTTPProviderItem, len(indexes))
		for position, index := range indexes {
			items[position] = build.group.Items[index]
		}
		build.group.Items = items
		build.group.State = rollGroup(items)
		groups = append(groups, build.group)
		targetsOf = append(targetsOf, build.targets)
		result.groups[id] = build.ref
	}
	// The body budget: drop whole groups, least urgent first (ties: the last).
	// Each group is encoded once: the body is the frame plus each group and
	// its comma (one comma too many, so the sum is never short).
	sizes := make([]int, len(groups))
	total := frameSize()
	for index, group := range groups {
		data, _ := json.Marshal(group)
		sizes[index] = len(data) + 1
		total += sizes[index]
	}
	// The group cap uses the same rule, so a blocked agent in a late group stays.
	if len(groups) > maxGroups || total > maxBodyBytes {
		drops := make([]int, len(groups))
		for index := range drops {
			drops[index] = index
		}
		slices.SortFunc(drops, func(a, b int) int {
			if order := rank[groups[b].State] - rank[groups[a].State]; order != 0 {
				return order
			}
			return b - a
		})
		dropped, left := make([]bool, len(groups)), len(groups)
		for _, drop := range drops {
			if left <= maxGroups && total <= maxBodyBytes {
				break
			}
			for _, key := range targetsOf[drop] {
				delete(result.targets, key)
			}
			delete(result.groups, groups[drop].ID)
			total -= sizes[drop]
			dropped[drop] = true
			left--
		}
		kept := make([]protocol.HTTPProviderGroup, 0, left)
		for index, group := range groups {
			if !dropped[index] {
				kept = append(kept, group)
			}
		}
		groups, truncated = kept, true
	}
	result.response = protocol.HTTPProviderSnapshotResponse{Provider: ID, State: protocol.HTTPProviderStateReady, Groups: groups}
	if truncated {
		result.response.Truncated = &truncated
	}
	return result
}

// frameSize is the encoded response without groups, with "truncated".
func frameSize() int {
	truncated := true
	data, _ := json.Marshal(protocol.HTTPProviderSnapshotResponse{
		Provider: ID, State: protocol.HTTPProviderStateReady, Groups: []protocol.HTTPProviderGroup{}, Truncated: &truncated,
	})
	return len(data)
}

// agentWord maps herdr's agent status to a phone word; anything else
// (a newer herdr) is unknown.
func agentWord(status string) string {
	switch status {
	case stateBlocked, stateDone, stateWorking, stateIdle:
		return status
	}
	return stateUnknown
}

func urgent(first, second string) string {
	if rank[second] < rank[first] {
		return second
	}
	return first
}

func rollItem(targets []protocol.HTTPProviderTarget) string {
	state := stateNone
	for _, wire := range targets {
		state = urgent(state, wire.State)
	}
	return state
}

func rollGroup(items []protocol.HTTPProviderItem) string {
	state := stateNone
	for _, item := range items {
		state = urgent(state, item.State)
	}
	return state
}

// openTarget is the most urgent agent target (ties: the lowest tab), else
// the active tab's target when it can open, else none.
func openTarget(targets []protocol.HTTPProviderTarget, active string) string {
	best := -1
	for index, wire := range targets {
		if wire.State != stateNone && wire.CanOpen && (best < 0 || rank[wire.State] < rank[targets[best].State]) {
			best = index
		}
	}
	if best >= 0 {
		return targets[best].ID
	}
	return active
}

// Clean removes Unicode Cc and Cf characters (controls, bidi controls,
// invisible characters), collapses spaces and keeps at most maxRunes runes
// and maxNameBytes bytes. An empty result becomes fallback.
func Clean(value string, maxRunes int, fallback string) string {
	return clean(value, maxRunes, maxNameBytes, fallback)
}

func clean(value string, maxRunes, maxBytes int, fallback string) string {
	var builder strings.Builder
	runes := 0
	space := false
	for _, character := range value {
		switch {
		case unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character) || character == utf8.RuneError:
			continue
		case unicode.IsSpace(character):
			space = builder.Len() > 0
			continue
		}
		if space {
			if runes+1 >= maxRunes || builder.Len()+1 >= maxBytes {
				break
			}
			builder.WriteByte(' ')
			runes++
			space = false
		}
		if runes == maxRunes || builder.Len()+utf8.RuneLen(character) > maxBytes {
			break
		}
		builder.WriteRune(character)
		runes++
	}
	// A space is written before the rune after it; when that rune does not fit, the space must go too.
	result := strings.TrimRight(builder.String(), " ")
	if result == "" {
		return fallback
	}
	return result
}

// titleNameBytes is the most bytes one name may have in a Title, so the
// whole title fits the protocol's name limit (maxNameBytes) even when both
// names are made of 4-byte characters.
const titleNameBytes = (maxNameBytes - len(Name+" ·  · ")) / 2

// Title is the fixed "herdr · group · item" format, one name when they are
// equal (a workspace outside git), each name at most maxRunes runes and
// titleNameBytes bytes.
func Title(group, item string, maxRunes int) string {
	group, item = clean(group, maxRunes, titleNameBytes, Name), clean(item, maxRunes, titleNameBytes, Name)
	if group == item {
		return Name + " · " + group
	}
	return Name + " · " + group + " · " + item
}

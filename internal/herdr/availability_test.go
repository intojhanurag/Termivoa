//go:build !windows

package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// These commands fail if executed: discovery must inspect files, never run agents.
func installedCommand(t *testing.T, folder, name string) string {
	t.Helper()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf executed > \"$0.ran\"\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func withoutAgents(t *testing.T, data []byte) []byte {
	t.Helper()
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	return bytes.Replace(data, snapshot["agents"], []byte("[\n]"), 1)
}

func TestInstalledKindsBeforeFirstAgentAndAfterLastStops(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	bin := filepath.Join(provider.config.Home, "agents")
	installedCommand(t, bin, "claude")
	installedCommand(t, bin, "codex")
	provider.config.PathEnv = bin
	fixture := readFixture(t, "snapshot-0.8.2.json")
	for _, snapshot := range [][]byte{withoutAgents(t, fixture), fixture, withoutAgents(t, fixture)} {
		server.Snapshot(snapshot)
		provider.step(context.Background())
		info, _ := provider.Info()
		if !reflect.DeepEqual(info.AgentKinds, []string{"claude", "codex"}) {
			t.Fatalf("installed kinds = %v, want [claude codex] even with no running agents", info.AgentKinds)
		}
		if err := protocol.ValidateProviderInfo(info); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{"claude", "codex"} {
		if _, err := os.Stat(filepath.Join(bin, command+".ran")); !os.IsNotExist(err) {
			t.Fatalf("discovery executed %s: %v", command, err)
		}
	}
}

func TestCreateStartsFirstInstalledAgent(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	bin := filepath.Join(provider.config.Home, "agents")
	installedCommand(t, bin, "claude")
	provider.config.PathEnv = bin
	world := newCreateWorld(t, server)
	world.base = string(withoutAgents(t, []byte(world.base)))
	server.Snapshot([]byte(world.base))
	runProvider(t, provider)
	waitState(t, provider, protocol.HTTPProviderStateReady)
	agent := "claude"
	response, err := provider.Create(context.Background(), protocol.HTTPProviderCreateRequest{GroupID: shopGroup, Name: "brave-fox", Agent: &agent})
	if err != nil || !response.AgentStarted || response.Problem != nil {
		t.Fatalf("first agent create = %+v %v", response, err)
	}
	if call := world.call("agent.start"); call["kind"] != agent {
		t.Fatalf("agent.start = %v", call)
	}
}

func TestInstalledKindsCanonicalCommandsAndBounds(t *testing.T) {
	bin := filepath.Join(tempRoot(t), "agents")
	installedCommand(t, bin, "claude")
	installedCommand(t, bin, "cursor-agent")
	installedCommand(t, bin, "kiro-cli")
	installedCommand(t, bin, "future") // unknown canonical command: running evidence only
	installedCommand(t, bin, "amp")    // installed but not advertised by this herdr
	codex := installedCommand(t, bin, "codex")
	if err := os.Chmod(codex, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(bin, "gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(bin, "missing"), filepath.Join(bin, "pi")); err != nil {
		t.Fatal(err)
	}
	startable := []string{"claude", "cursor", "kiro", "codex", "gemini", "pi", "future"}
	want := map[string]bool{"claude": true, "cursor": true, "kiro": true}
	if got := installedKinds(startable, bin); !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical commands = %v", got)
	}
	if got := installedKinds(startable, strings.Repeat("relative"+string(os.PathListSeparator), maxAgentPathEntries)+bin); len(got) != 0 {
		t.Fatalf("searched beyond PATH entry limit: %v", got)
	}
	if got := installedKinds(startable, ""); len(got) != 0 {
		t.Fatalf("empty PATH searched current directory: %v", got)
	}
	for _, command := range []string{"claude", "cursor-agent", "kiro-cli", "future", "amp"} {
		if _, err := os.Stat(filepath.Join(bin, command+".ran")); !os.IsNotExist(err) {
			t.Fatalf("discovery executed %s: %v", command, err)
		}
	}
	// Running aliases/custom launchers remain usable, but never override the help allowlist.
	if got := agentKinds(startable, map[string]int{"future": 2, "codex": 1, "amp": 9}, want); !reflect.DeepEqual(got, []string{"future", "codex", "claude", "cursor", "kiro"}) {
		t.Fatalf("offered kinds = %v", got)
	}
	var all []string
	for kind := range agentExecutables {
		all = append(all, kind)
	}
	available := make(map[string]bool)
	for _, kind := range all {
		available[kind] = true
	}
	slices.Sort(all)
	if got := agentKinds(all, nil, available); !reflect.DeepEqual(got, all[:maxAgentKinds]) {
		t.Fatalf("wire bound/order = %v", got)
	}
	help := make([]string, 2*maxStartableKinds)
	// Duplicate-free valid kinds cannot grow the help-derived work beyond 64.
	for i := range help {
		help[i] = "kind" + string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	if got := parseKinds([]byte("[possible values: " + strings.Join(help, ", ") + "]")); len(got) != maxStartableKinds {
		t.Fatalf("help bound = %d", len(got))
	}
}

func TestManageStartsFirstInstalledAgent(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	bin := filepath.Join(provider.config.Home, "agents")
	installedCommand(t, bin, "claude")
	provider.config.PathEnv = bin
	base := withoutAgents(t, readFixture(t, "snapshot-0.8.2.json"))
	addTab := func(agent bool) []byte {
		var snapshot map[string]json.RawMessage
		if err := json.Unmarshal(base, &snapshot); err != nil {
			t.Error(err)
			return nil
		}
		for list, record := range map[string]string{
			"tabs":  `{"tab_id":"w1:t9","workspace_id":"w1","label":"9","number":9}`,
			"panes": `{"pane_id":"w1:p9","tab_id":"w1:t9","terminal_id":"term_9","workspace_id":"w1"}`,
		} {
			var rows []json.RawMessage
			_ = json.Unmarshal(snapshot[list], &rows)
			snapshot[list], _ = json.Marshal(append(rows, json.RawMessage(record)))
		}
		if agent {
			snapshot["agents"] = json.RawMessage(`[{"agent":"claude","agent_status":"idle","pane_id":"w1:p9","tab_id":"w1:t9","terminal_id":"term_9","workspace_id":"w1"}]`)
		}
		data, _ := json.Marshal(snapshot)
		return data
	}
	server.Snapshot(base)
	server.Handle("tab.create", func(json.RawMessage) (any, *herdrtest.Error) {
		server.Snapshot(addTab(false))
		return map[string]any{"root_pane": map[string]any{"pane_id": "w1:p9", "terminal_id": "term_9"}}, nil
	})
	var called atomic.Bool
	server.Handle("agent.start", func(params json.RawMessage) (any, *herdrtest.Error) {
		var values map[string]any
		_ = json.Unmarshal(params, &values)
		called.Store(values["kind"] == "claude" && values["pane_id"] == "w1:p9")
		server.Snapshot(addTab(true))
		return map[string]any{"type": "agent_started"}, nil
	})
	server.Handle("agent.get", agentGet(readyAgent))
	runProvider(t, provider)
	waitState(t, provider, protocol.HTTPProviderStateReady)
	agent := "claude"
	request := manageRequest(protocol.NewTab, "w1", "")
	request.Agent = &agent
	response, err := provider.Manage(context.Background(), request, Project{})
	if err != nil || response.AgentStarted == nil || !*response.AgentStarted || response.AgentProblem != nil || !called.Load() {
		t.Fatalf("first agent in new tab = %+v %v; agent.start called correctly=%v", response, err, called.Load())
	}
}

func TestInstalledKindsRefreshOnTimerAndSettingsRead(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	bin := filepath.Join(provider.config.Home, "agents")
	provider.config.PathEnv = bin
	now := fixedNow
	provider.now = func() time.Time { return now }
	server.Snapshot(withoutAgents(t, readFixture(t, "snapshot-0.8.2.json")))
	provider.step(context.Background())
	if info, _ := provider.Info(); len(info.AgentKinds) != 0 {
		t.Fatalf("absent command offered: %v", info.AgentKinds)
	}
	path := installedCommand(t, bin, "claude")
	provider.step(context.Background())
	if info, _ := provider.Info(); len(info.AgentKinds) != 0 {
		t.Fatal("rescanned within cache interval")
	}
	now = now.Add(availabilityEvery)
	provider.step(context.Background())
	if info, _ := provider.Info(); !reflect.DeepEqual(info.AgentKinds, []string{"claude"}) {
		t.Fatalf("timer did not find installation: %v", info.AgentKinds)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	provider.LookAgain()
	provider.step(context.Background())
	if info, _ := provider.Info(); len(info.AgentKinds) != 0 {
		t.Fatalf("settings refresh kept removed command: %v", info.AgentKinds)
	}
}

// A settings read of a ready herdr rescans agent commands only: it must not
// reset the binary lookup or retry timers (review of #246).
func TestRefreshAgentsKeepsRetryTimers(t *testing.T) {
	provider, server, _ := newTestProvider(t, true)
	bin := filepath.Join(provider.config.Home, "agents")
	provider.config.PathEnv = bin
	now := fixedNow
	provider.now = func() time.Time { return now }
	server.Snapshot(withoutAgents(t, readFixture(t, "snapshot-0.8.2.json")))
	provider.step(context.Background())
	installedCommand(t, bin, "claude")
	lookupAt, retryAt := now.Add(time.Hour), now.Add(2*time.Hour)
	provider.lookupAt, provider.retryAt = lookupAt, retryAt
	provider.RefreshAgents()
	provider.step(context.Background())
	if info, _ := provider.Info(); !reflect.DeepEqual(info.AgentKinds, []string{"claude"}) {
		t.Fatalf("refresh did not rescan agents: %v", info.AgentKinds)
	}
	if !provider.lookupAt.Equal(lookupAt) || !provider.retryAt.Equal(retryAt) || provider.lookNow.Load() {
		t.Fatalf("refresh reset timers: lookup %v retry %v lookNow %v", provider.lookupAt, provider.retryAt, provider.lookNow.Load())
	}
}

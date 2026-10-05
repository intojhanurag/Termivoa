package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateProviderManageRequest(t *testing.T) {
	text := func(value string) *string { return &value }
	item, name, project := text("w3"), text("silver-meadow"), text("81111111-1111-4111-8111-111111111111")
	agent, prompt, group, tree := text("claude"), text("fix the test\n\tthen run it"), text("g7d0f77ccdcfd"), text("fix/old-idea")
	for _, test := range []struct {
		name    string
		request HTTPProviderManageRequest
		valid   bool
	}{
		{"close", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: name}, true},
		{"remove", HTTPProviderManageRequest{Action: RemoveItem, ItemID: item, Name: name}, true},
		{"new tab", HTTPProviderManageRequest{Action: NewTab, ItemID: item}, true},
		{"close tab", HTTPProviderManageRequest{Action: CloseTab, ItemID: item, TargetID: text("w3:p1.term_1")}, true},
		{"close tab without a target", HTTPProviderManageRequest{Action: CloseTab, ItemID: item}, false},
		{"close tab with a bad target", HTTPProviderManageRequest{Action: CloseTab, ItemID: item, TargetID: text("--force")}, false},
		{"new tab with a target", HTTPProviderManageRequest{Action: NewTab, ItemID: item, TargetID: text("w3:p1.term_1")}, false},
		{"new workspace", HTTPProviderManageRequest{Action: NewWorkspace, ProjectID: project}, true},
		{"start", HTTPProviderManageRequest{Action: StartServer}, true},
		{"unknown action", HTTPProviderManageRequest{Action: "forceRemove", ItemID: item, Name: name}, false},
		{"no action", HTTPProviderManageRequest{ItemID: item}, false},
		{"close without a name", HTTPProviderManageRequest{Action: CloseItem, ItemID: item}, false},
		{"remove without an item", HTTPProviderManageRequest{Action: RemoveItem, Name: name}, false},
		{"close with a project", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: name, ProjectID: project}, false},
		{"new tab with a name", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Name: name}, false},
		{"new tab without an item", HTTPProviderManageRequest{Action: NewTab}, false},
		{"new workspace with an item", HTTPProviderManageRequest{Action: NewWorkspace, ProjectID: project, ItemID: item}, false},
		{"new workspace without a project", HTTPProviderManageRequest{Action: NewWorkspace}, false},
		{"start with an item", HTTPProviderManageRequest{Action: StartServer, ItemID: item}, false},
		{"start with a project", HTTPProviderManageRequest{Action: StartServer, ProjectID: project}, false},
		{"item is a flag", HTTPProviderManageRequest{Action: NewTab, ItemID: text("--force")}, false},
		{"item is a path", HTTPProviderManageRequest{Action: NewTab, ItemID: text("../w3")}, false},
		{"project is a path", HTTPProviderManageRequest{Action: NewWorkspace, ProjectID: text("/etc")}, false},
		{"empty name", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: text("")}, false},
		{"escape in the name", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: text("a\x1b[2J")}, false},
		{"long name", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: text(strings.Repeat("a", 81))}, false},
		// Revision 6: an agent in a new tab.
		{"new tab with an agent", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Agent: agent}, true},
		{"new tab with an agent and a prompt", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Agent: agent, Prompt: prompt}, true},
		{"a prompt without an agent", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Prompt: prompt}, false},
		{"an agent that is not a kind word", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Agent: text("claude --yolo")}, false},
		{"an escape in the prompt", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Agent: agent, Prompt: text("a\x1b[2J")}, false},
		{"an empty prompt", HTTPProviderManageRequest{Action: NewTab, ItemID: item, Agent: agent, Prompt: text("")}, false},
		{"close with an agent", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: name, Agent: agent}, false},
		{"close tab with an agent", HTTPProviderManageRequest{Action: CloseTab, ItemID: item, TargetID: text("w3:p1.term_1"), Agent: agent}, false},
		{"new workspace with an agent", HTTPProviderManageRequest{Action: NewWorkspace, ProjectID: project, Agent: agent}, false},
		{"start with a prompt", HTTPProviderManageRequest{Action: StartServer, Prompt: prompt}, false},
		// Revision 6: open a closed worktree.
		{"open", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: tree}, true},
		{"open without a worktree", HTTPProviderManageRequest{Action: OpenItem, GroupID: group}, false},
		{"open without a group", HTTPProviderManageRequest{Action: OpenItem, Worktree: tree}, false},
		{"open with an item", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: tree, ItemID: item}, false},
		{"open with an agent", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: tree, Agent: agent}, false},
		{"open a path", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: text("../shop")}, false},
		{"open a flag", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: text("--force")}, false},
		{"open an upper-case name", HTTPProviderManageRequest{Action: OpenItem, GroupID: group, Worktree: text("Odd_Name")}, false},
		{"open with a bad group", HTTPProviderManageRequest{Action: OpenItem, GroupID: text("../g1"), Worktree: tree}, false},
		{"new tab with a group", HTTPProviderManageRequest{Action: NewTab, ItemID: item, GroupID: group}, false},
		{"close with a worktree", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: name, Worktree: tree}, false},
	} {
		if err := ValidateProviderManageRequest(test.request); (err == nil) != test.valid {
			t.Errorf("%s: %v", test.name, err)
		}
	}
}

func gatewayEnvelope(t *testing.T, kind GatewayRPCMessageType, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"version": Version, "type": kind, "requestId": "81111111-1111-4111-8111-111111111111", "payload": json.RawMessage(body),
	})
	return data
}

// The manage payload and result have their own cases: a manage result is not
// checked as a create result (which needs itemId and targetId).
func TestGatewayProviderManageEnvelopes(t *testing.T) {
	text := func(value string) *string { return &value }
	credential := strings.Repeat("A", 43)
	valid := GatewayRPCProviderManagePayload{Credential: credential, Provider: "herdr", Request: HTTPProviderManageRequest{Action: StartServer}}
	if _, err := DecodeGatewayRPCRequest(gatewayEnvelope(t, GatewayProvidersManage, valid)); err != nil {
		t.Fatalf("manage request: %v", err)
	}
	for name, payload := range map[string]GatewayRPCProviderManagePayload{
		"a field of another action": {Credential: credential, Provider: "herdr", Request: HTTPProviderManageRequest{Action: StartServer, ItemID: text("w3")}},
		"bad provider":              {Credential: credential, Provider: "Herdr", Request: valid.Request},
		"an agent on a close":       {Credential: credential, Provider: "herdr", Request: HTTPProviderManageRequest{Action: CloseItem, ItemID: text("w3"), Name: text("a"), Agent: text("claude")}},
		"a prompt without an agent": {Credential: credential, Provider: "herdr", Request: HTTPProviderManageRequest{Action: NewTab, ItemID: text("w3"), Prompt: text("go")}},
		"no credential":             {Provider: "herdr", Request: valid.Request},
	} {
		if _, err := DecodeGatewayRPCRequest(gatewayEnvelope(t, GatewayProvidersManage, payload)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	problem, wrong := Uncommitted, HTTPProviderManageProblem("forced")
	started, notStarted, failed, odd := true, false, AgentFailed, HTTPProviderCreateProblem("agentAngry")
	for name, test := range map[string]struct {
		response HTTPProviderManageResponse
		valid    bool
	}{
		"empty":       {HTTPProviderManageResponse{}, true},
		"item":        {HTTPProviderManageResponse{ItemID: text("w3")}, true},
		"target":      {HTTPProviderManageResponse{ItemID: text("w3"), TargetID: text("w3:p2.term_1")}, true},
		"uncommitted": {HTTPProviderManageResponse{ItemID: text("w3"), Problem: &problem}, true},
		"bad target":  {HTTPProviderManageResponse{TargetID: text("/etc/passwd")}, false},
		"bad item":    {HTTPProviderManageResponse{ItemID: text("--force")}, false},
		"bad problem": {HTTPProviderManageResponse{Problem: &wrong}, false},
		"agent":       {HTTPProviderManageResponse{ItemID: text("w3"), TargetID: text("w3:p2.term_1"), AgentStarted: &started}, true},
		"no agent":    {HTTPProviderManageResponse{ItemID: text("w3"), AgentStarted: &notStarted, AgentProblem: &failed}, true},
		"bad agent":   {HTTPProviderManageResponse{ItemID: text("w3"), AgentStarted: &notStarted, AgentProblem: &odd}, false},
	} {
		if _, err := DecodeGatewayRPCResponse(gatewayEnvelope(t, GatewayProvidersManageResult, test.response)); (err == nil) != test.valid {
			t.Errorf("manage result %s: %v", name, err)
		}
	}
	if GatewayRPCResults[GatewayProvidersManage] != GatewayProvidersManageResult {
		t.Fatal("gateway.providers.manage has no result type")
	}
}

func TestProviderInfoStatesAndFlags(t *testing.T) {
	yes := true
	info := HTTPProviderInfo{ID: "herdr", Name: "herdr", State: NotInstalled, Enabled: true, CanInstall: &yes,
		Notify: []HTTPProviderNotifyState{}, AgentKinds: []string{}}
	if err := ValidateProviderInfo(info); err != nil {
		t.Fatalf("notInstalled: %v", err)
	}
	if err := ValidateProviderSnapshot(HTTPProviderSnapshotResponse{Provider: "herdr", State: NotInstalled, Groups: []HTTPProviderGroup{}}); err != nil {
		t.Fatalf("notInstalled snapshot: %v", err)
	}
	info.State = NotRunning
	if ValidateProviderInfo(info) == nil {
		t.Fatal("canInstall with notRunning was accepted")
	}
	info.CanInstall, info.CanStart = nil, &yes
	if err := ValidateProviderInfo(info); err != nil {
		t.Fatalf("canStart with notRunning: %v", err)
	}
	info.State = HTTPProviderStateReady
	if ValidateProviderInfo(info) == nil {
		t.Fatal("canStart with ready was accepted")
	}
}

func TestProviderSnapshotClosedWorktrees(t *testing.T) {
	snapshot := func(closed ...string) HTTPProviderSnapshotResponse {
		return HTTPProviderSnapshotResponse{Provider: "herdr", State: HTTPProviderStateReady, Groups: []HTTPProviderGroup{{
			ID: "g7d0f77ccdcfd", Name: "shop", State: "none", CanCreate: true, Items: []HTTPProviderItem{}, Closed: closed,
		}}}
	}
	if err := ValidateProviderSnapshot(snapshot("old-idea", "fix/login")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "Odd_Name", "../shop", "/home/demo/shop", "a b", "--force", "a.lock"} {
		if ValidateProviderSnapshot(snapshot(name)) == nil {
			t.Errorf("closed name %q was accepted", name)
		}
	}
	many := make([]string, MaxProviderClosed+1)
	for index := range many {
		many[index] = "tree-" + strings.Repeat("a", index+1)
	}
	if ValidateProviderSnapshot(snapshot(many...)) == nil {
		t.Error("17 closed names were accepted")
	}
	if err := ValidateProviderSnapshot(snapshot(many[:MaxProviderClosed]...)); err != nil {
		t.Error(err)
	}
}

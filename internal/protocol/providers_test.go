package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateProviderManageRequest(t *testing.T) {
	text := func(value string) *string { return &value }
	item, name, project := text("w3"), text("silver-meadow"), text("81111111-1111-4111-8111-111111111111")
	for _, test := range []struct {
		name    string
		request HTTPProviderManageRequest
		valid   bool
	}{
		{"close", HTTPProviderManageRequest{Action: CloseItem, ItemID: item, Name: name}, true},
		{"remove", HTTPProviderManageRequest{Action: RemoveItem, ItemID: item, Name: name}, true},
		{"new tab", HTTPProviderManageRequest{Action: NewTab, ItemID: item}, true},
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
		"no credential":             {Provider: "herdr", Request: valid.Request},
	} {
		if _, err := DecodeGatewayRPCRequest(gatewayEnvelope(t, GatewayProvidersManage, payload)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	problem, wrong := Uncommitted, HTTPProviderManageProblem("forced")
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

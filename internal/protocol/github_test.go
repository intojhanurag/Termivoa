package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateGitHubRequest(t *testing.T) {
	text := func(value string) *string { return &value }
	for _, test := range []struct {
		name    string
		request HTTPGitHubRequest
		valid   bool
	}{
		{"connect", HTTPGitHubRequest{Action: Connect}, true},
		{"cancel", HTTPGitHubRequest{Action: Cancel}, true},
		{"disconnect", HTTPGitHubRequest{Action: Disconnect}, true},
		{"stop", HTTPGitHubRequest{Action: Stop}, true},
		{"setup", HTTPGitHubRequest{Action: Setup, Repo: text("octo-cat/my.notes_2")}, true},
		{"setup with a long name", HTTPGitHubRequest{Action: Setup, Repo: text(strings.Repeat("a", 39) + "/" + strings.Repeat("b", 100))}, true},
		{"setup without a repo", HTTPGitHubRequest{Action: Setup}, false},
		{"connect with a repo", HTTPGitHubRequest{Action: Connect, Repo: text("octo/notes")}, false},
		{"stop with a repo", HTTPGitHubRequest{Action: Stop, Repo: text("octo/notes")}, false},
		{"unknown action", HTTPGitHubRequest{Action: "clone", Repo: text("octo/notes")}, false},
		{"no action", HTTPGitHubRequest{}, false},
		{"name ends in .git", HTTPGitHubRequest{Action: Setup, Repo: text("octo/notes.git")}, false},
		{"name ends in .GIT", HTTPGitHubRequest{Action: Setup, Repo: text("octo/notes.GIT")}, false},
		{"name starts with a dot", HTTPGitHubRequest{Action: Setup, Repo: text("octo/.ssh")}, false},
		{"name starts with a dash", HTTPGitHubRequest{Action: Setup, Repo: text("octo/-x")}, false},
		{"owner with a dot", HTTPGitHubRequest{Action: Setup, Repo: text("oc.to/notes")}, false},
		{"path", HTTPGitHubRequest{Action: Setup, Repo: text("octo/notes/../x")}, false},
		{"no owner", HTTPGitHubRequest{Action: Setup, Repo: text("notes")}, false},
		{"owner too long", HTTPGitHubRequest{Action: Setup, Repo: text(strings.Repeat("a", 40) + "/b")}, false},
		{"name too long", HTTPGitHubRequest{Action: Setup, Repo: text("a/" + strings.Repeat("b", 101))}, false},
		{"newline", HTTPGitHubRequest{Action: Setup, Repo: text("octo/notes\n")}, false},
	} {
		if err := ValidateGitHubRequest(test.request); (err == nil) != test.valid {
			t.Errorf("%s: err = %v", test.name, err)
		}
	}
}

func TestValidateGitHubResponses(t *testing.T) {
	login, bad := "octo-cat", "-octo"
	percent, over := int64(40), int64(101)
	problem, wrong := NameTaken, HTTPGitHubSetupProblem("lost")
	id := "81111111-1111-4111-8111-111111111111"
	ok := HTTPGitHubResponse{State: HTTPGitHubStateReady, App: "termivoa", Login: &login,
		Connect: &HTTPGitHubConnect{UserCode: "ABCD-1234", ExpiresAt: 1},
		Setup:   &HTTPGitHubSetup{Repo: "octo/notes", Phase: Cloning, Percent: &percent, ProjectID: &id}}
	if err := ValidateGitHubResponse(ok); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*HTTPGitHubResponse){
		"state":   func(r *HTTPGitHubResponse) { r.State = "unavailable" },
		"app":     func(r *HTTPGitHubResponse) { r.App = "Termivoa/x" },
		"login":   func(r *HTTPGitHubResponse) { r.Login = &bad },
		"code":    func(r *HTTPGitHubResponse) { r.Connect = &HTTPGitHubConnect{UserCode: "abcd-1234"} },
		"problem": func(r *HTTPGitHubResponse) { p := HTTPGitHubProblem("other"); r.Problem = &p },
		"phase":   func(r *HTTPGitHubResponse) { r.Setup = &HTTPGitHubSetup{Repo: "octo/notes", Phase: "done"} },
		"percent": func(r *HTTPGitHubResponse) { r.Setup.Percent = &over },
		"setup":   func(r *HTTPGitHubResponse) { r.Setup.Problem = &wrong },
		"project": func(r *HTTPGitHubResponse) { r.Setup.ProjectID = &bad },
		"repo":    func(r *HTTPGitHubResponse) { r.Setup.Repo = "../x" },
	} {
		response := ok
		setup := *ok.Setup
		response.Setup = &setup
		change(&response)
		if ValidateGitHubResponse(response) == nil {
			t.Errorf("%s: an invalid response passed", name)
		}
	}
	ok.Setup.Problem = &problem
	if err := ValidateGitHubResponse(ok); err != nil {
		t.Fatal(err)
	}

	if ValidateGitHubRepos(HTTPGitHubReposResponse{}) == nil {
		t.Error("repos must be present")
	}
	if ValidateGitHubRepos(HTTPGitHubReposResponse{Repos: make([]HTTPGitHubRepo, MaxGitHubRepos+1)}) == nil {
		t.Error("1001 repos passed")
	}
	if err := ValidateGitHubRepos(HTTPGitHubReposResponse{Repos: []HTTPGitHubRepo{{Repo: "octo/notes", SizeKB: 3}}}); err != nil {
		t.Fatal(err)
	}
	if ValidateGitHubInfo(HTTPGitHubInfo{State: NeedsInstall, Login: &login}) != nil || ValidateGitHubInfo(HTTPGitHubInfo{}) == nil {
		t.Error("github info validation is wrong")
	}
}

func TestGitHubRPCMessages(t *testing.T) {
	envelope := func(messageType string, payload any) []byte {
		body, _ := json.Marshal(payload)
		data, _ := json.Marshal(map[string]any{"version": 1, "type": messageType,
			"requestId": "81111111-1111-4111-8111-111111111111", "payload": json.RawMessage(body)})
		return data
	}
	credential := strings.Repeat("A", 43)
	repo := "octo/notes"
	for _, data := range [][]byte{
		envelope("gateway.github.get", GatewayRPCCredentialPayload{Credential: credential}),
		envelope("gateway.github.repos", GatewayRPCCredentialPayload{Credential: credential}),
		envelope("gateway.github.action", GatewayRPCGitHubActionPayload{Credential: credential,
			Request: HTTPGitHubRequest{Action: Setup, Repo: &repo}}),
	} {
		if _, err := DecodeGatewayRPCRequest(data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DecodeGatewayRPCRequest(envelope("gateway.github.action", GatewayRPCGitHubActionPayload{
		Credential: credential, Request: HTTPGitHubRequest{Action: Connect, Repo: &repo}})); err == nil {
		t.Fatal("a connect with a repo passed")
	}
	for _, messageType := range []string{"admin.github.status", "admin.github.disconnect", "admin.github.credential"} {
		if _, err := DecodeAdminRPCRequest(envelope(messageType, BrokerRPCEmptyPayload{})); err != nil {
			t.Fatal(err)
		}
	}
	state, login := HTTPGitHubStateReady, "octo"
	for _, test := range []struct {
		messageType string
		payload     any
		valid       bool
	}{
		{"admin.github.status.result", AdminRPCGitHubStatusPayload{}, true},
		{"admin.github.status.result", AdminRPCGitHubStatusPayload{State: &state, Login: &login}, true},
		{"admin.github.disconnect.result", AdminRPCGitHubStatusPayload{Login: &login}, false},
		{"admin.github.credential.result", AdminRPCGitHubCredentialPayload{Username: "octo", Password: "ghu_" + strings.Repeat("a", 36), ExpiresAt: 1}, true},
		{"admin.github.credential.result", AdminRPCGitHubCredentialPayload{Username: "octo", Password: "short", ExpiresAt: 1}, false},
		{"admin.github.credential.result", AdminRPCGitHubCredentialPayload{Username: "octo", Password: "ghu_" + strings.Repeat("a", 36) + "\n", ExpiresAt: 1}, false},
		{"admin.github.credential.result", AdminRPCGitHubCredentialPayload{Username: "-octo", Password: "ghu_" + strings.Repeat("a", 36), ExpiresAt: 1}, false},
	} {
		if _, err := DecodeAdminRPCResponse(envelope(test.messageType, test.payload)); (err == nil) != test.valid {
			t.Errorf("%s %+v: err = %v", test.messageType, test.payload, err)
		}
	}
}

package broker

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/github"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// fakeGitHub is a githubService that records calls and returns err.
type fakeGitHub struct {
	mu     sync.Mutex
	calls  []string
	err    error
	login  *string
	closed bool
}

func (fake *fakeGitHub) record(call string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, call)
}

func (fake *fakeGitHub) response() protocol.HTTPGitHubResponse {
	return protocol.HTTPGitHubResponse{State: protocol.HTTPGitHubStateReady, App: "termivoa", Login: fake.login}
}

func (fake *fakeGitHub) Info() protocol.HTTPGitHubInfo {
	return protocol.HTTPGitHubInfo{State: protocol.HTTPGitHubStateReady, Login: fake.login}
}
func (fake *fakeGitHub) State(device string) protocol.HTTPGitHubResponse {
	fake.record("state " + device)
	return fake.response()
}
func (fake *fakeGitHub) Connect(_ context.Context, device string) (protocol.HTTPGitHubResponse, error) {
	fake.record("connect " + device)
	return fake.response(), fake.err
}
func (fake *fakeGitHub) Cancel(device string) protocol.HTTPGitHubResponse {
	fake.record("cancel " + device)
	return fake.response()
}
func (fake *fakeGitHub) Disconnect(_ context.Context, device string) (protocol.HTTPGitHubResponse, bool, error) {
	fake.record("disconnect " + device)
	return fake.response(), false, fake.err
}
func (fake *fakeGitHub) Repos(context.Context) (protocol.HTTPGitHubReposResponse, error) {
	fake.record("repos")
	return protocol.HTTPGitHubReposResponse{Repos: []protocol.HTTPGitHubRepo{{Repo: "octo/notes", SizeKB: 12}}}, fake.err
}
func (fake *fakeGitHub) Setup(_ context.Context, device, repo string) (protocol.HTTPGitHubResponse, error) {
	fake.record("setup " + device + " " + repo)
	return fake.response(), fake.err
}
func (fake *fakeGitHub) Stop(device string) protocol.HTTPGitHubResponse {
	fake.record("stop " + device)
	return fake.response()
}
func (fake *fakeGitHub) Credential(context.Context) (string, string, time.Time, error) {
	fake.record("credential")
	return "octo", "ghu_" + strings.Repeat("a", 36), time.Unix(1_800_000_000, 0), fake.err
}
func (fake *fakeGitHub) Close() { fake.closed = true }

func TestGitHubRoutes(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	credential := pairDevice(t, service, "phone").Credential
	device := pairDevice(t, service, "tablet")
	client := startRPCTestClient(t, fixture.server, protocol.Gateway)
	login := "octo"
	fake := &fakeGitHub{login: &login}
	repo := "octo/notes"
	action := func(credential string, action protocol.HTTPGitHubAction, repo *string) protocol.GatewayRPCEnvelope {
		return client.gateway(t, protocol.GatewayGithubAction, protocol.GatewayRPCGitHubActionPayload{
			Credential: credential, Request: protocol.HTTPGitHubRequest{Action: action, Repo: repo}})
	}
	settings := func() *protocol.HTTPGitHubInfo {
		result, err := protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](client.gateway(t, protocol.GatewaySettings, credentialPayload(credential)))
		if err != nil {
			t.Fatal(err)
		}
		return result.Github
	}

	// Off: no settings field, and every route says so after authorization.
	if settings() != nil {
		t.Fatal("settings carry github while the feature is off")
	}
	for _, response := range []protocol.GatewayRPCEnvelope{
		client.gateway(t, protocol.GatewayGithubGet, credentialPayload(credential)),
		client.gateway(t, protocol.GatewayGithubRepos, credentialPayload(credential)),
		action(credential, protocol.Connect, nil),
	} {
		if code := gatewayErrorCode(t, response); code != protocol.InvalidRequest ||
			gatewayErrorMessage(t, response) != "GitHub is not available on this computer" {
			t.Fatalf("off = %s %q", code, gatewayErrorMessage(t, response))
		}
	}

	fixture.server.github = fake
	if info := settings(); info == nil || info.State != protocol.HTTPGitHubStateReady || *info.Login != "octo" {
		t.Fatalf("settings github = %+v", info)
	}
	if code := gatewayErrorCode(t, client.gateway(t, protocol.GatewayGithubGet, credentialPayload(strings.Repeat("X", 43)))); code != protocol.Unauthorized {
		t.Fatalf("bad credential = %s", code)
	}
	for _, response := range []protocol.GatewayRPCEnvelope{
		client.gateway(t, protocol.GatewayGithubGet, credentialPayload(credential)),
		action(credential, protocol.Connect, nil),
		action(credential, protocol.Cancel, nil),
		action(credential, protocol.Setup, &repo),
		action(credential, protocol.Stop, nil),
		action(credential, protocol.Disconnect, nil),
	} {
		if response.Type == protocol.GatewayError {
			t.Fatalf("route failed: %s", gatewayErrorMessage(t, response))
		}
	}
	// The fake's revoke fails: the disconnect answer says so.
	if result, err := protocol.DecodeRPCPayload[protocol.HTTPGitHubResponse](action(credential, protocol.Disconnect, nil)); err != nil ||
		result.RevokeFailed == nil || !*result.RevokeFailed {
		t.Fatalf("disconnect revokeFailed = %+v, %v", result.RevokeFailed, err)
	}
	fake.calls = fake.calls[:len(fake.calls)-1]
	if response := client.gateway(t, protocol.GatewayGithubRepos, credentialPayload(credential)); response.Type != protocol.GatewayGithubReposResult {
		t.Fatalf("repos = %s", response.Type)
	}
	if len(fake.calls) != 7 || !strings.HasPrefix(fake.calls[3], "setup ") || !strings.HasSuffix(fake.calls[3], " octo/notes") ||
		fake.calls[0] == "state " || fake.calls[6] != "repos" || fake.calls[1] == "connect " {
		t.Fatalf("calls = %q", fake.calls)
	}

	// Limits are per device and per action: setup has 3 a minute.
	for range 2 {
		if response := action(credential, protocol.Setup, &repo); response.Type == protocol.GatewayError {
			t.Fatal("setup within the limit failed")
		}
	}
	response := action(credential, protocol.Setup, &repo)
	if code := gatewayErrorCode(t, response); code != protocol.LimitExceeded ||
		gatewayErrorMessage(t, response) != "too many requests; wait a minute and try again" {
		t.Fatalf("fourth setup = %s %q", code, gatewayErrorMessage(t, response))
	}
	if response := action(device.Credential, protocol.Setup, &repo); response.Type == protocol.GatewayError {
		t.Fatal("another device shares the limit")
	}
	if response := action(credential, protocol.Stop, nil); response.Type == protocol.GatewayError {
		t.Fatal("stop shares the setup limit")
	}

	// Errors leave with fixed texts.
	for err, want := range map[error]string{
		github.ErrNotConnected:    "GitHub is not connected on this computer",
		github.ErrGitHubDown:      "this computer cannot reach GitHub",
		github.ErrBusy:            "GitHub is busy with another phone's request; try again",
		github.ErrNoGit:           "git is not installed on this computer",
		context.DeadlineExceeded:  "broker is unavailable",
		github.ErrTooManyProjects: "saved project limit reached",
		io.ErrUnexpectedEOF:       "broker could not complete the request",
	} {
		fake.err = err
		response := client.gateway(t, protocol.GatewayGithubRepos, credentialPayload(device.Credential))
		if got := gatewayErrorMessage(t, response); got != want {
			t.Fatalf("%v = %q, want %q", err, got, want)
		}
		fixture.server.githubLimits = providerLimits{}
	}
}

func TestGitHubAdmin(t *testing.T) {
	fixture, _, _, _, _ := newAuthStreamFixture(t)
	client := startRPCTestClient(t, fixture.server, protocol.CLI)
	status := func(messageType protocol.AdminRPCMessageType) protocol.AdminRPCGitHubStatusPayload {
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCGitHubStatusPayload](client.admin(t, messageType, protocol.BrokerRPCEmptyPayload{}))
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if off := status(protocol.AdminGithubStatus); off.State != nil || off.Login != nil {
		t.Fatalf("off status = %+v", off)
	}
	if response := client.admin(t, protocol.AdminGithubCredential, protocol.BrokerRPCEmptyPayload{}); response.Type != protocol.AdminError {
		t.Fatalf("credential while off = %s", response.Type)
	}

	login := "octo"
	fake := &fakeGitHub{login: &login}
	fixture.server.github = fake
	if on := status(protocol.AdminGithubStatus); *on.State != protocol.HTTPGitHubStateReady || *on.Login != "octo" {
		t.Fatalf("status = %+v", on)
	}
	if after := status(protocol.AdminGithubDisconnect); after.State == nil || fake.calls[0] != "disconnect " {
		t.Fatalf("disconnect = %+v %q", after, fake.calls)
	}
	credential, err := protocol.DecodeRPCPayload[protocol.AdminRPCGitHubCredentialPayload](
		client.admin(t, protocol.AdminGithubCredential, protocol.BrokerRPCEmptyPayload{}))
	if err != nil || credential.Username != "octo" || !strings.HasPrefix(credential.Password, "ghu_") || credential.ExpiresAt != 1_800_000_000 {
		t.Fatalf("credential = %v %v", credential.Username, err)
	}
	fake.err = github.ErrNotConnected
	response := client.admin(t, protocol.AdminGithubCredential, protocol.BrokerRPCEmptyPayload{})
	payload, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
	if response.Type != protocol.AdminError || payload.Code != protocol.HostUnavailable || payload.Message != "GitHub is not connected on this computer" {
		t.Fatalf("not connected = %s %+v", response.Type, payload)
	}

	// A gateway peer never reaches the admin messages.
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	if _, err := gateway.exchange(t, string(protocol.AdminGithubCredential), protocol.BrokerRPCEmptyPayload{}); err == nil {
		t.Fatal("a gateway peer got an answer to admin.github.credential")
	}
}

func TestGitHubFeatureOffBuildsNoService(t *testing.T) {
	fixture := newManagementFixture(t)
	server, err := NewManagementServer(ManagementServerConfig{
		Registry: fixture.registry, Store: fixture.persistence, BrokerGeneration: fixture.server.config.BrokerGeneration,
		HostBuild: "test", GitHub: &github.Config{Home: t.TempDir(), DataDir: t.TempDir(), Profile: "development"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// This build has no GitHub app (no ClientID): the service is nil.
	if github.ClientID == "" && server.github != nil {
		t.Fatal("a build without a client ID has a GitHub service")
	}
	server.closeGitHub()
}

package broker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

// providerFixture is a broker with herdr wired to a fake herdr in a temp
// folder (design S16): a socket server and a probe script.
type providerFixture struct {
	*managementFixture
	service    *auth.Service
	credential string
	herdr      *herdrtest.Server
	config     herdr.Config
	gateway    *rpcTestClient
	pushes     chan webpush.ProviderEvent
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	root, err := os.MkdirTemp("/tmp", "herdr-broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary, err := herdrtest.Script(filepath.Join(root, "bin"), true, "pi, claude, codex")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	server, err := herdrtest.Listen(filepath.Join(home, ".config", "herdr", "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	snapshot, err := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	server.Snapshot(snapshot)
	provider := &providerFixture{
		managementFixture: fixture, service: service, herdr: server,
		config: herdr.Config{BinaryPaths: []string{binary}, Home: home},
		pushes: make(chan webpush.ProviderEvent, 64),
	}
	fixture.server.providerPush = func(event webpush.ProviderEvent) bool {
		provider.pushes <- event
		return true
	}
	provider.attach(t)
	provider.credential = pairDevice(t, service, "Phone").Credential
	provider.gateway = startRPCTestClient(t, fixture.server, protocol.Gateway)
	return provider
}

// attach builds the provider host as NewManagementServer does and runs its
// poll loop until the test ends.
func (fixture *providerFixture) attach(t *testing.T) {
	t.Helper()
	host, err := newProviderHost(context.Background(), fixture.server, fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.providers = host
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		host.provider.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (fixture *providerFixture) waitState(t *testing.T, state protocol.HTTPProviderState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, _ := fixture.server.providers.provider.Info()
		if info.State == state {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider state = %s, want %s", info.State, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fixture *providerFixture) get(t *testing.T, credential, provider string) protocol.GatewayRPCEnvelope {
	t.Helper()
	return fixture.gateway.gateway(t, protocol.GatewayProvidersGet, protocol.GatewayRPCProviderPayload{Credential: credential, Provider: provider})
}

func (fixture *providerFixture) update(t *testing.T, credential string, request protocol.HTTPProviderUpdateRequest) protocol.GatewayRPCEnvelope {
	t.Helper()
	return fixture.gateway.gateway(t, protocol.GatewayProvidersUpdate, protocol.GatewayRPCProviderUpdatePayload{
		Credential: credential, Provider: herdr.ID, Request: request,
	})
}

// gatewayFrom sends one gateway request with a forwarded browser origin.
func (client *rpcTestClient) gatewayFrom(t *testing.T, origin string, messageType protocol.GatewayRPCMessageType, payload any) protocol.GatewayRPCEnvelope {
	t.Helper()
	body, _ := json.Marshal(payload)
	data, _ := json.Marshal(map[string]any{
		"version": 1, "type": messageType, "requestId": uuid.New().String(), "origin": origin, "payload": json.RawMessage(body),
	})
	_ = client.conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := client.writer.WriteFrame(data); err != nil {
		t.Fatal(err)
	}
	reply, err := client.reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	response, err := protocol.DecodeGatewayRPCResponse(reply)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func gatewayErrorMessage(t *testing.T, response protocol.GatewayRPCEnvelope) string {
	t.Helper()
	payload, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
	return payload.Message
}

func TestSettingsListHerdrOnlyWhenInstalled(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	settings, err := protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](
		fixture.gateway.gateway(t, protocol.GatewaySettings, credentialPayload(fixture.credential)))
	if err != nil || len(settings.Providers) != 1 {
		t.Fatalf("settings = %+v %v", settings, err)
	}
	info := settings.Providers[0]
	if info.ID != "herdr" || info.State != protocol.HTTPProviderStateReady || !info.Enabled || *info.Version != "0.8.2" ||
		len(info.Notify) != 2 || !info.CanCreate || strings.Join(info.AgentKinds, ",") != "claude,codex" {
		t.Fatalf("provider info = %+v", info)
	}

	// No herdr binary: no provider at all, as on a computer without herdr.
	fixture.config.BinaryPaths = []string{filepath.Join(filepath.Dir(fixture.config.BinaryPaths[0]), "missing")}
	fixture.attach(t)
	time.Sleep(50 * time.Millisecond)
	settings, _ = protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](
		fixture.gateway.gateway(t, protocol.GatewaySettings, credentialPayload(fixture.credential)))
	if settings.Providers != nil {
		t.Fatalf("a computer without herdr lists %+v", settings.Providers)
	}
	if code := gatewayErrorCode(t, fixture.get(t, fixture.credential, "herdr")); code != protocol.InvalidRequest {
		t.Fatalf("get without herdr = %s", code)
	}

	// No Herdr config (Windows, tests): nothing either.
	fixture.server.providers = nil
	settings, _ = protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](
		fixture.gateway.gateway(t, protocol.GatewaySettings, credentialPayload(fixture.credential)))
	if settings.Providers != nil {
		t.Fatal("no config lists a provider")
	}
}

func TestProviderGetAuthorizesLikeTheSessionsList(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	snapshot, err := protocol.DecodeRPCPayload[protocol.HTTPProviderSnapshotResponse](fixture.get(t, fixture.credential, "herdr"))
	if err != nil || snapshot.State != protocol.HTTPProviderStateReady || len(snapshot.Groups) != 3 {
		t.Fatalf("snapshot = %+v %v", snapshot, err)
	}
	if code := gatewayErrorCode(t, fixture.get(t, strings.Repeat("X", 43), "herdr")); code != protocol.Unauthorized {
		t.Fatalf("bad credential = %s", code)
	}
	if code := gatewayErrorCode(t, fixture.get(t, fixture.credential, "tmux")); code != protocol.InvalidRequest {
		t.Fatalf("unknown provider = %s", code)
	}
	// A device paired with this computer's own app, asked from another app.
	response := fixture.gateway.gatewayFrom(t, "https://evil.tailx.ts.net", protocol.GatewayProvidersGet,
		protocol.GatewayRPCProviderPayload{Credential: fixture.credential, Provider: "herdr"})
	if code := gatewayErrorCode(t, response); code != protocol.InvalidRequest {
		t.Fatalf("foreign origin = %s", code)
	}
	// A hung herdr never slows the read: the answer comes from the cache.
	fixture.herdr.Hang(true)
	started := time.Now()
	for range 5 {
		if response := fixture.get(t, fixture.credential, "herdr"); response.Type != protocol.GatewayProvidersGetResult {
			t.Fatalf("get while hung = %s", response.Type)
		}
		fixture.gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(fixture.credential))
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("reads waited on a hung herdr: %v", elapsed)
	}
}

func TestProviderUpdateStoresSettingsAndIsLimitedPerDevice(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	off := false
	info, err := protocol.DecodeRPCPayload[protocol.HTTPProviderInfo](fixture.update(t, fixture.credential,
		protocol.HTTPProviderUpdateRequest{Enabled: &off, Notify: []protocol.HTTPProviderNotifyState{protocol.Done}}))
	if err != nil || info.Enabled || info.State != protocol.Off || len(info.Notify) != 1 || info.Notify[0] != protocol.Done || info.CanCreate {
		t.Fatalf("update = %+v %v", info, err)
	}
	snapshot, _ := protocol.DecodeRPCPayload[protocol.HTTPProviderSnapshotResponse](fixture.get(t, fixture.credential, "herdr"))
	if snapshot.State != protocol.Off || len(snapshot.Groups) != 0 {
		t.Fatalf("snapshot while off = %+v", snapshot)
	}
	fixture.waitState(t, protocol.Off)
	fixture.herdr.Calls()
	time.Sleep(2500 * time.Millisecond)
	if calls := fixture.herdr.Calls(); len(calls) != 0 {
		t.Fatalf("polls while off: %v", calls)
	}
	for key, want := range map[string]string{providerEnabledKey: "0", providerNotifyKey: "done"} {
		if value, found, err := fixture.persistence.GetSetting(context.Background(), key); err != nil || !found || value != want {
			t.Fatalf("%s = %q %v %v", key, value, found, err)
		}
	}
	// A broker restart keeps them.
	fixture.attach(t)
	if info, _ := fixture.server.providers.info(); info.Enabled || info.State != protocol.Off || len(info.Notify) != 1 {
		t.Fatalf("after restart = %+v", info)
	}

	// 10 changes a minute per device; the 11th is refused, another device is not.
	on := true
	for index := range providerUpdatesPerMinute {
		if response := fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &on}); response.Type == protocol.GatewayError {
			t.Fatalf("update %d refused", index)
		}
	}
	response := fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &on})
	if code := gatewayErrorCode(t, response); code != protocol.LimitExceeded || strings.Contains(gatewayErrorMessage(t, response), "herdr:") {
		t.Fatalf("11th update = %s", code)
	}
	other := pairDevice(t, fixture.service, "Tablet").Credential
	if response := fixture.update(t, other, protocol.HTTPProviderUpdateRequest{Enabled: &on}); response.Type == protocol.GatewayError {
		t.Fatal("another device shares the limit")
	}
}

// TestProviderPushCarriesNamesOnly is design S4: the fake herdr's titles carry
// a marker that no push payload may contain.
func TestProviderPushCarriesNamesOnly(t *testing.T) {
	fixture := newProviderFixture(t)
	marked, err := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.9.3.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.herdr.Snapshot(marked)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	time.Sleep(2500 * time.Millisecond) // the baseline poll of the marked snapshot
	blocked := strings.Replace(string(marked), `"agent":"codex","agent_status":"working"`, `"agent":"codex","agent_status":"blocked"`, 1)
	if blocked == string(marked) {
		t.Fatal("fixture changed")
	}
	fixture.herdr.Snapshot([]byte(blocked))
	var event webpush.ProviderEvent
	select {
	case event = <-fixture.pushes:
	case <-time.After(10 * time.Second):
		t.Fatal("no push for a blocked agent")
	}
	message, ok := webpush.ProviderMessage(event)
	payload, _ := json.Marshal(message)
	if !ok || message.Title != "herdr · shop · silver-meadow" || message.Body != "codex needs you. Tap to answer." ||
		message.Target != "w3:p1.term_65d0031602b703" || message.Kind != webpush.KindWaiting || message.SessionID != "" {
		t.Fatalf("payload = %s", payload)
	}
	for _, secret := range []string{"TITLE-MARKER", "secret task", "/home/demo", ".claude"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("payload leaks %q: %s", secret, payload)
		}
	}
	select {
	case extra := <-fixture.pushes:
		t.Fatalf("baseline or repeat pushed: %+v", extra)
	default:
	}
}

func TestProviderPushFollowsTheSettings(t *testing.T) {
	fixture := newProviderFixture(t)
	host := fixture.server.providers
	blocked := herdr.Change{Key: "w1:p1.term_1", Group: "shop", Item: "shop", Agent: "claude", To: "blocked"}
	done := herdr.Change{Key: "w1:p1.term_1", Group: "shop", Item: "shop", Agent: "claude", To: "done", Worked: 3 * time.Minute}
	expect := func(change herdr.Change, want bool) {
		t.Helper()
		fixture.server.providerChanged(change)
		select {
		case event := <-fixture.pushes:
			if !want {
				t.Fatalf("pushed %+v", event)
			}
		default:
			if want {
				t.Fatalf("no push for %+v", change)
			}
		}
	}
	expect(blocked, true)
	expect(done, true)
	notify := []protocol.HTTPProviderNotifyState{protocol.Done}
	host.notify.Store(&notify)
	expect(blocked, false)
	expect(done, true)
	host.enabled.Store(false)
	expect(done, false)
}

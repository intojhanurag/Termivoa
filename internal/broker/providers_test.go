package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
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

	// No herdr binary: a notInstalled card that the official installer can
	// fill, also when the provider is switched off; every route refuses it.
	fixture.config.BinaryPaths = []string{filepath.Join(filepath.Dir(fixture.config.BinaryPaths[0]), "missing")}
	fixture.attach(t)
	fixture.waitState(t, protocol.NotInstalled)
	fixture.server.providers.enabled.Store(false)
	settings, _ = protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](
		fixture.gateway.gateway(t, protocol.GatewaySettings, credentialPayload(fixture.credential)))
	if len(settings.Providers) != 1 || settings.Providers[0].State != protocol.NotInstalled || settings.Providers[0].CanStart != nil ||
		settings.Providers[0].CanInstall == nil || !*settings.Providers[0].CanInstall {
		t.Fatalf("a computer without herdr lists %+v", settings.Providers)
	}
	fixture.server.providers.enabled.Store(true)
	for name, response := range map[string]protocol.GatewayRPCEnvelope{
		"get":    fixture.get(t, fixture.credential, "herdr"),
		"open":   fixture.open(t, fixture.credential, "w1:p1.term_65d0031602b701"),
		"create": fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "x"}),
		"manage": fixture.manage(t, fixture.credential, protocol.HTTPProviderManageRequest{Action: protocol.StartServer}),
	} {
		if code := gatewayErrorCode(t, response); code != protocol.HostUnavailable || !strings.Contains(gatewayErrorMessage(t, response), "not installed") {
			t.Fatalf("%s without herdr = %s %q", name, code, gatewayErrorMessage(t, response))
		}
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

// fakeStarts makes every registry start a stream-fake session with its own
// process, and records what was started.
type fakeStarts struct {
	mu        sync.Mutex
	configs   []SessionConfig
	processes []*streamProcess
}

func (fixture *managementFixture) fakeStarts() *fakeStarts {
	starts := &fakeStarts{}
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		process := &streamProcess{viewerProcess: newViewerProcess()}
		config.InputQueueCapacity, config.PTYOutputQueue = DefaultInputQueueCapacity, DefaultPTYOutputQueue
		ctx, cancel := context.WithCancel(context.Background())
		core, err := terminal.New(ctx, terminal.Config{Columns: config.Process.Columns, Rows: config.Process.Rows})
		if err != nil {
			cancel()
			return nil, err
		}
		starts.mu.Lock()
		starts.configs = append(starts.configs, config)
		starts.processes = append(starts.processes, process)
		starts.mu.Unlock()
		return startSessionRuntime(ctx, cancel, parent.Done(), config, process, core)
	}
	return starts
}

func (starts *fakeStarts) count() int {
	starts.mu.Lock()
	defer starts.mu.Unlock()
	return len(starts.configs)
}

func (starts *fakeStarts) last() (SessionConfig, *streamProcess) {
	starts.mu.Lock()
	defer starts.mu.Unlock()
	return starts.configs[len(starts.configs)-1], starts.processes[len(starts.processes)-1]
}

func (fixture *providerFixture) open(t *testing.T, credential, target string) protocol.GatewayRPCEnvelope {
	t.Helper()
	return fixture.gateway.gateway(t, protocol.GatewayProvidersOpen, protocol.GatewayRPCProviderOpenPayload{
		Credential: credential, Provider: herdr.ID,
		Request: protocol.HTTPProviderOpenRequest{TargetID: target, Columns: 45, Rows: 20},
	})
}

func (fixture *providerFixture) mustOpen(t *testing.T, target string) protocol.HTTPProviderOpenResponse {
	t.Helper()
	response := fixture.open(t, fixture.credential, target)
	opened, err := protocol.DecodeRPCPayload[protocol.HTTPProviderOpenResponse](response)
	if err != nil || response.Type != protocol.GatewayProvidersOpenResult {
		t.Fatalf("open %s = %s %s", target, response.Type, response.Payload)
	}
	return opened
}

const (
	targetShop   = "w1:p1.term_65d0031602b701"
	targetLogs   = "w1:p3.term_65d0031602b712"
	targetNotes  = "w2:p1.term_65d0031602b702"
	targetSilver = "w3:p1.term_65d0031602b703"
	targetAPI    = "w4:p1.term_65d0031602b704"
	targetLucky  = "w5:p1.term_65d0031602b705"
)

func TestProviderOpenStartsTheAttachClientOnce(t *testing.T) {
	fixture := newProviderFixture(t)
	starts := fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	opened := fixture.mustOpen(t, targetShop)
	session := opened.Session
	if opened.Reused || session.Title != "herdr · shop" || session.Columns != 45 || session.Rows != 20 ||
		session.Provider != "herdr" || session.TargetID != targetShop || session.State != protocol.Running {
		t.Fatalf("open = %+v", opened)
	}
	config, _ := starts.last()
	binary := fixture.config.BinaryPaths[0]
	if config.Process.Executable != binary || strings.Join(config.Process.Args, " ") != "agent attach w1:p1" ||
		config.Process.Columns != 45 || config.Process.Rows != 20 || !config.DoublePrefix || config.Notify != nil {
		t.Fatalf("started %+v", config)
	}
	// The fixture's checkout path does not exist: the session starts in the
	// home folder and Changes is refused (N3).
	if config.Process.Directory != fixture.config.Home {
		t.Fatalf("directory = %q", config.Process.Directory)
	}
	for _, want := range []string{"HOME=" + fixture.config.Home, "HERDR_SOCKET_PATH=" + fixture.herdr.Path, "TERM=xterm-256color"} {
		if !slices.Contains(config.Process.Env, want) {
			t.Fatalf("env %v lacks %s", config.Process.Env, want)
		}
	}
	// A second open of the same terminal reuses it, and two at once make one.
	if again := fixture.mustOpen(t, targetShop); !again.Reused || again.Session.ID != session.ID {
		t.Fatalf("reopen = %+v", again)
	}
	var wait sync.WaitGroup
	ids := make(chan string, 2)
	for range 2 {
		wait.Go(func() {
			client := startRPCTestClient(t, fixture.server, protocol.Gateway)
			response := client.gateway(t, protocol.GatewayProvidersOpen, protocol.GatewayRPCProviderOpenPayload{
				Credential: fixture.credential, Provider: herdr.ID,
				Request: protocol.HTTPProviderOpenRequest{TargetID: targetSilver, Columns: 45, Rows: 20},
			})
			opened, _ := protocol.DecodeRPCPayload[protocol.HTTPProviderOpenResponse](response)
			ids <- opened.Session.ID
		})
	}
	wait.Wait()
	if first, second := <-ids, <-ids; first == "" || first != second || starts.count() != 2 {
		t.Fatalf("parallel opens = %s %s, %d starts", first, second, starts.count())
	}
	// Never in the sessions list, pterm ls or the shutdown list.
	list, _ := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](
		fixture.gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(fixture.credential)))
	for _, record := range list.Sessions {
		if record.ID == session.ID {
			t.Fatal("a provider session is in GET /api/v1/sessions")
		}
	}
	statuses, _ := fixture.registry.List(context.Background())
	for _, status := range statuses {
		if status.ID == session.ID {
			t.Fatal("a provider session is in Registry.List")
		}
	}
	if _, err := fixture.persistence.GetSession(context.Background(), session.ID); err == nil {
		t.Fatal("a provider session was stored")
	}
}

func TestProviderOpenRefusals(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	// A pane ID with another terminal ID (a restarted herdr), or no such pane.
	for _, target := range []string{"w1:p1.term_000000000000", "w9:p1.term_1"} {
		if code := gatewayErrorCode(t, fixture.open(t, fixture.credential, target)); code != protocol.SessionMissing {
			t.Fatalf("%s = %s", target, code)
		}
	}
	// Cap: 4 provider sessions, apart from the 8 normal ones; no eviction.
	for _, target := range []string{targetShop, targetSilver, targetAPI, targetLucky} {
		fixture.mustOpen(t, target)
	}
	response := fixture.open(t, fixture.credential, targetLogs)
	if code := gatewayErrorCode(t, response); code != protocol.LimitExceeded ||
		gatewayErrorMessage(t, response) != "4 herdr terminals are open; close one and try again" {
		t.Fatalf("5th open = %s %q", code, gatewayErrorMessage(t, response))
	}
	fixture.registry.mu.Lock()
	normal, provider := fixture.registry.liveCounts()
	fixture.registry.mu.Unlock()
	if normal != 1 || provider != 4 {
		t.Fatalf("live counts = %d normal, %d provider", normal, provider)
	}
	// Off: host_unavailable.
	off := false
	fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &off})
	if code := gatewayErrorCode(t, fixture.open(t, fixture.credential, targetShop)); code != protocol.HostUnavailable {
		t.Fatalf("open while off = %s", code)
	}
	// 20 opens a minute per device.
	on := true
	fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &on})
	other := pairDevice(t, fixture.service, "Tablet").Credential
	for range providerOpensPerMinute {
		fixture.open(t, other, targetShop)
	}
	if code := gatewayErrorCode(t, fixture.open(t, other, targetShop)); code != protocol.LimitExceeded {
		t.Fatalf("21st open = %s", code)
	}
}

// attachTo attaches a stream client to any session and applies its snapshot.
func (client *streamClient) attachTo(t *testing.T, id, generation string) {
	t.Helper()
	client.await(t, protocol.Welcome, "")
	client.send(t, protocol.SessionAttach, &id, &generation, protocol.EmptyPayload{})
	begin, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](client.await(t, protocol.SnapshotBegin, ""))
	if err != nil {
		t.Fatal(err)
	}
	client.await(t, protocol.SnapshotEnd, "")
	client.send(t, protocol.SnapshotApplied, &id, &generation, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})
}

func TestProviderSessionRefusesFeatures(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	session := fixture.mustOpen(t, targetShop).Session
	id, generation := session.ID, session.Generation
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attachTo(t, id, generation)
	for kind, payload := range map[protocol.MessageType]any{
		protocol.HistoryRead:    protocol.HistoryReadPayload{HistoryEpoch: "1", HistoryRevision: "1", Offset: 0, Limit: 10},
		protocol.TasksList:      protocol.EmptyPayload{},
		protocol.BranchesList:   protocol.EmptyPayload{},
		protocol.WorktreeRemove: protocol.WorktreeRemovePayload{Lease: strings.Repeat("A", 43)},
		protocol.ChangesList:    protocol.EmptyPayload{}, // N3: the checkout folder is refused
		protocol.FilesList:      protocol.FilesListPayload{Path: ""},
	} {
		request := client.send(t, kind, &id, &generation, payload)
		failure, err := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
		if err != nil || failure.Code != protocol.InvalidRequest {
			t.Fatalf("%s = %+v %v", kind, failure, err)
		}
	}
	// Rename, terminate, preview and upload act on stored sessions only.
	operation := uuid.New().String()
	if code := gatewayErrorCode(t, fixture.gateway.gateway(t, protocol.GatewaySessionTerminate, protocol.GatewayRPCSessionTerminatePayload{
		Credential: fixture.credential, SessionID: id, Request: protocol.HTTPSessionTerminateRequest{OperationID: operation, Generation: generation},
	})); code != protocol.SessionMissing {
		t.Fatalf("terminate = %s", code)
	}
	if code := gatewayErrorCode(t, fixture.gateway.gateway(t, protocol.GatewaySessionRename, protocol.GatewayRPCSessionRenamePayload{
		Credential: fixture.credential, SessionID: id,
		Request: protocol.HTTPSessionRenameRequest{OperationID: uuid.New().String(), Generation: generation, Title: "x"},
	})); code != protocol.SessionMissing {
		t.Fatalf("rename = %s", code)
	}
	if code := gatewayErrorCode(t, fixture.gateway.gateway(t, protocol.GatewayPreviewCheck, protocol.GatewayRPCPreviewCheckPayload{
		Credential: fixture.credential, SessionID: id, Port: 3000,
	})); code != protocol.InvalidRequest {
		t.Fatalf("preview = %s", code)
	}
	if status, err := fixture.registry.Get(id); err != nil {
		t.Fatal(err)
	} else if presence, _ := status.Presence(context.Background(), false); presence.Viewers != 1 {
		t.Fatalf("viewers = %d", presence.Viewers)
	}
}

func TestProviderSessionChangesUseAnOwnedCheckout(t *testing.T) {
	fixture := newProviderFixture(t)
	starts := fixture.fakeStarts()
	checkout, err := os.MkdirTemp("/tmp", "herdr-checkout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(checkout) })
	snapshot, _ := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	fixture.herdr.Snapshot([]byte(strings.ReplaceAll(string(snapshot), `"checkout_path":"/home/demo/shop"`, `"checkout_path":"`+checkout+`"`)))
	time.Sleep(2500 * time.Millisecond)
	session := fixture.mustOpen(t, targetShop).Session
	config, _ := starts.last()
	resolved, _ := filepath.EvalSymlinks(checkout)
	if config.Process.Directory != resolved {
		t.Fatalf("directory = %q, want %q", config.Process.Directory, resolved)
	}
	managed, err := fixture.registry.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if folder, err := managed.ChangesFolder(context.Background()); err != nil || folder != resolved {
		t.Fatalf("changes folder = %q %v", folder, err)
	}
}

func TestProviderSessionsEndWithoutViewers(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	host := fixture.server.providers
	ctx := context.Background()
	opened := fixture.mustOpen(t, targetShop).Session
	viewed := fixture.mustOpen(t, targetSilver).Session
	now := time.Now()
	ended := func(id string) bool {
		_, err := fixture.registry.Get(id)
		return err != nil
	}
	// The first 10 s count as viewed: the phone may not have attached yet.
	host.reap(ctx, now.Add(5*time.Second))
	if ended(opened.ID) || ended(viewed.ID) {
		t.Fatal("a session ended during its first 10 s")
	}
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attachTo(t, viewed.ID, viewed.Generation)
	host.reap(ctx, now.Add(11*time.Second))
	waitFor(t, func() bool { return ended(opened.ID) })
	if ended(viewed.ID) {
		t.Fatal("a watched session ended")
	}
	// The viewer leaves: 10 s later the session ends.
	_ = client.conn.Close()
	host.mu.Lock()
	watched := host.sessions[targetSilver].session
	host.mu.Unlock()
	waitFor(t, func() bool {
		presence, err := watched.Presence(ctx, false)
		return err == nil && presence.Viewers == 0
	})
	host.reap(ctx, now.Add(15*time.Second))
	if ended(viewed.ID) {
		t.Fatal("ended before 10 s without a viewer")
	}
	host.reap(ctx, now.Add(22*time.Second))
	waitFor(t, func() bool { return ended(viewed.ID) })
	host.reap(ctx, now.Add(23*time.Second))
	host.mu.Lock()
	left := len(host.sessions)
	host.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d ended sessions kept", left)
	}

	// The target leaves the snapshot (pane closed, herdr restarted): it ends.
	gone := fixture.mustOpen(t, targetAPI).Session
	snapshot, _ := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	fixture.herdr.Snapshot([]byte(strings.ReplaceAll(string(snapshot), "term_65d0031602b704", "term_65d0031602b7ff")))
	waitFor(t, func() bool { return !host.provider.Has(targetAPI) })
	host.reap(ctx, time.Now())
	waitFor(t, func() bool { return ended(gone.ID) })

	// Off ends every provider session.
	off := fixture.mustOpen(t, targetLucky).Session
	host.enabled.Store(false)
	host.reap(ctx, time.Now())
	waitFor(t, func() bool { return ended(off.ID) })
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// providerInputFixture is one provider session (rpcSessionID) on a real
// terminal core and a recording process, with a controlling CLI client.
func providerInputFixture(t *testing.T, double bool) (*streamClient, *streamProcess, protocol.ControlChangedPayload) {
	t.Helper()
	fixture := newManagementFixture(t)
	starts := fixture.fakeStarts()
	spec := ptyhost.Spec{Executable: "/bin/false", Columns: 80, Rows: 24}
	if double {
		if _, id, _, err := fixture.registry.CreateProvider(spec, ""); err != nil || id != rpcSessionID {
			t.Fatalf("create provider = %s %v", id, err)
		}
	} else if _, err := fixture.registry.Create(context.Background(), CreateSessionRequest{
		OperationID: uuid.New().String(), Title: "shell", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID, Process: spec,
	}); err != nil {
		t.Fatal(err)
	}
	config, process := starts.last()
	if config.DoublePrefix != double {
		t.Fatalf("DoublePrefix = %v", config.DoublePrefix)
	}
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	return client, process, client.acquire(t)
}

func sendInput(t *testing.T, client *streamClient, kind protocol.MessageType, payload any) {
	t.Helper()
	request := client.action(t, kind, payload)
	if message := client.await(t, "", request); message.Type != protocol.InputAccepted {
		for message.Type != protocol.InputAccepted && message.Type != protocol.InputRejected {
			message = client.await(t, "", request)
		}
		if message.Type != protocol.InputAccepted {
			t.Fatalf("%s rejected: %s", kind, message.Payload)
		}
	}
}

// TestProviderInputDoublesCtrlBForEveryKind is N2: the single PTY writer
// doubles 0x02 after any input is built. Text and paste cannot carry 0x02
// (they refuse control bytes), so they prove the path stays unchanged;
// ctrl+b as a key is doubled; an answer card writes through the same writer.
func TestProviderInputDoublesCtrlBForEveryKind(t *testing.T) {
	ctrlB := protocol.InputKeyPayload{Key: protocol.Character, Character: new("b"), Modifiers: protocol.Modifiers{Ctrl: true}}
	enter := protocol.InputKeyPayload{Key: protocol.Enter}
	for _, test := range []struct {
		name  string
		input func(client *streamClient, lease string)
		want  string
	}{
		{"key", func(client *streamClient, lease string) {
			ctrlB.Lease, ctrlB.InputSequence = lease, "1"
			sendInput(t, client, protocol.InputKey, ctrlB)
		}, "\x02\x02"},
		{"text", func(client *streamClient, lease string) {
			sendInput(t, client, protocol.InputText, protocol.InputTextPayload{Lease: lease, InputSequence: "1", Text: "yes"})
		}, "yes"},
		{"paste", func(client *streamClient, lease string) {
			sendInput(t, client, protocol.InputPaste, protocol.InputPastePayload{Lease: lease, InputSequence: "1", Text: "a\tb"})
		}, "a\tb"},
		{"compose", func(client *streamClient, lease string) { // the composer: a paste, then enter
			sendInput(t, client, protocol.InputPaste, protocol.InputPastePayload{Lease: lease, InputSequence: "1", Text: "fix the tests"})
			enter.Lease, enter.InputSequence = lease, "2"
			sendInput(t, client, protocol.InputKey, enter)
		}, "fix the tests\r"},
		{"snippet", func(client *streamClient, lease string) { // a snippet: a paste, ctrl+b, enter
			sendInput(t, client, protocol.InputPaste, protocol.InputPastePayload{Lease: lease, InputSequence: "1", Text: "git status"})
			ctrlB.Lease, ctrlB.InputSequence = lease, "2"
			sendInput(t, client, protocol.InputKey, ctrlB)
			enter.Lease, enter.InputSequence = lease, "3"
			sendInput(t, client, protocol.InputKey, enter)
		}, "git status\x02\x02\r"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, process, grant := providerInputFixture(t, true)
			test.input(client, *grant.Lease)
			waitStreamInput(t, process, test.want)
		})
	}
	t.Run("answer", func(t *testing.T) {
		client, process, grant := providerInputFixture(t, true)
		process.Emit([]byte(agentDialog))
		question := awaitAgentState(t, client, protocol.Waiting).Question
		sendInput(t, client, protocol.InputAnswer, protocol.InputAnswerPayload{
			Lease: *grant.Lease, InputSequence: "1", QuestionID: question.ID, OptionIndex: 1,
		})
		waitStreamInput(t, process, "2")
	})
	t.Run("normal session", func(t *testing.T) {
		client, process, grant := providerInputFixture(t, false)
		ctrlB.Lease, ctrlB.InputSequence = *grant.Lease, "1"
		sendInput(t, client, protocol.InputKey, ctrlB)
		waitStreamInput(t, process, "\x02")
	})
}

// The doubling sits in the writer, after the emulator built the bytes, so
// it covers any input kind (and any later one).
func TestDoublePrefixIsInTheSingleWriter(t *testing.T) {
	process := &streamProcess{viewerProcess: newViewerProcess()}
	core := newStubTerminal()
	ctx, cancel := context.WithCancel(context.Background())
	session, err := startSessionRuntime(ctx, cancel, nil, SessionConfig{
		ID: rpcSessionID, Generation: rpcSessionGen, Process: ptyhost.Spec{Columns: 80, Rows: 24}, DoublePrefix: true,
		InputQueueCapacity: DefaultInputQueueCapacity, PTYOutputQueue: DefaultPTYOutputQueue,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	core.output <- []byte("a\x02b\x02\x02")
	waitStreamInput(t, process, "a\x02\x02b\x02\x02\x02\x02")
}

// TestProviderSessionRunsFakeHerdr opens a terminal end to end: the built
// fakeherdr serves the socket and runs the attach client in a real PTY at
// the phone's size; ctrl+b reaches it as one literal ctrl+b; the session
// ends after its viewer leaves and the attach client exits.
func TestProviderSessionRunsFakeHerdr(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fakeherdr")
	}
	fixture := newProviderFixture(t)
	root := filepath.Dir(filepath.Dir(fixture.config.BinaryPaths[0]))
	binary, err := herdrtest.Build(filepath.Join(root, "fake"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.herdr.Stop()
	folder := filepath.Dir(fixture.herdr.Path)
	snapshot, _ := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	if err := herdrtest.Scenario(folder, false, []string{"claude", "codex"}, snapshot); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(binary, "server")
	server.Env = []string{"HOME=" + fixture.config.Home}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		_ = server.Wait()
	})
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(folder, "control.sock")); return err == nil })
	fixture.registry.config.start = func(ctx context.Context, config SessionConfig) (registrySession, error) {
		return StartSession(ctx, config)
	}
	fixture.config.BinaryPaths = []string{binary}
	fixture.attach(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)

	session := fixture.mustOpen(t, targetShop).Session
	managed, err := fixture.registry.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		frame, err := managed.Snapshot(context.Background())
		return err == nil && strings.Contains(screenText(frame), "FAKE-HERDR w1:p1 45x20")
	})
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attachTo(t, session.ID, session.Generation)
	id, generation := session.ID, session.Generation
	request := client.send(t, protocol.ControlAcquire, &id, &generation, protocol.EmptyPayload{})
	grant, _ := protocol.DecodePayload[protocol.ControlChangedPayload](client.await(t, protocol.ControlChanged, request))
	request = client.send(t, protocol.InputKey, &id, &generation, protocol.InputKeyPayload{
		Lease: *grant.Lease, InputSequence: "1", Key: protocol.Character, Character: new("b"), Modifiers: protocol.Modifiers{Ctrl: true},
	})
	client.await(t, protocol.InputAccepted, request)
	request = client.send(t, protocol.InputText, &id, &generation, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "2", Text: "q"})
	client.await(t, protocol.InputAccepted, request)
	log := func() string { data, _ := os.ReadFile(filepath.Join(folder, "fake.log")); return string(data) }
	// One literal ctrl+b then q: never ctrl+b q (detach).
	waitFor(t, func() bool { return strings.Contains(log(), "input w1:p1 02") && strings.Contains(log(), "71") })
	if strings.Contains(log(), "exit 0 detach") {
		t.Fatal("the phone detached the attach client")
	}
	_ = client.conn.Close()
	clock := time.Now()
	waitFor(t, func() bool {
		clock = clock.Add(providerIdleEnd + time.Second)
		fixture.server.providers.reap(context.Background(), clock)
		return strings.Contains(log(), "attach w1:p1 exit")
	})
	waitFor(t, func() bool { _, err := fixture.registry.Get(session.ID); return err != nil })
}

func (fixture *providerFixture) create(t *testing.T, credential string, request protocol.HTTPProviderCreateRequest) protocol.GatewayRPCEnvelope {
	t.Helper()
	return fixture.gateway.gateway(t, protocol.GatewayProvidersCreate, protocol.GatewayRPCProviderCreatePayload{
		Credential: credential, Provider: herdr.ID, Request: request,
	})
}

func TestProviderCreateRoute(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	const shop = "g7d0f77ccdcfd"
	base, _ := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	fixture.herdr.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		// herdr adds the workspace: the new terminal is in the next snapshot.
		snapshot := string(base)
		for list, record := range map[string]string{
			"workspaces": `{"workspace_id":"w9","label":"brave-fox","number":9,"active_tab_id":"w9:t1","worktree":{"repo_key":"/home/demo/shop/.git","repo_name":"shop","checkout_path":"/x","is_linked_worktree":true}}`,
			"tabs":       `{"tab_id":"w9:t1","workspace_id":"w9","label":"1","number":1}`,
			"panes":      `{"pane_id":"w9:p1","tab_id":"w9:t1","terminal_id":"term_9"}`,
			"layouts":    `{"tab_id":"w9:t1","focused_pane_id":"w9:p1"}`,
		} {
			snapshot = strings.Replace(snapshot, "\""+list+"\":[\n", "\""+list+"\":[\n"+record+",", 1)
		}
		fixture.herdr.Snapshot([]byte(snapshot))
		return map[string]any{"workspace": map[string]any{"workspace_id": "w9"}, "root_pane": map[string]any{"pane_id": "w9:p1", "terminal_id": "term_9"}}, nil
	})
	created, err := protocol.DecodeRPCPayload[protocol.HTTPProviderCreateResponse](
		fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "brave-fox"}))
	if err != nil || created.ItemID != "w9" || created.TargetID != "w9:p1.term_9" || created.AgentStarted || created.Problem != nil {
		t.Fatalf("create = %+v %v", created, err)
	}
	// herdr's and git's text never reaches the phone.
	codex, sh := "codex", "pi"
	for _, test := range []struct {
		request protocol.HTTPProviderCreateRequest
		herdr   *herdrtest.Error
		code    protocol.ErrorCode
		message string
	}{
		{protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "a"}, &herdrtest.Error{Code: "worktree_create_failed", Message: "fatal: '/home/demo/.herdr/worktrees/shop/a' already exists"},
			protocol.WorktreeExists, "a worktree or branch with this name already exists"},
		{protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "a"}, &herdrtest.Error{Code: "worktree_create_failed", Message: "fatal: /home/demo/secret is locked"},
			protocol.WorktreeFailed, "herdr could not make the worktree."},
		{protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "a"}, &herdrtest.Error{Code: "not_git_worktree", Message: "/home/demo"},
			protocol.NotARepository, "this project is not in a git repository"},
		{protocol.HTTPProviderCreateRequest{GroupID: "ws-w2", Name: "a"}, nil, protocol.NotARepository, "this project is not in a git repository"},
		{protocol.HTTPProviderCreateRequest{GroupID: "gffffffffffff", Name: "a"}, nil, protocol.SessionMissing, "that project is gone"},
		// pi is a start kind, but no pi runs in this herdr: not offered.
		{protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "a", Agent: &sh}, nil, protocol.InvalidRequest, "herdr cannot start that agent here"},
	} {
		if test.herdr != nil {
			failure := test.herdr
			fixture.herdr.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) { return nil, failure })
		}
		other := pairDevice(t, fixture.service, "Phone").Credential // the create limit is per device
		response := fixture.create(t, other, test.request)
		if code, message := gatewayErrorCode(t, response), gatewayErrorMessage(t, response); code != test.code || message != test.message {
			t.Fatalf("%+v = %s %q", test.request, code, message)
		}
	}
	// One create or manage at a time: the lock is shared, and the text says so (issue 200). A manage holds it here.
	entered, release := make(chan struct{}), make(chan struct{})
	fixture.herdr.Handle("tab.create", func(json.RawMessage) (any, *herdrtest.Error) {
		close(entered)
		<-release
		return nil, &herdrtest.Error{Code: "internal"}
	})
	slow := make(chan struct{})
	go func() {
		defer close(slow)
		item := "w1"
		_, _ = fixture.server.providers.provider.Manage(context.Background(), protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: &item}, herdr.Project{})
	}()
	<-entered
	busy := fixture.create(t, pairDevice(t, fixture.service, "Phone").Credential, protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "b"})
	close(release)
	<-slow
	if code, message := gatewayErrorCode(t, busy), gatewayErrorMessage(t, busy); code != protocol.LimitExceeded || message != "herdr is busy with another change; try again" {
		t.Fatalf("create while another change runs = %s %q", code, message)
	}
	// 3 creates a minute per device.
	for range providerCreatesPerMinute - 1 {
		fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "b", Agent: &codex})
	}
	if code := gatewayErrorCode(t, fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "c"})); code != protocol.LimitExceeded {
		t.Fatalf("4th create = %s", code)
	}
	off := false
	fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &off})
	other := pairDevice(t, fixture.service, "Phone").Credential
	if code := gatewayErrorCode(t, fixture.create(t, other, protocol.HTTPProviderCreateRequest{GroupID: shop, Name: "d"})); code != protocol.HostUnavailable {
		t.Fatalf("create while off = %s", code)
	}
}

// TestProviderSessionIsNotAStoredSession: a provider session is never the
// source of a new session's worktree, nor an upload target (rule 8).
func TestProviderSessionIsNotAStoredSession(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	session := fixture.mustOpen(t, targetShop).Session
	ctx := context.Background()
	if _, err := fixture.server.prepareWorktree(ctx, protocol.WorktreeCreate{
		SourceSessionID: session.ID, Name: "feature", Base: "HEAD",
	}); !errors.Is(err, errProviderFeature) {
		t.Fatalf("worktree from a provider session = %v", err)
	}
	if _, _, err := fixture.server.authorizeUpload(ctx, nil, fixture.credential, session.ID, session.Generation); !errors.Is(err, errProviderFeature) {
		t.Fatalf("upload to a provider session = %v", err)
	}
}

// TestProviderOpenEndsAnAttachItCannotAnswer: an answer the gateway would
// refuse must not leave the attach client running (the laptop pane stays
// phone-sized and the phone can never open it).
func TestProviderOpenEndsAnAttachItCannotAnswer(t *testing.T) {
	fixture := newProviderFixture(t)
	starts := fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	// Before the title fix, two names of 4-byte characters made such an answer.
	previous := validateProviderOpen
	validateProviderOpen = func(protocol.HTTPProviderOpenResponse) error { return errors.New("title too long") }
	t.Cleanup(func() { validateProviderOpen = previous })
	host := fixture.server.providers
	response := fixture.open(t, fixture.credential, targetShop)
	if response.Type != protocol.GatewayError || starts.count() != 1 {
		t.Fatalf("open = %s, %d starts", response.Type, starts.count())
	}
	waitFor(t, func() bool {
		fixture.registry.mu.Lock()
		defer fixture.registry.mu.Unlock()
		_, provider := fixture.registry.liveCounts()
		return provider == 0
	})
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.sessions) != 0 {
		t.Fatalf("kept %d sessions", len(host.sessions))
	}
}

// TestReapWaitsForAnOpenOfTheSameTarget: the reaper must not end a session
// that an open is returning as reused at that moment.
func TestReapWaitsForAnOpenOfTheSameTarget(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	host := fixture.server.providers
	session := fixture.mustOpen(t, targetShop).Session
	host.mu.Lock()
	open := host.sessions[targetShop]
	open.viewed = time.Now().Add(-time.Minute) // idle: due to end
	host.mu.Unlock()
	// An open of this target holds its lock and is about to answer "reused".
	unlock := host.locks.lock(targetShop)
	reaped := make(chan struct{})
	go func() {
		host.reap(context.Background(), time.Now())
		close(reaped)
	}()
	time.Sleep(200 * time.Millisecond)
	host.mu.Lock()
	open.viewed = time.Now()
	host.mu.Unlock()
	unlock()
	<-reaped
	if _, err := fixture.registry.Get(session.ID); err != nil {
		t.Fatalf("the reaper ended a session an open reused: %v", err)
	}
	select {
	case <-open.session.Ended():
		t.Fatal("the reaper ended a session an open reused")
	default:
	}
}

// TestProviderOpenSaysWhenTheAttachEndsAtOnce: herdr refuses a second client
// and a pane can close during the open. The open answers with that, and
// keeps no session (design section 6, rule 10).
func TestProviderOpenSaysWhenTheAttachEndsAtOnce(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.fakeStarts()
	start := fixture.registry.config.start
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		session, err := start(parent, config)
		if err == nil {
			_ = session.Terminate(context.Background())
		}
		return session, err
	}
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	if code := gatewayErrorCode(t, fixture.open(t, fixture.credential, targetShop)); code != protocol.OperationConflict {
		t.Fatalf("open = %s", code)
	}
	fixture.server.providers.mu.Lock()
	kept := len(fixture.server.providers.sessions)
	fixture.server.providers.mu.Unlock()
	if kept != 0 {
		t.Fatalf("%d sessions kept", kept)
	}
}

func (fixture *providerFixture) manage(t *testing.T, credential string, request protocol.HTTPProviderManageRequest) protocol.GatewayRPCEnvelope {
	t.Helper()
	return fixture.gateway.gateway(t, protocol.GatewayProvidersManage, protocol.GatewayRPCProviderManagePayload{
		Credential: credential, Provider: herdr.ID, Request: request,
	})
}

func TestProviderManageRoute(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	var mu sync.Mutex
	var calls []string
	var failure *herdrtest.Error
	for _, method := range []string{"workspace.close", "worktree.remove", "tab.create", "workspace.create"} {
		fixture.herdr.Handle(method, func(params json.RawMessage) (any, *herdrtest.Error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, method+" "+string(params))
			if failure != nil {
				return nil, failure
			}
			return map[string]any{"workspace": map[string]any{"workspace_id": "w9"}, "root_pane": map[string]any{"pane_id": "w1:p3", "terminal_id": "term_65d0031602b712"}}, nil
		})
	}
	text := func(value string) *string { return &value }
	closeTree := protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: text("w3"), Name: text("silver-meadow")}
	removeTree := protocol.HTTPProviderManageRequest{Action: protocol.RemoveItem, ItemID: text("w3"), Name: text("silver-meadow")}
	device := func() string { return pairDevice(t, fixture.service, "Phone").Credential } // the limit is per device

	done, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](fixture.manage(t, fixture.credential, closeTree))
	if err != nil || done.ItemID == nil || *done.ItemID != "w3" || done.TargetID != nil || done.Problem != nil ||
		len(calls) != 1 || calls[0] != `workspace.close {"workspace_id":"w3"}` {
		t.Fatalf("close = %+v %v %v", done, err, calls)
	}
	// A new tab answers its terminal (this herdr has `terminal attach`).
	tab, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](
		fixture.manage(t, fixture.credential, protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w1")}))
	if err != nil || tab.TargetID == nil || *tab.TargetID != "w1:p3.term_65d0031602b712" {
		t.Fatalf("new tab = %+v %v", tab, err)
	}
	// Uncommitted changes are an answer, not an error; git's text stays on the host.
	failure = &herdrtest.Error{Code: "dirty_worktree_requires_force", Message: "fatal: '/home/demo/secret' contains modified or untracked files"}
	response := fixture.manage(t, fixture.credential, removeTree)
	dirty, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](response)
	if err != nil || dirty.Problem == nil || *dirty.Problem != protocol.Uncommitted || strings.Contains(string(response.Payload), "secret") {
		t.Fatalf("remove with changes = %s %v", response.Payload, err)
	}

	for _, test := range []struct {
		name    string
		request protocol.HTTPProviderManageRequest
		herdr   *herdrtest.Error
		code    protocol.ErrorCode
		message string
	}{
		{"gone", protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w77")}, nil, protocol.SessionMissing, "that workspace is gone"},
		{"renamed", protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: text("w3"), Name: text("old-name")}, nil,
			protocol.OperationConflict, "that workspace changed; look at the list and try again"},
		{"main checkout", protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: text("w1"), Name: text("shop")}, nil,
			protocol.OperationConflict, "close this project's worktrees first"},
		{"not linked", protocol.HTTPProviderManageRequest{Action: protocol.RemoveItem, ItemID: text("w2"), Name: text("notes")}, nil,
			protocol.InvalidRequest, "only a linked worktree can be removed"},
		{"remove fails", removeTree, &herdrtest.Error{Code: "worktree_remove_failed", Message: "fatal: /home/demo/secret is locked"},
			protocol.WorktreeFailed, "herdr could not remove the worktree."},
		{"close fails", closeTree, &herdrtest.Error{Code: "internal", Message: "/home/demo/secret"},
			protocol.HostUnavailable, "herdr did not do that; look at the list and try again"},
		{"start while running", protocol.HTTPProviderManageRequest{Action: protocol.StartServer}, nil,
			protocol.InvalidRequest, "herdr cannot be started on this computer now"},
		{"unknown project", protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace, ProjectID: text(uuid.New().String())}, nil,
			protocol.ProjectMissing, "that project is gone, or its folder cannot be used"},
		// The saved projects of the fixture name folders that do not exist.
		{"project folder gone", protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace, ProjectID: text(rpcProjectID)}, nil,
			protocol.ProjectMissing, "that project is gone, or its folder cannot be used"},
	} {
		mu.Lock()
		failure, calls = test.herdr, nil
		mu.Unlock()
		response := fixture.manage(t, device(), test.request)
		if code, message := gatewayErrorCode(t, response), gatewayErrorMessage(t, response); code != test.code || message != test.message {
			t.Fatalf("%s = %s %q", test.name, code, message)
		}
		if (test.herdr != nil) != (len(calls) == 1) {
			t.Fatalf("%s: herdr calls = %v", test.name, calls)
		}
	}
	failure = nil

	// Two workspaces at one repository's main checkout: herdr closes them only together, which a close from
	// the phone never does. Its own text; herdr is not asked (issue 200).
	base, _ := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	tree := `"worktree":{"repo_key":"/home/demo/site/.git","repo_name":"site","checkout_path":"/home/demo/site","is_linked_worktree":false}`
	fixture.herdr.Snapshot([]byte(`{"version":"0.8.2","protocol":20,"workspaces":[{"workspace_id":"w5","label":"site","number":1,` + tree +
		`},{"workspace_id":"w7","label":"moved","number":2,` + tree + `}],"tabs":[],"panes":[],"layouts":[],"agents":[]}`))
	shared := fixture.manage(t, device(), protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: text("w7"), Name: text("moved")})
	if code, message := gatewayErrorCode(t, shared), gatewayErrorMessage(t, shared); code != protocol.OperationConflict ||
		message != "herdr closes the workspaces at this project's main folder only together; close them in herdr on this computer" || len(calls) != 0 {
		t.Fatalf("close one of two workspaces at a main checkout = %s %q %v", code, message, calls)
	}
	fixture.herdr.Snapshot(base)

	// The same authorization as create: a credential, and an origin this device is paired with.
	if code := gatewayErrorCode(t, fixture.manage(t, strings.Repeat("X", 43), closeTree)); code != protocol.Unauthorized {
		t.Fatalf("bad credential = %s", code)
	}
	foreign := fixture.gateway.gatewayFrom(t, "https://evil.tailx.ts.net", protocol.GatewayProvidersManage,
		protocol.GatewayRPCProviderManagePayload{Credential: fixture.credential, Provider: herdr.ID, Request: closeTree})
	if code := gatewayErrorCode(t, foreign); code != protocol.InvalidRequest || len(calls) != 0 {
		t.Fatalf("foreign origin = %s %v", code, calls)
	}

	// 30 manages a minute per device (3 were used above); another device is not held back.
	for index := 3; index < providerManagesPerMinute; index++ {
		if response := fixture.manage(t, fixture.credential, closeTree); response.Type == protocol.GatewayError {
			t.Fatalf("manage %d = %s", index, gatewayErrorMessage(t, response))
		}
	}
	if code := gatewayErrorCode(t, fixture.manage(t, fixture.credential, closeTree)); code != protocol.LimitExceeded {
		t.Fatalf("31st manage = %s", code)
	}
	if response := fixture.manage(t, device(), closeTree); response.Type == protocol.GatewayError {
		t.Fatalf("another device = %s", gatewayErrorMessage(t, response))
	}
	off := false
	fixture.update(t, fixture.credential, protocol.HTTPProviderUpdateRequest{Enabled: &off})
	if code := gatewayErrorCode(t, fixture.manage(t, device(), closeTree)); code != protocol.HostUnavailable {
		t.Fatalf("manage while off = %s", code)
	}
}

// Revision 6 (docs/herdr-manage.md, section 14): an agent in a new tab, with
// create's kind check and create's limit; closed worktrees in the list, and
// openItem for one of them.
func TestProviderManageAgentTabAndClosedWorktrees(t *testing.T) {
	fixture := newProviderFixture(t)
	const shop = "g7d0f77ccdcfd"
	var mu sync.Mutex
	var calls []string
	record := func(method string, result any) {
		fixture.herdr.Handle(method, func(params json.RawMessage) (any, *herdrtest.Error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, method+" "+string(params))
			return result, nil
		})
	}
	made := map[string]any{"workspace": map[string]any{"workspace_id": "w9"}, "root_pane": map[string]any{"pane_id": "w1:p3", "terminal_id": "term_65d0031602b712"}}
	record("tab.create", made)
	record("worktree.open", made)
	record("agent.start", map[string]any{})
	record("agent.prompt", map[string]any{})
	// The host asks herdr for the agent before the prompt: it is ready.
	fixture.herdr.Handle("agent.get", func(json.RawMessage) (any, *herdrtest.Error) {
		return json.RawMessage(`{"agent":{"agent":"claude","agent_status":"idle","interactive_ready":true}}`), nil
	})
	fixture.herdr.Handle("worktree.list", func(json.RawMessage) (any, *herdrtest.Error) {
		return json.RawMessage(`{"worktrees":[
			{"path":"/home/demo/secret/old-idea","branch":"old-idea","is_linked_worktree":true},
			{"path":"/home/demo/secret/open","branch":"silver-meadow","is_linked_worktree":true,"open_workspace_id":"w3"},
			{"path":"/home/demo/secret/odd","branch":"Odd_Name","is_linked_worktree":true}]}`), nil
	})
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	text := func(value string) *string { return &value }
	device := func() string { return pairDevice(t, fixture.service, "Phone").Credential }

	// The list has the closed worktree's name, and nothing else of herdr's answer.
	var listed protocol.GatewayRPCEnvelope
	waitFor(t, func() bool {
		listed = fixture.get(t, fixture.credential, herdr.ID)
		snapshot, err := protocol.DecodeRPCPayload[protocol.HTTPProviderSnapshotResponse](listed)
		return err == nil && len(snapshot.Groups) == 3 && slices.Equal(snapshot.Groups[0].Closed, []string{"old-idea"}) && snapshot.Groups[1].Closed == nil
	})
	if strings.Contains(string(listed.Payload), "secret") || strings.Contains(string(listed.Payload), "Odd_Name") {
		t.Fatalf("the list = %s", listed.Payload)
	}

	open := protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: text(shop), Worktree: text("old-idea")}
	opened, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](fixture.manage(t, fixture.credential, open))
	if err != nil || opened.ItemID == nil || *opened.ItemID != "w9" || opened.TargetID == nil ||
		len(calls) != 1 || calls[0] != `worktree.open {"branch":"old-idea","focus":false,"workspace_id":"w1"}` {
		t.Fatalf("open = %+v %v %v", opened, err, calls)
	}
	calls = nil
	for _, test := range []struct {
		name    string
		request protocol.HTTPProviderManageRequest
		code    protocol.ErrorCode
		message string
	}{
		{"a name that is not closed", protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: text(shop), Worktree: text("silver-meadow")},
			protocol.SessionMissing, "that worktree is not there to open; look at the list and try again"},
		{"a name herdr does not list", protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: text(shop), Worktree: text("ghost")},
			protocol.SessionMissing, "that worktree is not there to open; look at the list and try again"},
		{"an unknown group", protocol.HTTPProviderManageRequest{Action: protocol.OpenItem, GroupID: text("gffffffffffff"), Worktree: text("old-idea")},
			protocol.SessionMissing, "that worktree is not there to open; look at the list and try again"},
		// pi is a start kind, but no pi runs in this herdr: not offered (create's rule).
		{"an agent that is not offered", protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w1"), Agent: text("pi")},
			protocol.InvalidRequest, "herdr cannot start that agent here"},
	} {
		response := fixture.manage(t, device(), test.request)
		if code, message := gatewayErrorCode(t, response), gatewayErrorMessage(t, response); code != test.code || message != test.message {
			t.Fatalf("%s = %s %q", test.name, code, message)
		}
		if len(calls) != 0 {
			t.Fatalf("%s: herdr calls = %v", test.name, calls)
		}
	}

	// A new tab with an agent: the tab, then create's agent start and prompt.
	agentTab := protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w1"), Agent: text("claude"), Prompt: text("fix it")}
	phone := device()
	tab, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](fixture.manage(t, phone, agentTab))
	if err != nil || tab.AgentStarted == nil || !*tab.AgentStarted || tab.AgentProblem != nil || tab.TargetID == nil || len(calls) != 3 ||
		!strings.HasPrefix(calls[1], `agent.start {"kind":"claude","name":"claude-w1-p3","pane_id":"w1:p3"`) || calls[2] != `agent.prompt {"target":"w1:p3","text":"fix it"}` {
		t.Fatalf("agent tab = %+v %v %v", tab, err, calls)
	}
	// It is also a create: 3 a minute per device, counted with the creates.
	if response := fixture.create(t, phone, protocol.HTTPProviderCreateRequest{GroupID: "gffffffffffff", Name: "a"}); gatewayErrorCode(t, response) != protocol.SessionMissing {
		t.Fatalf("a create after one agent tab = %s", gatewayErrorMessage(t, response))
	}
	if response := fixture.manage(t, phone, agentTab); response.Type == protocol.GatewayError {
		t.Fatalf("the 3rd create or agent tab = %s", gatewayErrorMessage(t, response))
	}
	calls = nil
	response := fixture.manage(t, phone, agentTab)
	if code, message := gatewayErrorCode(t, response), gatewayErrorMessage(t, response); code != protocol.LimitExceeded ||
		message != "too many requests; wait a minute and try again" || len(calls) != 0 {
		t.Fatalf("the 4th = %s %q %v", code, message, calls)
	}
	// A tab without an agent is not a create.
	if response := fixture.manage(t, phone, protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: text("w1")}); response.Type == protocol.GatewayError {
		t.Fatalf("a shell tab after the create limit = %s", gatewayErrorMessage(t, response))
	}
}

// newWorkspace: the phone names a project by ID; the broker resolves and
// checks the folder, and herdr gets the resolved path and the project's name.
func TestProviderManageNewWorkspaceUsesAnOwnedProjectFolder(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	var asked map[string]any
	fixture.herdr.Handle("workspace.create", func(params json.RawMessage) (any, *herdrtest.Error) {
		_ = json.Unmarshal(params, &asked)
		return map[string]any{"workspace": map[string]any{"workspace_id": "w9"}, "root_pane": map[string]any{"pane_id": "w9:p1", "terminal_id": "/etc/passwd"}}, nil
	})
	folder := filepath.Join(fixture.config.Home, "shop")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.config.Home, "link")
	if err := os.Symlink(folder, link); err != nil {
		t.Fatal(err)
	}
	put := func(name, path string) *string {
		id, now := uuid.New().String(), time.Now().UTC()
		project := store.Project{ID: id, DisplayName: name, CanonicalPath: path, CreatedAt: now, UpdatedAt: now}
		if err := fixture.persistence.PutProject(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		return &id
	}
	request := protocol.HTTPProviderManageRequest{Action: protocol.NewWorkspace, ProjectID: put("Web shop", link)}
	made, err := protocol.DecodeRPCPayload[protocol.HTTPProviderManageResponse](fixture.manage(t, fixture.credential, request))
	resolved, _ := filepath.EvalSymlinks(folder)
	// A terminal ID that is not a handle is never sent to the phone.
	if err != nil || made.ItemID == nil || *made.ItemID != "w9" || made.TargetID != nil ||
		asked["cwd"] != resolved || asked["label"] != "Web shop" || asked["focus"] != false {
		t.Fatalf("new workspace = %+v %v, herdr got %v", made, err, asked)
	}
	// A folder of another user (root's /usr) is refused before herdr is asked.
	if os.Geteuid() != 0 {
		asked = nil
		request.ProjectID = put("System", "/usr")
		if code := gatewayErrorCode(t, fixture.manage(t, fixture.credential, request)); code != protocol.ProjectMissing || asked != nil {
			t.Fatalf("another user's folder = %s, herdr got %v", code, asked)
		}
	}
}

// "Check again": a settings read makes the poll loop look for herdr now,
// not at its next 30 s lookup, and never more often than every 2 s.
func TestSettingsReadLooksForHerdrAgain(t *testing.T) {
	fixture := newProviderFixture(t)
	binary := fixture.config.BinaryPaths[0]
	script, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	fixture.attach(t)
	fixture.waitState(t, protocol.NotInstalled)
	if err := os.WriteFile(binary, script, 0o755); err != nil { // the user installs herdr
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond) // a poll turn passes: the loop does not look by itself yet
	if info, _ := fixture.server.providers.provider.Info(); info.State != protocol.NotInstalled {
		t.Fatalf("found without a settings read: %s", info.State)
	}
	host := fixture.server.providers
	host.settingsInfo(time.Now())
	if first := host.looked.Load(); first == 0 {
		t.Fatal("the settings read did not ask")
	} else if host.settingsInfo(time.Now().Add(time.Second)); host.looked.Load() != first {
		t.Fatal("a second read within 2 s asked again")
	}
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	// Ready: a read asks nothing.
	before := host.looked.Load()
	if host.settingsInfo(time.Now().Add(time.Minute)); host.looked.Load() != before {
		t.Fatal("a read of a ready provider asked the loop to look")
	}
}

// startServer end to end with the fake herdr binary: the broker starts
// `herdr server`, the provider turns ready, and the server has the session
// environment.
func TestProviderManageStartsTheServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fakeherdr")
	}
	fixture := newProviderFixture(t)
	fixture.herdr.Stop()
	binary, err := herdrtest.Build(filepath.Join(filepath.Dir(fixture.config.Home), "fake"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.config.BinaryPaths, fixture.config.PathEnv = []string{binary}, filepath.Dir(binary)
	fixture.attach(t)
	fixture.waitState(t, protocol.NotRunning)
	settings, _ := protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](
		fixture.gateway.gateway(t, protocol.GatewaySettings, credentialPayload(fixture.credential)))
	if len(settings.Providers) != 1 || settings.Providers[0].CanStart == nil || settings.Providers[0].CanInstall != nil {
		t.Fatalf("settings = %+v", settings.Providers)
	}
	folder := filepath.Join(fixture.config.Home, ".config", "herdr")
	log := func() string { data, _ := os.ReadFile(filepath.Join(folder, "fake.log")); return string(data) }
	t.Cleanup(func() {
		var pid int
		if _, rest, ok := strings.Cut(log(), "pid="); ok {
			_, _ = fmt.Sscanf(rest, "%d", &pid)
		}
		if process, err := os.FindProcess(pid); err == nil && pid > 0 {
			_ = process.Signal(syscall.SIGTERM)
			waitFor(t, func() bool { return process.Signal(syscall.Signal(0)) != nil })
		}
	})
	response := fixture.manage(t, fixture.credential, protocol.HTTPProviderManageRequest{Action: protocol.StartServer})
	if response.Type != protocol.GatewayProvidersManageResult {
		t.Fatalf("start = %s %q", gatewayErrorCode(t, response), gatewayErrorMessage(t, response))
	}
	if info, _ := fixture.server.providers.info(); info.State != protocol.HTTPProviderStateReady || info.CanStart != nil {
		t.Fatalf("after the start = %+v", info)
	}
	if text := log(); !strings.Contains(text, "PATH="+fixture.config.PathEnv+" TERM=xterm-256color") {
		t.Fatalf("the server's environment: %s", text)
	}
}

package broker

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

type authClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *authClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *authClock) Set(value time.Time) {
	clock.mu.Lock()
	clock.now = value
	clock.mu.Unlock()
}

// newAuthStreamFixture adds a real auth service to the live-session fixture.
func newAuthStreamFixture(t *testing.T) (*managementFixture, *Session, *streamProcess, *auth.Service, *authClock) {
	t.Helper()
	fixture, session, process := newStreamFixture(t)
	fixture.registry.config.newID = func() (string, error) { return uuid.New().String(), nil }
	clock := &authClock{now: time.Now().UTC()}
	service, err := auth.NewService(auth.ServiceConfig{Store: fixture.persistence, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.config.Auth = service
	return fixture, session, process, service, clock
}

func pairDevice(t *testing.T, service *auth.Service, label string) auth.PollResult {
	t.Helper()
	ctx := context.Background()
	grant, _, err := service.CreateGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := service.Redeem(ctx, grant, label)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret)
	if err != nil || !result.Approved {
		t.Fatalf("pair = %#v, %v", result, err)
	}
	return result
}

type rpcTestClient struct {
	conn   net.Conn
	reader *ipc.FrameReader
	writer *ipc.FrameWriter
}

func startRPCTestClient(t *testing.T, server *ManagementServer, kind protocol.IPCClientKind) *rpcTestClient {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go func() { _ = server.serveConnection(context.Background(), serverConn) }()
	if _, err := ipc.ClientHandshake(context.Background(), clientConn, kind, "rpc-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	reader, _ := ipc.NewFrameReader(clientConn, protocol.MaxOutboundMessageBytes)
	writer, _ := ipc.NewFrameWriter(clientConn, protocol.MaxInboundMessageBytes)
	return &rpcTestClient{conn: clientConn, reader: reader, writer: writer}
}

// exchange writes one RPC frame and returns the raw response, or an error if
// the broker poisoned the connection instead of answering.
func (client *rpcTestClient) exchange(t *testing.T, messageType string, payload any) ([]byte, error) {
	t.Helper()
	requestID := uuid.New().String()
	body, _ := json.Marshal(payload)
	data, _ := json.Marshal(map[string]any{"version": 1, "type": messageType, "requestId": requestID, "payload": json.RawMessage(body)})
	_ = client.conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := client.writer.WriteFrame(data); err != nil {
		return nil, err
	}
	return client.reader.ReadFrame()
}

func (client *rpcTestClient) gateway(t *testing.T, messageType protocol.GatewayRPCMessageType, payload any) protocol.GatewayRPCEnvelope {
	t.Helper()
	data, err := client.exchange(t, string(messageType), payload)
	if err != nil {
		t.Fatalf("%s: %v", messageType, err)
	}
	response, err := protocol.DecodeGatewayRPCResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (client *rpcTestClient) admin(t *testing.T, messageType protocol.AdminRPCMessageType, payload any) protocol.AdminRPCEnvelope {
	t.Helper()
	data, err := client.exchange(t, string(messageType), payload)
	if err != nil {
		t.Fatalf("%s: %v", messageType, err)
	}
	response, err := protocol.DecodeAdminRPCResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func gatewayErrorCode(t *testing.T, response protocol.GatewayRPCEnvelope) protocol.ErrorCode {
	t.Helper()
	if response.Type != protocol.GatewayError {
		t.Fatalf("expected gateway.error, got %s", response.Type)
	}
	payload, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
	return payload.Code
}

func credentialPayload(credential string) protocol.GatewayRPCCredentialPayload {
	return protocol.GatewayRPCCredentialPayload{Credential: credential}
}

func TestPeerRoleSelectsTheOnlyReachableRPCSchema(t *testing.T) {
	fixture, _, _, _, _ := newAuthStreamFixture(t)
	for name, attempt := range map[string]struct {
		kind        protocol.IPCClientKind
		messageType string
		payload     any
	}{
		"gateway management":     {protocol.Gateway, "session.list.request", protocol.BrokerRPCSessionListRequestPayload{Limit: 1}},
		"gateway administration": {protocol.Gateway, "admin.pair.grant", protocol.BrokerRPCEmptyPayload{}},
		"cli gateway rpc":        {protocol.CLI, "gateway.auth.check", credentialPayload(strings.Repeat("A", 43))},
	} {
		t.Run(name, func(t *testing.T) {
			client := startRPCTestClient(t, fixture.server, attempt.kind)
			if data, err := client.exchange(t, attempt.messageType, attempt.payload); err == nil {
				t.Fatalf("broker answered a cross-role request: %s", data)
			}
		})
	}
}

// TestAdminDeviceListReportsStatus checks that admin.device.list surfaces the
// store's live-credential status: freshly paired devices are active, a
// credential that ages past its lifetime reports expired, and revocation
// overrides an already-expired credential.
func TestAdminDeviceListReportsStatus(t *testing.T) {
	fixture, _, _, service, clock := newAuthStreamFixture(t)
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	day := 24 * time.Hour
	base := clock.Now()

	alpha := pairDevice(t, service, "Alpha")
	bravo := pairDevice(t, service, "Bravo")

	statusByID := func() map[string]protocol.AdminRPCDevice {
		t.Helper()
		result, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceListResultPayload](
			local.admin(t, protocol.AdminDeviceList, protocol.BrokerRPCEmptyPayload{}))
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]protocol.AdminRPCDevice{}
		for _, device := range result.Devices {
			byID[device.ID] = device
		}
		return byID
	}

	devices := statusByID()
	if devices[alpha.DeviceID].Status != protocol.Active || devices[bravo.DeviceID].Status != protocol.Active {
		t.Fatalf("freshly paired devices = %#v", devices)
	}

	// Past both the 30-day absolute and 7-day inactivity lifetime.
	clock.Set(base.Add(31 * day))
	devices = statusByID()
	if devices[alpha.DeviceID].Status != protocol.Expired || devices[bravo.DeviceID].Status != protocol.Expired {
		t.Fatalf("expired devices = %#v", devices)
	}

	if _, err := service.RevokeDevice(context.Background(), bravo.DeviceID); err != nil {
		t.Fatal(err)
	}
	devices = statusByID()
	if devices[alpha.DeviceID].Status != protocol.Expired {
		t.Fatalf("alpha status changed after bravo revocation = %#v", devices[alpha.DeviceID])
	}
	if devices[bravo.DeviceID].Status != protocol.AdminRPCDeviceStatusRevoked || !devices[bravo.DeviceID].Revoked {
		t.Fatalf("revoked device = %#v", devices[bravo.DeviceID])
	}
}

func TestGatewayPairingAndCredentialAuthorizedSessionRPC(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)

	origin := "https://mac.example.ts.net"
	local.admin(t, protocol.AdminConfigOrigin, protocol.AdminRPCOriginPayload{Origin: &origin})
	grantResponse := local.admin(t, protocol.AdminPairGrant, protocol.BrokerRPCEmptyPayload{})
	grant, _ := protocol.DecodeRPCPayload[protocol.AdminRPCPairGrantResultPayload](grantResponse)
	if grant.Origin == nil || *grant.Origin != origin {
		t.Fatalf("grant result = %#v", grant)
	}
	redeemResponse := gateway.gateway(t, protocol.GatewayPairRedeem, protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Phone"})
	redeemed, err := protocol.DecodeRPCPayload[protocol.HTTPPairRedeemResponse](redeemResponse)
	if err != nil {
		t.Fatalf("redeem = %s, %v", redeemResponse.Type, err)
	}
	if code := gatewayErrorCode(t, gateway.gateway(t, protocol.GatewayPairRedeem,
		protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Other"})); code != protocol.PairingExpired {
		t.Fatalf("second redemption code = %s", code)
	}
	poll := protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret}
	pollResult, _ := protocol.DecodeRPCPayload[protocol.GatewayRPCPairPollResultPayload](gateway.gateway(t, protocol.GatewayPairPoll, poll))
	if pollResult.Response.Status != protocol.HTTPPairPollStatusPending || pollResult.Credential != nil {
		t.Fatalf("pending poll = %#v", pollResult)
	}
	pending, _ := protocol.DecodeRPCPayload[protocol.AdminRPCPairPendingResultPayload](
		local.admin(t, protocol.AdminPairPending, protocol.BrokerRPCEmptyPayload{}))
	if len(pending.Pending) != 1 || pending.Pending[0].VerificationCode != redeemed.VerificationCode {
		t.Fatalf("pending = %#v", pending)
	}
	local.admin(t, protocol.AdminPairApprove, protocol.AdminRPCPendingIDPayload{PendingID: redeemed.PendingID})
	if response := local.admin(t, protocol.AdminPairApprove, protocol.AdminRPCPendingIDPayload{PendingID: redeemed.PendingID}); response.Type != protocol.AdminError {
		t.Fatalf("approval replay = %s", response.Type)
	}
	pollResult, _ = protocol.DecodeRPCPayload[protocol.GatewayRPCPairPollResultPayload](gateway.gateway(t, protocol.GatewayPairPoll, poll))
	if pollResult.Response.Status != protocol.Approved || pollResult.Credential == nil {
		t.Fatalf("approved poll = %#v", pollResult)
	}
	credential := *pollResult.Credential
	if code := gatewayErrorCode(t, gateway.gateway(t, protocol.GatewayPairPoll, poll)); code != protocol.PairingExpired {
		t.Fatalf("issuance replay code = %s", code)
	}

	principal, _ := protocol.DecodeRPCPayload[protocol.GatewayRPCPrincipalPayload](
		gateway.gateway(t, protocol.GatewayAuthCheck, credentialPayload(credential)))
	if principal.DeviceLabel != "Phone" {
		t.Fatalf("principal = %#v", principal)
	}
	settingsResponse := gateway.gateway(t, protocol.GatewaySettings, credentialPayload(credential))
	if strings.Contains(string(settingsResponse.Payload), "/tmp") || strings.Contains(string(settingsResponse.Payload), "/bin/zsh") {
		t.Fatalf("settings leaked a local path: %s", settingsResponse.Payload)
	}
	settings, _ := protocol.DecodeRPCPayload[protocol.HTTPSettingsResponse](settingsResponse)
	if len(settings.Projects) != 2 || len(settings.ShellProfiles) != 1 || settings.BrokerGeneration != registryBrokerGeneration {
		t.Fatalf("settings = %#v", settings)
	}
	list, _ := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(credential)))
	if len(list.Sessions) != 1 || list.Sessions[0].ID != rpcSessionID {
		t.Fatalf("session list = %#v", list)
	}
	create := protocol.GatewayRPCSessionCreatePayload{Credential: credential, Request: protocol.HTTPSessionCreateRequest{
		OperationID: "87777777-7777-4777-8777-777777777777", ProjectID: rpcProjectTwo, ShellProfileID: rpcProfileID,
	}}
	created, err := protocol.DecodeRPCPayload[protocol.HTTPSessionMutationResponse](gateway.gateway(t, protocol.GatewaySessionCreate, create))
	if err != nil || created.OperationID != create.Request.OperationID {
		t.Fatalf("create = %#v, %v", created, err)
	}
	rename := protocol.GatewayRPCSessionRenamePayload{Credential: credential, SessionID: created.Session.ID,
		Request: protocol.HTTPSessionRenameRequest{OperationID: "88888888-8888-4888-8888-888888888888", Generation: created.Session.Generation, Title: "Renamed"}}
	renamed, _ := protocol.DecodeRPCPayload[protocol.HTTPSessionMutationResponse](gateway.gateway(t, protocol.GatewaySessionRename, rename))
	if renamed.Session.Title != "Renamed" {
		t.Fatalf("rename = %#v", renamed)
	}

	// Authorization precedes receipt replay: a revoked credential cannot read
	// the result of an operation it previously completed.
	devices, _ := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceListResultPayload](
		local.admin(t, protocol.AdminDeviceList, protocol.BrokerRPCEmptyPayload{}))
	if len(devices.Devices) != 1 || devices.Devices[0].Revoked {
		t.Fatalf("devices = %#v", devices)
	}
	revoked, _ := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceRevokeResultPayload](
		local.admin(t, protocol.AdminDeviceRevoke, protocol.AdminRPCDeviceIDPayload{DeviceID: devices.Devices[0].ID}))
	if revoked.DeviceID != devices.Devices[0].ID || revoked.ClosedStreams != 0 {
		t.Fatalf("revoke = %#v", revoked)
	}
	for _, request := range []struct {
		messageType protocol.GatewayRPCMessageType
		payload     any
	}{
		{protocol.GatewaySessionCreate, create}, {protocol.GatewaySessionRename, rename},
		{protocol.GatewayAuthCheck, credentialPayload(credential)}, {protocol.GatewaySessionList, credentialPayload(credential)},
		{protocol.GatewayLiveOpen, credentialPayload(credential)},
	} {
		if code := gatewayErrorCode(t, gateway.gateway(t, request.messageType, request.payload)); code != protocol.ErrorCodeRevoked {
			t.Fatalf("%s after revocation = %s", request.messageType, code)
		}
	}
	if _, err := service.Authenticate(context.Background(), credential); !errors.Is(err, auth.ErrRevoked) {
		t.Fatalf("service authentication after revoke = %v", err)
	}
}

// startBrowserClient opens a credential-bound live stream exactly as the
// gateway does: gateway.live.open, then a canonical browser hello.
func startBrowserClient(t *testing.T, server *ManagementServer, credential string) *streamClient {
	t.Helper()
	rpc := startRPCTestClient(t, server, protocol.Gateway)
	if response := rpc.gateway(t, protocol.GatewayLiveOpen, credentialPayload(credential)); response.Type != protocol.GatewayLiveOpenResult {
		t.Fatalf("live open = %s", response.Type)
	}
	_ = rpc.conn.SetDeadline(time.Time{})
	return adoptStreamClient(t, rpc.conn, rpc.writer, rpc.reader, protocol.Browser)
}

func adoptStreamClient(t *testing.T, conn net.Conn, writer *ipc.FrameWriter, reader *ipc.FrameReader, kind protocol.ClientKind) *streamClient {
	t.Helper()
	client := &streamClient{conn: conn, writer: writer, messages: make(chan protocol.Envelope, 64), done: make(chan struct{})}
	go func() {
		defer close(client.done)
		for {
			data, err := reader.ReadFrame()
			if err != nil {
				client.err = err
				return
			}
			envelope, err := protocol.DecodeHost(data)
			if err != nil {
				client.err = err
				return
			}
			select {
			case client.messages <- envelope:
			case <-time.After(3 * time.Second):
				client.err = errors.New("test client stopped consuming messages")
				return
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); <-client.done })
	client.send(t, protocol.Hello, nil, nil, protocol.HelloPayload{
		ClientKind: kind, ClientBuild: "test", CapabilityProfile: protocol.PhoneTerminalV1, SupportedVersions: []int64{1},
	})
	return client
}

func awaitClosed(t *testing.T, client *streamClient) {
	t.Helper()
	select {
	case <-client.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream remained open")
	}
}

func TestBrowserLiveModeRequiresCredentialBoundGatewayPeer(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	issued := pairDevice(t, service, "Phone")
	// A gateway hello without gateway.live.open and a CLI browser hello are
	// both unauthorized; a bound gateway cannot claim the desktop kind.
	unbound := startStreamClient(t, fixture.server, protocol.Gateway)
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](unbound.await(t, protocol.Error, "")); failure.Code != protocol.Unauthorized {
		t.Fatalf("unbound gateway = %#v", failure)
	}
	clientConn, serverConn := net.Pipe()
	go func() { _ = fixture.server.serveConnection(context.Background(), serverConn) }()
	if _, err := ipc.ClientHandshake(context.Background(), clientConn, protocol.CLI, "cli"); err != nil {
		t.Fatal(err)
	}
	writer, _ := ipc.NewFrameWriter(clientConn, protocol.MaxInboundMessageBytes)
	reader, _ := ipc.NewFrameReader(clientConn, protocol.MaxOutboundMessageBytes)
	cliBrowser := adoptStreamClient(t, clientConn, writer, reader, protocol.Browser)
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](cliBrowser.await(t, protocol.Error, "")); failure.Code != protocol.Unauthorized {
		t.Fatalf("cli browser = %#v", failure)
	}
	rpc := startRPCTestClient(t, fixture.server, protocol.Gateway)
	rpc.gateway(t, protocol.GatewayLiveOpen, credentialPayload(issued.Credential))
	_ = rpc.conn.SetDeadline(time.Time{})
	boundDesktop := adoptStreamClient(t, rpc.conn, rpc.writer, rpc.reader, protocol.Desktop)
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](boundDesktop.await(t, protocol.Error, "")); failure.Code != protocol.Unauthorized {
		t.Fatalf("bound desktop = %#v", failure)
	}
	if code := gatewayErrorCode(t, startRPCTestClient(t, fixture.server, protocol.Gateway).gateway(t,
		protocol.GatewayLiveOpen, credentialPayload(strings.Repeat("A", 43)))); code != protocol.Unauthorized {
		t.Fatalf("unknown credential live open = %s", code)
	}
}

func TestDeviceRevocationClosesLiveStreamAndReleasesControl(t *testing.T) {
	fixture, session, process, service, _ := newAuthStreamFixture(t)
	issued := pairDevice(t, service, "Phone")
	other := pairDevice(t, service, "Tablet")
	browser := startBrowserClient(t, fixture.server, issued.Credential)
	browser.attach(t)
	grant := browser.acquire(t)
	request := browser.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "before"})
	browser.await(t, protocol.InputAccepted, request)
	waitStreamInput(t, process, "before")
	bystander := startBrowserClient(t, fixture.server, other.Credential)
	bystander.attach(t)

	closed, err := service.RevokeDevice(context.Background(), issued.DeviceID)
	if err != nil || closed != 1 {
		t.Fatalf("revoke = %d, %v", closed, err)
	}
	notice, _ := protocol.DecodePayload[protocol.AccessRevokedPayload](browser.await(t, protocol.AccessRevoked, ""))
	if !strings.Contains(notice.Message, "revoked") {
		t.Fatalf("revocation notice = %#v", notice)
	}
	awaitClosed(t, browser)
	if control := session.controller.State(); control.ConnectionID != "" {
		t.Fatalf("revoked connection kept control: %#v", control)
	}
	// The unrelated device's stream remains live and can take control.
	bystanderGrant := bystander.acquire(t)
	request = bystander.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *bystanderGrant.Lease, InputSequence: "1", Text: "-after"})
	bystander.await(t, protocol.InputAccepted, request)
	waitStreamInput(t, process, "before-after")
}

func TestLogoutClosesOnlyThatCredentialsStream(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	first, second := pairDevice(t, service, "Phone"), pairDevice(t, service, "Laptop")
	firstStream := startBrowserClient(t, fixture.server, first.Credential)
	firstStream.attach(t)
	secondStream := startBrowserClient(t, fixture.server, second.Credential)
	secondStream.attach(t)
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	if response := gateway.gateway(t, protocol.GatewayAuthLogout, credentialPayload(first.Credential)); response.Type != protocol.GatewayAuthLogoutResult {
		t.Fatalf("logout = %s", response.Type)
	}
	firstStream.await(t, protocol.AccessRevoked, "")
	awaitClosed(t, firstStream)
	secondStream.acquire(t)
}

// A host clock step back is host_unavailable, not unauthorized, so the
// gateway does not clear the phone's only-once-delivered cookie.
func TestGatewayClockRollbackIsHostUnavailable(t *testing.T) {
	fixture, _, _, service, clock := newAuthStreamFixture(t)
	start := clock.Now()
	issued := pairDevice(t, service, "Phone")
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	clock.Set(start.Add(-time.Minute))
	if code := gatewayErrorCode(t, gateway.gateway(t, protocol.GatewayAuthCheck, credentialPayload(issued.Credential))); code != protocol.HostUnavailable {
		t.Fatalf("auth check after clock rollback = %s", code)
	}
	clock.Set(start)
	if response := gateway.gateway(t, protocol.GatewayAuthCheck, credentialPayload(issued.Credential)); response.Type != protocol.GatewayAuthCheckResult {
		t.Fatalf("auth check after the clock recovers = %s", response.Type)
	}
}

func TestLiveStreamClosesWhenCredentialExpires(t *testing.T) {
	fixture, _, _, service, clock := newAuthStreamFixture(t)
	start := clock.Now()
	issued := pairDevice(t, service, "Phone")
	// Keep the credential active, then open the stream 500ms before its
	// absolute expiry so it expires during the attached live connection.
	for day := 6; day < 30; day += 6 {
		clock.Set(start.Add(time.Duration(day) * 24 * time.Hour))
		if _, err := service.Authenticate(context.Background(), issued.Credential); err != nil {
			t.Fatal(err)
		}
	}
	clock.Set(start.Add(auth.BrowserAbsoluteLifetime - 500*time.Millisecond))
	browser := startBrowserClient(t, fixture.server, issued.Credential)
	browser.await(t, protocol.Welcome, "")
	browser.action(t, protocol.SessionAttach, protocol.EmptyPayload{})
	browser.await(t, protocol.SnapshotEnd, "")
	notice, _ := protocol.DecodePayload[protocol.AccessRevokedPayload](browser.await(t, protocol.AccessRevoked, ""))
	if !strings.Contains(notice.Message, "expired") {
		t.Fatalf("expiry notice = %#v", notice)
	}
	awaitClosed(t, browser)
}

func TestRevocationRacingAttachmentLeavesNoLiveViewer(t *testing.T) {
	fixture, session, _, service, _ := newAuthStreamFixture(t)
	for range 10 {
		issued := pairDevice(t, service, "Phone")
		browser := startBrowserClient(t, fixture.server, issued.Credential)
		browser.await(t, protocol.Welcome, "")
		browser.action(t, protocol.SessionAttach, protocol.EmptyPayload{})
		if _, err := service.RevokeDevice(context.Background(), issued.DeviceID); err != nil {
			t.Fatal(err)
		}
		awaitClosed(t, browser)
	}
	// No revoked stream kept control, and a local viewer can still attach and
	// take control afterwards.
	if control := session.controller.State(); control.ConnectionID != "" {
		t.Fatalf("revoked stream holds control: %#v", control)
	}
	desktop := startStreamClient(t, fixture.server, protocol.CLI)
	desktop.attach(t)
	desktop.acquire(t)
}

// revokeAfterAdmission authorizes exactly one check (admission in the actor)
// and fails every later check (immediately before encoding).
type revokeAfterAdmission struct{ checks atomic.Int32 }

func (access *revokeAfterAdmission) Check() error {
	if access.checks.Add(1) > 1 {
		return auth.ErrRevoked
	}
	return nil
}

func TestQueuedInputRevokedAfterAdmissionIsNeverEncoded(t *testing.T) {
	_, session, process, _, _ := newAuthStreamFixture(t)
	ctx := context.Background()
	viewer, err := session.AttachViewer(ctx, ViewerConfig{
		ConnectionID: "89999999-9999-4999-8999-999999999991", DeviceID: "89999999-9999-4999-8999-999999999992",
		Generation: rpcSessionGen, Sink: discardViewerSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	grant, err := session.AcquireControl(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	access := &revokeAfterAdmission{}
	identity := MutationIdentity{Generation: rpcSessionGen, ConnectionID: grant.ConnectionID, DeviceID: grant.DeviceID, Lease: grant.Lease, Access: access}
	if _, err := session.SubmitInput(ctx, identity, 1, InputAction{Kind: InputKindText, Text: "queued"}); err != nil {
		t.Fatalf("admission = %v", err)
	}
	if _, err := session.SubmitInput(ctx, identity, 2, InputAction{Kind: InputKindText, Text: "later"}); !errors.Is(err, ErrAccessRevoked) {
		t.Fatalf("post-revocation admission = %v", err)
	}
	// A barrier through the actor proves the queued action was processed.
	if _, err := session.Status(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := process.inputText(); got != "" {
			t.Fatalf("revoked queued input reached the child: %q", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if access.checks.Load() < 2 {
		t.Fatalf("actor did not recheck access before encoding: %d checks", access.checks.Load())
	}
}

type discardViewerSink struct{}

func (discardViewerSink) Send(_ context.Context, _ []byte, drained func()) error {
	drained()
	return nil
}

func TestAdministrationProjectsProfilesAndOrigin(t *testing.T) {
	fixture, _, _, _, _ := newAuthStreamFixture(t)
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	directory := t.TempDir()
	name := "Fixture"
	added, _ := protocol.DecodeRPCPayload[protocol.AdminRPCProjectResultPayload](
		local.admin(t, protocol.AdminProjectAdd, protocol.AdminRPCProjectAddPayload{Path: directory, Name: &name}))
	again, _ := protocol.DecodeRPCPayload[protocol.AdminRPCProjectResultPayload](
		local.admin(t, protocol.AdminProjectAdd, protocol.AdminRPCProjectAddPayload{Path: directory + "/."}))
	if added.Project.Name != "Fixture" || again.Project.ID != added.Project.ID {
		t.Fatalf("project add = %#v then %#v", added, again)
	}
	for _, path := range []string{"relative/path", directory + "/missing"} {
		if response := local.admin(t, protocol.AdminProjectAdd, protocol.AdminRPCProjectAddPayload{Path: path}); response.Type != protocol.AdminError {
			t.Fatalf("invalid project path %q accepted", path)
		}
	}
	projects, _ := protocol.DecodeRPCPayload[protocol.AdminRPCProjectListResultPayload](
		local.admin(t, protocol.AdminProjectList, protocol.BrokerRPCEmptyPayload{}))
	if len(projects.Projects) != 3 {
		t.Fatalf("projects = %#v", projects)
	}
	profiles, _ := protocol.DecodeRPCPayload[protocol.AdminRPCProfileListResultPayload](
		local.admin(t, protocol.AdminProfileList, protocol.BrokerRPCEmptyPayload{}))
	if len(profiles.Profiles) != 1 || profiles.Profiles[0].Executable != "/bin/zsh" {
		t.Fatalf("profiles = %#v", profiles)
	}
	origin, _ := protocol.DecodeRPCPayload[protocol.AdminRPCOriginPayload](
		local.admin(t, protocol.AdminConfigOrigin, protocol.AdminRPCOriginPayload{}))
	if origin.Origin != nil {
		t.Fatalf("unset origin = %v", *origin.Origin)
	}
	bad := "http://mac.example.ts.net"
	if data, err := local.exchange(t, string(protocol.AdminConfigOrigin), protocol.AdminRPCOriginPayload{Origin: &bad}); err == nil {
		t.Fatalf("insecure origin answered instead of rejected: %s", data)
	}
}

func attachPresenceViewer(t *testing.T, session *Session, connectionID, deviceID string) *ViewerSubscription {
	t.Helper()
	viewer, err := session.AttachViewer(context.Background(), ViewerConfig{
		ConnectionID: connectionID, DeviceID: deviceID, Generation: rpcSessionGen, Sink: discardViewerSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = viewer.Close() })
	return viewer
}

func TestSessionPresenceTracksControlAndViewers(t *testing.T) {
	_, session, _ := newStreamFixture(t)
	ctx := context.Background()
	const device = "8aaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	want := func(controller string, viewers int) {
		t.Helper()
		presence, err := session.Presence(ctx, false)
		if err != nil || !reflect.DeepEqual(presence, SessionPresence{ControllerDevice: controller, Viewers: viewers}) {
			t.Fatalf("presence = %#v, %v; want controller %q, %d viewers", presence, err, controller, viewers)
		}
	}
	want("", 0)
	first := attachPresenceViewer(t, session, "8aaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2", device)
	attachPresenceViewer(t, session, "8aaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3", device)
	want("", 2)
	grant, err := session.AcquireControl(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	want(device, 2)
	identity := MutationIdentity{Generation: rpcSessionGen, ConnectionID: grant.ConnectionID, DeviceID: grant.DeviceID, Lease: grant.Lease}
	if err := session.ReleaseControl(ctx, identity); err != nil {
		t.Fatal(err)
	}
	want("", 2)
	if _, err := session.AcquireControl(ctx, first); err != nil {
		t.Fatal(err)
	}
	want(device, 2)
	_ = first.Close()
	want("", 1)
}

func TestGatewaySessionListReportsControllerPerDevice(t *testing.T) {
	fixture, session, _, service, _ := newAuthStreamFixture(t)
	phone := pairDevice(t, service, "Pocket")
	tablet := pairDevice(t, service, "Tablet")
	viewer := attachPresenceViewer(t, session, "8bbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1", phone.DeviceID)
	attachPresenceViewer(t, session, "8bbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2", tablet.DeviceID)
	if _, err := session.AcquireControl(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	list := func(credential string) protocol.BrokerRPCSessionRecord {
		t.Helper()
		response := gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(credential))
		for _, secret := range []string{"8bbbbbbb", "Pocket", "Tablet", phone.DeviceID, tablet.DeviceID} {
			if strings.Contains(string(response.Payload), secret) {
				t.Fatalf("session list leaked %q: %s", secret, response.Payload)
			}
		}
		result, err := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](response)
		if err != nil || len(result.Sessions) != 1 {
			t.Fatalf("list = %#v, %v", result, err)
		}
		return result.Sessions[0]
	}
	check := func(record protocol.BrokerRPCSessionRecord, isYou *bool) {
		t.Helper()
		if record.Controlled == nil || !*record.Controlled || record.ViewerCount == nil || *record.ViewerCount != 2 {
			t.Fatalf("presence = %#v", record)
		}
		if (isYou == nil) != (record.ControllerIsYou == nil) || (isYou != nil && *isYou != *record.ControllerIsYou) {
			t.Fatalf("controllerIsYou = %v, want %v", record.ControllerIsYou, isYou)
		}
	}
	yes, no := true, false
	check(list(phone.Credential), &yes)
	check(list(tablet.Credential), &no)

	cli, err := fixture.server.listSessions(context.Background(), protocol.BrokerRPCSessionListRequestPayload{Limit: 10})
	if err != nil || len(cli.Sessions) != 1 {
		t.Fatalf("cli list = %#v, %v", cli, err)
	}
	check(cli.Sessions[0], nil)
	if cli.Sessions[0].Preview != nil || cli.Sessions[0].LastOutputAt != nil || cli.Sessions[0].Agent != nil {
		t.Fatalf("cli list carries terminal activity: %#v", cli.Sessions[0])
	}
}

// A session that already exited or was lost can stay in the registry for a
// moment. It must not report presence, the same as a session that is gone.
func TestWithPresenceSkipsEndedSessions(t *testing.T) {
	fixture, session, process, service, _ := newAuthStreamFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.Emit([]byte("still here\r\n"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Preview == "still here" })
	viewer := attachPresenceViewer(t, session, "8ccccccc-cccc-4ccc-8ccc-ccccccccccc1", phone.DeviceID)
	if _, err := session.AcquireControl(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	metadata, err := fixture.persistence.GetSession(context.Background(), session.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []protocol.State{protocol.Exited, protocol.Lost} {
		record := managementSessionRecord(metadata)
		record.State = state
		got := fixture.server.withPresence(context.Background(), record, phone.DeviceID)
		if got.Controlled != nil || got.ViewerCount != nil || got.ControllerIsYou != nil ||
			got.LastOutputAt != nil || got.Preview != nil || got.Agent != nil {
			t.Fatalf("%s session has presence: %#v", state, got)
		}
	}
	running := fixture.server.withPresence(context.Background(), managementSessionRecord(metadata), phone.DeviceID)
	if running.Controlled == nil || !*running.Controlled || running.LastOutputAt == nil || running.Preview == nil {
		t.Fatalf("running session lost presence: %#v", running)
	}
}

func waitPresence(t *testing.T, session *Session, ready func(SessionPresence) bool) SessionPresence {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		presence, err := session.Presence(context.Background(), true)
		if err == nil && ready(presence) {
			return presence
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence never became ready: %#v, %v", presence, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lastOutputAt moves only when child output is applied. Resize, snapshots,
// viewer attach/resync and control changes leave it alone.
func TestLastOutputTracksOnlyChildOutput(t *testing.T) {
	_, session, process := newStreamFixture(t)
	ctx := context.Background()
	if presence, err := session.Presence(ctx, true); err != nil || !presence.LastOutput.IsZero() || presence.Preview != "" {
		t.Fatalf("fresh session activity = %#v, %v", presence, err)
	}
	before := time.Now()
	process.Emit([]byte("\x1b[2J\x1b[Hbuild ok\r\n\x1b[1mtests   \ue0b0passed\x1b[0m\r\n"))
	first := waitPresence(t, session, func(presence SessionPresence) bool { return !presence.LastOutput.IsZero() })
	if first.LastOutput.Before(before) {
		t.Fatalf("lastOutput %v precedes the output", first.LastOutput)
	}
	first = waitPresence(t, session, func(presence SessionPresence) bool { return presence.Preview == "tests \ufffdpassed" })

	time.Sleep(5 * time.Millisecond)
	viewer := attachPresenceViewer(t, session, "8ddddddd-dddd-4ddd-8ddd-ddddddddddd1", "8ddddddd-dddd-4ddd-8ddd-ddddddddddd2")
	grant, err := session.AcquireControl(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	identity := MutationIdentity{Generation: rpcSessionGen, ConnectionID: grant.ConnectionID, DeviceID: grant.DeviceID, Lease: grant.Lease}
	if err := session.Resize(ctx, identity, 100, 30); err != nil {
		t.Fatal(err)
	}
	_ = viewer.Resync(ctx)
	if _, err := session.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.ReleaseControl(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if presence, err := session.Presence(ctx, true); err != nil || !presence.LastOutput.Equal(first.LastOutput) {
		t.Fatalf("non-output actions moved lastOutput: %v -> %#v, %v", first.LastOutput, presence, err)
	}

	process.Emit([]byte("next"))
	second := waitPresence(t, session, func(presence SessionPresence) bool { return presence.LastOutput.After(first.LastOutput) })
	if second.Preview != "next" {
		t.Fatalf("preview after output = %q", second.Preview)
	}
	process.Emit([]byte("\x1b[2J"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Preview == "" })
}

func TestGatewaySessionListCarriesActivity(t *testing.T) {
	fixture, _, process, service, _ := newAuthStreamFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.Emit([]byte("$ make\r\n" + strings.Repeat("界", 100) + "\r\n")) // Wraps at 80 columns.
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	want := strings.Repeat("界", 20)
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(phone.Credential))
		result, err := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](response)
		if err != nil || len(result.Sessions) != 1 {
			t.Fatalf("list = %#v, %v", result, err)
		}
		record := result.Sessions[0]
		if record.Preview != nil && *record.Preview == want {
			if record.LastOutputAt == nil || len(*record.LastOutputAt) != len("2026-09-27T00:00:00.000Z") ||
				!strings.HasSuffix(*record.LastOutputAt, "Z") {
				t.Fatalf("lastOutputAt = %v", record.LastOutputAt)
			}
			if _, err := time.Parse(time.RFC3339Nano, *record.LastOutputAt); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway list never carried the preview: %#v", record)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The gateway reads the configured origin without a credential; it is the
// value `pterm config origin` saved, and absent until one is set.
func TestGatewayOriginFollowsConfiguredOrigin(t *testing.T) {
	fixture, _, _, _, _ := newAuthStreamFixture(t)
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	read := func() *string {
		t.Helper()
		result, err := protocol.DecodeRPCPayload[protocol.GatewayRPCOriginResultPayload](
			gateway.gateway(t, protocol.GatewayOrigin, protocol.BrokerRPCEmptyPayload{}))
		if err != nil {
			t.Fatal(err)
		}
		return result.Origin
	}
	if origin := read(); origin != nil {
		t.Fatalf("unset origin = %q", *origin)
	}
	want := "https://mac.example.ts.net"
	if response := local.admin(t, protocol.AdminConfigOrigin, protocol.AdminRPCOriginPayload{Origin: &want}); response.Type != protocol.AdminConfigOriginResult {
		t.Fatalf("set origin = %s", response.Type)
	}
	if origin := read(); origin == nil || *origin != want {
		t.Fatalf("configured origin = %v", origin)
	}
}

// A browser renames only the device its own credential belongs to.
func TestGatewayDeviceRenameTargetsTheCallingDevice(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	alpha := pairDevice(t, service, "Alpha")
	bravo := pairDevice(t, service, "Bravo")

	response := gateway.gateway(t, protocol.GatewayDeviceRename, protocol.GatewayRPCDeviceRenamePayload{
		Credential: alpha.Credential, Request: protocol.HTTPDeviceRenameRequest{Label: "Work phone"},
	})
	summary, err := protocol.DecodeRPCPayload[protocol.HTTPDeviceSummary](response)
	if err != nil || summary.ID != alpha.DeviceID || summary.Label != "Work phone" {
		t.Fatalf("rename = %#v, %v", summary, err)
	}
	devices, _ := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceListResultPayload](
		local.admin(t, protocol.AdminDeviceList, protocol.BrokerRPCEmptyPayload{}))
	labels := map[string]string{}
	for _, device := range devices.Devices {
		labels[device.ID] = device.Label
	}
	if labels[alpha.DeviceID] != "Work phone" || labels[bravo.DeviceID] != "Bravo" {
		t.Fatalf("labels after rename = %v", labels)
	}

	unknown := gateway.gateway(t, protocol.GatewayDeviceRename, protocol.GatewayRPCDeviceRenamePayload{
		Credential: strings.Repeat("A", 43), Request: protocol.HTTPDeviceRenameRequest{Label: "Evil"},
	})
	if code := gatewayErrorCode(t, unknown); code != protocol.Unauthorized {
		t.Fatalf("unknown credential rename = %s", code)
	}
	if _, err := service.RevokeDevice(context.Background(), bravo.DeviceID); err != nil {
		t.Fatal(err)
	}
	revoked := gateway.gateway(t, protocol.GatewayDeviceRename, protocol.GatewayRPCDeviceRenamePayload{
		Credential: bravo.Credential, Request: protocol.HTTPDeviceRenameRequest{Label: "Evil"},
	})
	if code := gatewayErrorCode(t, revoked); code != protocol.ErrorCodeRevoked {
		t.Fatalf("revoked credential rename = %s", code)
	}

	renamed, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceRenamePayload](local.admin(t, protocol.AdminDeviceRename,
		protocol.AdminRPCDeviceRenamePayload{DeviceID: bravo.DeviceID, Label: "Old phone"}))
	if err != nil || renamed.DeviceID != bravo.DeviceID || renamed.Label != "Old phone" {
		t.Fatalf("admin rename = %#v, %v", renamed, err)
	}
	missing := local.admin(t, protocol.AdminDeviceRename,
		protocol.AdminRPCDeviceRenamePayload{DeviceID: "00000000-0000-4000-8000-000000009999", Label: "Ghost"})
	if missing.Type != protocol.AdminError {
		t.Fatalf("rename of a missing device = %s", missing.Type)
	}
}

type acceptingPushService struct{ endpoints chan string }

func (service acceptingPushService) RoundTrip(request *http.Request) (*http.Response, error) {
	service.endpoints <- request.URL.String()
	return &http.Response{StatusCode: http.StatusCreated, Body: http.NoBody, Header: http.Header{}}, nil
}

// Push requests act only on the calling device and apply the host allow-list.
func TestGatewayPushSubscribeTestAndUnsubscribe(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	alpha := pairDevice(t, service, "Alpha")
	credential := protocol.GatewayRPCCredentialPayload{Credential: alpha.Credential}

	if code := gatewayErrorCode(t, gateway.gateway(t, protocol.GatewayPushConfig, credential)); code != protocol.HostUnavailable {
		t.Fatalf("push without a notifier = %s", code)
	}
	pushService := acceptingPushService{endpoints: make(chan string, 4)}
	notifier, err := webpush.NewNotifier(context.Background(), webpush.NotifierConfig{
		Store: fixture.persistence, Revoked: service.DeviceRevoked, CredentialIdle: auth.BrowserInactivityLifetime,
		Client: &http.Client{Transport: pushService},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.config.Push = notifier

	config, err := protocol.DecodeRPCPayload[protocol.HTTPPushConfigResponse](gateway.gateway(t, protocol.GatewayPushConfig, credential))
	if err != nil || config.PublicKey != notifier.PublicKey() {
		t.Fatalf("config = %#v, %v", config, err)
	}
	phone, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	subscription := func(endpoint string) protocol.GatewayRPCPushSubscribePayload {
		return protocol.GatewayRPCPushSubscribePayload{Credential: alpha.Credential, Request: protocol.HTTPPushSubscribeRequest{
			Endpoint: endpoint, Keys: protocol.HTTPPushSubscriptionKeys{
				P256Dh: base64.RawURLEncoding.EncodeToString(phone.PublicKey().Bytes()),
				Auth:   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
			},
		}}
	}
	if code := gatewayErrorCode(t, gateway.gateway(t, protocol.GatewayPushSubscribe,
		subscription("https://metadata.example/latest"))); code != protocol.InvalidRequest {
		t.Fatalf("unknown push host = %s", code)
	}
	endpoint := "https://fcm.googleapis.com/fcm/send/alpha"
	if response := gateway.gateway(t, protocol.GatewayPushSubscribe, subscription(endpoint)); response.Type != protocol.GatewayPushSubscribeResult {
		t.Fatalf("subscribe = %s", response.Type)
	}
	sent, err := protocol.DecodeRPCPayload[protocol.HTTPPushTestResponse](gateway.gateway(t, protocol.GatewayPushTest, credential))
	if err != nil || sent.Sent != 1 || <-pushService.endpoints != endpoint {
		t.Fatalf("test = %#v, %v", sent, err)
	}
	if response := gateway.gateway(t, protocol.GatewayPushUnsubscribe, protocol.GatewayRPCPushUnsubscribePayload{
		Credential: alpha.Credential, Request: protocol.HTTPPushUnsubscribeRequest{Endpoint: endpoint},
	}); response.Type != protocol.GatewayPushUnsubscribeResult {
		t.Fatalf("unsubscribe = %s", response.Type)
	}
	left, _ := fixture.persistence.ListPushSubscriptions(context.Background(), "", time.Now(), auth.BrowserInactivityLifetime, 10)
	if len(left) != 0 {
		t.Fatalf("subscriptions after unsubscribe = %d", len(left))
	}
	unknown := gateway.gateway(t, protocol.GatewayPushConfig, protocol.GatewayRPCCredentialPayload{Credential: strings.Repeat("A", 43)})
	if code := gatewayErrorCode(t, unknown); code != protocol.Unauthorized {
		t.Fatalf("unknown credential = %s", code)
	}
}

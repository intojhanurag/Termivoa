package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

const (
	homeOrigin = "https://vps.tailx.ts.net" // This computer.
	macOrigin  = "https://mac.tailx.ts.net" // ada's laptop: a user-owned app origin.
	ciOrigin   = "https://ci.tailx.ts.net"  // A tagged node.
	piOrigin   = "https://pi.tailx.ts.net"  // bob's node: another owner.
)

// fakeTailscale serves `tailscale status --json` for this computer (vps,
// owned by ada) from mutable peers.
type fakeTailscale struct {
	mu    sync.Mutex
	peers map[string]string // host label -> node ID
	err   error
}

func (fake *fakeTailscale) read(context.Context) ([]byte, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.err != nil {
		return nil, fake.err
	}
	peers := map[string]any{}
	for label, id := range fake.peers {
		peer := map[string]any{"ID": id, "DNSName": label + ".tailx.ts.net.", "UserID": 1}
		switch {
		case label == "ci":
			peer["UserID"], peer["Tags"] = 9, []string{"tag:server"}
		case label == "pi":
			peer["UserID"] = 2
		}
		peers["key-"+label] = peer
	}
	return json.Marshal(map[string]any{
		"BackendState": "Running", "MagicDNSSuffix": "tailx.ts.net",
		"Self": map[string]any{"ID": "nVps", "DNSName": "vps.tailx.ts.net.", "UserID": 1},
		"User": map[string]any{"1": map[string]string{"LoginName": "ada@example.com"},
			"2": map[string]string{"LoginName": "bob@example.com"}, "9": map[string]string{"LoginName": "tagged-devices"}},
		"Peer": peers,
	})
}

func (fake *fakeTailscale) set(label, id string, err error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if label != "" {
		fake.peers[label] = id
	}
	fake.err = err
}

type trustFixture struct {
	*managementFixture
	session   *Session
	service   *auth.Service
	clock     *authClock
	tailscale *fakeTailscale
	monitor   *tailnet.Monitor
	local     *rpcTestClient
	gateway   *rpcTestClient
}

func newTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	fixture, session, _, service, clock := newAuthStreamFixture(t)
	fake := &fakeTailscale{peers: map[string]string{"mac": "nMac", "ci": "nCi", "pi": "nPi"}}
	for index := range 9 {
		fake.peers[fmt.Sprint("pc", index)] = fmt.Sprint("nPc", index)
	}
	monitor := tailnet.NewMonitor(tailnet.Config{Source: fake.read, Now: clock.Now})
	fixture.server.config.Tailnet, fixture.server.config.Now = monitor, clock.Now
	trust := &trustFixture{
		managementFixture: fixture, session: session, service: service, clock: clock, tailscale: fake, monitor: monitor,
		local: startRPCTestClient(t, fixture.server, protocol.CLI), gateway: startRPCTestClient(t, fixture.server, protocol.Gateway),
	}
	origin := homeOrigin
	trust.local.admin(t, protocol.AdminConfigOrigin, protocol.AdminRPCOriginPayload{Origin: &origin})
	trust.refresh(t)
	return trust
}

// refresh publishes the fake's current status as the latest snapshot.
func (fixture *trustFixture) refresh(t *testing.T) {
	t.Helper()
	fixture.clock.Set(fixture.clock.Now().Add(tailnet.DefaultMaxAge))
	if _, err := fixture.monitor.Fresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// from sends one gateway request carrying a forwarded origin ("" = none).
func (fixture *trustFixture) from(t *testing.T, origin string, messageType protocol.GatewayRPCMessageType, payload any) protocol.GatewayRPCEnvelope {
	t.Helper()
	data, err := exchangeFrom(t, fixture.gateway, origin, string(messageType), payload)
	if err != nil {
		t.Fatalf("%s: %v", messageType, err)
	}
	response, err := protocol.DecodeGatewayRPCResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func exchangeFrom(t *testing.T, client *rpcTestClient, origin, messageType string, payload any) ([]byte, error) {
	t.Helper()
	requestID, _ := randomUUID()
	body, _ := json.Marshal(payload)
	message := map[string]any{"version": 1, "type": messageType, "requestId": requestID, "payload": json.RawMessage(body)}
	if origin != "" {
		message["origin"] = origin
	}
	data, _ := json.Marshal(message)
	_ = client.conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := client.writer.WriteFrame(data); err != nil {
		return nil, err
	}
	return client.reader.ReadFrame()
}

func (fixture *trustFixture) grant(t *testing.T, via string, allowTagged bool) protocol.AdminRPCPairGrantResultPayload {
	t.Helper()
	payload := protocol.AdminRPCPairGrantPayload{Via: &via}
	if allowTagged {
		payload.AllowTagged = &allowTagged
	}
	response := fixture.local.admin(t, protocol.AdminPairGrant, payload)
	result, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairGrantResultPayload](response)
	if err != nil {
		t.Fatalf("grant via %s = %s, %v", via, response.Payload, err)
	}
	return result
}

// pairVia pairs a phone through the gateway as the app at origin does.
func (fixture *trustFixture) pairVia(t *testing.T, via, origin string, allowTagged bool) auth.PollResult {
	t.Helper()
	grant := fixture.grant(t, via, allowTagged)
	redeemed, err := protocol.DecodeRPCPayload[protocol.HTTPPairRedeemResponse](fixture.from(t, origin, protocol.GatewayPairRedeem,
		protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Phone"}))
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := fixture.service.Approve(context.Background(), redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	poll, err := protocol.DecodeRPCPayload[protocol.GatewayRPCPairPollResultPayload](fixture.from(t, origin, protocol.GatewayPairPoll,
		protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret}))
	if err != nil || poll.Credential == nil {
		t.Fatalf("poll = %#v, %v", poll, err)
	}
	return auth.PollResult{Approved: true, DeviceID: poll.Response.Device.ID, Credential: *poll.Credential}
}

func (fixture *trustFixture) code(t *testing.T, origin string, messageType protocol.GatewayRPCMessageType, payload any) protocol.ErrorCode {
	t.Helper()
	response := fixture.from(t, origin, messageType, payload)
	if response.Type == protocol.GatewayError {
		return gatewayErrorCode(t, response)
	}
	return ""
}

func (fixture *trustFixture) devices(t *testing.T) map[string]protocol.AdminRPCDevice {
	t.Helper()
	result, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceListResultPayload](
		fixture.local.admin(t, protocol.AdminDeviceList, protocol.BrokerRPCEmptyPayload{}))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.AdminRPCDevice{}
	for _, device := range result.Devices {
		byID[device.ID] = device
	}
	return byID
}

func (fixture *trustFixture) probe(t *testing.T) protocol.GatewayRPCOriginResultPayload {
	t.Helper()
	result, err := protocol.DecodeRPCPayload[protocol.GatewayRPCOriginResultPayload](
		fixture.from(t, "", protocol.GatewayOrigin, protocol.BrokerRPCEmptyPayload{}))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// startBrowserFrom opens a live stream the way the gateway does for a
// WebSocket from origin ("" = own origin).
func startBrowserFrom(t *testing.T, server *ManagementServer, credential, origin string) *streamClient {
	t.Helper()
	rpc := startRPCTestClient(t, server, protocol.Gateway)
	data, err := exchangeFrom(t, rpc, origin, string(protocol.GatewayLiveOpen), credentialPayload(credential))
	if err != nil {
		t.Fatal(err)
	}
	if response, _ := protocol.DecodeGatewayRPCResponse(data); response.Type != protocol.GatewayLiveOpenResult {
		t.Fatalf("live open from %q = %s", origin, data)
	}
	_ = rpc.conn.SetDeadline(time.Time{})
	return adoptStreamClient(t, rpc.conn, rpc.writer, rpc.reader, protocol.Browser)
}

func TestBoundGrantRedeemsAndPollsOnlyFromItsOrigin(t *testing.T) {
	fixture := newTrustFixture(t)
	grant := fixture.grant(t, "mac", false)
	if grant.AppOrigin == nil || *grant.AppOrigin != macOrigin || grant.AppNode == nil || grant.AppNode.ID != "nMac" ||
		grant.AppNode.Owner == nil || *grant.AppNode.Owner != "ada@example.com" {
		t.Fatalf("grant = %#v", grant)
	}
	if probe := fixture.probe(t); !reflect.DeepEqual(probe.PairingOrigins, []string{macOrigin}) || probe.BoundOrigins != nil {
		t.Fatalf("probe while pairing = %#v", probe)
	}
	redeem := protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Phone"}
	for _, origin := range []string{"", homeOrigin, piOrigin, ciOrigin, "https://mac.tailx.ts.net.evil.com", "https://xmac.tailx.ts.net"} {
		if code := fixture.code(t, origin, protocol.GatewayPairRedeem, redeem); code != protocol.InvalidRequest {
			t.Fatalf("redeem from %q = %q", origin, code)
		}
	}
	// "null" and a non-canonical origin never reach dispatch: the request is malformed.
	for _, origin := range []string{"null", "https://mac.tailx.ts.net:443/"} {
		client := startRPCTestClient(t, fixture.server, protocol.Gateway)
		if data, err := exchangeFrom(t, client, origin, string(protocol.GatewayPairRedeem), redeem); err == nil {
			t.Fatalf("origin %q answered: %s", origin, data)
		}
	}
	// The refused attempts did not consume the grant.
	redeemed, err := protocol.DecodeRPCPayload[protocol.HTTPPairRedeemResponse](fixture.from(t, macOrigin, protocol.GatewayPairRedeem, redeem))
	if err != nil {
		t.Fatalf("redeem from the bound origin: %v", err)
	}
	pending, _ := protocol.DecodeRPCPayload[protocol.AdminRPCPairPendingResultPayload](
		fixture.local.admin(t, protocol.AdminPairPending, protocol.BrokerRPCEmptyPayload{}))
	if len(pending.Pending) != 1 || pending.Pending[0].AppOrigin == nil || *pending.Pending[0].AppOrigin != macOrigin ||
		pending.Pending[0].AppNode.ID != "nMac" {
		t.Fatalf("pending = %#v", pending)
	}
	if err := fixture.service.Approve(context.Background(), redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	poll := protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret}
	for _, origin := range []string{"", piOrigin} {
		if code := fixture.code(t, origin, protocol.GatewayPairPoll, poll); code != protocol.InvalidRequest {
			t.Fatalf("poll from %q = %q", origin, code)
		}
	}
	result, err := protocol.DecodeRPCPayload[protocol.GatewayRPCPairPollResultPayload](fixture.from(t, macOrigin, protocol.GatewayPairPoll, poll))
	if err != nil || result.Credential == nil {
		t.Fatalf("poll from the bound origin = %#v, %v", result, err)
	}
	device := fixture.devices(t)[result.Response.Device.ID]
	if device.AppOrigin == nil || *device.AppOrigin != macOrigin || device.AppNode == nil || device.AppNode.ID != "nMac" {
		t.Fatalf("device = %#v", device)
	}
	if probe := fixture.probe(t); !reflect.DeepEqual(probe.BoundOrigins, []string{macOrigin}) || probe.PairingOrigins != nil {
		t.Fatalf("probe after pairing = %#v", probe)
	}

	// An unbound grant pairs only from this computer's own origin.
	plain, _ := protocol.DecodeRPCPayload[protocol.AdminRPCPairGrantResultPayload](
		fixture.local.admin(t, protocol.AdminPairGrant, protocol.AdminRPCPairGrantPayload{}))
	if plain.AppOrigin != nil {
		t.Fatalf("unbound grant = %#v", plain)
	}
	unbound := protocol.HTTPPairRedeemRequest{Grant: plain.Grant, DeviceLabel: "Phone"}
	if code := fixture.code(t, macOrigin, protocol.GatewayPairRedeem, unbound); code != protocol.InvalidRequest {
		t.Fatalf("unbound grant from an app origin = %q", code)
	}
	if code := fixture.code(t, homeOrigin, protocol.GatewayPairRedeem, unbound); code != "" {
		t.Fatalf("unbound grant from own origin = %q", code)
	}
}

func TestPairViaOwnerTaggedAndAmbiguousNames(t *testing.T) {
	fixture := newTrustFixture(t)
	refused := func(via string, allowTagged bool, want string) {
		t.Helper()
		payload := protocol.AdminRPCPairGrantPayload{Via: &via}
		if allowTagged {
			payload.AllowTagged = &allowTagged
		}
		response := fixture.local.admin(t, protocol.AdminPairGrant, payload)
		failure, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
		if response.Type != protocol.AdminError || !strings.Contains(failure.Message, want) {
			t.Fatalf("via %q = %s %#v, want %q", via, response.Type, failure, want)
		}
	}
	refused("pi", false, "another Tailscale user")
	refused("pi", true, "another Tailscale user") // allowTagged never waives the owner rule.
	refused("ci", false, "--allow-tagged")
	refused("vps", false, "no computer") // This computer itself.
	refused("laptop", false, "no computer")
	refused("https://mac.tailx.ts.net.evil.com", false, "no computer")
	if grant := fixture.grant(t, "ci", true); *grant.AppOrigin != ciOrigin || grant.AppNode.Owner != nil ||
		!reflect.DeepEqual(grant.AppNode.Tags, []string{"tag:server"}) {
		t.Fatalf("tagged grant = %#v", grant)
	}
	if grant := fixture.grant(t, "https://mac.tailx.ts.net", false); *grant.AppOrigin != macOrigin {
		t.Fatalf("full origin grant = %#v", grant)
	}
	// allowTagged without via is malformed.
	allow := true
	if data, err := startRPCTestClient(t, fixture.server, protocol.CLI).exchange(t, string(protocol.AdminPairGrant),
		protocol.AdminRPCPairGrantPayload{AllowTagged: &allow}); err == nil {
		t.Fatalf("allowTagged without via answered: %s", data)
	}
	// A short name that matches two own-tailnet nodes is refused.
	fixture.tailscale.set("mac.lab", "nLabMac", nil)
	fixture.refresh(t)
	refused("mac", false, "more than one")
	if grant := fixture.grant(t, macOrigin, false); grant.AppNode.ID != "nMac" {
		t.Fatalf("full origin beats ambiguity: %#v", grant)
	}
	// Without a readable status nothing can be pinned.
	fixture.tailscale.set("", "", errors.New("tailscale stopped"))
	fixture.clock.Set(fixture.clock.Now().Add(tailnet.DefaultMaxAge))
	response := fixture.local.admin(t, protocol.AdminPairGrant, protocol.AdminRPCPairGrantPayload{Via: new("mac")})
	if failure, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response); failure.Code != protocol.HostUnavailable {
		t.Fatalf("via without status = %#v", failure)
	}
}

// Origin binding is per device, never a union: each credential works from
// its own app origin and this computer's own origin only.
func TestPerDeviceOriginBinding(t *testing.T) {
	fixture := newTrustFixture(t)
	viaMac := fixture.pairVia(t, "mac", macOrigin, false)
	viaCi := fixture.pairVia(t, "ci", ciOrigin, true)
	direct := pairDevice(t, fixture.service, "Direct")
	if probe := fixture.probe(t); !reflect.DeepEqual(probe.BoundOrigins, []string{ciOrigin, macOrigin}) {
		t.Fatalf("bound origins = %v", probe.BoundOrigins)
	}
	for _, test := range []struct {
		credential, origin string
		want               protocol.ErrorCode
	}{
		{viaMac.Credential, macOrigin, ""},
		{viaMac.Credential, "", ""},
		{viaMac.Credential, homeOrigin, ""},
		{viaMac.Credential, "http://127.0.0.1:8787", ""}, // the development gateway's own origin
		{viaMac.Credential, ciOrigin, protocol.InvalidRequest},
		{viaMac.Credential, "https://mac.tailx.ts.net.evil.com", protocol.InvalidRequest},
		{viaMac.Credential, "https://xmac.tailx.ts.net", protocol.InvalidRequest},
		{viaCi.Credential, ciOrigin, ""},
		{viaCi.Credential, macOrigin, protocol.InvalidRequest},
		{direct.Credential, "", ""},
		{direct.Credential, macOrigin, protocol.InvalidRequest},
	} {
		for _, messageType := range []protocol.GatewayRPCMessageType{protocol.GatewaySettings, protocol.GatewayAuthCheck, protocol.GatewaySessionList} {
			if code := fixture.code(t, test.origin, messageType, credentialPayload(test.credential)); code != test.want {
				t.Errorf("%s from %q = %q, want %q", messageType, test.origin, code, test.want)
			}
		}
	}
	// A refused live stream, logout and upload change nothing.
	if code := fixture.code(t, ciOrigin, protocol.GatewayLiveOpen, credentialPayload(viaMac.Credential)); code != protocol.InvalidRequest {
		t.Fatalf("live open from another app origin = %q", code)
	}
	if code := fixture.code(t, ciOrigin, protocol.GatewayAuthLogout, credentialPayload(viaMac.Credential)); code != protocol.InvalidRequest {
		t.Fatalf("logout from another app origin = %q", code)
	}
	upload := startRPCTestClient(t, fixture.server, protocol.Gateway)
	data, err := exchangeFrom(t, upload, ciOrigin, string(protocol.GatewayUploadBegin), protocol.GatewayRPCUploadBeginPayload{
		Credential: viaMac.Credential, SessionID: rpcSessionID, Generation: rpcSessionGen, Size: 1})
	if response, _ := protocol.DecodeGatewayRPCResponse(data); err != nil || gatewayErrorCode(t, response) != protocol.InvalidRequest {
		t.Fatalf("upload from another app origin = %s, %v", data, err)
	}
	if devices := fixture.devices(t); devices[viaMac.DeviceID].Status != protocol.Active {
		t.Fatalf("refusals revoked a device: %#v", devices[viaMac.DeviceID])
	}
	if code := fixture.code(t, macOrigin, protocol.GatewayAuthLogout, credentialPayload(viaMac.Credential)); code != "" {
		t.Fatalf("logout from the bound origin = %q", code)
	}
}

// A definite node change refuses at once and revokes the bound devices on the
// next sweep, closing their streams; it never re-binds.
func TestNodeChangeRevokesBoundDevices(t *testing.T) {
	fixture := newTrustFixture(t)
	viaMac := fixture.pairVia(t, "mac", macOrigin, false)
	viaCi := fixture.pairVia(t, "ci", ciOrigin, true)
	direct := pairDevice(t, fixture.service, "Direct")
	fromMac := startBrowserFrom(t, fixture.server, viaMac.Credential, macOrigin)
	fromMac.attach(t)
	ownStream := startBrowserFrom(t, fixture.server, viaMac.Credential, "")
	ownStream.attach(t)
	fromCi := startBrowserFrom(t, fixture.server, viaCi.Credential, ciOrigin)
	fromCi.attach(t)

	fixture.tailscale.set("mac", "nNewMac", nil) // mac reinstalled Tailscale, or someone else took the name
	fixture.refresh(t)
	if code := fixture.code(t, macOrigin, protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != protocol.ErrorCodeRevoked {
		t.Fatalf("request after node change = %q", code)
	}
	if probe := fixture.probe(t); !reflect.DeepEqual(probe.BoundOrigins, []string{ciOrigin}) {
		t.Fatalf("changed origin still in CORS: %v", probe.BoundOrigins)
	}
	fixture.server.sweepTrust(context.Background())
	for _, stream := range []*streamClient{fromMac, ownStream} {
		stream.await(t, protocol.AccessRevoked, "")
		awaitClosed(t, stream)
	}
	devices := fixture.devices(t)
	if !devices[viaMac.DeviceID].Revoked || devices[viaCi.DeviceID].Revoked || devices[direct.DeviceID].Revoked {
		t.Fatalf("devices after node change = %#v", devices)
	}
	// Never re-bound: the old node coming back does not revive the device.
	fixture.tailscale.set("mac", "nMac", nil)
	fixture.refresh(t)
	if code := fixture.code(t, "", protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != protocol.ErrorCodeRevoked {
		t.Fatalf("revoked device after the node returned = %q", code)
	}
	fromCi.acquire(t) // Unaffected.
}

// Unreadable status keeps the last good snapshot for the grace period, then
// refuses bound-origin requests and closes their streams without revoking.
func TestTailscaleGracePeriodRefusesWithoutRevoking(t *testing.T) {
	fixture := newTrustFixture(t)
	viaMac := fixture.pairVia(t, "mac", macOrigin, false)
	fromMac := startBrowserFrom(t, fixture.server, viaMac.Credential, macOrigin)
	fromMac.attach(t)
	ownStream := startBrowserFrom(t, fixture.server, viaMac.Credential, "")
	ownStream.attach(t)

	fixture.tailscale.set("", "", errors.New("tailscaled is not running"))
	fixture.clock.Set(fixture.clock.Now().Add(tailnet.DefaultGrace - time.Second))
	fixture.server.sweepTrust(context.Background())
	if code := fixture.code(t, macOrigin, protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != "" {
		t.Fatalf("request inside the grace period = %q", code)
	}
	fixture.clock.Set(fixture.clock.Now().Add(time.Second))
	if code := fixture.code(t, macOrigin, protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != protocol.HostUnavailable {
		t.Fatalf("request after the grace period = %q", code)
	}
	if code := fixture.code(t, macOrigin, protocol.GatewayLiveOpen, credentialPayload(viaMac.Credential)); code != protocol.HostUnavailable {
		t.Fatalf("live open after the grace period = %q", code)
	}
	if code := fixture.code(t, "", protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != "" {
		t.Fatalf("own-origin request after the grace period = %q", code)
	}
	if probe := fixture.probe(t); probe.BoundOrigins != nil {
		t.Fatalf("untrusted origin in CORS: %v", probe.BoundOrigins)
	}
	fixture.server.sweepTrust(context.Background())
	fromMac.await(t, protocol.AccessRevoked, "")
	awaitClosed(t, fromMac)
	if control := fixture.session.controller.State(); control.ConnectionID != "" {
		t.Fatalf("closed stream kept control: %#v", control)
	}
	ownStream.acquire(t) // The same device from this computer's own origin is unaffected.
	if devices := fixture.devices(t); devices[viaMac.DeviceID].Revoked {
		t.Fatal("grace expiry revoked the device")
	}
	fixture.tailscale.set("", "", nil)
	fixture.refresh(t)
	if code := fixture.code(t, macOrigin, protocol.GatewaySettings, credentialPayload(viaMac.Credential)); code != "" {
		t.Fatalf("request after recovery = %q", code)
	}
}

// revoke --via revokes every device bound to the origin through the normal
// revocation path: their streams close, control is released, and pairings
// bound to the origin are dropped. Other devices are unaffected.
func TestRevokeViaClosesStreamsAndDropsPairings(t *testing.T) {
	fixture := newTrustFixture(t)
	first := fixture.pairVia(t, "mac", macOrigin, false)
	second := fixture.pairVia(t, "mac", macOrigin, false)
	viaCi := fixture.pairVia(t, "ci", ciOrigin, true)
	direct := pairDevice(t, fixture.service, "Direct")
	controller := startBrowserFrom(t, fixture.server, first.Credential, macOrigin)
	controller.attach(t)
	controller.acquire(t)
	bystander := startBrowserFrom(t, fixture.server, direct.Credential, "")
	bystander.attach(t)
	waiting := fixture.grant(t, "mac", false)

	response := fixture.local.admin(t, protocol.AdminDeviceRevokeVia, protocol.AdminRPCRevokeViaPayload{AppOrigin: macOrigin})
	result, err := protocol.DecodeRPCPayload[protocol.AdminRPCRevokeViaResultPayload](response)
	if err != nil {
		t.Fatalf("revoke via = %s", response.Payload)
	}
	got := map[string]bool{}
	for _, id := range result.DeviceIDS {
		got[id] = true
	}
	if len(got) != 2 || !got[first.DeviceID] || !got[second.DeviceID] || result.ClosedStreams != 1 || result.AppOrigin != macOrigin {
		t.Fatalf("revoke via result = %#v", result)
	}
	controller.await(t, protocol.AccessRevoked, "")
	awaitClosed(t, controller)
	if control := fixture.session.controller.State(); control.ConnectionID != "" {
		t.Fatalf("revoked stream kept control: %#v", control)
	}
	if code := fixture.code(t, macOrigin, protocol.GatewayPairRedeem,
		protocol.HTTPPairRedeemRequest{Grant: waiting.Grant, DeviceLabel: "Phone"}); code != protocol.PairingExpired {
		t.Fatalf("grant via the revoked origin = %q", code)
	}
	if probe := fixture.probe(t); !reflect.DeepEqual(probe.BoundOrigins, []string{ciOrigin}) || probe.PairingOrigins != nil {
		t.Fatalf("probe after revoke via = %#v", probe)
	}
	devices := fixture.devices(t)
	if !devices[first.DeviceID].Revoked || !devices[second.DeviceID].Revoked || devices[viaCi.DeviceID].Revoked || devices[direct.DeviceID].Revoked {
		t.Fatalf("devices = %#v", devices)
	}
	bystander.acquire(t)
	// Repeating it finds nothing more to revoke.
	again, _ := protocol.DecodeRPCPayload[protocol.AdminRPCRevokeViaResultPayload](
		fixture.local.admin(t, protocol.AdminDeviceRevokeVia, protocol.AdminRPCRevokeViaPayload{AppOrigin: macOrigin}))
	if len(again.DeviceIDS) != 0 || again.ClosedStreams != 0 {
		t.Fatalf("second revoke via = %#v", again)
	}
}

func TestComputerListPerDevice(t *testing.T) {
	fixture := newTrustFixture(t)
	phone := pairDevice(t, fixture.service, "Phone")
	other := pairDevice(t, fixture.service, "Tablet")
	add := func(credential, origin string, state protocol.HTTPComputerState) protocol.GatewayRPCEnvelope {
		return fixture.from(t, "", protocol.GatewayComputersAdd, protocol.GatewayRPCComputerAddPayload{
			Credential: credential, Request: protocol.HTTPComputerAddRequest{Origin: origin, Label: "mac", State: state}})
	}
	list := func(credential string) protocol.GatewayRPCComputerListResultPayload {
		result, err := protocol.DecodeRPCPayload[protocol.GatewayRPCComputerListResultPayload](
			fixture.from(t, "", protocol.GatewayComputersList, credentialPayload(credential)))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	if code := gatewayErrorCode(t, add(phone.Credential, macOrigin, protocol.Confirmed)); code != protocol.InvalidRequest {
		t.Fatalf("confirm before pending = %q", code)
	}
	added, err := protocol.DecodeRPCPayload[protocol.HTTPComputerAddResponse](add(phone.Credential, macOrigin, protocol.HTTPComputerStatePending))
	if err != nil || added.State != protocol.HTTPComputerStatePending || added.Node.ID != "nMac" || *added.Node.Owner != "ada@example.com" {
		t.Fatalf("pending add = %#v, %v", added, err)
	}
	if got := list(phone.Credential); len(got.Computers) != 0 || !reflect.DeepEqual(got.PendingOrigins, []string{macOrigin}) {
		t.Fatalf("list while pending = %#v", got)
	}
	if got := list(other.Credential); len(got.Computers) != 0 || len(got.PendingOrigins) != 0 {
		t.Fatalf("other device sees the list: %#v", got)
	}
	if added, _ = protocol.DecodeRPCPayload[protocol.HTTPComputerAddResponse](add(phone.Credential, macOrigin, protocol.Confirmed)); added.State != protocol.Confirmed {
		t.Fatalf("confirm = %#v", added)
	}
	if got := list(phone.Credential); !reflect.DeepEqual(got.Computers, []protocol.HTTPComputer{{Origin: macOrigin, Label: "mac"}}) || len(got.PendingOrigins) != 0 {
		t.Fatalf("list after confirm = %#v", got)
	}
	tagged, err := protocol.DecodeRPCPayload[protocol.HTTPComputerAddResponse](add(phone.Credential, ciOrigin, protocol.HTTPComputerStatePending))
	if err != nil || tagged.Node.Owner != nil || !reflect.DeepEqual(tagged.Node.Tags, []string{"tag:server"}) {
		t.Fatalf("tagged computer = %#v, %v", tagged, err)
	}
	for _, origin := range []string{piOrigin, homeOrigin, "https://gone.tailx.ts.net", "https://box.taily.ts.net"} {
		if code := gatewayErrorCode(t, add(phone.Credential, origin, protocol.HTTPComputerStatePending)); code != protocol.InvalidRequest {
			t.Fatalf("add %s = %q", origin, code)
		}
	}
	// At most 8 per device, pending and confirmed together.
	for index := range 6 {
		if response := add(phone.Credential, fmt.Sprintf("https://pc%d.tailx.ts.net", index), protocol.HTTPComputerStatePending); response.Type != protocol.GatewayComputersAddResult {
			t.Fatalf("add pc%d = %s", index, response.Payload)
		}
	}
	if code := gatewayErrorCode(t, add(phone.Credential, "https://pc8.tailx.ts.net", protocol.HTTPComputerStatePending)); code != protocol.LimitExceeded {
		t.Fatalf("ninth computer = %q", code)
	}
	// Pending entries expire and are deleted; confirmed ones stay.
	fixture.clock.Set(fixture.clock.Now().Add(PendingComputerLifetime))
	fixture.refresh(t)
	if got := list(phone.Credential); len(got.Computers) != 1 || len(got.PendingOrigins) != 0 {
		t.Fatalf("list after pending expiry = %#v", got)
	}
	if response := add(phone.Credential, "https://pc8.tailx.ts.net", protocol.HTTPComputerStatePending); response.Type != protocol.GatewayComputersAddResult {
		t.Fatalf("expired entries still hold slots: %s", response.Payload)
	}
	add(other.Credential, macOrigin, protocol.HTTPComputerStatePending)
	add(other.Credential, macOrigin, protocol.Confirmed)
	all, _ := protocol.DecodeRPCPayload[protocol.AdminRPCComputerListResultPayload](
		fixture.local.admin(t, protocol.AdminComputersList, protocol.BrokerRPCEmptyPayload{}))
	if len(all.Computers) != 2 || all.Computers[0].Origin != macOrigin || all.Computers[1].Origin != macOrigin {
		t.Fatalf("admin list = %#v", all)
	}
	if response := fixture.from(t, "", protocol.GatewayComputersRemove, protocol.GatewayRPCComputerRemovePayload{
		Credential: phone.Credential, Origin: macOrigin}); response.Type != protocol.GatewayComputersRemoveResult {
		t.Fatalf("remove = %s", response.Payload)
	}
	if got := list(phone.Credential); len(got.Computers) != 0 {
		t.Fatalf("list after remove = %#v", got)
	}
	removed, _ := protocol.DecodeRPCPayload[protocol.AdminRPCComputerRemoveResultPayload](
		fixture.local.admin(t, protocol.AdminComputersRemove, protocol.AdminRPCComputerRemovePayload{Origin: macOrigin}))
	if removed.Removed != 1 {
		t.Fatalf("admin remove = %#v", removed)
	}
}

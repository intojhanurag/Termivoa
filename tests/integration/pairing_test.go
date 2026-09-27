//go:build darwin || windows

package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const pairingOperationID = "c6666666-6666-4666-8666-666666666666"

// TestPairedBrowserControlsSessionUntilRevoked drives the real broker through
// the gateway client exactly as the browser gateway will: pairing, local
// approval, one-time credential issuance, authenticated session creation, a
// credential-bound live stream with control and input, then local revocation.
func TestPairedBrowserControlsSessionUntilRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtimeDir, state := startLiveBroker(t, ctx)
	admin, err := localclient.DialAdmin(ctx, runtimeDir, "pairing-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	gateway, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: runtimeDir, ClientBuild: "pairing-integration"})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()

	grant, err := admin.CreatePairingGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := gateway.RedeemPairing(ctx, protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Integration phone"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := admin.PendingPairings(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != redeemed.PendingID || pending[0].VerificationCode != redeemed.VerificationCode {
		t.Fatalf("local pending view = %#v, %v", pending, err)
	}
	poll := protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret}
	if result, err := gateway.PollPairing(ctx, poll); err != nil || result.Response.Status != protocol.Pending || result.Credential != "" {
		t.Fatalf("pending poll = %#v, %v", result, err)
	}
	if err := admin.ApprovePairing(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	issued, err := gateway.PollPairing(ctx, poll)
	if err != nil || issued.Response.Status != protocol.Approved || issued.Credential == "" {
		t.Fatalf("approved poll = %#v, %v", issued, err)
	}
	credential := issued.Credential
	principal, err := gateway.Authenticate(ctx, credential)
	if err != nil || principal.DeviceLabel != "Integration phone" || !principal.ExpiresAt.Equal(issued.CredentialExpiresAt) {
		t.Fatalf("principal = %#v, %v", principal, err)
	}
	settings, err := gateway.Settings(ctx, credential)
	if err != nil || settings.BrokerGeneration != state.Generation {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	created, err := gateway.CreateSession(ctx, credential, protocol.HTTPSessionCreateRequest{
		OperationID: pairingOperationID, ProjectID: liveProjectID, ShellProfileID: liveProfileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := state.Registry.Get(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitLiveScreen(t, ctx, managed, "BOOT_QUERY_OK")
	waitListedPreview(t, ctx, gateway, credential, "GRID 界 e\u0301 👩\u200d💻")

	stream, err := gateway.OpenLive(ctx, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	live := &browserStream{t: t, ctx: ctx, stream: stream, session: created.Session}
	live.send(protocol.Hello, false, protocol.HelloPayload{
		ClientKind: protocol.Browser, ClientBuild: "pairing-integration",
		CapabilityProfile: protocol.PhoneTerminalV1, SupportedVersions: []int64{protocol.Version},
	})
	live.await(protocol.Welcome)
	live.send(protocol.SessionAttach, true, protocol.EmptyPayload{})
	begin, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](live.await(protocol.SnapshotBegin))
	live.await(protocol.SnapshotEnd)
	live.send(protocol.SnapshotApplied, true, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})
	acquire := live.send(protocol.ControlAcquire, true, protocol.EmptyPayload{})
	var grantPayload protocol.ControlChangedPayload
	for grantPayload.Lease == nil {
		message := live.await(protocol.ControlChanged)
		if message.RequestID == acquire {
			grantPayload, _ = protocol.DecodePayload[protocol.ControlChangedPayload](message)
		}
	}
	live.send(protocol.InputText, true, protocol.InputTextPayload{Lease: *grantPayload.Lease, InputSequence: "1", Text: "PAIRED"})
	live.await(protocol.InputAccepted)
	live.send(protocol.InputKey, true, protocol.InputKeyPayload{Lease: *grantPayload.Lease, InputSequence: "2", Key: protocol.Enter})
	live.await(protocol.InputAccepted)
	waitLiveScreen(t, ctx, managed, liveReceipt(1, "PAIRED"))
	waitListedPreview(t, ctx, gateway, credential, liveReceipt(1, "PAIRED"))

	devices, err := admin.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].ID != principal.DeviceID {
		t.Fatalf("devices = %#v, %v", devices, err)
	}
	revoked, err := admin.RevokeDevice(ctx, principal.DeviceID)
	if err != nil || revoked.ClosedStreams != 1 {
		t.Fatalf("revoke = %#v, %v", revoked, err)
	}
	live.await(protocol.AccessRevoked)
	if _, err := stream.Receive(ctx); !errors.Is(err, gatewayclient.ErrAccessRevoked) {
		t.Fatalf("receive after access.revoked = %v", err)
	}
	var remote *gatewayapi.Error
	if _, err := gateway.Authenticate(ctx, credential); !errors.As(err, &remote) || remote.Code != protocol.ErrorCodeRevoked {
		t.Fatalf("authenticate after revoke = %v", err)
	}
	if _, err := gateway.OpenLive(ctx, credential); !errors.As(err, &remote) || remote.Code != protocol.ErrorCodeRevoked {
		t.Fatalf("open live after revoke = %v", err)
	}
	status, err := managed.Status(ctx)
	if err != nil || status.State != "running" {
		t.Fatalf("revocation changed the managed child: %#v, %v", status, err)
	}
}

// waitListedPreview polls the browser session list until the live session's
// preview is the wanted last non-blank row and lastOutputAt is present.
func waitListedPreview(t *testing.T, ctx context.Context, gateway *gatewayclient.Client, credential, want string) {
	t.Helper()
	waitLiveCondition(t, ctx, "session list preview "+want, func() bool {
		list, err := gateway.ListSessions(ctx, credential)
		if err != nil || len(list.Sessions) != 1 {
			return false
		}
		record := list.Sessions[0]
		return record.Preview != nil && *record.Preview == want && record.LastOutputAt != nil
	})
}

type browserStream struct {
	t       *testing.T
	ctx     context.Context
	stream  gatewayapi.LiveStream
	session protocol.BrokerRPCSessionRecord
}

func (live *browserStream) send(kind protocol.MessageType, sessionScoped bool, payload any) string {
	live.t.Helper()
	requestID, err := randomRequestID()
	if err != nil {
		live.t.Fatal(err)
	}
	body, _ := json.Marshal(payload)
	envelope := protocol.Envelope{Version: protocol.Version, Type: kind, RequestID: requestID, Payload: body}
	if sessionScoped {
		envelope.SessionID, envelope.Generation = &live.session.ID, &live.session.Generation
	}
	data, _ := json.Marshal(envelope)
	if err := live.stream.Send(live.ctx, data); err != nil {
		live.t.Fatalf("send %s: %v", kind, err)
	}
	return requestID
}

func (live *browserStream) await(kind protocol.MessageType) protocol.Envelope {
	live.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(live.ctx, 5*time.Second)
		data, err := live.stream.Receive(ctx)
		cancel()
		if err != nil {
			live.t.Fatalf("waiting for %s: %v", kind, err)
		}
		envelope, err := protocol.DecodeHost(data)
		if err != nil {
			live.t.Fatal(err)
		}
		if envelope.Type == kind {
			return envelope
		}
		if envelope.Type == protocol.Error || envelope.Type == protocol.InputRejected {
			live.t.Fatalf("waiting for %s received %s: %s", kind, envelope.Type, envelope.Payload)
		}
	}
}

func randomRequestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for index, value := range id {
		if index == 4 || index == 6 || index == 8 || index == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[value>>4], hexDigits[value&0x0f])
	}
	return string(out), nil
}

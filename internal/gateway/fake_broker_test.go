package gateway

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	validCredential   = strings.Repeat("A", 43)
	revokedCredential = strings.Repeat("R", 43)
	clockCredential   = strings.Repeat("K", 43) // broker clock stepped back
	testSecret        = strings.Repeat("s", 43)
)

const (
	uuidA = "11111111-1111-4111-8111-111111111111"
	uuidB = "22222222-2222-4222-9222-222222222222"
	uuidC = "33333333-3333-4333-a333-333333333333"
)

// fakeBroker authorizes validCredential only and records calls.
type fakeBroker struct {
	mu          sync.Mutex
	calls       []string
	pollResults []gatewayapi.PollResult
	pollErr     error
	opErr       error // returned by session operations when set
	opened      chan *fakeStream
	origin      string // returned by Origin
	previews    map[uint16]string
	previewErr  error // returned by PreviewTarget for an authorized credential
	offerErr    error // returned by OfferComputer
	offers      []protocol.HTTPComputerOfferRequest
	found       []protocol.HTTPFoundComputer
}

func newFakeBroker() *fakeBroker { return &fakeBroker{opened: make(chan *fakeStream, 4)} }

func (broker *fakeBroker) record(call string) {
	broker.mu.Lock()
	broker.calls = append(broker.calls, call)
	broker.mu.Unlock()
}

func (broker *fakeBroker) called(call string) bool {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for _, value := range broker.calls {
		if value == call {
			return true
		}
	}
	return false
}

func (broker *fakeBroker) authorize(credential string) error {
	switch credential {
	case validCredential:
		return nil
	case revokedCredential:
		return &gatewayapi.Error{Code: protocol.ErrorCodeRevoked, Message: "access revoked"}
	case clockCredential:
		return &gatewayapi.Error{Code: protocol.HostUnavailable, Message: "host clock changed; retry"}
	default:
		return &gatewayapi.Error{Code: protocol.Unauthorized, Message: "sign in required"}
	}
}

func (broker *fakeBroker) session() protocol.HTTPSessionMutationResponse {
	return protocol.HTTPSessionMutationResponse{OperationID: uuidA, Session: protocol.BrokerRPCSessionRecord{
		ID: uuidB, Generation: uuidC, ProjectID: uuidA, ShellProfileID: uuidA, Title: "shell",
		Columns: 80, Rows: 24, State: protocol.Running, CreatedAt: "2026-09-27T00:00:00Z", UpdatedAt: "2026-09-27T00:00:00Z",
	}}
}

func (broker *fakeBroker) RedeemPairing(_ context.Context, request protocol.HTTPPairRedeemRequest) (protocol.HTTPPairRedeemResponse, error) {
	broker.record("redeem")
	return protocol.HTTPPairRedeemResponse{
		PendingID: uuidA, PollSecret: testSecret, VerificationCode: "123-456", ExpiresAt: "2026-09-27T00:02:00Z",
	}, nil
}

func (broker *fakeBroker) PollPairing(context.Context, protocol.HTTPPairPollRequest) (gatewayapi.PollResult, error) {
	broker.record("poll")
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if len(broker.pollResults) == 0 {
		return gatewayapi.PollResult{}, broker.pollErr
	}
	result := broker.pollResults[0]
	broker.pollResults = broker.pollResults[1:]
	return result, nil
}

func (broker *fakeBroker) Authenticate(_ context.Context, credential string) (gatewayapi.Principal, error) {
	broker.record("authenticate")
	return gatewayapi.Principal{DeviceID: uuidA, DeviceLabel: "phone", CredentialID: uuidB, ExpiresAt: time.Now().Add(time.Hour)},
		broker.authorize(credential)
}

func (broker *fakeBroker) Logout(_ context.Context, credential string) error {
	broker.record("logout")
	return broker.authorize(credential)
}

func (broker *fakeBroker) Settings(_ context.Context, credential string) (protocol.HTTPSettingsResponse, error) {
	broker.record("settings")
	return protocol.HTTPSettingsResponse{
		Device: protocol.HTTPDeviceSummary{ID: uuidA, Label: "phone"}, Projects: []protocol.HTTPNamedOption{},
		ShellProfiles: []protocol.HTTPNamedOption{}, HostBuild: "test", BrokerGeneration: uuidC,
		ProtocolRevision: protocol.ProtocolRevision, MinProtocolRevision: protocol.MinProtocolRevision,
	}, broker.authorize(credential)
}

func (broker *fakeBroker) ListSessions(_ context.Context, credential string) (protocol.HTTPSessionListResponse, error) {
	broker.record("list")
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPSessionListResponse{}, err
	}
	return protocol.HTTPSessionListResponse{Sessions: []protocol.BrokerRPCSessionRecord{broker.session().Session}}, broker.opErr
}

func (broker *fakeBroker) mutate(call, credential string) (protocol.HTTPSessionMutationResponse, error) {
	broker.record(call)
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPSessionMutationResponse{}, err
	}
	return broker.session(), broker.opErr
}

func (broker *fakeBroker) CreateSession(_ context.Context, credential string, _ protocol.HTTPSessionCreateRequest) (protocol.HTTPSessionMutationResponse, error) {
	return broker.mutate("create", credential)
}

func (broker *fakeBroker) RenameSession(_ context.Context, credential, sessionID string, _ protocol.HTTPSessionRenameRequest) (protocol.HTTPSessionMutationResponse, error) {
	return broker.mutate("rename:"+sessionID, credential)
}

func (broker *fakeBroker) TerminateSession(_ context.Context, credential, sessionID string, _ protocol.HTTPSessionTerminateRequest) (protocol.HTTPSessionMutationResponse, error) {
	return broker.mutate("terminate:"+sessionID, credential)
}

func (broker *fakeBroker) RenameDevice(_ context.Context, credential string, request protocol.HTTPDeviceRenameRequest) (protocol.HTTPDeviceSummary, error) {
	broker.record("rename-device:" + request.Label)
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPDeviceSummary{}, err
	}
	return protocol.HTTPDeviceSummary{ID: uuidA, Label: request.Label}, broker.opErr
}

func (broker *fakeBroker) PushConfig(_ context.Context, credential string) (protocol.HTTPPushConfigResponse, error) {
	broker.record("push-config")
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPPushConfigResponse{}, err
	}
	return protocol.HTTPPushConfigResponse{PublicKey: "B" + strings.Repeat("A", 86)}, broker.opErr
}

func (broker *fakeBroker) PushSubscribe(_ context.Context, credential string, request protocol.HTTPPushSubscribeRequest) error {
	broker.record("push-subscribe:" + request.Endpoint)
	if err := broker.authorize(credential); err != nil {
		return err
	}
	return broker.opErr
}

func (broker *fakeBroker) PushUnsubscribe(_ context.Context, credential string, request protocol.HTTPPushUnsubscribeRequest) error {
	broker.record("push-unsubscribe:" + request.Endpoint)
	if err := broker.authorize(credential); err != nil {
		return err
	}
	return broker.opErr
}

func (broker *fakeBroker) PushTest(_ context.Context, credential string) (protocol.HTTPPushTestResponse, error) {
	broker.record("push-test")
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPPushTestResponse{}, err
	}
	return protocol.HTTPPushTestResponse{Sent: 1}, broker.opErr
}

func (broker *fakeBroker) Upload(_ context.Context, credential, sessionID, generation string, data []byte) (protocol.HTTPUploadResponse, error) {
	broker.record("upload:" + sessionID + "/" + generation + "/" + string(data))
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	return protocol.HTTPUploadResponse{
		Path: ".termivoa/uploads/20260927-143012-a1b2c3d4.png", Size: int64(len(data)), ContentType: protocol.ImagePNG,
	}, broker.opErr
}

// PreviewTarget allows port 5173 of uuidB (owned, IPv4) and port 5174 of
// uuidB (owned, IPv6 only); everything else is not a dev server of it. It
// records the origin the gateway forwarded ("preview:<session>:<port>@<origin>";
// own origin leaves it empty).
func (broker *fakeBroker) PreviewTarget(ctx context.Context, credential, sessionID string, port uint16) (string, error) {
	origin, _ := gatewayapi.ForwardedOrigin(ctx)
	broker.record("preview:" + sessionID + ":" + strconv.Itoa(int(port)) + "@" + origin)
	if err := broker.authorize(credential); err != nil {
		return "", err
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.previewErr != nil {
		return "", broker.previewErr
	}
	if host, ok := broker.previews[port]; ok && sessionID == uuidB {
		return host, nil
	}
	return "", &gatewayapi.Error{Code: protocol.PreviewMissing, Message: "no dev server"}
}

func (broker *fakeBroker) setPreview(port uint16, host string) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.previews == nil {
		broker.previews = map[uint16]string{}
	}
	if host == "" {
		delete(broker.previews, port)
	} else {
		broker.previews[port] = host
	}
}

func (broker *fakeBroker) setOrigin(origin string) {
	broker.mu.Lock()
	broker.origin = origin
	broker.mu.Unlock()
}

func (broker *fakeBroker) OriginProbe(context.Context) (protocol.GatewayRPCOriginResultPayload, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.origin == "" {
		return protocol.GatewayRPCOriginResultPayload{}, nil
	}
	origin := broker.origin
	return protocol.GatewayRPCOriginResultPayload{Origin: &origin}, nil
}

func (broker *fakeBroker) ListComputers(_ context.Context, credential string) (protocol.GatewayRPCComputerListResultPayload, error) {
	return protocol.GatewayRPCComputerListResultPayload{Computers: []protocol.HTTPComputer{}, PendingOrigins: []string{}},
		broker.authorize(credential)
}

func (broker *fakeBroker) AddComputer(_ context.Context, credential string, request protocol.HTTPComputerAddRequest) (protocol.HTTPComputerAddResponse, error) {
	return protocol.HTTPComputerAddResponse{Computer: protocol.HTTPComputer{Origin: request.Origin, Label: request.Label},
		State: request.State, Node: protocol.TailnetNode{ID: "nFake", Tags: []string{}}}, broker.authorize(credential)
}

func (broker *fakeBroker) RemoveComputer(_ context.Context, credential, _ string) error {
	return broker.authorize(credential)
}

// OfferComputer records the offer with the origin the gateway forwarded
// ("offer@<origin>"; the offer route must never forward one).
func (broker *fakeBroker) OfferComputer(ctx context.Context, request protocol.HTTPComputerOfferRequest) error {
	origin, _ := gatewayapi.ForwardedOrigin(ctx)
	broker.record("offer@" + origin)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.offers = append(broker.offers, request)
	return broker.offerErr
}

// FoundComputers refuses a forwarded origin like the broker's authorizeOwn.
func (broker *fakeBroker) FoundComputers(ctx context.Context, credential string) (protocol.HTTPComputerFoundResponse, error) {
	origin, _ := gatewayapi.ForwardedOrigin(ctx)
	broker.record("found@" + origin)
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPComputerFoundResponse{}, err
	}
	if origin != "" {
		return protocol.HTTPComputerFoundResponse{}, &gatewayapi.Error{Code: protocol.InvalidRequest, Message: "this app is not allowed to use this computer"}
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return protocol.HTTPComputerFoundResponse{Computers: append([]protocol.HTTPFoundComputer{}, broker.found...)}, broker.opErr
}

func (broker *fakeBroker) OpenLive(_ context.Context, credential string) (gatewayapi.LiveStream, error) {
	broker.record("open")
	if err := broker.authorize(credential); err != nil {
		return nil, err
	}
	stream := &fakeStream{
		toPhone: make(chan []byte, 64), fromPhone: make(chan []byte, 64),
		end: make(chan struct{}), closed: make(chan struct{}),
	}
	broker.opened <- stream
	return stream, nil
}

// fakeStream: tests push broker messages on toPhone and close end to finish
// the stream after queued messages drain.
type fakeStream struct {
	toPhone, fromPhone chan []byte
	end, closed        chan struct{}
	endOnce, closeOnce sync.Once
	sendErr            atomic.Pointer[gatewayapi.Error]
	sends              atomic.Int32
}

func (stream *fakeStream) finish() { stream.endOnce.Do(func() { close(stream.end) }) }

func (stream *fakeStream) Send(ctx context.Context, envelope []byte) error {
	defer stream.sends.Add(1)
	select {
	case <-stream.end: // like gatewayclient after access.revoked
		return gatewayapi.ErrStreamEnded
	default:
	}
	if rejected := stream.sendErr.Load(); rejected != nil {
		return rejected
	}
	select {
	case stream.fromPhone <- envelope:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stream.closed:
		return gatewayapi.ErrStreamEnded
	}
}

func (stream *fakeStream) Receive(ctx context.Context) ([]byte, error) {
	select {
	case data := <-stream.toPhone:
		return data, nil
	default:
	}
	select {
	case data := <-stream.toPhone:
		return data, nil
	case <-stream.end:
		return nil, gatewayapi.ErrStreamEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-stream.closed:
		return nil, gatewayapi.ErrStreamEnded
	}
}

func (stream *fakeStream) Close() error {
	stream.closeOnce.Do(func() { close(stream.closed) })
	return nil
}

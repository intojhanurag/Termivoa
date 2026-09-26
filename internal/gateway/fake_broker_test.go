package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	validCredential   = strings.Repeat("A", 43)
	revokedCredential = strings.Repeat("R", 43)
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
		return &gatewayapi.Error{Code: protocol.Revoked, Message: "access revoked"}
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
}

var errStreamEnded = errors.New("stream ended")

func (stream *fakeStream) finish() { stream.endOnce.Do(func() { close(stream.end) }) }

func (stream *fakeStream) Send(ctx context.Context, envelope []byte) error {
	select {
	case stream.fromPhone <- envelope:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stream.closed:
		return errStreamEnded
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
		return nil, errStreamEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-stream.closed:
		return nil, errStreamEnded
	}
}

func (stream *fakeStream) Close() error {
	stream.closeOnce.Do(func() { close(stream.closed) })
	return nil
}

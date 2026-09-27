package gatewayclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	testBroker     = "10000000-0000-4000-8000-000000000001"
	testPending    = "10000000-0000-4000-8000-000000000002"
	testDevice     = "10000000-0000-4000-8000-000000000003"
	testCredID     = "10000000-0000-4000-8000-000000000004"
	testSession    = "10000000-0000-4000-8000-000000000005"
	testGeneration = "10000000-0000-4000-8000-000000000006"
	testProject    = "10000000-0000-4000-8000-000000000007"
	testProfile    = "10000000-0000-4000-8000-000000000008"
	testOperation  = "10000000-0000-4000-8000-000000000009"
	testOther      = "10000000-0000-4000-8000-00000000000a"
)

var (
	testCredential = strings.Repeat("C", 43)
	testGrant      = strings.Repeat("G", 43)
	testPollSecret = strings.Repeat("P", 43)
	testIssued     = strings.Repeat("I", 43)
	testOrigin     = "https://mac.example.ts.net"
)

// handler answers one validated request on the numbered connection (from 1).
// A nil response leaves the request unanswered until the connection closes.
type handler func(connection int, request protocol.GatewayRPCEnvelope) []byte

// pipeClient returns a client whose dials create in-memory connections served
// by handle, and the number of dials so far.
func pipeClient(t *testing.T, handle handler) (*Client, *atomic.Int32) {
	t.Helper()
	dials := new(atomic.Int32)
	client := newClient(func(context.Context) (net.Conn, error) {
		clientEnd, serverEnd := net.Pipe()
		go serveRPC(t, serverEnd, int(dials.Add(1)), handle)
		return clientEnd, nil
	})
	t.Cleanup(func() { _ = client.Close() })
	return client, dials
}

func serveRPC(t *testing.T, connection net.Conn, number int, handle handler) {
	defer connection.Close()
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	for {
		data, err := reader.ReadFrame()
		if err != nil {
			return
		}
		request, err := protocol.DecodeGatewayRPCRequest(data)
		if err != nil {
			t.Errorf("client sent an invalid request: %v", err)
			return
		}
		response := handle(number, request)
		if response == nil {
			_, _ = reader.ReadFrame()
			return
		}
		if err := writer.WriteFrame(response); err != nil {
			return
		}
	}
}

func reply(t *testing.T, requestID string, messageType protocol.GatewayRPCMessageType, payload any) []byte {
	t.Helper()
	data := rawReply(t, requestID, messageType, payload)
	if _, err := protocol.DecodeGatewayRPCResponse(data); err != nil {
		t.Errorf("test response is invalid: %v", err)
	}
	return data
}

// rawReply builds a response without validating it.
func rawReply(t *testing.T, requestID string, messageType protocol.GatewayRPCMessageType, payload any) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Error(err)
	}
	data, err := json.Marshal(protocol.GatewayRPCEnvelope{
		Version: protocol.Version, Type: messageType, RequestID: requestID, Payload: payloadJSON,
	})
	if err != nil {
		t.Error(err)
	}
	return data
}

func testRecord() protocol.BrokerRPCSessionRecord {
	return protocol.BrokerRPCSessionRecord{
		ID: testSession, Generation: testGeneration, Title: "Terminal",
		ProjectID: testProject, ShellProfileID: testProfile, State: protocol.Running,
		Columns: 80, Rows: 24, CreatedAt: "2026-09-27T00:00:00Z", UpdatedAt: "2026-09-27T00:00:01Z",
	}
}

func TestMethodsRoundTripPayloads(t *testing.T) {
	issuedExpiry := "2026-10-27T00:00:00.5Z"
	mutation := protocol.HTTPSessionMutationResponse{OperationID: testOperation, Session: testRecord()}
	createRequest := protocol.HTTPSessionCreateRequest{
		OperationID: testOperation, ProjectID: testProject, ShellProfileID: testProfile,
	}
	renameRequest := protocol.HTTPSessionRenameRequest{Generation: testGeneration, OperationID: testOperation, Title: "Build"}
	terminateRequest := protocol.HTTPSessionTerminateRequest{Generation: testGeneration, OperationID: testOperation}
	settings := protocol.HTTPSettingsResponse{
		BrokerGeneration: testBroker, Device: protocol.HTTPDeviceSummary{ID: testDevice, Label: "Phone"},
		HostBuild: "host-test", Projects: []protocol.HTTPNamedOption{{ID: testProject, Name: "repo"}},
		ShellProfiles: []protocol.HTTPNamedOption{{ID: testProfile, Name: "zsh"}},
	}
	cases := []struct {
		name        string
		requestType protocol.GatewayRPCMessageType
		payload     any // expected request payload
		result      any // broker result payload
		call        func(context.Context, *Client) (any, error)
		want        any
	}{
		{
			name: "redeem", requestType: protocol.GatewayPairRedeem,
			payload: protocol.HTTPPairRedeemRequest{DeviceLabel: "Phone", Grant: testGrant},
			result: protocol.HTTPPairRedeemResponse{
				ExpiresAt: "2026-09-27T00:05:00Z", PendingID: testPending, PollSecret: testPollSecret, VerificationCode: "123-456",
			},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.RedeemPairing(ctx, protocol.HTTPPairRedeemRequest{DeviceLabel: "Phone", Grant: testGrant})
			},
			want: protocol.HTTPPairRedeemResponse{
				ExpiresAt: "2026-09-27T00:05:00Z", PendingID: testPending, PollSecret: testPollSecret, VerificationCode: "123-456",
			},
		},
		{
			name: "poll pending", requestType: protocol.GatewayPairPoll,
			payload: protocol.HTTPPairPollRequest{PendingID: testPending, PollSecret: testPollSecret},
			result:  protocol.GatewayRPCPairPollResultPayload{Response: protocol.HTTPPairPollResponse{Status: protocol.Pending}},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: testPending, PollSecret: testPollSecret})
			},
			want: gatewayapi.PollResult{Response: protocol.HTTPPairPollResponse{Status: protocol.Pending}},
		},
		{
			name: "poll approved", requestType: protocol.GatewayPairPoll,
			payload: protocol.HTTPPairPollRequest{PendingID: testPending, PollSecret: testPollSecret},
			result: protocol.GatewayRPCPairPollResultPayload{
				Response: protocol.HTTPPairPollResponse{
					Status: protocol.Approved, Device: &protocol.HTTPDeviceSummary{ID: testDevice, Label: "Phone"},
				},
				Credential: &testIssued, CredentialExpiresAt: &issuedExpiry,
			},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: testPending, PollSecret: testPollSecret})
			},
			want: gatewayapi.PollResult{
				Response: protocol.HTTPPairPollResponse{
					Status: protocol.Approved, Device: &protocol.HTTPDeviceSummary{ID: testDevice, Label: "Phone"},
				},
				Credential: testIssued, CredentialExpiresAt: time.Date(2026, 10, 27, 0, 0, 0, 5e8, time.UTC),
			},
		},
		{
			name: "authenticate", requestType: protocol.GatewayAuthCheck,
			payload: protocol.GatewayRPCCredentialPayload{Credential: testCredential},
			result: protocol.GatewayRPCPrincipalPayload{
				CredentialID: testCredID, DeviceID: testDevice, DeviceLabel: "Phone", ExpiresAt: "2026-10-27T00:00:00Z",
			},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.Authenticate(ctx, testCredential)
			},
			want: gatewayapi.Principal{
				DeviceID: testDevice, DeviceLabel: "Phone", CredentialID: testCredID,
				ExpiresAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "logout", requestType: protocol.GatewayAuthLogout,
			payload: protocol.GatewayRPCCredentialPayload{Credential: testCredential},
			result:  protocol.BrokerRPCEmptyPayload{},
			call: func(ctx context.Context, client *Client) (any, error) {
				return nil, client.Logout(ctx, testCredential)
			},
		},
		{
			name: "settings", requestType: protocol.GatewaySettings,
			payload: protocol.GatewayRPCCredentialPayload{Credential: testCredential},
			result:  settings,
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.Settings(ctx, testCredential)
			},
			want: settings,
		},
		{
			name: "list", requestType: protocol.GatewaySessionList,
			payload: protocol.GatewayRPCCredentialPayload{Credential: testCredential},
			result:  protocol.HTTPSessionListResponse{Sessions: []protocol.BrokerRPCSessionRecord{testRecord()}},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.ListSessions(ctx, testCredential)
			},
			want: protocol.HTTPSessionListResponse{Sessions: []protocol.BrokerRPCSessionRecord{testRecord()}},
		},
		{
			name: "create", requestType: protocol.GatewaySessionCreate,
			payload: protocol.GatewayRPCSessionCreatePayload{Credential: testCredential, Request: createRequest},
			result:  mutation,
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.CreateSession(ctx, testCredential, createRequest)
			},
			want: mutation,
		},
		{
			name: "rename", requestType: protocol.GatewaySessionRename,
			payload: protocol.GatewayRPCSessionRenamePayload{
				Credential: testCredential, SessionID: testSession, Request: renameRequest,
			},
			result: mutation,
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.RenameSession(ctx, testCredential, testSession, renameRequest)
			},
			want: mutation,
		},
		{
			name: "terminate", requestType: protocol.GatewaySessionTerminate,
			payload: protocol.GatewayRPCSessionTerminatePayload{
				Credential: testCredential, SessionID: testSession, Request: terminateRequest,
			},
			result: mutation,
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.TerminateSession(ctx, testCredential, testSession, terminateRequest)
			},
			want: mutation,
		},
		{
			name: "rename device", requestType: protocol.GatewayDeviceRename,
			payload: protocol.GatewayRPCDeviceRenamePayload{
				Credential: testCredential, Request: protocol.HTTPDeviceRenameRequest{Label: "Work phone"},
			},
			result: protocol.HTTPDeviceSummary{ID: testDevice, Label: "Work phone"},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.RenameDevice(ctx, testCredential, protocol.HTTPDeviceRenameRequest{Label: "Work phone"})
			},
			want: protocol.HTTPDeviceSummary{ID: testDevice, Label: "Work phone"},
		},
		{
			name: "origin", requestType: protocol.GatewayOrigin,
			payload: protocol.BrokerRPCEmptyPayload{},
			result:  protocol.GatewayRPCOriginResultPayload{Origin: &testOrigin},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.Origin(ctx)
			},
			want: testOrigin,
		},
		{
			name: "no origin", requestType: protocol.GatewayOrigin,
			payload: protocol.BrokerRPCEmptyPayload{},
			result:  protocol.GatewayRPCOriginResultPayload{},
			call: func(ctx context.Context, client *Client) (any, error) {
				return client.Origin(ctx)
			},
			want: "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			wantPayload, err := json.Marshal(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
				if request.Type != test.requestType || string(request.Payload) != string(wantPayload) {
					t.Errorf("request = %s %s, want %s %s", request.Type, request.Payload, test.requestType, wantPayload)
				}
				return reply(t, request.RequestID, protocol.GatewayRPCResults[request.Type], test.result)
			})
			got, err := test.call(context.Background(), client)
			if err != nil {
				t.Fatal(err)
			}
			if test.want != nil && !reflect.DeepEqual(got, test.want) {
				t.Fatalf("result = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestGatewayErrorIsTypedAndKeepsConnection(t *testing.T) {
	client, dials := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
		return reply(t, request.RequestID, protocol.GatewayError,
			protocol.BrokerRPCErrorPayload{Code: protocol.ErrorCodeRevoked, Message: "device was revoked"})
	})
	for range 2 {
		_, err := client.Authenticate(context.Background(), testCredential)
		var remote *gatewayapi.Error
		if !errors.As(err, &remote) || remote.Code != protocol.ErrorCodeRevoked {
			t.Fatalf("error = %v, want revoked", err)
		}
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want 1", dials.Load())
	}
}

func TestCorrelationFailurePoisonsAndRedials(t *testing.T) {
	principal := protocol.GatewayRPCPrincipalPayload{
		CredentialID: testCredID, DeviceID: testDevice, DeviceLabel: "Phone", ExpiresAt: "2026-10-27T00:00:00Z",
	}
	cases := map[string]func(request protocol.GatewayRPCEnvelope) []byte{
		"request ID mismatch": func(request protocol.GatewayRPCEnvelope) []byte {
			return reply(t, testOther, protocol.GatewayAuthCheckResult, principal)
		},
		"wrong result type": func(request protocol.GatewayRPCEnvelope) []byte {
			return reply(t, request.RequestID, protocol.GatewayAuthLogoutResult, protocol.BrokerRPCEmptyPayload{})
		},
		"invalid frame": func(protocol.GatewayRPCEnvelope) []byte { return []byte(`{"version":1}`) },
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			client, dials := pipeClient(t, func(connection int, request protocol.GatewayRPCEnvelope) []byte {
				if connection == 1 {
					return bad(request)
				}
				return reply(t, request.RequestID, protocol.GatewayAuthCheckResult, principal)
			})
			_, err := client.Authenticate(context.Background(), testCredential)
			if err == nil || errors.Is(err, ErrDeliveryUncertain) {
				t.Fatalf("error = %v, want a certain failure", err)
			}
			if _, err := client.Authenticate(context.Background(), testCredential); err != nil {
				t.Fatal(err)
			}
			if dials.Load() != 2 {
				t.Fatalf("dials = %d, want 2", dials.Load())
			}
		})
	}
}

func TestMutationOperationMismatchIsUncertain(t *testing.T) {
	client, dials := pipeClient(t, func(connection int, request protocol.GatewayRPCEnvelope) []byte {
		operation := testOperation
		if connection == 1 {
			operation = testOther
		}
		return reply(t, request.RequestID, protocol.GatewaySessionCreateResult,
			protocol.HTTPSessionMutationResponse{OperationID: operation, Session: testRecord()})
	})
	request := protocol.HTTPSessionCreateRequest{OperationID: testOperation, ProjectID: testProject, ShellProfileID: testProfile}
	if _, err := client.CreateSession(context.Background(), testCredential, request); !errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("error = %v, want delivery uncertain", err)
	}
	if _, err := client.CreateSession(context.Background(), testCredential, request); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 2 {
		t.Fatalf("dials = %d, want 2", dials.Load())
	}
}

func TestCancellationInterruptsBlockedCall(t *testing.T) {
	received := make(chan struct{}, 2)
	client, dials := pipeClient(t, func(int, protocol.GatewayRPCEnvelope) []byte {
		received <- struct{}{}
		return nil
	})
	calls := map[string]struct {
		call      func(context.Context) error
		uncertain bool
	}{
		"read": {func(ctx context.Context) error {
			_, err := client.Settings(ctx, testCredential)
			return err
		}, false},
		"mutation": {func(ctx context.Context) error {
			_, err := client.TerminateSession(ctx, testCredential, testSession,
				protocol.HTTPSessionTerminateRequest{Generation: testGeneration, OperationID: testOperation})
			return err
		}, true},
	}
	for name, test := range calls {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- test.call(ctx) }()
		<-received
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrDeliveryUncertain) != test.uncertain {
				t.Fatalf("%s: error = %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: cancellation did not interrupt the call", name)
		}
	}
	if dials.Load() != 2 {
		t.Fatalf("dials = %d, want 2 (canceled connection must be poisoned)", dials.Load())
	}
}

func TestDialFailureIsHostUnavailable(t *testing.T) {
	client := newClient(func(context.Context) (net.Conn, error) {
		return nil, errors.New("dial /private/runtime/broker.sock: refused")
	})
	_, err := client.ListSessions(context.Background(), testCredential)
	var remote *gatewayapi.Error
	if !errors.As(err, &remote) || remote.Code != protocol.HostUnavailable || strings.Contains(err.Error(), "/private") {
		t.Fatalf("error = %v, want safe host_unavailable", err)
	}
}

func TestSecretsNeverAppearInErrors(t *testing.T) {
	badIssued := strings.Repeat("I", 42) + "!"
	expiry := "2026-10-27T00:00:00Z"
	var calls atomic.Int32 // requests are serialized; numbered from 1
	client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
		switch calls.Add(1) {
		case 1:
			return reply(t, request.RequestID, protocol.GatewayError,
				protocol.BrokerRPCErrorPayload{Code: protocol.Unauthorized, Message: "credential is not valid"})
		case 2:
			return reply(t, request.RequestID, protocol.GatewayAuthLogoutResult, protocol.BrokerRPCEmptyPayload{})
		default:
			return rawReply(t, request.RequestID, protocol.GatewayPairPollResult, protocol.GatewayRPCPairPollResultPayload{
				Response: protocol.HTTPPairPollResponse{
					Status: protocol.Approved, Device: &protocol.HTTPDeviceSummary{ID: testDevice, Label: "Phone"},
				},
				Credential: &badIssued, CredentialExpiresAt: &expiry,
			})
		}
	})
	ctx := context.Background()
	malformed := strings.Repeat("M", 42) + "!"
	var errs []error
	_, err := client.Settings(ctx, testCredential) // gateway.error
	errs = append(errs, err)
	_, err = client.Settings(ctx, testCredential) // wrong result type
	errs = append(errs, err)
	_, err = client.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: testPending, PollSecret: testPollSecret})
	if !errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("malformed approval error = %v, want delivery uncertain", err)
	}
	errs = append(errs, err)
	_, err = client.Authenticate(ctx, malformed)
	errs = append(errs, err)
	_, err = client.RedeemPairing(ctx, protocol.HTTPPairRedeemRequest{DeviceLabel: "", Grant: testGrant})
	errs = append(errs, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client.OpenLive(canceled, testCredential)
	errs = append(errs, err)
	for index, err := range errs {
		if err == nil {
			t.Fatalf("errs[%d] is nil", index)
		}
		for _, secret := range []string{testCredential, testGrant, testPollSecret, badIssued, malformed} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("errs[%d] leaks a secret: %v", index, err)
			}
		}
	}
}

func TestNewDialsBrokerAsGateway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tempBase := ""
	if runtime.GOOS == "darwin" {
		tempBase = "/tmp"
	}
	runtimeDir, err := os.MkdirTemp(tempBase, "ptgw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{RuntimeDir: runtimeDir, BrokerGeneration: testBroker})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		hello, err := ipc.ServerHandshake(ctx, connection, testBroker, "broker-test")
		if err != nil {
			serverDone <- err
			return
		}
		if hello.ClientKind != protocol.Gateway {
			serverDone <- errors.New("client did not declare the gateway role")
			return
		}
		serveRPC(t, connection, 1, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
			return reply(t, request.RequestID, protocol.GatewaySessionListResult,
				protocol.HTTPSessionListResponse{Sessions: []protocol.BrokerRPCSessionRecord{}})
		})
		serverDone <- nil
	}()

	if _, err := New(Config{RuntimeDir: runtimeDir}); err == nil {
		t.Fatal("New accepted an empty client build")
	}
	client, err := New(Config{RuntimeDir: runtimeDir, ClientBuild: "gateway-test"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListSessions(ctx, testCredential)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 0 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListSessions(ctx, testCredential); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("error after Close = %v", err)
	}
}

// liveBroker accepts gateway.live.open on each dial, then runs live with the
// connection's frame reader and writer.
func liveBroker(t *testing.T, live func(reader *ipc.FrameReader, writer *ipc.FrameWriter)) *Client {
	t.Helper()
	client := newClient(func(context.Context) (net.Conn, error) {
		clientEnd, serverEnd := net.Pipe()
		go func() {
			defer serverEnd.Close()
			reader, _ := ipc.NewFrameReader(serverEnd, protocol.MaxInboundMessageBytes)
			writer, _ := ipc.NewFrameWriter(serverEnd, protocol.MaxOutboundMessageBytes)
			data, err := reader.ReadFrame()
			if err != nil {
				return
			}
			request, err := protocol.DecodeGatewayRPCRequest(data)
			if err != nil || request.Type != protocol.GatewayLiveOpen {
				t.Errorf("live open request = %s, %v", data, err)
				return
			}
			payload, _ := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
			if payload.Credential != testCredential {
				t.Error("live open carried the wrong credential")
			}
			if err := writer.WriteFrame(reply(t, request.RequestID, protocol.GatewayLiveOpenResult,
				protocol.BrokerRPCEmptyPayload{})); err != nil {
				return
			}
			live(reader, writer)
		}()
		return clientEnd, nil
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func envelope(t *testing.T, messageType protocol.MessageType, payload any) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(protocol.Envelope{
		Version: protocol.Version, Type: messageType, RequestID: testOther, Payload: payloadJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func helloJSON(t *testing.T) []byte {
	data := envelope(t, protocol.Hello, protocol.HelloPayload{
		CapabilityProfile: protocol.PhoneTerminalV1, ClientBuild: "browser-test",
		ClientKind: protocol.Browser, SupportedVersions: []int64{protocol.Version},
	})
	if _, err := protocol.DecodeClient(data); err != nil {
		t.Fatal(err)
	}
	return data
}

func welcomeJSON(t *testing.T) []byte {
	data := envelope(t, protocol.Welcome, protocol.WelcomePayload{
		BrokerGeneration: testBroker, CapabilityProfile: protocol.PhoneTerminalV1, HostBuild: "host-test",
		NegotiatedVersion: protocol.Version, Limits: protocol.ProtocolLimits{
			InboundMessageBytes: protocol.MaxInboundMessageBytes, OutboundMessageBytes: protocol.MaxOutboundMessageBytes,
			OutgoingQueueBytes: 2 * 1024 * 1024, MaxTextBytes: protocol.MaxTextBytes, MaxPasteBytes: protocol.MaxPasteBytes,
			MinColumns: protocol.MinColumns, MaxColumns: protocol.MaxColumns, MinRows: protocol.MinRows,
			MaxRows: protocol.MaxRows, MaxHistoryPageRows: protocol.MaxHistoryPageRows,
		},
	})
	if _, err := protocol.DecodeHost(data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLiveStreamRelaysUntilAccessRevoked(t *testing.T) {
	hello, welcome := helloJSON(t), welcomeJSON(t)
	revoked := envelope(t, protocol.AccessRevoked, protocol.AccessRevokedPayload{Message: "access was revoked"})
	if _, err := protocol.DecodeHost(revoked); err != nil {
		t.Fatal(err)
	}
	client := liveBroker(t, func(reader *ipc.FrameReader, writer *ipc.FrameWriter) {
		// The first frame after open must be the hello: the rejected welcome
		// below must never have been written.
		data, err := reader.ReadFrame()
		if err != nil || string(data) != string(hello) {
			t.Errorf("broker received %s, %v; want hello", data, err)
			return
		}
		_ = writer.WriteFrame(welcome)
		_ = writer.WriteFrame(revoked)
	})
	ctx := context.Background()
	stream, err := client.OpenLive(ctx, testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Send(ctx, welcome); err == nil {
		t.Fatal("Send accepted a host envelope")
	}
	if err := stream.Send(ctx, hello); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{welcome, revoked} {
		got, err := stream.Receive(ctx)
		if err != nil || string(got) != string(want) {
			t.Fatalf("Receive = %s, %v; want %s", got, err, want)
		}
	}
	if _, err := stream.Receive(ctx); !errors.Is(err, ErrAccessRevoked) {
		t.Fatalf("Receive after revocation = %v", err)
	}
	if err := stream.Send(ctx, hello); !errors.Is(err, ErrAccessRevoked) || !errors.Is(err, gatewayapi.ErrStreamEnded) {
		t.Fatalf("Send after revocation = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveStreamRejectsInvalidHostFrame(t *testing.T) {
	hello := helloJSON(t)
	client := liveBroker(t, func(_ *ipc.FrameReader, writer *ipc.FrameWriter) {
		_ = writer.WriteFrame(hello) // a client message is invalid in this direction
	})
	stream, err := client.OpenLive(context.Background(), testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Receive(context.Background()); err == nil {
		t.Fatal("Receive accepted a client envelope")
	}
	if err := stream.Send(context.Background(), hello); err == nil {
		t.Fatal("Send succeeded on a failed stream")
	}
}

func TestLiveReceiveCancellationClosesStream(t *testing.T) {
	client := liveBroker(t, func(reader *ipc.FrameReader, _ *ipc.FrameWriter) {
		_, _ = reader.ReadFrame() // hold until the client closes
	})
	stream, err := client.OpenLive(context.Background(), testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := stream.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive = %v, want deadline exceeded", err)
	}
	if _, err := stream.Receive(context.Background()); err == nil {
		t.Fatal("Receive succeeded after a canceled Receive")
	}
}

func TestOpenLiveBrokerErrorIsTyped(t *testing.T) {
	closed := make(chan struct{})
	client := newClient(func(context.Context) (net.Conn, error) {
		clientEnd, serverEnd := net.Pipe()
		go func() {
			defer close(closed)
			reader, _ := ipc.NewFrameReader(serverEnd, protocol.MaxInboundMessageBytes)
			writer, _ := ipc.NewFrameWriter(serverEnd, protocol.MaxOutboundMessageBytes)
			data, _ := reader.ReadFrame()
			request, err := protocol.DecodeGatewayRPCRequest(data)
			if err != nil {
				t.Error(err)
				return
			}
			_ = writer.WriteFrame(reply(t, request.RequestID, protocol.GatewayError,
				protocol.BrokerRPCErrorPayload{Code: protocol.Unauthorized, Message: "credential is not valid"}))
			if _, err := reader.ReadFrame(); err == nil {
				t.Error("connection stayed open after a rejected live open")
			}
		}()
		return clientEnd, nil
	})
	defer client.Close()
	_, err := client.OpenLive(context.Background(), testCredential)
	var remote *gatewayapi.Error
	if !errors.As(err, &remote) || remote.Code != protocol.Unauthorized {
		t.Fatalf("error = %v, want unauthorized", err)
	}
	<-closed
}

// After a broker restart the idle shared connection is dead. A write that
// fails on it delivered nothing, so the client redials once and the call
// succeeds. A request that was written is never retried.
func TestDeadIdleConnectionRedialsOnceBeforeDelivery(t *testing.T) {
	tempBase := ""
	if runtime.GOOS == "darwin" {
		tempBase = "/tmp"
	}
	dir, err := os.MkdirTemp(tempBase, "ptgw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := dir + "/rpc.sock"
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int32
	firstClosed := make(chan struct{})
	go func() {
		for number := 1; ; number++ {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				reader, _ := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
				writer, _ := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
				// Connection 1 answers once and closes (a broker restart).
				// Connection 2 answers once, then reads a request and closes
				// without answering.
				for answered := 0; ; answered++ {
					data, err := reader.ReadFrame()
					if err != nil {
						return
					}
					requests.Add(1)
					if answered == 1 {
						return
					}
					request, err := protocol.DecodeGatewayRPCRequest(data)
					if err != nil {
						t.Errorf("invalid request: %v", err)
						return
					}
					var response []byte
					if request.Type == protocol.GatewayAuthCheck {
						response = reply(t, request.RequestID, protocol.GatewayAuthCheckResult, protocol.GatewayRPCPrincipalPayload{
							CredentialID: testCredID, DeviceID: testDevice, DeviceLabel: "Phone", ExpiresAt: "2026-10-27T00:00:00Z",
						})
					} else {
						response = reply(t, request.RequestID, protocol.GatewaySessionCreateResult,
							protocol.HTTPSessionMutationResponse{OperationID: testOperation, Session: testRecord()})
					}
					if err := writer.WriteFrame(response); err != nil {
						return
					}
					if number == 1 {
						_ = connection.Close()
						close(firstClosed)
						return
					}
				}
			}()
		}
	}()
	var dials atomic.Int32
	client := newClient(func(ctx context.Context) (net.Conn, error) {
		dials.Add(1)
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Authenticate(ctx, testCredential); err != nil {
		t.Fatal(err)
	}
	<-firstClosed
	request := protocol.HTTPSessionCreateRequest{OperationID: testOperation, ProjectID: testProject, ShellProfileID: testProfile}
	if _, err := client.CreateSession(ctx, testCredential, request); err != nil {
		t.Fatalf("call after broker restart = %v", err)
	}
	if dials.Load() != 2 || requests.Load() != 2 {
		t.Fatalf("dials = %d, requests = %d; want 2, 2", dials.Load(), requests.Load())
	}
	// Written but unanswered: uncertain, and not retried.
	if _, err := client.CreateSession(ctx, testCredential, request); !errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("unanswered mutation = %v, want delivery uncertain", err)
	}
	if dials.Load() != 2 || requests.Load() != 3 {
		t.Fatalf("dials = %d, requests = %d; want 2, 3", dials.Load(), requests.Load())
	}
}

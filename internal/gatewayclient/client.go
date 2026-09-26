// Package gatewayclient is the browser gateway's client of the broker over the
// restricted gateway IPC role. It only transports requests: the broker parses
// and re-authorizes the raw browser credential on every call, and this package
// caches no authorization decision. Credentials, grants, and poll secrets are
// never logged or included in an error message.
package gatewayclient

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	// ErrDeliveryUncertain means a state-changing request may have reached the
	// broker, but no valid correlated result arrived. A pairing redeem or poll
	// may have consumed a one-time secret. Callers must not retry blindly.
	ErrDeliveryUncertain = errors.New("gatewayclient: broker delivery is uncertain")
	// ErrAccessRevoked is returned by a live stream after it relayed
	// access.revoked, which is always the broker's last live message.
	ErrAccessRevoked = errors.New("gatewayclient: live stream access was revoked")
)

var (
	_ gatewayapi.Broker     = (*Client)(nil)
	_ gatewayapi.LiveStream = (*liveStream)(nil)
)

// Config identifies the broker endpoint and this gateway build.
type Config struct {
	RuntimeDir  string // validated broker runtime directory (same value the gateway uses for health probes)
	ClientBuild string // 1..128 bytes, sent in ipc.hello
}

// Client serializes RPC exchanges on one lazily dialed connection. A
// connection that fails framing, validation, correlation, or cancellation is
// closed, and the next call dials a new one. Live streams use their own
// connections.
// ponytail: one serialized connection; add a small pool if gateway RPC latency
// under concurrent browsers matters.
type Client struct {
	dial func(context.Context) (net.Conn, error)
	slot chan struct{}
	done chan struct{}

	mu     sync.Mutex // guards conn and closed against Close
	conn   *rpcConn
	closed bool
}

type rpcConn struct {
	conn   net.Conn
	reader *ipc.FrameReader
	writer *ipc.FrameWriter
}

// New validates config. It does not dial.
func New(config Config) (*Client, error) {
	if config.RuntimeDir == "" {
		return nil, errors.New("gatewayclient: runtime directory must not be empty")
	}
	if len(config.ClientBuild) == 0 || len(config.ClientBuild) > 128 {
		return nil, errors.New("gatewayclient: client build must be 1..128 bytes")
	}
	return newClient(func(ctx context.Context) (net.Conn, error) {
		connection, _, _, err := ipc.DialBroker(ctx, config.RuntimeDir, protocol.Gateway, config.ClientBuild)
		return connection, err
	}), nil
}

func newClient(dial func(context.Context) (net.Conn, error)) *Client {
	client := &Client{dial: dial, slot: make(chan struct{}, 1), done: make(chan struct{})}
	client.slot <- struct{}{}
	return client
}

// Close closes the shared RPC connection and interrupts an active exchange.
// It is idempotent. Streams returned by OpenLive are closed by their owners.
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil
	}
	client.closed = true
	close(client.done)
	if client.conn == nil {
		return nil
	}
	err := client.conn.conn.Close()
	client.conn = nil
	return err
}

func (client *Client) RedeemPairing(
	ctx context.Context,
	request protocol.HTTPPairRedeemRequest,
) (protocol.HTTPPairRedeemResponse, error) {
	return roundTrip[protocol.HTTPPairRedeemResponse](ctx, client, protocol.GatewayPairRedeem, request, true, nil)
}

func (client *Client) PollPairing(
	ctx context.Context,
	request protocol.HTTPPairPollRequest,
) (gatewayapi.PollResult, error) {
	var result gatewayapi.PollResult
	// Convert inside the exchange so a malformed approval poisons the
	// connection and reports that the one-time credential may be lost.
	_, err := roundTrip(ctx, client, protocol.GatewayPairPoll, request, true,
		func(payload protocol.GatewayRPCPairPollResultPayload) error {
			result = gatewayapi.PollResult{Response: payload.Response}
			if payload.Response.Status != protocol.Approved {
				return nil
			}
			// The protocol decoder already required the credential and expiry.
			expiresAt, err := time.Parse(time.RFC3339Nano, *payload.CredentialExpiresAt)
			if err != nil {
				return errors.New("gatewayclient: approved poll carries a malformed expiry")
			}
			result.Credential = *payload.Credential
			result.CredentialExpiresAt = expiresAt
			return nil
		})
	if err != nil {
		return gatewayapi.PollResult{}, err
	}
	return result, nil
}

func (client *Client) Authenticate(ctx context.Context, credential string) (gatewayapi.Principal, error) {
	if err := checkCredential(credential); err != nil {
		return gatewayapi.Principal{}, err
	}
	var principal gatewayapi.Principal
	_, err := roundTrip(ctx, client, protocol.GatewayAuthCheck,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false,
		func(payload protocol.GatewayRPCPrincipalPayload) error {
			expiresAt, err := time.Parse(time.RFC3339Nano, payload.ExpiresAt)
			if err != nil {
				return errors.New("gatewayclient: principal carries a malformed expiry")
			}
			principal = gatewayapi.Principal{
				DeviceID: payload.DeviceID, DeviceLabel: payload.DeviceLabel,
				CredentialID: payload.CredentialID, ExpiresAt: expiresAt,
			}
			return nil
		})
	if err != nil {
		return gatewayapi.Principal{}, err
	}
	return principal, nil
}

func (client *Client) Logout(ctx context.Context, credential string) error {
	if err := checkCredential(credential); err != nil {
		return err
	}
	_, err := roundTrip[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.GatewayAuthLogout,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, true, nil)
	return err
}

func (client *Client) Settings(ctx context.Context, credential string) (protocol.HTTPSettingsResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPSettingsResponse{}, err
	}
	return roundTrip[protocol.HTTPSettingsResponse](ctx, client, protocol.GatewaySettings,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

func (client *Client) ListSessions(ctx context.Context, credential string) (protocol.HTTPSessionListResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPSessionListResponse{}, err
	}
	return roundTrip[protocol.HTTPSessionListResponse](ctx, client, protocol.GatewaySessionList,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

func (client *Client) CreateSession(
	ctx context.Context,
	credential string,
	request protocol.HTTPSessionCreateRequest,
) (protocol.HTTPSessionMutationResponse, error) {
	return client.mutate(ctx, protocol.GatewaySessionCreate, credential, request.OperationID,
		protocol.GatewayRPCSessionCreatePayload{Credential: credential, Request: request})
}

func (client *Client) RenameSession(
	ctx context.Context,
	credential, sessionID string,
	request protocol.HTTPSessionRenameRequest,
) (protocol.HTTPSessionMutationResponse, error) {
	return client.mutate(ctx, protocol.GatewaySessionRename, credential, request.OperationID,
		protocol.GatewayRPCSessionRenamePayload{Credential: credential, SessionID: sessionID, Request: request})
}

func (client *Client) TerminateSession(
	ctx context.Context,
	credential, sessionID string,
	request protocol.HTTPSessionTerminateRequest,
) (protocol.HTTPSessionMutationResponse, error) {
	return client.mutate(ctx, protocol.GatewaySessionTerminate, credential, request.OperationID,
		protocol.GatewayRPCSessionTerminatePayload{Credential: credential, SessionID: sessionID, Request: request})
}

func (client *Client) mutate(
	ctx context.Context,
	requestType protocol.GatewayRPCMessageType,
	credential, operationID string,
	payload any,
) (protocol.HTTPSessionMutationResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPSessionMutationResponse{}, err
	}
	// Keep operation correlation inside the serialized exchange so a mismatched
	// acknowledgement poisons the connection before another mutation is written.
	return roundTrip(ctx, client, requestType, payload, true, func(result protocol.HTTPSessionMutationResponse) error {
		if result.OperationID != operationID {
			return errors.New("gatewayclient: result operation ID mismatch")
		}
		return nil
	})
}

// roundTrip performs one serialized exchange on the shared connection. When
// uncertain is set, any failure after the request may have been written is
// reported as ErrDeliveryUncertain. check runs before the connection is
// released; its failure poisons the connection.
func roundTrip[R any](
	ctx context.Context,
	client *Client,
	requestType protocol.GatewayRPCMessageType,
	payload any,
	uncertain bool,
	check func(R) error,
) (R, error) {
	var result R
	if ctx == nil {
		return result, errors.New("gatewayclient: context must not be nil")
	}
	data, requestID, err := encodeRequest(requestType, payload)
	if err != nil {
		return result, err
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-client.done:
		return result, net.ErrClosed
	case <-client.slot:
	}
	defer func() { client.slot <- struct{}{} }()

	connection, err := client.connection(ctx)
	if err != nil {
		return result, err
	}
	clearDeadline, err := setContextDeadline(ctx, connection.conn.SetDeadline)
	if err != nil {
		return result, err // ctx ended before anything was written
	}
	defer clearDeadline()
	fail := func(err error) (R, error) {
		client.poison(connection)
		var zero R
		return zero, failure(ctx, err, uncertain)
	}

	response, err := connection.exchange(data, requestID, requestType)
	var remote *gatewayapi.Error
	if errors.As(err, &remote) {
		return result, remote
	}
	if err != nil {
		return fail(err)
	}
	if result, err = protocol.DecodeRPCPayload[R](response); err != nil {
		return fail(err)
	}
	if check != nil {
		if err := check(result); err != nil {
			return fail(err)
		}
	}
	return result, nil
}

// connection returns the shared connection, dialing one if needed. The caller
// must hold the slot.
func (client *Client) connection(ctx context.Context) (*rpcConn, error) {
	client.mu.Lock()
	current, closed := client.conn, client.closed
	client.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if current != nil {
		return current, nil
	}
	connection, err := dialRPC(ctx, client.dial)
	if err != nil {
		return nil, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		_ = connection.conn.Close()
		return nil, net.ErrClosed
	}
	client.conn = connection
	return connection, nil
}

func (client *Client) poison(connection *rpcConn) {
	_ = connection.conn.Close()
	client.mu.Lock()
	if client.conn == connection {
		client.conn = nil
	}
	client.mu.Unlock()
}

// OpenLive dials a dedicated connection, binds it to the credential with
// gateway.live.open, and returns it as a live stream.
func (client *Client) OpenLive(ctx context.Context, credential string) (gatewayapi.LiveStream, error) {
	if ctx == nil {
		return nil, errors.New("gatewayclient: context must not be nil")
	}
	if err := checkCredential(credential); err != nil {
		return nil, err
	}
	data, requestID, err := encodeRequest(protocol.GatewayLiveOpen,
		protocol.GatewayRPCCredentialPayload{Credential: credential})
	if err != nil {
		return nil, err
	}
	select {
	case <-client.done:
		return nil, net.ErrClosed
	default:
	}
	connection, err := dialRPC(ctx, client.dial)
	if err != nil {
		return nil, err
	}
	clearDeadline, err := setContextDeadline(ctx, connection.conn.SetDeadline)
	if err != nil {
		_ = connection.conn.Close()
		return nil, err
	}
	_, err = connection.exchange(data, requestID, protocol.GatewayLiveOpen)
	clearDeadline()
	if err != nil {
		_ = connection.conn.Close()
		var remote *gatewayapi.Error
		if errors.As(err, &remote) {
			return nil, remote
		}
		return nil, failure(ctx, err, false)
	}
	return &liveStream{conn: connection.conn, reader: connection.reader, writer: connection.writer}, nil
}

func dialRPC(ctx context.Context, dial func(context.Context) (net.Conn, error)) (*rpcConn, error) {
	connection, err := dial(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// The dial error may name runtime paths; the gateway only needs the code.
		return nil, &gatewayapi.Error{Code: protocol.HostUnavailable, Message: "broker is unavailable"}
	}
	reader, err := ipc.NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	writer, err := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &rpcConn{conn: connection, reader: reader, writer: writer}, nil
}

// exchange writes one validated request and reads its correlated response. A
// *gatewayapi.Error leaves the connection usable; any other error does not.
func (connection *rpcConn) exchange(
	data []byte,
	requestID string,
	requestType protocol.GatewayRPCMessageType,
) (protocol.GatewayRPCEnvelope, error) {
	if err := connection.writer.WriteFrame(data); err != nil {
		return protocol.GatewayRPCEnvelope{}, err
	}
	responseData, err := connection.reader.ReadFrame()
	if err != nil {
		return protocol.GatewayRPCEnvelope{}, err
	}
	response, err := protocol.DecodeGatewayRPCResponse(responseData)
	if err != nil {
		return protocol.GatewayRPCEnvelope{}, err
	}
	if response.RequestID != requestID {
		return protocol.GatewayRPCEnvelope{}, errors.New("gatewayclient: response request ID mismatch")
	}
	if response.Type == protocol.GatewayError {
		payload, err := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
		if err != nil {
			return protocol.GatewayRPCEnvelope{}, err
		}
		return protocol.GatewayRPCEnvelope{}, &gatewayapi.Error{Code: payload.Code, Message: payload.Message}
	}
	if response.Type != protocol.GatewayRPCResults[requestType] {
		return protocol.GatewayRPCEnvelope{}, fmt.Errorf("gatewayclient: unexpected response type %q", response.Type)
	}
	return response, nil
}

// encodeRequest builds and validates a request before any connection is used.
// Validation errors describe shape only and never echo secret values.
func encodeRequest(requestType protocol.GatewayRPCMessageType, payload any) ([]byte, string, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, "", fmt.Errorf("gatewayclient: create request ID: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("gatewayclient: encode %s payload: %w", requestType, err)
	}
	data, err := json.Marshal(protocol.GatewayRPCEnvelope{
		Version: protocol.Version, Type: requestType, RequestID: requestID, Payload: payloadJSON,
	})
	if err != nil {
		return nil, "", fmt.Errorf("gatewayclient: encode %s request: %w", requestType, err)
	}
	if _, err := protocol.DecodeGatewayRPCRequest(data); err != nil {
		return nil, "", &gatewayapi.Error{Code: protocol.InvalidRequest, Message: "request is invalid"}
	}
	return data, requestID, nil
}

// checkCredential rejects a malformed cookie without a broker round trip. The
// broker answers the same way for any credential it cannot authorize.
func checkCredential(credential string) error {
	if protocol.ValidateSecretText(credential) != nil {
		return &gatewayapi.Error{Code: protocol.Unauthorized, Message: "credential is invalid"}
	}
	return nil
}

func failure(ctx context.Context, err error, uncertain bool) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	if uncertain {
		return fmt.Errorf("%w: %w", ErrDeliveryUncertain, err)
	}
	return err
}

// setContextDeadline interrupts one blocking operation when ctx ends, through
// the connection's SetDeadline, SetReadDeadline, or SetWriteDeadline. It sets
// no deadline from ctx.Deadline itself, so an interrupted operation always
// observes ctx.Err.
func setContextDeadline(ctx context.Context, set func(time.Time) error) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cancelFinished := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = set(time.Now())
		close(cancelFinished)
	})
	return func() {
		if !stopCancel() {
			<-cancelFinished
		}
		_ = set(time.Time{})
	}, nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

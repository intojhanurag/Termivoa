// Package gatewayclient is the browser gateway's client of the broker over the
// restricted gateway IPC role. It only transports requests: the broker parses
// and re-authorizes the raw browser credential on every call, and this package
// caches no authorization decision. Credentials, grants, and poll secrets are
// never logged or included in an error message.
package gatewayclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
	"uuid"

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
	// access.revoked, which is always the broker's last live message. It wraps
	// gatewayapi.ErrStreamEnded.
	ErrAccessRevoked = fmt.Errorf("gatewayclient: live stream access was revoked: %w", gatewayapi.ErrStreamEnded)
)

// errWriteFailed marks an exchange whose request frame was not fully written.
var errWriteFailed = errors.New("gatewayclient: request write failed")

var (
	_ gatewayapi.Broker     = (*Client)(nil)
	_ gatewayapi.LiveStream = (*liveStream)(nil)
)

// Config identifies the broker endpoint and this gateway build.
type Config struct {
	RuntimeDir  string // validated broker runtime directory (same value the gateway uses for health probes)
	ClientBuild string // 1..128 bytes, sent in ipc.hello
}

const (
	maxConcurrentSlowRPC        = 4
	maxConcurrentIntegrationRPC = 4
)

// Client keeps fast RPC exchanges on one lazily dialed connection. Blocking
// operations use at most maxConcurrentSlowRPC independent connections so they
// cannot hold up authentication and session reads. GitHub and herdr
// create/manage calls use their own maxConcurrentIntegrationRPC slots so they
// cannot starve session create/terminate. A connection that fails
// framing, validation, correlation, or cancellation is closed. Live streams
// use their own connections.
type Client struct {
	dial      func(context.Context) (net.Conn, error)
	slot      chan struct{}
	slowSlots chan struct{}
	extSlots  chan struct{} // GitHub and herdr create/manage
	done      chan struct{}

	mu         sync.Mutex // guards conn, operations and closed against Close
	conn       *rpcConn
	operations map[*rpcOperation]struct{}
	closed     bool
}

// Registered before dialing, so Close also cancels a dial in progress.
type rpcOperation struct {
	cancel context.CancelFunc
	conn   net.Conn
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
	client := &Client{
		dial: dial, slot: make(chan struct{}, 1), done: make(chan struct{}),
		slowSlots: make(chan struct{}, maxConcurrentSlowRPC), extSlots: make(chan struct{}, maxConcurrentIntegrationRPC),
		operations: make(map[*rpcOperation]struct{}),
	}
	client.slot <- struct{}{}
	return client
}

// Close closes the shared and active slow RPC connections and interrupts dials.
// It is idempotent. Streams returned by OpenLive are closed by their owners.
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil
	}
	client.closed = true
	close(client.done)
	for operation := range client.operations {
		operation.cancel()
		if operation.conn != nil {
			_ = operation.conn.Close()
		}
	}
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

// RenameDevice renames the credential's own device. The broker chooses the
// device from the credential; the result must not name another one's label.
func (client *Client) RenameDevice(
	ctx context.Context,
	credential string,
	request protocol.HTTPDeviceRenameRequest,
) (protocol.HTTPDeviceSummary, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPDeviceSummary{}, err
	}
	return roundTrip(ctx, client, protocol.GatewayDeviceRename,
		protocol.GatewayRPCDeviceRenamePayload{Credential: credential, Request: request}, true,
		func(result protocol.HTTPDeviceSummary) error {
			if result.Label != request.Label {
				return errors.New("gatewayclient: rename result label mismatch")
			}
			return nil
		})
}

func (client *Client) PushConfig(ctx context.Context, credential string) (protocol.HTTPPushConfigResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPPushConfigResponse{}, err
	}
	return roundTrip[protocol.HTTPPushConfigResponse](ctx, client, protocol.GatewayPushConfig,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

func (client *Client) PushSubscribe(ctx context.Context, credential string, request protocol.HTTPPushSubscribeRequest) error {
	if err := checkCredential(credential); err != nil {
		return err
	}
	_, err := roundTrip[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.GatewayPushSubscribe,
		protocol.GatewayRPCPushSubscribePayload{Credential: credential, Request: request}, true, nil)
	return err
}

func (client *Client) PushUnsubscribe(ctx context.Context, credential string, request protocol.HTTPPushUnsubscribeRequest) error {
	if err := checkCredential(credential); err != nil {
		return err
	}
	_, err := roundTrip[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.GatewayPushUnsubscribe,
		protocol.GatewayRPCPushUnsubscribePayload{Credential: credential, Request: request}, true, nil)
	return err
}

func (client *Client) PushTest(ctx context.Context, credential string) (protocol.HTTPPushTestResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPPushTestResponse{}, err
	}
	return roundTrip[protocol.HTTPPushTestResponse](ctx, client, protocol.GatewayPushTest,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, true, nil)
}

// Upload sends one image on a dedicated connection: gateway.upload.begin,
// chunks of at most MaxUploadChunkBytes, then gateway.upload.commit. Each
// message waits for its result, so the broker bounds what is in flight.
func (client *Client) Upload(
	ctx context.Context, credential, sessionID, generation string, data []byte,
) (protocol.HTTPUploadResponse, error) {
	var result protocol.HTTPUploadResponse
	if ctx == nil {
		return result, errors.New("gatewayclient: context must not be nil")
	}
	if err := checkCredential(credential); err != nil {
		return result, err
	}
	select {
	case <-client.done:
		return result, net.ErrClosed
	default:
	}
	connection, err := dialRPC(ctx, client.dial)
	if err != nil {
		return result, err
	}
	defer connection.conn.Close()
	clearDeadline, err := setContextDeadline(ctx, connection.conn.SetDeadline)
	if err != nil {
		return result, err
	}
	defer clearDeadline()
	send := func(requestType protocol.GatewayRPCMessageType, payload any) (protocol.GatewayRPCEnvelope, error) {
		data, requestID, err := encodeRequest(ctx, requestType, payload)
		if err != nil {
			return protocol.GatewayRPCEnvelope{}, err
		}
		return connection.exchange(data, requestID, requestType)
	}
	fail := func(err error) (protocol.HTTPUploadResponse, error) {
		var remote *gatewayapi.Error
		if errors.As(err, &remote) {
			return protocol.HTTPUploadResponse{}, remote
		}
		return protocol.HTTPUploadResponse{}, failure(ctx, err, true)
	}
	if _, err := send(protocol.GatewayUploadBegin, protocol.GatewayRPCUploadBeginPayload{
		Credential: credential, SessionID: sessionID, Generation: generation, Size: int64(len(data)),
	}); err != nil {
		return fail(err)
	}
	for start := 0; start < len(data); start += protocol.MaxUploadChunkBytes {
		chunk := data[start:min(start+protocol.MaxUploadChunkBytes, len(data))]
		if _, err := send(protocol.GatewayUploadChunk, protocol.GatewayRPCUploadChunkPayload{
			Data: base64.StdEncoding.EncodeToString(chunk),
		}); err != nil {
			return fail(err)
		}
	}
	response, err := send(protocol.GatewayUploadCommit, protocol.GatewayRPCCredentialPayload{Credential: credential})
	if err != nil {
		return fail(err)
	}
	if result, err = protocol.DecodeRPCPayload[protocol.HTTPUploadResponse](response); err != nil {
		return fail(err)
	}
	return result, nil
}

func (client *Client) PreviewTarget(ctx context.Context, credential, sessionID string, port uint16) (string, error) {
	if err := checkCredential(credential); err != nil {
		return "", err
	}
	result, err := roundTrip[protocol.GatewayRPCPreviewTargetPayload](ctx, client, protocol.GatewayPreviewCheck,
		protocol.GatewayRPCPreviewCheckPayload{Credential: credential, SessionID: sessionID, Port: int64(port)}, false, nil)
	return string(result.Host), err
}

// OriginProbe returns the configured origin and the CORS origin sets.
func (client *Client) OriginProbe(ctx context.Context) (protocol.GatewayRPCOriginResultPayload, error) {
	return roundTrip[protocol.GatewayRPCOriginResultPayload](ctx, client, protocol.GatewayOrigin,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
}

func (client *Client) ListComputers(ctx context.Context, credential string) (protocol.GatewayRPCComputerListResultPayload, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.GatewayRPCComputerListResultPayload{}, err
	}
	return roundTrip[protocol.GatewayRPCComputerListResultPayload](ctx, client, protocol.GatewayComputersList,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

func (client *Client) AddComputer(
	ctx context.Context, credential string, request protocol.HTTPComputerAddRequest,
) (protocol.HTTPComputerAddResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPComputerAddResponse{}, err
	}
	return roundTrip(ctx, client, protocol.GatewayComputersAdd,
		protocol.GatewayRPCComputerAddPayload{Credential: credential, Request: request}, true,
		func(result protocol.HTTPComputerAddResponse) error {
			if result.Computer.Origin != request.Origin {
				return errors.New("gatewayclient: computer result origin mismatch")
			}
			return nil
		})
}

func (client *Client) RemoveComputer(ctx context.Context, credential, origin string) error {
	if err := checkCredential(credential); err != nil {
		return err
	}
	_, err := roundTrip[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.GatewayComputersRemove,
		protocol.GatewayRPCComputerRemovePayload{Credential: credential, Origin: origin}, true, nil)
	return err
}

// OfferComputer never carries a forwarded origin (see encodeRequest).
func (client *Client) OfferComputer(ctx context.Context, request protocol.HTTPComputerOfferRequest) error {
	_, err := roundTrip[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.GatewayComputersOffer, request, true, nil)
	return err
}

func (client *Client) FoundComputers(ctx context.Context, credential string) (protocol.HTTPComputerFoundResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPComputerFoundResponse{}, err
	}
	return roundTrip[protocol.HTTPComputerFoundResponse](ctx, client, protocol.GatewayComputersFound,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

func (client *Client) Provider(ctx context.Context, credential, provider string) (protocol.HTTPProviderSnapshotResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPProviderSnapshotResponse{}, err
	}
	return roundTrip[protocol.HTTPProviderSnapshotResponse](ctx, client, protocol.GatewayProvidersGet,
		protocol.GatewayRPCProviderPayload{Credential: credential, Provider: provider}, false, nil)
}

func (client *Client) UpdateProvider(
	ctx context.Context, credential, provider string, request protocol.HTTPProviderUpdateRequest,
) (protocol.HTTPProviderInfo, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPProviderInfo{}, err
	}
	return roundTrip(ctx, client, protocol.GatewayProvidersUpdate,
		protocol.GatewayRPCProviderUpdatePayload{Credential: credential, Provider: provider, Request: request}, true,
		func(result protocol.HTTPProviderInfo) error {
			if result.ID != provider {
				return errors.New("gatewayclient: provider result mismatch")
			}
			return nil
		})
}

func (client *Client) OpenProvider(
	ctx context.Context, credential, provider string, request protocol.HTTPProviderOpenRequest,
) (protocol.HTTPProviderOpenResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPProviderOpenResponse{}, err
	}
	return roundTrip(ctx, client, protocol.GatewayProvidersOpen,
		protocol.GatewayRPCProviderOpenPayload{Credential: credential, Provider: provider, Request: request}, true,
		func(result protocol.HTTPProviderOpenResponse) error {
			if result.Session.Provider != provider || result.Session.TargetID != request.TargetID {
				return errors.New("gatewayclient: provider session mismatch")
			}
			return nil
		})
}

// CreateProvider runs on a dedicated connection: it can take up to 45 s and
// must not hold the shared one that every other request waits for.
func (client *Client) CreateProvider(
	ctx context.Context, credential, provider string, request protocol.HTTPProviderCreateRequest,
) (protocol.HTTPProviderCreateResponse, error) {
	return dedicated[protocol.HTTPProviderCreateResponse](ctx, client, credential, protocol.GatewayProvidersCreate,
		protocol.GatewayRPCProviderCreatePayload{Credential: credential, Provider: provider, Request: request})
}

// ManageProvider runs on a dedicated connection, like CreateProvider: one
// herdr call can take 15 s.
func (client *Client) ManageProvider(
	ctx context.Context, credential, provider string, request protocol.HTTPProviderManageRequest,
) (protocol.HTTPProviderManageResponse, error) {
	return dedicated[protocol.HTTPProviderManageResponse](ctx, client, credential, protocol.GatewayProvidersManage,
		protocol.GatewayRPCProviderManagePayload{Credential: credential, Provider: provider, Request: request})
}

// UpdateChat turns Chat view off on this computer.
func (client *Client) UpdateChat(ctx context.Context, credential string, request protocol.HTTPChatUpdateRequest) (protocol.HTTPChatInfo, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPChatInfo{}, err
	}
	return roundTrip[protocol.HTTPChatInfo](ctx, client, protocol.GatewayChatUpdate,
		protocol.GatewayRPCChatUpdatePayload{Credential: credential, Request: request}, true, nil)
}

func (client *Client) GitHub(ctx context.Context, credential string) (protocol.HTTPGitHubResponse, error) {
	if err := checkCredential(credential); err != nil {
		return protocol.HTTPGitHubResponse{}, err
	}
	return roundTrip[protocol.HTTPGitHubResponse](ctx, client, protocol.GatewayGithubGet,
		protocol.GatewayRPCCredentialPayload{Credential: credential}, false, nil)
}

// GitHubAction runs on a dedicated connection: a GitHub call can take 15 s.
func (client *Client) GitHubAction(
	ctx context.Context, credential string, request protocol.HTTPGitHubRequest,
) (protocol.HTTPGitHubResponse, error) {
	return dedicated[protocol.HTTPGitHubResponse](ctx, client, credential, protocol.GatewayGithubAction,
		protocol.GatewayRPCGitHubActionPayload{Credential: credential, Request: request})
}

// GitHubRepos runs on a dedicated connection, like GitHubAction.
func (client *Client) GitHubRepos(ctx context.Context, credential string) (protocol.HTTPGitHubReposResponse, error) {
	return dedicated[protocol.HTTPGitHubReposResponse](ctx, client, credential, protocol.GatewayGithubRepos,
		protocol.GatewayRPCCredentialPayload{Credential: credential})
}

// dedicated sends one bounded, isolated exchange and never retries it.
func dedicated[Result any](
	ctx context.Context, client *Client, credential string, requestType protocol.GatewayRPCMessageType, payload any,
) (Result, error) {
	var result Result
	if err := checkCredential(credential); err != nil {
		return result, err
	}
	return isolatedRoundTrip[Result](ctx, client, client.extSlots, requestType, payload, true, nil)
}

// slowRequest covers handlers that wait for git/processes, network delivery,
// or process start. Cached reads (including the per-request preview check)
// and authentication keep the shared connection.
func slowRequest(requestType protocol.GatewayRPCMessageType) bool {
	switch requestType {
	case protocol.GatewaySessionCreate, protocol.GatewaySessionTerminate,
		protocol.GatewayPushTest, protocol.GatewayProvidersOpen:
		return true
	}
	return false
}

func isolatedRoundTrip[R any](ctx context.Context, client *Client, slots chan struct{}, requestType protocol.GatewayRPCMessageType,
	payload any, uncertain bool, check func(R) error,
) (R, error) {
	var result R
	if ctx == nil {
		return result, errors.New("gatewayclient: context must not be nil")
	}
	data, requestID, err := encodeRequest(ctx, requestType, payload)
	if err != nil {
		return result, err
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-client.done:
		return result, net.ErrClosed
	default:
	}
	// Do not build an unbounded queue of blocked HTTP handlers or broker dials.
	select {
	case slots <- struct{}{}:
	default:
		return result, &gatewayapi.Error{Code: protocol.LimitExceeded, Message: "too many operations in progress; try again after one finishes"}
	}
	defer func() { <-slots }()
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	operation := &rpcOperation{cancel: cancel}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return result, net.ErrClosed
	}
	client.operations[operation] = struct{}{}
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		delete(client.operations, operation)
		client.mu.Unlock()
	}()
	connection, err := dialRPC(operationCtx, client.dial)
	if err != nil {
		return result, err
	}
	defer connection.conn.Close()
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return result, net.ErrClosed
	}
	operation.conn = connection.conn
	client.mu.Unlock()
	clearDeadline, err := setContextDeadline(operationCtx, connection.conn.SetDeadline)
	if err != nil {
		return result, err
	}
	defer clearDeadline()
	response, err := connection.exchange(data, requestID, requestType)
	var remote *gatewayapi.Error
	if errors.As(err, &remote) {
		return result, remote
	}
	if err == nil {
		result, err = protocol.DecodeRPCPayload[R](response)
	}
	if err == nil && check != nil {
		err = check(result)
	}
	if err != nil {
		var none R
		return none, failure(operationCtx, err, uncertain)
	}
	return result, nil
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
	// Keep operation correlation inside the exchange so a mismatched
	// acknowledgement is uncertain and closes the connection.
	return roundTrip(ctx, client, requestType, payload, true, func(result protocol.HTTPSessionMutationResponse) error {
		if result.OperationID != operationID {
			return errors.New("gatewayclient: result operation ID mismatch")
		}
		return nil
	})
}

// roundTrip isolates known blocking handlers; other exchanges serialize on
// the shared connection. With uncertain set, a failure after possible delivery
// is reported as ErrDeliveryUncertain. check runs before the connection is
// released; its failure poisons the connection.
func roundTrip[R any](
	ctx context.Context,
	client *Client,
	requestType protocol.GatewayRPCMessageType,
	payload any,
	uncertain bool,
	check func(R) error,
) (R, error) {
	if slowRequest(requestType) {
		return isolatedRoundTrip[R](ctx, client, client.slowSlots, requestType, payload, uncertain, check)
	}
	var result R
	if ctx == nil {
		return result, errors.New("gatewayclient: context must not be nil")
	}
	data, requestID, err := encodeRequest(ctx, requestType, payload)
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

	var (
		connection *rpcConn
		response   protocol.GatewayRPCEnvelope
	)
	for {
		var reused bool
		connection, reused, err = client.connection(ctx)
		if err != nil {
			return result, err
		}
		var clearDeadline func()
		if clearDeadline, err = setContextDeadline(ctx, connection.conn.SetDeadline); err != nil {
			return result, err // ctx ended before anything was written
		}
		response, err = connection.exchange(data, requestID, requestType)
		clearDeadline()
		// An idle shared connection is dead after a broker restart. A failed
		// write left at most a partial frame, which the broker never acts on,
		// so redial once. Never retry after the whole request was written.
		if reused && errors.Is(err, errWriteFailed) && ctx.Err() == nil {
			client.poison(connection)
			continue
		}
		break
	}
	fail := func(err error) (R, error) {
		client.poison(connection)
		var zero R
		return zero, failure(ctx, err, uncertain)
	}

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

// connection returns the shared connection, dialing one if needed, and
// whether it was reused. The caller must hold the slot.
func (client *Client) connection(ctx context.Context) (*rpcConn, bool, error) {
	client.mu.Lock()
	current, closed := client.conn, client.closed
	client.mu.Unlock()
	if closed {
		return nil, false, net.ErrClosed
	}
	if current != nil {
		return current, true, nil
	}
	connection, err := dialRPC(ctx, client.dial)
	if err != nil {
		return nil, false, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		_ = connection.conn.Close()
		return nil, false, net.ErrClosed
	}
	client.conn = connection
	return connection, false, nil
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
	data, requestID, err := encodeRequest(ctx, protocol.GatewayLiveOpen,
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
		return protocol.GatewayRPCEnvelope{}, fmt.Errorf("%w: %w", errWriteFailed, err)
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
// It forwards the browser origin from gatewayapi.WithForwardedOrigin, on every
// request but the origin probe. Validation errors
// describe shape only and never echo secret values.
func encodeRequest(ctx context.Context, requestType protocol.GatewayRPCMessageType, payload any) ([]byte, string, error) {
	requestID := uuid.New().String()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("gatewayclient: encode %s payload: %w", requestType, err)
	}
	envelope := protocol.GatewayRPCEnvelope{Version: protocol.Version, Type: requestType, RequestID: requestID, Payload: payloadJSON}
	// The origin probe and an offer never carry an origin: the offer route
	// admits only requests without one (docs/found-on-network.md, H1).
	if origin, ok := gatewayapi.ForwardedOrigin(ctx); ok && requestType != protocol.GatewayOrigin && requestType != protocol.GatewayComputersOffer {
		envelope.Origin = &origin
	}
	data, err := json.Marshal(envelope)
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

// Package gatewayapi is the boundary between the browser gateway and the
// broker. The gateway transports requests; the broker authorizes every call
// from the raw browser credential. No implementation here owns state.
package gatewayapi

import (
	"context"
	"errors"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Principal is the broker-resolved identity of one browser credential.
// It is display and routing data, never proof of authorization.
type Principal struct {
	DeviceID     string
	DeviceLabel  string
	CredentialID string
	ExpiresAt    time.Time
}

// ErrStreamEnded is wrapped by LiveStream errors after the broker ended the
// stream on purpose (it sent access.revoked). It is not a transport failure:
// the relay stops reading, flushes queued messages and closes with 1008.
var ErrStreamEnded = errors.New("gatewayapi: live stream ended")

// Error carries a stable public error code and a safe short message.
type Error struct {
	Code    protocol.ErrorCode
	Message string
}

func (err *Error) Error() string { return string(err.Code) + ": " + err.Message }

// PollResult is the broker decision for one pairing poll. Credential is set
// only on the single approved response and is the raw cookie value.
type PollResult struct {
	Response            protocol.HTTPPairPollResponse
	Credential          string
	CredentialExpiresAt time.Time
}

// Broker is implemented over restricted gateway IPC. Every credential-taking
// method is re-authorized by the broker; the gateway never caches decisions
// beyond one request.
type Broker interface {
	RedeemPairing(ctx context.Context, request protocol.HTTPPairRedeemRequest) (protocol.HTTPPairRedeemResponse, error)
	PollPairing(ctx context.Context, request protocol.HTTPPairPollRequest) (PollResult, error)
	Authenticate(ctx context.Context, credential string) (Principal, error)
	Logout(ctx context.Context, credential string) error
	Settings(ctx context.Context, credential string) (protocol.HTTPSettingsResponse, error)
	ListSessions(ctx context.Context, credential string) (protocol.HTTPSessionListResponse, error)
	CreateSession(ctx context.Context, credential string, request protocol.HTTPSessionCreateRequest) (protocol.HTTPSessionMutationResponse, error)
	RenameSession(ctx context.Context, credential, sessionID string, request protocol.HTTPSessionRenameRequest) (protocol.HTTPSessionMutationResponse, error)
	TerminateSession(ctx context.Context, credential, sessionID string, request protocol.HTTPSessionTerminateRequest) (protocol.HTTPSessionMutationResponse, error)
	// RenameDevice renames only the device that owns the credential.
	RenameDevice(ctx context.Context, credential string, request protocol.HTTPDeviceRenameRequest) (protocol.HTTPDeviceSummary, error)
	// Push requests act only on the device that owns the credential.
	PushConfig(ctx context.Context, credential string) (protocol.HTTPPushConfigResponse, error)
	PushSubscribe(ctx context.Context, credential string, request protocol.HTTPPushSubscribeRequest) error
	PushUnsubscribe(ctx context.Context, credential string, request protocol.HTTPPushUnsubscribeRequest) error
	PushTest(ctx context.Context, credential string) (protocol.HTTPPushTestResponse, error)
	// Origin returns the configured browser origin, or "" when none is set.
	// It needs no credential: the origin is not a secret.
	Origin(ctx context.Context) (string, error)
	// OpenLive binds a new broker live stream to the credential. The stream
	// starts as an observer and carries canonical envelope JSON both ways.
	OpenLive(ctx context.Context, credential string) (LiveStream, error)
}

// LiveStream carries one canonical envelope JSON message per call. The broker
// sends access.revoked before closing a stream whose credential stops being
// valid; Send and Receive then return an error that wraps ErrStreamEnded.
type LiveStream interface {
	Send(ctx context.Context, envelope []byte) error
	Receive(ctx context.Context) ([]byte, error)
	Close() error
}

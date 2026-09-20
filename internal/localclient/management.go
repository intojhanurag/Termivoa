// Package localclient provides bounded clients for the broker's restricted
// local IPC endpoint. It never reads the broker database or starts a process.
package localclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// SessionRef is the complete identity of one broker-owned session generation.
type SessionRef struct {
	ID         string
	Generation string
}

// RemoteError is a correlated, typed rejection returned by the broker.
type RemoteError struct {
	Code       protocol.ErrorCode
	Message    string
	SessionRef *SessionRef
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("broker rejected management request (%s): %s", err.Code, err.Message)
}

// DeliveryUncertainError means a mutation frame may have reached the broker,
// but its correlated result could not be validated. Callers must inspect
// current broker state and must not automatically retry with new input.
type DeliveryUncertainError struct {
	err error
}

func (err *DeliveryUncertainError) Error() string {
	return fmt.Sprintf("management operation delivery is uncertain: %v", err.err)
}

func (err *DeliveryUncertainError) Unwrap() error { return err.err }

// IsDeliveryUncertain reports whether a mutation may already have been
// accepted even though no valid acknowledgement reached the client.
func IsDeliveryUncertain(err error) bool {
	var uncertain *DeliveryUncertainError
	return errors.As(err, &uncertain)
}

// RequestIDSource returns a fresh canonical UUID for request correlation.
type RequestIDSource func() (string, error)

// ManagementClient serializes bounded request/response exchanges on one local
// IPC connection. A CLI invocation should use its own short-lived client;
// long-lived terminal streaming uses a separate future connection.
type ManagementClient struct {
	connection net.Conn
	reader     *ipc.FrameReader
	writer     *ipc.FrameWriter
	requestID  RequestIDSource
	requestMu  sync.Mutex

	slot      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// DialManagement authenticates as a local CLI and verifies that the endpoint
// descriptor reaches the same live broker generation.
func DialManagement(ctx context.Context, runtimeDir, clientBuild string) (*ManagementClient, error) {
	connection, _, _, err := ipc.DialBroker(ctx, runtimeDir, protocol.CLI, clientBuild)
	if err != nil {
		return nil, err
	}
	client, err := newManagementClient(connection, newUUID)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return client, nil
}

func newManagementClient(connection net.Conn, requestID RequestIDSource) (*ManagementClient, error) {
	if connection == nil {
		return nil, errors.New("local client connection must not be nil")
	}
	if requestID == nil {
		return nil, errors.New("local client request ID source must not be nil")
	}
	reader, err := ipc.NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	if err != nil {
		return nil, err
	}
	writer, err := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	if err != nil {
		return nil, err
	}
	client := &ManagementClient{
		connection: connection,
		reader:     reader,
		writer:     writer,
		requestID:  requestID,
		slot:       make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	client.slot <- struct{}{}
	return client, nil
}

// Close interrupts an active exchange and makes the client unusable. It is
// idempotent and does not affect the broker or any managed session.
func (client *ManagementClient) Close() error {
	client.closeOnce.Do(func() {
		close(client.done)
		client.closeErr = client.connection.Close()
	})
	return client.closeErr
}

func (client *ManagementClient) ListPage(
	ctx context.Context,
	cursor *string,
	limit int64,
) (protocol.BrokerRPCSessionListResultPayload, error) {
	request, err := client.request(protocol.SessionListRequest, nil, protocol.BrokerRPCSessionListRequestPayload{
		Cursor: cursor,
		Limit:  limit,
	})
	if err != nil {
		return protocol.BrokerRPCSessionListResultPayload{}, err
	}
	response, err := client.roundTrip(ctx, request, protocol.SessionListResult, false)
	if err != nil {
		return protocol.BrokerRPCSessionListResultPayload{}, err
	}
	return protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionListResultPayload](response)
}

func (client *ManagementClient) Status(
	ctx context.Context,
	ref SessionRef,
) (protocol.BrokerRPCSessionRecord, error) {
	request, err := client.request(protocol.SessionStatusRequest, &ref, protocol.BrokerRPCEmptyPayload{})
	if err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	response, err := client.roundTrip(ctx, request, protocol.SessionStatusResult, false)
	if err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionResultPayload](response)
	return payload.Session, err
}

// Create asks the broker to resolve saved configuration IDs and start a
// session. Executable paths, arguments, environment, and workdirs cannot be
// supplied through this API.
func (client *ManagementClient) Create(
	ctx context.Context,
	operationID, projectID, shellProfileID string,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	request, err := client.request(protocol.SessionCreateRequest, nil, protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: operationID, ProjectID: projectID, ShellProfileID: shellProfileID,
	})
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	response, err := client.roundTrip(ctx, request, protocol.SessionCreateResult, true)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](response)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, &DeliveryUncertainError{err: err}
	}
	if payload.OperationID != operationID {
		return protocol.BrokerRPCSessionMutationResultPayload{}, &DeliveryUncertainError{
			err: errors.New("create result operation ID mismatch"),
		}
	}
	return payload, nil
}

func (client *ManagementClient) Terminate(
	ctx context.Context,
	ref SessionRef,
	operationID string,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	request, err := client.request(protocol.SessionTerminateRequest, &ref,
		protocol.BrokerRPCSessionTerminateRequestPayload{OperationID: operationID})
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	response, err := client.roundTrip(ctx, request, protocol.SessionTerminateResult, true)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](response)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, &DeliveryUncertainError{err: err}
	}
	if payload.OperationID != operationID {
		return protocol.BrokerRPCSessionMutationResultPayload{}, &DeliveryUncertainError{
			err: errors.New("terminate result operation ID mismatch"),
		}
	}
	return payload, nil
}

func (client *ManagementClient) request(
	messageType protocol.BrokerRPCMessageType,
	ref *SessionRef,
	payload any,
) (protocol.BrokerRPCEnvelope, error) {
	client.requestMu.Lock()
	defer client.requestMu.Unlock()
	requestID, err := client.requestID()
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, fmt.Errorf("create management request ID: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, fmt.Errorf("encode management request payload: %w", err)
	}
	request := protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: messageType, RequestID: requestID, Payload: payloadJSON,
	}
	if ref != nil {
		request.SessionID = &ref.ID
		request.Generation = &ref.Generation
	}
	data, err := json.Marshal(request)
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, fmt.Errorf("encode management request: %w", err)
	}
	validated, err := protocol.DecodeBrokerRPCRequest(data)
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, err
	}
	return validated, nil
}

func (client *ManagementClient) roundTrip(
	ctx context.Context,
	request protocol.BrokerRPCEnvelope,
	expected protocol.BrokerRPCMessageType,
	mutation bool,
) (protocol.BrokerRPCEnvelope, error) {
	if ctx == nil {
		return protocol.BrokerRPCEnvelope{}, errors.New("local client context must not be nil")
	}
	select {
	case <-ctx.Done():
		return protocol.BrokerRPCEnvelope{}, ctx.Err()
	case <-client.done:
		return protocol.BrokerRPCEnvelope{}, net.ErrClosed
	case <-client.slot:
	}
	defer func() { client.slot <- struct{}{} }()
	select {
	case <-client.done:
		return protocol.BrokerRPCEnvelope{}, net.ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return protocol.BrokerRPCEnvelope{}, err
	}

	data, err := json.Marshal(request)
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, fmt.Errorf("encode management request: %w", err)
	}
	if _, err := protocol.DecodeBrokerRPCRequest(data); err != nil {
		return protocol.BrokerRPCEnvelope{}, err
	}
	clearDeadline, err := setContextDeadline(ctx, client.connection)
	if err != nil {
		return protocol.BrokerRPCEnvelope{}, err
	}
	defer clearDeadline()

	if err := client.writer.WriteFrame(data); err != nil {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx, err, mutation)
	}
	responseData, err := client.reader.ReadFrame()
	if err != nil {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx, err, mutation)
	}
	response, err := protocol.DecodeBrokerRPCResponse(responseData)
	if err != nil {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx, err, mutation)
	}
	if response.RequestID != request.RequestID {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx,
			errors.New("management response request ID mismatch"), mutation)
	}
	if !sameIdentity(response, request) {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx,
			errors.New("management response session identity mismatch"), mutation)
	}
	if response.Type == protocol.BrokerError {
		payload, decodeErr := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCErrorPayload](response)
		if decodeErr != nil {
			_ = client.Close()
			return protocol.BrokerRPCEnvelope{}, deliveryError(ctx, decodeErr, mutation)
		}
		remote := &RemoteError{Code: payload.Code, Message: payload.Message}
		if response.SessionID != nil {
			remote.SessionRef = &SessionRef{ID: *response.SessionID, Generation: *response.Generation}
		}
		return protocol.BrokerRPCEnvelope{}, remote
	}
	if response.Type != expected {
		_ = client.Close()
		return protocol.BrokerRPCEnvelope{}, deliveryError(ctx,
			fmt.Errorf("unexpected management response type %q", response.Type), mutation)
	}
	return response, nil
}

func sameIdentity(left, right protocol.BrokerRPCEnvelope) bool {
	if (left.SessionID == nil) != (right.SessionID == nil) ||
		(left.Generation == nil) != (right.Generation == nil) {
		return false
	}
	if left.SessionID == nil {
		return true
	}
	return *left.SessionID == *right.SessionID && *left.Generation == *right.Generation
}

func setContextDeadline(ctx context.Context, connection net.Conn) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set management deadline: %w", err)
		}
	}
	cancelFinished := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
		close(cancelFinished)
	})
	return func() {
		if !stopCancel() {
			<-cancelFinished
		}
		_ = connection.SetDeadline(time.Time{})
	}, nil
}

func deliveryError(ctx context.Context, err error, mutation bool) error {
	if contextErr := ctx.Err(); contextErr != nil {
		err = contextErr
	}
	if mutation {
		return &DeliveryUncertainError{err: err}
	}
	return err
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
}

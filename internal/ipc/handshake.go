package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const HandshakeTimeout = 5 * time.Second

// RemoteError is a typed, non-sensitive error returned by the broker during
// IPC negotiation.
type RemoteError struct {
	Code    protocol.ErrorCode
	Message string
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("ipc: remote %s: %s", err.Code, err.Message)
}

func ClientHandshake(
	ctx context.Context,
	connection net.Conn,
	clientKind protocol.IPCClientKind,
	clientBuild string,
) (protocol.IPCWelcomeClass, error) {
	return clientHandshake(ctx, connection, clientKind, clientBuild, HandshakeTimeout)
}

func clientHandshake(
	ctx context.Context,
	connection net.Conn,
	clientKind protocol.IPCClientKind,
	clientBuild string,
	timeout time.Duration,
) (protocol.IPCWelcomeClass, error) {
	if connection == nil {
		return protocol.IPCWelcomeClass{}, errors.New("ipc: handshake connection must not be nil")
	}
	requestID, err := newUUID()
	if err != nil {
		return protocol.IPCWelcomeClass{}, err
	}
	clearDeadline, err := setHandshakeDeadline(ctx, connection, timeout)
	if err != nil {
		return protocol.IPCWelcomeClass{}, err
	}
	defer clearDeadline()

	helloPayload, err := json.Marshal(protocol.IPCHelloClass{
		ClientKind: clientKind, ClientBuild: clientBuild, SupportedVersions: []int64{protocol.Version},
	})
	if err != nil {
		return protocol.IPCWelcomeClass{}, fmt.Errorf("ipc: encode hello payload: %w", err)
	}
	hello, err := json.Marshal(protocol.IPCEnvelope{
		Version:   protocol.Version,
		Type:      protocol.IPCHello,
		RequestID: requestID,
		Payload:   helloPayload,
	})
	if err != nil {
		return protocol.IPCWelcomeClass{}, fmt.Errorf("ipc: encode hello: %w", err)
	}
	if _, err := protocol.DecodeIPCClient(hello); err != nil {
		return protocol.IPCWelcomeClass{}, err
	}
	writer, _ := NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	if err := writer.WriteFrame(hello); err != nil {
		return protocol.IPCWelcomeClass{}, handshakeContextError(ctx, err)
	}
	reader, _ := NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	response, err := reader.ReadFrame()
	if err != nil {
		return protocol.IPCWelcomeClass{}, handshakeContextError(ctx, err)
	}
	envelope, err := protocol.DecodeIPCHost(response)
	if err != nil {
		return protocol.IPCWelcomeClass{}, err
	}
	if envelope.RequestID != requestID {
		return protocol.IPCWelcomeClass{}, errors.New("ipc: handshake response request ID mismatch")
	}
	if envelope.Type == protocol.IPCError {
		payload, err := protocol.DecodeIPCPayload[protocol.IPCErrorClass](envelope)
		if err != nil {
			return protocol.IPCWelcomeClass{}, err
		}
		return protocol.IPCWelcomeClass{}, &RemoteError{Code: payload.Code, Message: payload.Message}
	}
	payload, err := protocol.DecodeIPCPayload[protocol.IPCWelcomeClass](envelope)
	if err != nil {
		return protocol.IPCWelcomeClass{}, err
	}
	return payload, nil
}

func ServerHandshake(
	ctx context.Context,
	connection net.Conn,
	brokerGeneration, hostBuild string,
) (protocol.IPCHelloClass, error) {
	return serverHandshake(ctx, connection, brokerGeneration, hostBuild, HandshakeTimeout)
}

func serverHandshake(
	ctx context.Context,
	connection net.Conn,
	brokerGeneration, hostBuild string,
	timeout time.Duration,
) (protocol.IPCHelloClass, error) {
	if connection == nil {
		return protocol.IPCHelloClass{}, errors.New("ipc: handshake connection must not be nil")
	}
	clearDeadline, err := setHandshakeDeadline(ctx, connection, timeout)
	if err != nil {
		return protocol.IPCHelloClass{}, err
	}
	defer clearDeadline()
	reader, _ := NewFrameReader(connection, protocol.MaxInboundMessageBytes)
	request, err := reader.ReadFrame()
	if err != nil {
		return protocol.IPCHelloClass{}, handshakeContextError(ctx, err)
	}
	envelope, err := protocol.DecodeIPCClient(request)
	if err != nil {
		_ = writeHandshakeError(connection, request, protocol.ProtocolMismatch, "unsupported local protocol handshake")
		return protocol.IPCHelloClass{}, err
	}
	hello, err := protocol.DecodeIPCPayload[protocol.IPCHelloClass](envelope)
	if err != nil {
		return protocol.IPCHelloClass{}, err
	}
	welcomePayload, err := json.Marshal(protocol.IPCWelcomeClass{
		BrokerGeneration: brokerGeneration, HostBuild: hostBuild, NegotiatedVersion: protocol.Version,
	})
	if err != nil {
		return protocol.IPCHelloClass{}, fmt.Errorf("ipc: encode welcome payload: %w", err)
	}
	welcome, err := json.Marshal(protocol.IPCEnvelope{
		Version:   protocol.Version,
		Type:      protocol.IPCWelcome,
		RequestID: envelope.RequestID,
		Payload:   welcomePayload,
	})
	if err != nil {
		return protocol.IPCHelloClass{}, fmt.Errorf("ipc: encode welcome: %w", err)
	}
	if _, err := protocol.DecodeIPCHost(welcome); err != nil {
		return protocol.IPCHelloClass{}, err
	}
	writer, _ := NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	if err := writer.WriteFrame(welcome); err != nil {
		return protocol.IPCHelloClass{}, handshakeContextError(ctx, err)
	}
	return hello, nil
}

func writeHandshakeError(connection net.Conn, request []byte, code protocol.ErrorCode, message string) error {
	var candidate struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(request, &candidate); err != nil || candidate.RequestID == "" {
		return errors.New("ipc: malformed handshake has no usable request ID")
	}
	payload, err := json.Marshal(protocol.IPCErrorClass{Code: code, Message: message})
	if err != nil {
		return fmt.Errorf("ipc: encode handshake error payload: %w", err)
	}
	response, err := json.Marshal(protocol.IPCEnvelope{
		Version: protocol.Version, Type: protocol.IPCError, RequestID: candidate.RequestID, Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("ipc: encode handshake error: %w", err)
	}
	if _, err := protocol.DecodeIPCHost(response); err != nil {
		return err
	}
	writer, _ := NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	return writer.WriteFrame(response)
}

func setHandshakeDeadline(ctx context.Context, connection net.Conn, timeout time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		return nil, errors.New("ipc: handshake timeout must be positive")
	}
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("ipc: set handshake deadline: %w", err)
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

func handshakeContextError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return err
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("ipc: create request ID: %w", err)
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

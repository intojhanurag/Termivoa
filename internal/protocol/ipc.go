package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// IPCEnvelope is the separately versioned local broker handshake envelope.
// Once negotiated, ordinary broker requests use the canonical v1 Envelope.
type IPCEnvelope struct {
	Version   int                `json:"version"`
	Type      IPCMessageTypeEnum `json:"type"`
	RequestID string             `json:"requestId"`
	Payload   json.RawMessage    `json:"payload"`
}

func DecodeIPCClient(data []byte) (IPCEnvelope, error) {
	return decodeIPC(data, true)
}

func DecodeIPCHost(data []byte) (IPCEnvelope, error) {
	return decodeIPC(data, false)
}

func decodeIPC(data []byte, client bool) (IPCEnvelope, error) {
	limit := MaxOutboundMessageBytes
	if client {
		limit = MaxInboundMessageBytes
	}
	if len(data) == 0 || len(data) > limit {
		return IPCEnvelope{}, fmt.Errorf("protocol: IPC message size %d is outside 1..%d bytes", len(data), limit)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return IPCEnvelope{}, err
	}
	var envelope IPCEnvelope
	if err := strictDecode(data, &envelope); err != nil {
		return IPCEnvelope{}, fmt.Errorf("protocol: decode IPC envelope: %w", err)
	}
	if envelope.Version != Version {
		return IPCEnvelope{}, fmt.Errorf("protocol: IPC version %d is not supported", envelope.Version)
	}
	if !validUUID(envelope.RequestID) {
		return IPCEnvelope{}, errors.New("protocol: IPC requestId must be a canonical UUID")
	}

	switch envelope.Type {
	case IPCHello:
		if !client {
			return IPCEnvelope{}, errors.New("protocol: ipc.hello is not a host message")
		}
		payload, err := DecodeIPCPayload[IPCHelloClass](envelope)
		if err != nil {
			return IPCEnvelope{}, err
		}
		if (payload.ClientKind != CLI && payload.ClientKind != Gateway) ||
			len(payload.ClientBuild) == 0 || len(payload.ClientBuild) > 128 ||
			len(payload.SupportedVersions) != 1 || payload.SupportedVersions[0] != Version {
			return IPCEnvelope{}, errors.New("protocol: invalid IPC hello negotiation")
		}
	case IPCWelcome:
		if client {
			return IPCEnvelope{}, errors.New("protocol: ipc.welcome is not a client message")
		}
		payload, err := DecodeIPCPayload[IPCWelcomeClass](envelope)
		if err != nil {
			return IPCEnvelope{}, err
		}
		if !validUUID(payload.BrokerGeneration) || len(payload.HostBuild) == 0 ||
			len(payload.HostBuild) > 128 || payload.NegotiatedVersion != Version {
			return IPCEnvelope{}, errors.New("protocol: invalid IPC welcome negotiation")
		}
	case IPCError:
		if client {
			return IPCEnvelope{}, errors.New("protocol: ipc.error is not a client message")
		}
		payload, err := DecodeIPCPayload[IPCErrorClass](envelope)
		if err != nil {
			return IPCEnvelope{}, err
		}
		if (payload.Code != ProtocolMismatch && payload.Code != HostUnavailable && payload.Code != InternalError) ||
			len(payload.Message) == 0 || len(payload.Message) > 512 {
			return IPCEnvelope{}, errors.New("protocol: invalid IPC error")
		}
	default:
		return IPCEnvelope{}, fmt.Errorf("protocol: unsupported IPC message type %q", envelope.Type)
	}
	return envelope, nil
}

// DecodeIPCPayload strictly decodes a validated local IPC payload.
func DecodeIPCPayload[T any](envelope IPCEnvelope) (T, error) {
	var payload T
	if err := strictDecode(envelope.Payload, &payload); err != nil {
		return payload, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return payload, nil
}

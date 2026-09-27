package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxBrokerRPCListSessions      = 200
	MaxBrokerRPCCursorBytes       = 128
	MaxBrokerRPCRenameTitleBytes  = 128
	MaxBrokerRPCSessionTitleBytes = 256
	MaxBrokerRPCErrorBytes        = 512
	MaxBrokerRPCPreviewRunes      = 160
	MaxBrokerRPCPreviewBytes      = 480
)

// BrokerRPCEnvelope is the canonical session-management message carried on a
// length-prefixed local IPC connection after its handshake. RequestID pairs a
// result or error with one request; OperationID separately deduplicates each
// mutation across retries.
type BrokerRPCEnvelope struct {
	Version    int                  `json:"version"`
	Type       BrokerRPCMessageType `json:"type"`
	RequestID  string               `json:"requestId"`
	SessionID  *string              `json:"sessionId,omitempty"`
	Generation *string              `json:"generation,omitempty"`
	Payload    json.RawMessage      `json:"payload"`
}

// DecodeBrokerRPCRequest validates a CLI/gateway-to-broker management request.
func DecodeBrokerRPCRequest(data []byte) (BrokerRPCEnvelope, error) {
	return decodeBrokerRPC(data, true)
}

// DecodeBrokerRPCResponse validates a broker-to-CLI/gateway management result.
func DecodeBrokerRPCResponse(data []byte) (BrokerRPCEnvelope, error) {
	return decodeBrokerRPC(data, false)
}

// DecodeBrokerRPCPayload strictly decodes a validated management payload.
func DecodeBrokerRPCPayload[T any](envelope BrokerRPCEnvelope) (T, error) {
	var payload T
	if err := strictDecode(envelope.Payload, &payload); err != nil {
		return payload, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return payload, nil
}

var brokerRPCRequestTypes = map[BrokerRPCMessageType]bool{
	SessionListRequest: true, SessionCreateRequest: true, SessionRenameRequest: true,
	SessionTerminateRequest: true, SessionStatusRequest: true,
}

var brokerRPCResponseTypes = map[BrokerRPCMessageType]bool{
	SessionListResult: true, SessionCreateResult: true, SessionRenameResult: true,
	SessionTerminateResult: true, SessionStatusResult: true, BrokerError: true,
}

var brokerRPCSessionTypes = map[BrokerRPCMessageType]bool{
	SessionRenameRequest: true, SessionTerminateRequest: true, SessionStatusRequest: true,
	SessionRenameResult: true, SessionTerminateResult: true, SessionStatusResult: true,
}

func decodeBrokerRPC(data []byte, request bool) (BrokerRPCEnvelope, error) {
	limit := MaxOutboundMessageBytes
	if request {
		limit = MaxInboundMessageBytes
	}
	if len(data) == 0 || len(data) > limit {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: broker RPC message size %d is outside 1..%d bytes", len(data), limit)
	}
	if !utf8.Valid(data) {
		return BrokerRPCEnvelope{}, errors.New("protocol: broker RPC message must be valid UTF-8")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return BrokerRPCEnvelope{}, err
	}

	var envelope BrokerRPCEnvelope
	if err := strictDecode(data, &envelope); err != nil {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: decode broker RPC envelope: %w", err)
	}
	if envelope.Version != Version {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: broker RPC version %d is not supported", envelope.Version)
	}
	if !validUUID(envelope.RequestID) {
		return BrokerRPCEnvelope{}, errors.New("protocol: broker RPC requestId must be a canonical UUID")
	}
	if envelope.SessionID != nil && !validUUID(*envelope.SessionID) {
		return BrokerRPCEnvelope{}, errors.New("protocol: broker RPC sessionId must be a canonical UUID")
	}
	if envelope.Generation != nil && !validUUID(*envelope.Generation) {
		return BrokerRPCEnvelope{}, errors.New("protocol: broker RPC generation must be a canonical UUID")
	}

	if request {
		if !brokerRPCRequestTypes[envelope.Type] {
			return BrokerRPCEnvelope{}, fmt.Errorf("protocol: %q is not a broker RPC request", envelope.Type)
		}
	} else if !brokerRPCResponseTypes[envelope.Type] {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: %q is not a broker RPC response", envelope.Type)
	}

	if brokerRPCSessionTypes[envelope.Type] {
		if envelope.SessionID == nil || envelope.Generation == nil {
			return BrokerRPCEnvelope{}, fmt.Errorf("protocol: %q requires sessionId and generation", envelope.Type)
		}
	} else if envelope.Type == BrokerError {
		if (envelope.SessionID == nil) != (envelope.Generation == nil) {
			return BrokerRPCEnvelope{}, errors.New("protocol: broker error sessionId and generation must appear together")
		}
	} else if envelope.SessionID != nil || envelope.Generation != nil {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: %q must not carry session identity", envelope.Type)
	}

	if err := validateBrokerRPCPayload(envelope); err != nil {
		return BrokerRPCEnvelope{}, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return envelope, nil
}

func validateBrokerRPCPayload(envelope BrokerRPCEnvelope) error { //nolint:gocyclo
	switch envelope.Type {
	case SessionListRequest:
		payload, err := brokerRPCPayload[BrokerRPCSessionListRequestPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Limit < 1 || payload.Limit > MaxBrokerRPCListSessions {
			return errors.New("list limit is outside bounds")
		}
		return validateBrokerRPCCursor(payload.Cursor)
	case SessionCreateRequest:
		payload, err := brokerRPCPayload[BrokerRPCSessionCreateRequestPayload](envelope)
		if err != nil {
			return err
		}
		if !validUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		if !validUUID(payload.ProjectID) || !validUUID(payload.ShellProfileID) {
			return errors.New("projectId and shellProfileId must be canonical UUIDs")
		}
		return nil
	case SessionRenameRequest:
		payload, err := brokerRPCPayload[BrokerRPCSessionRenameRequestPayload](envelope)
		if err != nil {
			return err
		}
		if !validUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		return validateBrokerRPCText(payload.Title, MaxBrokerRPCRenameTitleBytes, "title")
	case SessionTerminateRequest:
		payload, err := brokerRPCPayload[BrokerRPCSessionTerminateRequestPayload](envelope)
		if err != nil {
			return err
		}
		if !validUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		return nil
	case SessionStatusRequest:
		_, err := brokerRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case SessionListResult:
		payload, err := brokerRPCPayload[BrokerRPCSessionListResultPayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.Sessions) > MaxBrokerRPCListSessions {
			return errors.New("session list is outside bounds")
		}
		if err := validateBrokerRPCCursor(payload.NextCursor); err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(payload.Sessions))
		for index := range payload.Sessions {
			record := payload.Sessions[index]
			if err := validateBrokerRPCSessionRecord(record); err != nil {
				return fmt.Errorf("sessions[%d]: %w", index, err)
			}
			key := record.ID + "\x00" + record.Generation
			if _, duplicate := seen[key]; duplicate {
				return errors.New("session list contains a duplicate identity")
			}
			seen[key] = struct{}{}
		}
		return nil
	case SessionCreateResult:
		payload, err := brokerRPCPayload[BrokerRPCSessionMutationResultPayload](envelope)
		if err != nil {
			return err
		}
		if !validUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		return validateBrokerRPCSessionRecord(payload.Session)
	case SessionRenameResult, SessionTerminateResult:
		payload, err := brokerRPCPayload[BrokerRPCSessionMutationResultPayload](envelope)
		if err != nil {
			return err
		}
		if !validUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		return validateBrokerRPCSessionResult(envelope, payload.Session)
	case SessionStatusResult:
		payload, err := brokerRPCPayload[BrokerRPCSessionResultPayload](envelope)
		if err != nil {
			return err
		}
		return validateBrokerRPCSessionResult(envelope, payload.Session)
	case BrokerError:
		payload, err := brokerRPCPayload[BrokerRPCErrorPayload](envelope)
		if err != nil {
			return err
		}
		if !validErrorCode(payload.Code) {
			return errors.New("unknown broker RPC error code")
		}
		return validateBrokerRPCText(payload.Message, MaxBrokerRPCErrorBytes, "error message")
	default:
		return errors.New("unknown broker RPC message type")
	}
}

func validateBrokerRPCSessionResult(envelope BrokerRPCEnvelope, record BrokerRPCSessionRecord) error {
	if err := validateBrokerRPCSessionRecord(record); err != nil {
		return err
	}
	if record.ID != *envelope.SessionID || record.Generation != *envelope.Generation {
		return errors.New("session result identity does not match its envelope")
	}
	return nil
}

func brokerRPCPayload[T any](envelope BrokerRPCEnvelope) (T, error) {
	var payload T
	if err := strictDecode(envelope.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func validateBrokerRPCCursor(cursor *string) error {
	if cursor == nil {
		return nil
	}
	if len(*cursor) == 0 || len(*cursor) > MaxBrokerRPCCursorBytes {
		return errors.New("cursor is outside bounds")
	}
	for _, character := range *cursor {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return errors.New("cursor must be base64url text")
		}
	}
	return nil
}

func validateBrokerRPCText(value string, maxBytes int, name string) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is outside bounds", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func validateBrokerRPCSessionTitle(value string) error {
	if value == "" || len(value) > MaxBrokerRPCSessionTitleBytes || strings.IndexByte(value, 0) >= 0 {
		return errors.New("session title must be 1..256 bytes without NUL")
	}
	return nil
}

func validateBrokerRPCSessionRecord(record BrokerRPCSessionRecord) error {
	if !validUUID(record.ID) || !validUUID(record.Generation) || !validUUID(record.ProjectID) || !validUUID(record.ShellProfileID) {
		return errors.New("session record contains a non-canonical UUID")
	}
	if err := validateBrokerRPCSessionTitle(record.Title); err != nil {
		return err
	}
	if record.State != Starting && record.State != Running && record.State != Exiting && record.State != Exited && record.State != Lost {
		return errors.New("session record has an invalid lifecycle state")
	}
	if err := validateDimensions(record.Columns, record.Rows); err != nil {
		return err
	}
	created, err := parseBrokerRPCTime(record.CreatedAt, "createdAt")
	if err != nil {
		return err
	}
	updated, err := parseBrokerRPCTime(record.UpdatedAt, "updatedAt")
	if err != nil {
		return err
	}
	if updated.Before(created) {
		return errors.New("updatedAt precedes createdAt")
	}
	started, err := parseOptionalBrokerRPCTime(record.StartedAt, "startedAt")
	if err != nil {
		return err
	}
	ended, err := parseOptionalBrokerRPCTime(record.EndedAt, "endedAt")
	if err != nil {
		return err
	}
	if started != nil && started.Before(created) {
		return errors.New("startedAt precedes createdAt")
	}
	if ended != nil && ended.Before(created) {
		return errors.New("endedAt precedes createdAt")
	}
	if started != nil && ended != nil && ended.Before(*started) {
		return errors.New("endedAt precedes startedAt")
	}
	if started != nil && updated.Before(*started) {
		return errors.New("updatedAt precedes startedAt")
	}
	if ended != nil && updated.Before(*ended) {
		return errors.New("updatedAt precedes endedAt")
	}
	terminal := record.State == Exited || record.State == Lost
	if terminal != (ended != nil) {
		return errors.New("endedAt does not match lifecycle state")
	}
	if record.ExitCode != nil && record.State != Exited {
		return errors.New("exitCode is only valid for an exited session")
	}
	if record.ExitCode != nil && (*record.ExitCode < -2147483648 || *record.ExitCode > 2147483647) {
		return errors.New("exitCode is outside int32 bounds")
	}
	if record.ViewerCount != nil && (*record.ViewerCount < 0 || *record.ViewerCount > 16) {
		return errors.New("viewerCount is outside bounds")
	}
	if (record.LastOutputAt != nil || record.Preview != nil || record.Agent != nil) && terminal {
		return errors.New("lastOutputAt, preview and agent are only valid for a live session")
	}
	if record.Agent != nil {
		if err := validateAgentStatus(*record.Agent); err != nil {
			return err
		}
	}
	if _, err := parseOptionalBrokerRPCTime(record.LastOutputAt, "lastOutputAt"); err != nil {
		return err
	}
	if record.Preview != nil {
		return ValidatePreviewText(*record.Preview)
	}
	return nil
}

// ValidatePreviewText applies the schema's preview rule: safe display text of
// at most 160 characters and 480 UTF-8 bytes.
func ValidatePreviewText(value string) error {
	if err := validateBrokerRPCText(value, MaxBrokerRPCPreviewBytes, "preview"); err != nil {
		return err
	}
	if utf8.RuneCountInString(value) > MaxBrokerRPCPreviewRunes {
		return errors.New("preview is outside bounds")
	}
	return nil
}

func parseBrokerRPCTime(value, name string) (time.Time, error) {
	if len(value) < 20 || len(value) > 35 {
		return time.Time{}, fmt.Errorf("%s is outside bounds", name)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", name, err)
	}
	return parsed, nil
}

func parseOptionalBrokerRPCTime(value *string, name string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := parseBrokerRPCTime(*value, name)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

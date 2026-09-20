package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	brokerRPCRequestID  = "11111111-1111-4111-8111-111111111111"
	brokerRPCSessionID  = "22222222-2222-4222-8222-222222222222"
	brokerRPCGeneration = "33333333-3333-4333-8333-333333333333"
	brokerRPCOperation  = "44444444-4444-4444-8444-444444444444"
	brokerRPCProject    = "55555555-5555-4555-8555-555555555555"
	brokerRPCProfile    = "66666666-6666-4666-8666-666666666666"
)

func TestDecodeBrokerRPCRequests(t *testing.T) {
	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	tests := []struct {
		messageType BrokerRPCMessageType
		identity    bool
		payload     any
	}{
		{SessionListRequest, false, BrokerRPCSessionListRequestPayload{Limit: 50}},
		{SessionCreateRequest, false, BrokerRPCSessionCreateRequestPayload{
			OperationID: brokerRPCOperation, ProjectID: brokerRPCProject, ShellProfileID: brokerRPCProfile,
		}},
		{SessionRenameRequest, true, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: "release shell"}},
		{SessionTerminateRequest, true, BrokerRPCSessionTerminateRequestPayload{OperationID: brokerRPCOperation}},
		{SessionStatusRequest, true, BrokerRPCEmptyPayload{}},
	}
	for _, test := range tests {
		t.Run(string(test.messageType), func(t *testing.T) {
			envelope := BrokerRPCEnvelope{Version: Version, Type: test.messageType, RequestID: brokerRPCRequestID}
			if test.identity {
				envelope.SessionID, envelope.Generation = &sessionID, &generation
			}
			message := encodeBrokerRPCTestMessage(t, envelope, test.payload)
			decoded, err := DecodeBrokerRPCRequest(message)
			if err != nil || decoded.Type != test.messageType {
				t.Fatalf("decoded = %#v, %v", decoded, err)
			}
		})
	}
}

func TestDecodeBrokerRPCResponses(t *testing.T) {
	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	record := validBrokerRPCSessionRecord()
	tests := []struct {
		messageType BrokerRPCMessageType
		identity    bool
		payload     any
	}{
		{SessionListResult, false, BrokerRPCSessionListResultPayload{Sessions: []BrokerRPCSessionRecord{record}}},
		{SessionCreateResult, true, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record}},
		{SessionRenameResult, true, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record}},
		{SessionTerminateResult, true, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record}},
		{SessionStatusResult, true, BrokerRPCSessionResultPayload{Session: record}},
		{BrokerError, false, BrokerRPCErrorPayload{Code: SessionMissing, Message: "session does not exist"}},
	}
	for _, test := range tests {
		t.Run(string(test.messageType), func(t *testing.T) {
			envelope := BrokerRPCEnvelope{Version: Version, Type: test.messageType, RequestID: brokerRPCRequestID}
			if test.identity {
				envelope.SessionID, envelope.Generation = &sessionID, &generation
			}
			message := encodeBrokerRPCTestMessage(t, envelope, test.payload)
			decoded, err := DecodeBrokerRPCResponse(message)
			if err != nil || decoded.Type != test.messageType {
				t.Fatalf("decoded = %#v, %v", decoded, err)
			}
		})
	}
}

func TestBrokerRPCRejectsWrongDirectionAndUnknownTypes(t *testing.T) {
	request := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionListRequest, RequestID: brokerRPCRequestID,
	}, BrokerRPCSessionListRequestPayload{Limit: 10})
	if _, err := DecodeBrokerRPCResponse(request); err == nil || !strings.Contains(err.Error(), "not a broker RPC response") {
		t.Fatalf("request accepted as response: %v", err)
	}

	response := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionListResult, RequestID: brokerRPCRequestID,
	}, BrokerRPCSessionListResultPayload{Sessions: []BrokerRPCSessionRecord{}})
	if _, err := DecodeBrokerRPCRequest(response); err == nil || !strings.Contains(err.Error(), "not a broker RPC request") {
		t.Fatalf("response accepted as request: %v", err)
	}

	unknown := []byte(`{"version":1,"type":"session.destroy.request","requestId":"` + brokerRPCRequestID + `","payload":{}}`)
	if _, err := DecodeBrokerRPCRequest(unknown); err == nil {
		t.Fatal("unknown message type was accepted")
	}
}

func TestBrokerRPCCreateAcceptsSavedIDsOnly(t *testing.T) {
	valid := []byte(`{"version":1,"type":"session.create.request","requestId":"` + brokerRPCRequestID + `","payload":{"operationId":"` + brokerRPCOperation + `","projectId":"` + brokerRPCProject + `","shellProfileId":"` + brokerRPCProfile + `"}}`)
	if _, err := DecodeBrokerRPCRequest(valid); err != nil {
		t.Fatalf("saved-ID create rejected: %v", err)
	}

	for _, forbidden := range []string{
		`"executable":"/bin/zsh"`,
		`"workingDirectory":"/tmp"`,
		`"arguments":["-l"]`,
		`"environment":{"TOKEN":"secret"}`,
	} {
		message := []byte(strings.Replace(string(valid), `"}}`, `",`+forbidden+`}}`, 1))
		if _, err := DecodeBrokerRPCRequest(message); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("forbidden create field %s error = %v", forbidden, err)
		}
	}
}

func TestBrokerRPCRejectsMalformedAndUnknownFields(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"version":1,"version":1,"type":"session.list.request","requestId":"` + brokerRPCRequestID + `","payload":{"limit":10}}`),
		[]byte(`{"version":1,"type":"session.list.request","requestId":"` + brokerRPCRequestID + `","extra":true,"payload":{"limit":10}}`),
		[]byte(`{"version":1,"type":"session.list.request","requestId":"` + brokerRPCRequestID + `","payload":{"limit":10,"extra":true}}`),
		[]byte(`{"version":1,"type":"session.list.request","requestId":"` + brokerRPCRequestID + `","payload":{"limit":10}} trailing`),
		[]byte(`{"version":1,"type":"session.list.request","requestId":"` + brokerRPCRequestID + `","payload":`),
		append([]byte(`{"version":1,"type":"session.rename.request","requestId":"`+brokerRPCRequestID+`","sessionId":"`+brokerRPCSessionID+`","generation":"`+brokerRPCGeneration+`","payload":{"operationId":"`+brokerRPCOperation+`","title":"`), []byte{0xff, '"', '}', '}'}...),
	}
	for index, message := range tests {
		if _, err := DecodeBrokerRPCRequest(message); err == nil {
			t.Fatalf("malformed message %d was accepted", index)
		}
	}
}

func TestBrokerRPCEnforcesIdentityAndMutationOperationIDs(t *testing.T) {
	missingIdentity := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionRenameRequest, RequestID: brokerRPCRequestID,
	}, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: "renamed"})
	if _, err := DecodeBrokerRPCRequest(missingIdentity); err == nil || !strings.Contains(err.Error(), "requires sessionId and generation") {
		t.Fatalf("missing identity error = %v", err)
	}

	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	for _, test := range []struct {
		messageType BrokerRPCMessageType
		payload     any
	}{
		{SessionCreateRequest, BrokerRPCSessionCreateRequestPayload{OperationID: "retry-me", ProjectID: brokerRPCProject, ShellProfileID: brokerRPCProfile}},
		{SessionRenameRequest, BrokerRPCSessionRenameRequestPayload{OperationID: "retry-me", Title: "renamed"}},
		{SessionTerminateRequest, BrokerRPCSessionTerminateRequestPayload{OperationID: "retry-me"}},
	} {
		envelope := BrokerRPCEnvelope{Version: Version, Type: test.messageType, RequestID: brokerRPCRequestID}
		if test.messageType != SessionCreateRequest {
			envelope.SessionID, envelope.Generation = &sessionID, &generation
		}
		if _, err := DecodeBrokerRPCRequest(encodeBrokerRPCTestMessage(t, envelope, test.payload)); err == nil || !strings.Contains(err.Error(), "operationId") {
			t.Fatalf("%s invalid operation ID error = %v", test.messageType, err)
		}
	}

	badResult := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionCreateResult, RequestID: brokerRPCRequestID,
		SessionID: &sessionID, Generation: &generation,
	}, BrokerRPCSessionMutationResultPayload{OperationID: "retry-me", Session: validBrokerRPCSessionRecord()})
	if _, err := DecodeBrokerRPCResponse(badResult); err == nil || !strings.Contains(err.Error(), "operationId") {
		t.Fatalf("mutation result invalid operation ID error = %v", err)
	}
}

func TestBrokerRPCEnforcesMessageAndFieldBounds(t *testing.T) {
	if _, err := DecodeBrokerRPCRequest([]byte(strings.Repeat("x", MaxInboundMessageBytes+1))); err == nil {
		t.Fatal("oversized request was accepted")
	}
	for _, payload := range []BrokerRPCSessionListRequestPayload{
		{Limit: 0},
		{Limit: MaxBrokerRPCListSessions + 1},
		{Limit: 1, Cursor: brokerRPCStringPointer(strings.Repeat("a", MaxBrokerRPCCursorBytes+1))},
		{Limit: 1, Cursor: brokerRPCStringPointer("bad+cursor")},
	} {
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionListRequest, RequestID: brokerRPCRequestID,
		}, payload)
		if _, err := DecodeBrokerRPCRequest(message); err == nil {
			t.Fatalf("out-of-bounds list payload accepted: %#v", payload)
		}
	}

	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	longTitle := strings.Repeat("é", 65)
	rename := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionRenameRequest, RequestID: brokerRPCRequestID,
		SessionID: &sessionID, Generation: &generation,
	}, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: longTitle})
	if _, err := DecodeBrokerRPCRequest(rename); err == nil {
		t.Fatal("title over the byte bound was accepted")
	}

	records := make([]BrokerRPCSessionRecord, MaxBrokerRPCListSessions+1)
	for index := range records {
		records[index] = validBrokerRPCSessionRecord()
	}
	list := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionListResult, RequestID: brokerRPCRequestID,
	}, BrokerRPCSessionListResultPayload{Sessions: records})
	if _, err := DecodeBrokerRPCResponse(list); err == nil {
		t.Fatal("oversized session page was accepted")
	}
}

func TestBrokerRPCValidatesSessionRecords(t *testing.T) {
	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	tests := []BrokerRPCSessionRecord{
		func() BrokerRPCSessionRecord {
			record := validBrokerRPCSessionRecord()
			record.UpdatedAt = "not-a-time"
			return record
		}(),
		func() BrokerRPCSessionRecord {
			record := validBrokerRPCSessionRecord()
			record.EndedAt = brokerRPCStringPointer("2026-09-20T10:00:00Z")
			return record
		}(),
		func() BrokerRPCSessionRecord {
			record := validBrokerRPCSessionRecord()
			record.ID = brokerRPCProject
			return record
		}(),
		func() BrokerRPCSessionRecord {
			record := validBrokerRPCSessionRecord()
			record.State = Exited
			record.EndedAt = brokerRPCStringPointer("2026-09-20T10:00:02Z")
			record.UpdatedAt = "2026-09-20T10:00:02Z"
			exitCode := int64(2147483648)
			record.ExitCode = &exitCode
			return record
		}(),
	}
	for index, record := range tests {
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionStatusResult, RequestID: brokerRPCRequestID,
			SessionID: &sessionID, Generation: &generation,
		}, BrokerRPCSessionResultPayload{Session: record})
		if _, err := DecodeBrokerRPCResponse(message); err == nil {
			t.Fatalf("invalid session record %d was accepted", index)
		}
	}

	duplicate := validBrokerRPCSessionRecord()
	message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionListResult, RequestID: brokerRPCRequestID,
	}, BrokerRPCSessionListResultPayload{Sessions: []BrokerRPCSessionRecord{duplicate, duplicate}})
	if _, err := DecodeBrokerRPCResponse(message); err == nil || !strings.Contains(err.Error(), "duplicate identity") {
		t.Fatalf("duplicate list identity error = %v", err)
	}
}

func TestBrokerRPCErrorCodesAreStableAndBounded(t *testing.T) {
	codes := []ErrorCode{
		Unauthorized, PairingExpired, PairingPending, Revoked, ProtocolMismatch, SessionMissing,
		GenerationChanged, SessionLost, NotController, StaleLease, InputGap, LimitExceeded,
		HistoryChanged, ResyncRequired, HostUnavailable, InternalError, InvalidRequest,
		OperationConflict, ProjectMissing, ShellProfileMissing,
	}
	for _, code := range codes {
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: BrokerError, RequestID: brokerRPCRequestID,
		}, BrokerRPCErrorPayload{Code: code, Message: "safe message"})
		if _, err := DecodeBrokerRPCResponse(message); err != nil {
			t.Errorf("stable error code %q rejected: %v", code, err)
		}
	}
	invalid := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: BrokerError, RequestID: brokerRPCRequestID,
	}, BrokerRPCErrorPayload{Code: "not_real", Message: "safe"})
	if _, err := DecodeBrokerRPCResponse(invalid); err == nil {
		t.Fatal("unknown broker error code was accepted")
	}
}

func encodeBrokerRPCTestMessage(t *testing.T, envelope BrokerRPCEnvelope, payload any) []byte {
	t.Helper()
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Payload = encodedPayload
	message, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func validBrokerRPCSessionRecord() BrokerRPCSessionRecord {
	return BrokerRPCSessionRecord{
		ID: brokerRPCSessionID, Generation: brokerRPCGeneration, Title: "project shell",
		ProjectID: brokerRPCProject, ShellProfileID: brokerRPCProfile, State: Running,
		Columns: 80, Rows: 24, CreatedAt: "2026-09-20T10:00:00Z",
		UpdatedAt: "2026-09-20T10:00:01Z", StartedAt: brokerRPCStringPointer("2026-09-20T10:00:01Z"),
	}
}

func brokerRPCStringPointer(value string) *string { return &value }

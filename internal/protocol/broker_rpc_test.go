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
		{SessionCreateResult, false, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record}},
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

func TestBrokerRPCRejectsCaseVariantOperationIDShadow(t *testing.T) {
	message := []byte(`{"version":1,"type":"session.create.request","requestId":"` + brokerRPCRequestID + `","payload":{"operationId":"` + brokerRPCOperation + `","OperationID":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","projectId":"` + brokerRPCProject + `","shellProfileId":"` + brokerRPCProfile + `"}}`)
	if _, err := DecodeBrokerRPCRequest(message); err == nil || !strings.Contains(err.Error(), `unknown field "OperationID"`) {
		t.Fatalf("case-variant operation ID shadow error = %v", err)
	}
}

func TestBrokerRPCRejectsUppercaseUUIDs(t *testing.T) {
	message := []byte(`{"version":1,"type":"session.create.request","requestId":"` + brokerRPCRequestID + `","payload":{"operationId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA","projectId":"` + brokerRPCProject + `","shellProfileId":"` + brokerRPCProfile + `"}}`)
	if _, err := DecodeBrokerRPCRequest(message); err == nil || !strings.Contains(err.Error(), "operationId") {
		t.Fatalf("uppercase operation UUID error = %v", err)
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
	createWithIdentity := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionCreateResult, RequestID: brokerRPCRequestID,
		SessionID: &sessionID, Generation: &generation,
	}, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: validBrokerRPCSessionRecord()})
	if _, err := DecodeBrokerRPCResponse(createWithIdentity); err == nil || !strings.Contains(err.Error(), "must not carry session identity") {
		t.Fatalf("create result identity error = %v", err)
	}

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
	boundaryTitle := strings.Repeat("é", 64)
	validRename := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionRenameRequest, RequestID: brokerRPCRequestID,
		SessionID: &sessionID, Generation: &generation,
	}, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: boundaryTitle})
	if _, err := DecodeBrokerRPCRequest(validRename); err != nil {
		t.Fatalf("128-byte Unicode title rejected: %v", err)
	}
	longTitle := strings.Repeat("é", 65)
	rename := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
		Version: Version, Type: SessionRenameRequest, RequestID: brokerRPCRequestID,
		SessionID: &sessionID, Generation: &generation,
	}, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: longTitle})
	if _, err := DecodeBrokerRPCRequest(rename); err == nil {
		t.Fatal("title over the byte bound was accepted")
	}

	for _, title := range []string{" padded ", "line\nbreak"} {
		rename = encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionRenameRequest, RequestID: brokerRPCRequestID,
			SessionID: &sessionID, Generation: &generation,
		}, BrokerRPCSessionRenameRequestPayload{OperationID: brokerRPCOperation, Title: title})
		if _, err := DecodeBrokerRPCRequest(rename); err == nil {
			t.Fatalf("unsafe rename title %q was accepted", title)
		}
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

func TestBrokerRPCPreservesStoredSessionTitles(t *testing.T) {
	for _, title := range []string{
		strings.Repeat("é", 128),
		strings.Repeat("x", 255) + "\n",
	} {
		record := validBrokerRPCSessionRecord()
		record.Title = title
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionCreateResult, RequestID: brokerRPCRequestID,
		}, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record})
		if _, err := DecodeBrokerRPCResponse(message); err != nil {
			t.Fatalf("store-valid %d-byte title rejected: %v", len(title), err)
		}
	}

	for _, title := range []string{strings.Repeat("é", 129), "bad\x00title"} {
		record := validBrokerRPCSessionRecord()
		record.Title = title
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionCreateResult, RequestID: brokerRPCRequestID,
		}, BrokerRPCSessionMutationResultPayload{OperationID: brokerRPCOperation, Session: record})
		if _, err := DecodeBrokerRPCResponse(message); err == nil {
			t.Fatalf("store-invalid %d-byte title accepted", len(title))
		}
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
		func() BrokerRPCSessionRecord {
			record := validBrokerRPCSessionRecord()
			viewers := int64(17)
			record.ViewerCount = &viewers
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

func TestBrokerRPCValidatesSessionActivity(t *testing.T) {
	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	decode := func(mutate func(*BrokerRPCSessionRecord)) error {
		record := validBrokerRPCSessionRecord()
		mutate(&record)
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionStatusResult, RequestID: brokerRPCRequestID,
			SessionID: &sessionID, Generation: &generation,
		}, BrokerRPCSessionResultPayload{Session: record})
		_, err := DecodeBrokerRPCResponse(message)
		return err
	}
	for name, value := range map[string]string{
		"ascii": "$ make test", "max runes": strings.Repeat("界", 160), "max bytes": strings.Repeat("€", 160),
		"emoji": "👩\u200d💻 done", "replacement": "a\ufffdb",
	} {
		if err := decode(func(record *BrokerRPCSessionRecord) {
			record.LastOutputAt = brokerRPCStringPointer("2026-09-20T10:00:01.234Z")
			record.Preview = &value
		}); err != nil {
			t.Errorf("valid %s preview rejected: %v", name, err)
		}
	}
	for name, value := range map[string]string{
		"empty": "", "leading space": " x", "trailing space": "x ", "trailing nbsp": "x\u00a0",
		"ideographic space": "\u3000x", "newline": "a\nb", "escape": "a\x1b[31mb", "tab": "a\tb",
		"del": "a\x7fb", "c1": "a\u009bb", "too many runes": strings.Repeat("a", 161),
		"too many bytes": strings.Repeat("😀", 121),
	} {
		if err := decode(func(record *BrokerRPCSessionRecord) { record.Preview = &value }); err == nil {
			t.Errorf("%s preview was accepted", name)
		}
	}
	for _, value := range []string{"", "yesterday", "2026-09-20", "2026-09-20T10:00:01.234567890123+00:00:00"} {
		if err := decode(func(record *BrokerRPCSessionRecord) { record.LastOutputAt = &value }); err == nil {
			t.Errorf("lastOutputAt %q was accepted", value)
		}
	}
	for _, state := range []State{Exited, Lost} {
		ended := func(record *BrokerRPCSessionRecord) {
			record.State = state
			record.EndedAt = brokerRPCStringPointer("2026-09-20T10:00:02Z")
			record.UpdatedAt = "2026-09-20T10:00:02Z"
		}
		if err := decode(ended); err != nil {
			t.Fatalf("%s record rejected: %v", state, err)
		}
		if err := decode(func(record *BrokerRPCSessionRecord) {
			ended(record)
			record.LastOutputAt = brokerRPCStringPointer("2026-09-20T10:00:01.234Z")
		}); err == nil {
			t.Errorf("%s record with lastOutputAt was accepted", state)
		}
		if err := decode(func(record *BrokerRPCSessionRecord) {
			ended(record)
			preview := "done"
			record.Preview = &preview
		}); err == nil {
			t.Errorf("%s record with preview was accepted", state)
		}
	}
}

func TestBrokerRPCErrorCodesAreStableAndBounded(t *testing.T) {
	codes := []ErrorCode{
		Unauthorized, PairingExpired, PairingPending, ErrorCodeRevoked, ProtocolMismatch, SessionMissing,
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

func TestBrokerRPCValidatesAgentStatus(t *testing.T) {
	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	decode := func(agent AgentStatus, state State) error {
		record := validBrokerRPCSessionRecord()
		if state != Running {
			record.State = state
			record.EndedAt = brokerRPCStringPointer("2026-09-20T10:00:02Z")
			record.UpdatedAt = "2026-09-20T10:00:02Z"
		}
		record.Agent = &agent
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionStatusResult, RequestID: brokerRPCRequestID,
			SessionID: &sessionID, Generation: &generation,
		}, BrokerRPCSessionResultPayload{Session: record})
		_, err := DecodeBrokerRPCResponse(message)
		return err
	}
	since := "2026-09-20T10:00:01.234Z"
	question := func() *AgentQuestion {
		return &AgentQuestion{ID: strings.Repeat("a", 32), Text: "Do you want to proceed?",
			Options: []AgentOption{{Label: "Yes"}, {Label: "No"}}}
	}
	withDetail := func(detail string) *AgentQuestion {
		value := question()
		value.Detail = &detail
		return value
	}
	if err := decode(AgentStatus{State: Waiting, StateSince: since, Question: question()}, Running); err != nil {
		t.Fatalf("valid waiting rejected: %v", err)
	}
	if err := decode(AgentStatus{State: Waiting, StateSince: since, Question: withDetail("Bash command\nnpm test -- --watch=false")}, Running); err != nil {
		t.Fatalf("valid detail rejected: %v", err)
	}
	if err := decode(AgentStatus{State: Idle, StateSince: since}, Running); err != nil {
		t.Fatalf("valid idle rejected: %v", err)
	}
	afterWork := true
	if err := decode(AgentStatus{State: Idle, StateSince: since, AfterWork: &afterWork}, Running); err != nil {
		t.Fatalf("idle after work rejected: %v", err)
	}
	for name, test := range map[string]struct {
		agent AgentStatus
		state State
	}{
		"exited":             {AgentStatus{State: Idle, StateSince: since}, Exited},
		"unknown state":      {AgentStatus{State: "thinking", StateSince: since}, Running},
		"bad since":          {AgentStatus{State: Idle, StateSince: "now"}, Running},
		"waiting no q":       {AgentStatus{State: Waiting, StateSince: since}, Running},
		"question on idle":   {AgentStatus{State: Idle, StateSince: since, Question: question()}, Running},
		"afterWork working":  {AgentStatus{State: Working, StateSince: since, AfterWork: &afterWork}, Running},
		"bad id":             {AgentStatus{State: Waiting, StateSince: since, Question: &AgentQuestion{ID: "ABC", Text: "Q?", Options: []AgentOption{{Label: "Yes"}}}}, Running},
		"control text":       {AgentStatus{State: Waiting, StateSince: since, Question: &AgentQuestion{ID: strings.Repeat("a", 32), Text: "Q\x1b?", Options: []AgentOption{{Label: "Yes"}}}}, Running},
		"no options":         {AgentStatus{State: Waiting, StateSince: since, Question: &AgentQuestion{ID: strings.Repeat("a", 32), Text: "Q?"}}, Running},
		"seven options":      {AgentStatus{State: Waiting, StateSince: since, Question: &AgentQuestion{ID: strings.Repeat("a", 32), Text: "Q?", Options: make([]AgentOption, 7)}}, Running},
		"detail control":     {AgentStatus{State: Waiting, StateSince: since, Question: withDetail("rm\x1b[31m")}, Running},
		"detail blank line":  {AgentStatus{State: Waiting, StateSince: since, Question: withDetail("a\n\nb")}, Running},
		"detail seven lines": {AgentStatus{State: Waiting, StateSince: since, Question: withDetail("1\n2\n3\n4\n5\n6\n7")}, Running},
		"detail long line":   {AgentStatus{State: Waiting, StateSince: since, Question: withDetail(strings.Repeat("x", 121))}, Running},
		"detail edge space":  {AgentStatus{State: Waiting, StateSince: since, Question: withDetail("a \nb")}, Running},
		"long label":         {AgentStatus{State: Waiting, StateSince: since, Question: &AgentQuestion{ID: strings.Repeat("a", 32), Text: "Q?", Options: []AgentOption{{Label: strings.Repeat("x", 81)}}}}, Running},
	} {
		if err := decode(test.agent, test.state); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

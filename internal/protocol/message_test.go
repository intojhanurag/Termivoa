package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testRequestID  = "11111111-1111-4111-8111-111111111111"
	testSessionID  = "22222222-2222-4222-8222-222222222222"
	testGeneration = "33333333-3333-4333-8333-333333333333"
	testSnapshotID = "44444444-4444-4444-8444-444444444444"
)

func TestDecodeClientHello(t *testing.T) {
	message := encodeTestMessage(t, Hello, HelloPayload{
		CapabilityProfile: PhoneTerminalV1,
		ClientBuild:       "browser-test",
		ClientKind:        Browser,
		SupportedVersions: []int64{1},
	}, false)

	envelope, err := DecodeClient(message)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodePayload[HelloPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ClientBuild != "browser-test" {
		t.Fatalf("client build = %q", payload.ClientBuild)
	}
}

func TestDecodeClientRejectsDirectionAndUnknownFields(t *testing.T) {
	validError := encodeTestMessage(t, Error, ErrorPayload{Code: InternalError, Message: "safe"}, false)
	if _, err := DecodeClient(validError); err == nil || !strings.Contains(err.Error(), "not a client message") {
		t.Fatalf("host direction error = %v", err)
	}

	unknownEnvelope := []byte(`{"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientBuild":"web","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"},"unexpected":true}`)
	if _, err := DecodeClient(unknownEnvelope); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown envelope field error = %v", err)
	}

	unknownPayload := []byte(`{"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientBuild":"web","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1","unexpected":true}}`)
	if _, err := DecodeClient(unknownPayload); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown payload field error = %v", err)
	}
}

func TestDecodeRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	duplicateEnvelope := []byte(`{"version":1,"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientBuild":"web","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"}}`)
	if _, err := DecodeClient(duplicateEnvelope); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate envelope error = %v", err)
	}

	duplicatePayload := []byte(`{"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientBuild":"web","clientBuild":"other","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"}}`)
	if _, err := DecodeClient(duplicatePayload); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate payload error = %v", err)
	}
}

func TestDecodeRejectsCaseVariantShadowFields(t *testing.T) {
	shadowedEnvelope := []byte(`{"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","RequestID":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","payload":{"clientBuild":"web","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"}}`)
	if _, err := DecodeClient(shadowedEnvelope); err == nil || !strings.Contains(err.Error(), `unknown field "RequestID"`) {
		t.Fatalf("case-variant envelope shadow error = %v", err)
	}

	shadowedPayload := []byte(`{"version":1,"type":"hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientBuild":"web","ClientBuild":"shadow","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"}}`)
	if _, err := DecodeClient(shadowedPayload); err == nil || !strings.Contains(err.Error(), `unknown field "ClientBuild"`) {
		t.Fatalf("case-variant payload shadow error = %v", err)
	}
}

func TestDecodeRejectsUppercaseUUIDs(t *testing.T) {
	message := []byte(`{"version":1,"type":"hello","requestId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA","payload":{"clientBuild":"web","clientKind":"browser","supportedVersions":[1],"capabilityProfile":"phone-terminal-v1"}}`)
	if _, err := DecodeClient(message); err == nil || !strings.Contains(err.Error(), "canonical UUID") {
		t.Fatalf("uppercase request UUID error = %v", err)
	}
}

func TestSessionMessagesRequireIdentity(t *testing.T) {
	message := encodeTestMessage(t, SessionAttach, EmptyPayload{}, false)
	if _, err := DecodeClient(message); err == nil || !strings.Contains(err.Error(), "requires sessionId and generation") {
		t.Fatalf("missing session identity error = %v", err)
	}

	helloWithSession := encodeTestMessage(t, Hello, HelloPayload{
		CapabilityProfile: PhoneTerminalV1,
		ClientBuild:       "browser-test",
		ClientKind:        Browser,
		SupportedVersions: []int64{1},
	}, true)
	if _, err := DecodeClient(helloWithSession); err == nil || !strings.Contains(err.Error(), "must not carry session identity") {
		t.Fatalf("unexpected hello identity error = %v", err)
	}

	errorWithPartialIdentity := encodeTestMessage(t, Error, ErrorPayload{Code: InternalError, Message: "safe"}, false)
	var partial map[string]any
	if err := json.Unmarshal(errorWithPartialIdentity, &partial); err != nil {
		t.Fatal(err)
	}
	partial["sessionId"] = testSessionID
	errorWithPartialIdentity, err := json.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHost(errorWithPartialIdentity); err == nil || !strings.Contains(err.Error(), "must appear together") {
		t.Fatalf("partial error identity error = %v", err)
	}
}

func TestDecodeRejectsMissingRequiredNestedFields(t *testing.T) {
	missingModifier := []byte(`{"version":1,"type":"input.key","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"lease":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","inputSequence":"1","key":"enter","modifiers":{"alt":false,"ctrl":false,"meta":false}}}`)
	if _, err := DecodeClient(missingModifier); err == nil || !strings.Contains(err.Error(), "modifiers.shift is missing") {
		t.Fatalf("missing nested required field error = %v", err)
	}

	missingNullableController := []byte(`{"version":1,"type":"control.changed","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"leaseEpoch":"1"}}`)
	if _, err := DecodeHost(missingNullableController); err == nil || !strings.Contains(err.Error(), "controllerConnectionId is missing") {
		t.Fatalf("missing required nullable field error = %v", err)
	}
}

func TestDecodeRejectsNullWhereSchemaRequiresAValue(t *testing.T) {
	nullOptional := []byte(`{"version":1,"type":"screen.resync","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"reason":"initial","lastRevision":null}}`)
	if _, err := DecodeClient(nullOptional); err == nil || !strings.Contains(err.Error(), "lastRevision must not be null") {
		t.Fatalf("null optional field error = %v", err)
	}

	nullRows := []byte(`{"version":1,"type":"screen.patch","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"baseRevision":"1","revision":"2","historyReset":false,"screen":{"columns":20,"rows":5,"buffer":"normal","cursor":{"column":0,"row":0,"style":"block","visible":true,"blink":false},"foreground":{"r":255,"g":255,"b":255,"a":255},"background":{"r":0,"g":0,"b":0,"a":255},"cursorColor":{"r":255,"g":255,"b":255,"a":255},"widthMethod":"wcwidth","historyEpoch":"0","historyRevision":"0","contentClipped":false},"rows":null}}`)
	if _, err := DecodeHost(nullRows); err == nil || !strings.Contains(err.Error(), "rows must not be null") {
		t.Fatalf("null rows error = %v", err)
	}

	nullController := []byte(`{"version":1,"type":"control.changed","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"controllerConnectionId":null,"leaseEpoch":"1"}}`)
	if _, err := DecodeHost(nullController); err != nil {
		t.Fatalf("required nullable controller id: %v", err)
	}
}

func TestInputValidationUsesByteAndControlBounds(t *testing.T) {
	valid := InputTextPayload{
		Lease: strings.Repeat("A", 43), InputSequence: "1", Text: "hello 世界",
	}
	if _, err := DecodeClient(encodeTestMessage(t, InputText, valid, true)); err != nil {
		t.Fatalf("valid text: %v", err)
	}

	for name, payload := range map[string]InputTextPayload{
		"leading zero sequence": {Lease: valid.Lease, InputSequence: "01", Text: "hello"},
		"control character":     {Lease: valid.Lease, InputSequence: "1", Text: "line\n"},
		"utf8 byte overflow":    {Lease: valid.Lease, InputSequence: "1", Text: strings.Repeat("界", MaxTextBytes/3+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeClient(encodeTestMessage(t, InputText, payload, true)); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}

	validPaste := InputPastePayload{Lease: valid.Lease, InputSequence: "2", Text: "one\ntwo"}
	if _, err := DecodeClient(encodeTestMessage(t, InputPaste, validPaste, true)); err != nil {
		t.Fatalf("valid multiline paste: %v", err)
	}
	validPaste.Text = "\x1b[31m"
	if _, err := DecodeClient(encodeTestMessage(t, InputPaste, validPaste, true)); err == nil {
		t.Fatal("paste containing escape was accepted")
	}
}

func TestKeyAndResizeValidation(t *testing.T) {
	lease := strings.Repeat("B", 43)
	key := InputKeyPayload{
		Lease: lease, InputSequence: "9", Key: Character, Character: stringPointer("c"),
		Modifiers: Modifiers{Ctrl: true},
	}
	if _, err := DecodeClient(encodeTestMessage(t, InputKey, key, true)); err != nil {
		t.Fatalf("valid Ctrl+C: %v", err)
	}
	key.Character = nil
	if _, err := DecodeClient(encodeTestMessage(t, InputKey, key, true)); err == nil {
		t.Fatal("character key without character was accepted")
	}

	resize := TerminalResizePayload{Lease: lease, Columns: MinColumns, Rows: MinRows}
	if _, err := DecodeClient(encodeTestMessage(t, TerminalResize, resize, true)); err != nil {
		t.Fatalf("valid resize: %v", err)
	}
	resize.Columns = MinColumns - 1
	if _, err := DecodeClient(encodeTestMessage(t, TerminalResize, resize, true)); err == nil {
		t.Fatal("out-of-bounds resize was accepted")
	}
}

func TestHostPatchRequiresExactRevisionAndCompleteRows(t *testing.T) {
	row := testRow(20)
	patch := ScreenPatchPayload{
		BaseRevision: "4",
		Revision:     "5",
		Screen:       testScreen(),
		Rows:         []ChangedRow{{Index: 0, Row: row}},
	}
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, patch, true)); err != nil {
		t.Fatalf("valid patch: %v", err)
	}

	patch.Revision = "4"
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, patch, true)); err == nil {
		t.Fatal("non-advancing revision was accepted")
	}
	patch.Revision = "5"
	patch.Rows[0].Row.Runs[0].Cells = patch.Rows[0].Row.Runs[0].Cells[:19]
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, patch, true)); err == nil {
		t.Fatal("incomplete changed row was accepted")
	}
}

func TestHostRowRejectsUnsafeCellContentAndUncoalescedRuns(t *testing.T) {
	patch := ScreenPatchPayload{
		BaseRevision: "1", Revision: "2", Screen: testScreen(),
		Rows: []ChangedRow{{Index: 0, Row: testRow(20)}},
	}
	patch.Rows[0].Row.Runs[0].Cells[0].Text = "\x1b"
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, patch, true)); err == nil {
		t.Fatal("escape character in cell was accepted")
	}

	patch.Rows[0].Row = testRow(20)
	run := patch.Rows[0].Row.Runs[0]
	patch.Rows[0].Row.Runs = []CellRun{
		{Column: 0, Style: run.Style, Cells: run.Cells[:10]},
		{Column: 10, Style: run.Style, Cells: run.Cells[10:]},
	}
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, patch, true)); err == nil {
		t.Fatal("adjacent equal style runs were accepted")
	}
}

func TestMessageSizeLimit(t *testing.T) {
	if _, err := DecodeClient(make([]byte, MaxInboundMessageBytes+1)); err == nil {
		t.Fatal("oversized inbound message was accepted")
	}
}

func encodeTestMessage(t *testing.T, messageType MessageType, payload any, session bool) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope{Version: Version, Type: messageType, RequestID: testRequestID, Payload: payloadJSON}
	if session {
		envelope.SessionID = stringPointer(testSessionID)
		envelope.Generation = stringPointer(testGeneration)
	}
	message, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func testScreen() ScreenMetadata {
	return ScreenMetadata{
		Columns: 20, Rows: 5, Buffer: Normal,
		Cursor:      Cursor{Column: 0, Row: 0, Style: Block, Visible: true},
		Foreground:  Color{R: 213, G: 220, B: 229, A: 255},
		Background:  Color{R: 11, G: 14, B: 20, A: 255},
		CursorColor: Color{R: 213, G: 220, B: 229, A: 255},
		WidthMethod: Wcwidth, HistoryEpoch: "0", HistoryRevision: "0",
	}
}

func testRow(columns int) TerminalRow {
	cells := make([]Cell, columns)
	for index := range cells {
		cells[index] = Cell{Text: " ", Width: 1}
	}
	style := CellStyle{
		Foreground: Color{R: 213, G: 220, B: 229, A: 255},
		Background: Color{R: 11, G: 14, B: 20, A: 255},
		Underline:  UnderlineNone,
	}
	return TerminalRow{Runs: []CellRun{{Column: 0, Style: style, Cells: cells}}}
}

func stringPointer(value string) *string { return &value }

func TestAnswerAndAgentStateValidation(t *testing.T) {
	lease := strings.Repeat("A", 43)
	valid := InputAnswerPayload{Lease: lease, InputSequence: "3", QuestionID: strings.Repeat("0f", 16), OptionIndex: 5}
	if _, err := DecodeClient(encodeTestMessage(t, InputAnswer, valid, true)); err != nil {
		t.Fatalf("valid answer: %v", err)
	}
	for name, payload := range map[string]InputAnswerPayload{
		"negative index": {Lease: lease, InputSequence: "3", QuestionID: valid.QuestionID, OptionIndex: -1},
		"index too big":  {Lease: lease, InputSequence: "3", QuestionID: valid.QuestionID, OptionIndex: 6},
		"short id":       {Lease: lease, InputSequence: "3", QuestionID: "0f", OptionIndex: 0},
		"bad sequence":   {Lease: lease, InputSequence: "03", QuestionID: valid.QuestionID, OptionIndex: 0},
	} {
		if _, err := DecodeClient(encodeTestMessage(t, InputAnswer, payload, true)); err == nil {
			t.Errorf("%s answer was accepted", name)
		}
	}
	if _, err := DecodeClient(encodeTestMessage(t, InputAnswer, valid, false)); err == nil {
		t.Error("answer without session identity was accepted")
	}

	agent := &AgentStatus{State: Working, StateSince: "2026-09-20T10:00:01.234Z"}
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Running, Agent: agent}, true)); err != nil {
		t.Fatalf("running agent state: %v", err)
	}
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Exiting, Agent: agent}, true)); err == nil {
		t.Fatal("agent state on an exiting session was accepted")
	}
	if _, err := DecodeHost(encodeTestMessage(t, InputRejected, InputRejectedPayload{Code: QuestionChanged, Message: "refresh"}, true)); err != nil {
		t.Fatalf("question_changed rejection: %v", err)
	}
}

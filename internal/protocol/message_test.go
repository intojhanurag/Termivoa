package protocol

import (
	"encoding/json"
	"slices"
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

func TestChangesValidation(t *testing.T) {
	if _, err := DecodeClient(encodeTestMessage(t, ChangesList, ChangesListPayload{}, true)); err != nil {
		t.Fatalf("changes.list: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesList, ChangesListPayload{}, false)); err == nil {
		t.Fatal("changes.list without session identity was accepted")
	}
	for _, path := range []string{"a.go", "dir/with space/x\ny", "a:b", "..x/y..", strings.Repeat("é", 4096)} {
		if _, err := DecodeClient(encodeTestMessage(t, ChangesFile, ChangesFilePayload{Path: path}, true)); err != nil {
			t.Errorf("path %q: %v", path, err)
		}
	}
	for _, path := range []string{"", "/etc/passwd", `\\server\share`, `C:\x`, "c:/x", "../x", "a/../b", `a\..\b`, "a/..", "nul\x00", strings.Repeat("a", 4097)} {
		if _, err := DecodeClient(encodeTestMessage(t, ChangesFile, ChangesFilePayload{Path: path}, true)); err == nil {
			t.Errorf("path %q was accepted", path)
		}
	}
	invalidUTF8 := []byte(`{"version":1,"type":"changes.file","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"path":"\xff"}}`)
	if _, err := DecodeClient(invalidUTF8); err == nil {
		t.Error("invalid UTF-8 path was accepted")
	}
	if _, err := DecodeHost(encodeTestMessage(t, ChangesSummary, ChangesSummaryPayload{State: ChangesStateReady, Files: []ChangedFile{}}, true)); err != nil {
		t.Fatalf("empty summary: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesSummary, ChangesSummaryPayload{State: ChangesStateReady, Files: []ChangedFile{}}, true)); err == nil {
		t.Fatal("client sent changes.summary")
	}

	file := ChangedFile{Path: "new.go", OldPath: stringPointer("old.go"), Status: Renamed, Additions: 1}
	valid := ChangesSummaryPayload{State: ChangesStateReady, Root: stringPointer("/repo"), Branch: stringPointer("main"), Files: []ChangedFile{file}}
	if _, err := DecodeHost(encodeTestMessage(t, ChangesSummary, valid, true)); err != nil {
		t.Fatalf("valid summary: %v", err)
	}
	for name, payload := range map[string]ChangesSummaryPayload{
		"bad state":      {State: "dirty", Files: []ChangedFile{}},
		"files not git":  {State: NotGit, Files: []ChangedFile{file}},
		"too many files": {State: ChangesStateReady, Files: make([]ChangedFile, MaxChangedFiles+1)},
		"bad status":     {State: ChangesStateReady, Files: []ChangedFile{{Path: "a", Status: "copied"}}},
		"negative count": {State: ChangesStateReady, Files: []ChangedFile{{Path: "a", Status: Modified, Deletions: -1}}},
		"absolute path":  {State: ChangesStateReady, Files: []ChangedFile{{Path: "/a", Status: Modified}}},
		"long branch":    {State: ChangesStateReady, Branch: stringPointer(strings.Repeat("b", 257)), Files: []ChangedFile{}},
		"empty root":     {State: ChangesStateReady, Root: stringPointer(""), Files: []ChangedFile{}},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesSummary, payload, true)); err == nil {
			t.Errorf("%s summary was accepted", name)
		}
	}
	if _, err := DecodeHost([]byte(`{"version":1,"type":"changes.summary","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"state":"ready","files":null,"truncated":false}}`)); err == nil {
		t.Error("null files were accepted")
	}

	one, two := int64(1), int64(2)
	lines := []DiffLine{
		{Kind: Hunk, Text: "@@ -1 +1,2 @@"},
		{Kind: Context, Text: "same", OldLine: &one, NewLine: &one},
		{Kind: Remove, Text: "old", OldLine: &two},
		{Kind: Add, Text: strings.Repeat("é", MaxDiffLineRunes), NewLine: &two},
	}
	if _, err := DecodeHost(encodeTestMessage(t, ChangesPatch, ChangesPatchPayload{Path: "a", State: PatchStateReady, Lines: lines}, true)); err != nil {
		t.Fatalf("valid patch: %v", err)
	}
	zero := int64(0)
	for name, payload := range map[string]ChangesPatchPayload{
		"bad state":      {Path: "a", State: "gone", Lines: []DiffLine{}},
		"binary lines":   {Path: "a", State: PatchStateBinary, Lines: lines},
		"too many lines": {Path: "a", State: PatchStateReady, Lines: make([]DiffLine, MaxDiffLines+1)},
		"long text":      {Path: "a", State: PatchStateReady, Lines: []DiffLine{{Kind: Hunk, Text: strings.Repeat("x", MaxDiffLineRunes+1)}}},
		"bad kind":       {Path: "a", State: PatchStateReady, Lines: []DiffLine{{Kind: "note", Text: "x"}}},
		"add old line":   {Path: "a", State: PatchStateReady, Lines: []DiffLine{{Kind: Add, Text: "x", OldLine: &one, NewLine: &one}}},
		"hunk numbers":   {Path: "a", State: PatchStateReady, Lines: []DiffLine{{Kind: Hunk, Text: "@@", NewLine: &one}}},
		"zero line":      {Path: "a", State: PatchStateReady, Lines: []DiffLine{{Kind: Remove, Text: "x", OldLine: &zero}}},
		"relative path":  {Path: "../a", State: Missing, Lines: []DiffLine{}},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesPatch, payload, true)); err == nil {
			t.Errorf("%s patch was accepted", name)
		}
	}
}

func TestChangesWriteValidation(t *testing.T) {
	lease := strings.Repeat("A", 43)
	valid := ChangesCommitPayload{Lease: lease, Message: "fix: " + strings.Repeat("é", MaxCommitMessage-5), Paths: []string{"a.go", "dir/b c"}}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesCommit, valid, true)); err != nil {
		t.Fatalf("valid commit: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesPush, ChangesPushPayload{Lease: lease}, true)); err != nil {
		t.Fatalf("valid push: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesPush, ChangesPushPayload{Lease: "short"}, true)); err == nil {
		t.Error("push with a short lease was accepted")
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesPush, ChangesPushPayload{Lease: lease}, false)); err == nil {
		t.Error("push without session identity was accepted")
	}
	for name, payload := range map[string]ChangesCommitPayload{
		"no lease":      {Message: "m", Paths: []string{"a"}},
		"empty message": {Lease: lease, Paths: []string{"a"}},
		"long message":  {Lease: lease, Message: strings.Repeat("m", MaxCommitMessage+1), Paths: []string{"a"}},
		"nul message":   {Lease: lease, Message: "a\x00b", Paths: []string{"a"}},
		"no paths":      {Lease: lease, Message: "m", Paths: []string{}},
		"many paths":    {Lease: lease, Message: "m", Paths: make([]string, MaxCommitPaths+1)},
		"outside path":  {Lease: lease, Message: "m", Paths: []string{"a", "../b"}},
		"absolute path": {Lease: lease, Message: "m", Paths: []string{"/etc/passwd"}},
	} {
		if _, err := DecodeClient(encodeTestMessage(t, ChangesCommit, payload, true)); err == nil {
			t.Errorf("%s commit was accepted", name)
		}
	}

	hash, output := "0a1b2c3", strings.Repeat("é", MaxGitOutput)
	for _, payload := range []ChangesCommittedPayload{
		{State: Committed, Commit: &hash}, {State: Nothing}, {State: CommitStateFailed, Output: &output},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesCommitted, payload, true)); err != nil {
			t.Errorf("valid committed %#v: %v", payload.State, err)
		}
	}
	long := output + "x"
	for name, payload := range map[string]ChangesCommittedPayload{
		"bad state":       {State: "done"},
		"no hash":         {State: Committed},
		"hash on failure": {State: CommitStateFailed, Commit: &hash},
		"upper hash":      {State: Committed, Commit: stringPointer("ABCDEF")},
		"short hash":      {State: Committed, Commit: stringPointer("abc")},
		"long output":     {State: CommitStateFailed, Output: &long},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesCommitted, payload, true)); err == nil {
			t.Errorf("%s committed was accepted", name)
		}
	}

	link := "https://github.com/o/r/pull/new/feat/x"
	for _, payload := range []ChangesPushedPayload{
		{State: Pushed, Branch: stringPointer("feat/x"), URL: &link}, {State: Pushed}, {State: NoRemote}, {State: PushStateFailed, Output: &output},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesPushed, payload, true)); err != nil {
			t.Errorf("valid pushed %#v: %v", payload, err)
		}
	}
	for name, payload := range map[string]ChangesPushedPayload{
		"bad state":      {State: "sent"},
		"url on failure": {State: PushStateFailed, URL: &link},
		"http url":       {State: Pushed, URL: stringPointer("http://github.com/o/r/pull/new/x")},
		"space url":      {State: Pushed, URL: stringPointer("https://github.com/o/r/pull/new/a b")},
		"control url":    {State: Pushed, URL: stringPointer("https://github.com/o/r/pull/new/x\x1b[K")},
		"no host":        {State: Pushed, URL: stringPointer("https:///pull/new/x")},
		"long url":       {State: Pushed, URL: stringPointer("https://h/" + strings.Repeat("a", 2039))},
		"empty branch":   {State: Pushed, Branch: stringPointer("")},
		"long output":    {State: PushStateFailed, Output: &long},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, ChangesPushed, payload, true)); err == nil {
			t.Errorf("%s pushed was accepted", name)
		}
	}
	if _, err := DecodeClient(encodeTestMessage(t, ChangesPushed, ChangesPushedPayload{State: NoRemote}, true)); err == nil {
		t.Error("client sent changes.pushed")
	}
}

func TestTasksValidation(t *testing.T) {
	if _, err := DecodeClient(encodeTestMessage(t, TasksList, TasksListPayload{}, true)); err != nil {
		t.Fatalf("tasks.list: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, TasksList, TasksListPayload{}, false)); err == nil {
		t.Fatal("tasks.list without session identity was accepted")
	}
	if _, err := DecodeClient([]byte(`{"version":1,"type":"tasks.list","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"folder":"/"}}`)); err == nil {
		t.Fatal("tasks.list with an unknown field was accepted")
	}
	task := ProjectTask{Name: "test:e2e/@a+b.c_d-1", Command: "pnpm run test:e2e/@a+b.c_d-1", Source: PackageJSON, Kind: Test}
	valid := TasksListedPayload{State: TasksStateReady, Folder: stringPointer("/src/é"), Tasks: []ProjectTask{task}}
	if _, err := DecodeHost(encodeTestMessage(t, TasksListed, valid, true)); err != nil {
		t.Fatalf("valid tasks.listed: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, TasksListed, valid, true)); err == nil {
		t.Fatal("client sent tasks.listed")
	}
	for _, payload := range []TasksListedPayload{{State: TasksStateNone, Tasks: []ProjectTask{}}, {State: TasksStateUnavailable, Tasks: []ProjectTask{}}} {
		if _, err := DecodeHost(encodeTestMessage(t, TasksListed, payload, true)); err != nil {
			t.Errorf("%s: %v", payload.State, err)
		}
	}
	with := func(change func(*ProjectTask)) []ProjectTask {
		changed := task
		change(&changed)
		return []ProjectTask{changed}
	}
	for name, payload := range map[string]TasksListedPayload{
		"bad state":     {State: "done", Tasks: []ProjectTask{}},
		"tasks on none": {State: TasksStateNone, Tasks: []ProjectTask{task}},
		"too many":      {State: TasksStateReady, Tasks: slices.Repeat([]ProjectTask{task}, MaxProjectTasks+1)},
		"empty folder":  {State: TasksStateReady, Folder: stringPointer(""), Tasks: []ProjectTask{}},
		"long folder":   {State: TasksStateReady, Folder: stringPointer("/" + strings.Repeat("a", 4096)), Tasks: []ProjectTask{}},
		"space name":    {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Name = "a b" })},
		"shell name":    {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Name = "a;rm" })},
		"option name":   {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Name = "-f" })},
		"long name":     {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Name = strings.Repeat("a", 129) })},
		"two lines":     {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Command = "npm run a\nrm -rf ~" })},
		"long command":  {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Command = strings.Repeat("a", 513) })},
		"empty command": {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Command = "" })},
		"bad source":    {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Source = "Rakefile" })},
		"bad kind":      {State: TasksStateReady, Tasks: with(func(task *ProjectTask) { task.Kind = "deploy" })},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, TasksListed, payload, true)); err == nil {
			t.Errorf("%s tasks.listed was accepted", name)
		}
	}
	if _, err := DecodeHost([]byte(`{"version":1,"type":"tasks.listed","requestId":"11111111-1111-4111-8111-111111111111","sessionId":"22222222-2222-4222-8222-222222222222","generation":"33333333-3333-4333-8333-333333333333","payload":{"state":"none","tasks":null}}`)); err == nil {
		t.Error("null tasks were accepted")
	}
}

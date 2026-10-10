package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func hostTestEnvelope(kind MessageType) Envelope {
	session, generation := testSessionID, testGeneration
	return Envelope{Version: Version, Type: kind, RequestID: testRequestID, SessionID: &session, Generation: &generation}
}

func hostTestPatch() ScreenPatchPayload {
	return ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: testScreen(), Rows: []ChangedRow{{Index: 0, Row: testRow(20)}}}
}

func TestEncodeHostTerminalBytesMatchStrictWirePath(t *testing.T) {
	patch := hostTestPatch()
	patch.Rows[0].Row.Runs[0].Cells[0].Text = "\"\\<&>é\u2028\u2029"
	link := "https://example.test/?x=\"<&>"
	patch.Rows[0].Row.Runs[0].Cells[0].Link = &link
	widePatch := hostTestPatch()
	widePatch.Rows[0].Row.Runs[0].Cells[0].Text = "界"
	widePatch.Rows[0].Row.Runs[0].Cells[0].Width = 2
	widePatch.Rows[0].Row.Runs[0].Cells[1] = Cell{Text: "", Width: 0, Continuation: true}
	widePatch.Rows[0].Row.Runs[0].Cells[2].Text = "\ufffd"
	widePatch.Rows[0].Row.Runs[0].Cells[2].ContentClipped = true
	for _, test := range []struct {
		kind    MessageType
		payload any
	}{
		{SnapshotBegin, SnapshotBeginPayload{SnapshotID: testSnapshotID, Revision: "2", Screen: testScreen()}},
		{SnapshotRows, SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "2", StartRow: 0, Rows: []TerminalRow{testRow(20)}}},
		{SnapshotEnd, SnapshotEndPayload{SnapshotID: testSnapshotID, Revision: "2", RowCount: 5}},
		{ScreenPatch, patch},
		{ScreenPatch, widePatch},
		{ScreenPatch, ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: testScreen(), Rows: []ChangedRow{}}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "1", HistoryRevision: "2", Columns: 20, Offset: 0, Complete: true, Rows: []TerminalRow{testRow(20)}}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "1", HistoryRevision: "2", Columns: 20, Offset: 0, Complete: true, Rows: []TerminalRow{}}},
		{InputAccepted, InputAcceptedPayload{InputSequence: "1"}},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			got, err := EncodeHost(hostTestEnvelope(test.kind), test.payload)
			if err != nil {
				t.Fatal(err)
			}
			want := encodeTestMessage(t, test.kind, test.payload, true)
			if !bytes.Equal(got, want) {
				t.Fatal("typed encoding differs from original payload/envelope serialization")
			}
			if _, err := DecodeHost(got); err != nil {
				t.Fatalf("typed encoder produced invalid wire bytes: %v", err)
			}
		})
	}
}

func TestEncodeHostSnapshotAndHistoryValidationParity(t *testing.T) {
	invalidHistoryNext := int64(4)
	for _, test := range []struct {
		kind    MessageType
		payload any
	}{
		{SnapshotBegin, SnapshotBeginPayload{SnapshotID: "\xff", Revision: "1", Screen: testScreen()}},
		{SnapshotBegin, SnapshotBeginPayload{SnapshotID: testSnapshotID, Revision: "01", Screen: testScreen()}},
		{SnapshotRows, SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "1", Rows: nil}},
		{SnapshotRows, SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "1", Rows: []TerminalRow{testRow(20)}, StartRow: -1}},
		{SnapshotRows, SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "1", Rows: []TerminalRow{testRow(20)}, StartRow: 200}},
		{SnapshotEnd, SnapshotEndPayload{SnapshotID: testSnapshotID, Revision: "1", RowCount: 201}},
		{SnapshotEnd, SnapshotEndPayload{SnapshotID: testSnapshotID, Revision: "\xff", RowCount: 5}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "\xff", HistoryRevision: "2", Columns: 20, Complete: true, Rows: []TerminalRow{}}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "1", HistoryRevision: "\xff", Columns: 20, Complete: true, Rows: []TerminalRow{}}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "1", HistoryRevision: "2", Columns: 20, Complete: true, Rows: nil}},
		{HistoryPage, HistoryPagePayload{HistoryEpoch: "1", HistoryRevision: "2", Columns: 20, Rows: []TerminalRow{testRow(20)}, NextOffset: &invalidHistoryNext}},
	} {
		if _, err := EncodeHost(hostTestEnvelope(test.kind), test.payload); err == nil {
			t.Fatalf("typed encoder accepted invalid %s payload", test.kind)
		}
		if _, err := DecodeHost(encodeTestMessage(t, test.kind, test.payload, true)); err == nil {
			t.Fatalf("strict decoder accepted invalid %s payload", test.kind)
		}
	}
}

func TestEncodeHostTerminalRejectsSameSemanticFailuresAsDecoder(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ScreenPatchPayload)
	}{
		{"nil rows", func(p *ScreenPatchPayload) { p.Rows = nil }},
		{"non-advancing revision", func(p *ScreenPatchPayload) { p.Revision = p.BaseRevision }},
		{"invalid decimal counter", func(p *ScreenPatchPayload) { p.Revision = "02" }},
		{"duplicate row", func(p *ScreenPatchPayload) { p.Rows = append(p.Rows, p.Rows[0]) }},
		{"row outside screen", func(p *ScreenPatchPayload) { p.Rows[0].Index = p.Screen.Rows }},
		{"incomplete row", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Cells = p.Rows[0].Row.Runs[0].Cells[:19] }},
		{"nil runs", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs = nil }},
		{"nil cells", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Cells = nil }},
		{"control text", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Cells[0].Text = "\x1b" }},
		{"unsafe link", func(p *ScreenPatchPayload) {
			link := "javascript:alert(1)"
			p.Rows[0].Row.Runs[0].Cells[0].Link = &link
		}},
		{"invalid width", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Cells[0].Width = 3 }},
		{"unpaired wide cell", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Cells[0].Width = 2 }},
		{"invalid style", func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Style.Foreground.A = 256 }},
		{"cursor outside screen", func(p *ScreenPatchPayload) { p.Screen.Cursor.Column = p.Screen.Columns }},
		{"invalid dimensions", func(p *ScreenPatchPayload) { p.Screen.Columns = 401 }},
		{"invalid width method", func(p *ScreenPatchPayload) { p.Screen.WidthMethod = "guess" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := hostTestPatch()
			test.mutate(&payload)
			if _, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload); err == nil {
				t.Fatal("typed encoder accepted invalid payload")
			}
			if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, payload, true)); err == nil {
				t.Fatal("strict wire decoder accepted invalid payload")
			}
		})
	}
}

func TestEncodeHostEnvelopeAndTypeRestrictions(t *testing.T) {
	for _, mutate := range []func(*Envelope){
		func(e *Envelope) { e.Version = 2 },
		func(e *Envelope) { e.Type = InputText },
		func(e *Envelope) { e.RequestID = "wrong" },
		func(e *Envelope) { e.SessionID = nil },
		func(e *Envelope) { bad := "wrong"; e.Generation = &bad },
	} {
		envelope := hostTestEnvelope(ScreenPatch)
		mutate(&envelope)
		if _, err := EncodeHost(envelope, hostTestPatch()); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	for _, payload := range []any{nil, map[string]any{}, json.RawMessage(`{}`), SnapshotRowsPayload{}, &ScreenPatchPayload{}} {
		if _, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload); err == nil {
			t.Fatalf("wrong generated payload type %T accepted", payload)
		}
	}
	// Low-rate messages still run the strict wire decoder, including duplicate
	// key and required-field checks for a caller-provided raw JSON payload.
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"inputSequence":"1","inputSequence":"2"}`),
		json.RawMessage(`{"InputSequence":"1"}`),
		json.RawMessage(`{}`),
	} {
		if _, err := EncodeHost(hostTestEnvelope(InputAccepted), raw); err == nil {
			t.Fatal("strict fallback accepted malformed JSON")
		}
	}
}

func TestEncodeHostTerminalEnforcesWireByteLimit(t *testing.T) {
	row := testRow(80)
	for i := range row.Runs[0].Cells {
		row.Runs[0].Cells[i].Text = strings.Repeat("<", 1024)
	}
	payload := SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "2", Rows: []TerminalRow{row, row, row}, StartRow: 0}
	if _, err := EncodeHost(hostTestEnvelope(SnapshotRows), payload); !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("escaped oversized payload error: %v", err)
	}
}

func TestEncodeHostRejectsLinkThatMarshalWouldExpandBeyondItsLimit(t *testing.T) {
	payload := hostTestPatch()
	prefix := "https://example.test/?x="
	link := prefix + strings.Repeat("x", 2048-len(prefix)-1) + "\xff"
	payload.Rows[0].Row.Runs[0].Cells[0].Link = &link
	// encoding/json replaces the malformed byte with U+FFFD, increasing the
	// decoded link length past 2048. A typed validator must not emit bytes the
	// receiving decoder rejects after that replacement.
	if _, err := DecodeHost(encodeTestMessage(t, ScreenPatch, payload, true)); err == nil {
		t.Fatal("strict decoder accepted the expanded link")
	}
	if _, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload); err == nil {
		t.Fatal("typed encoder accepted malformed UTF-8 in link")
	}
}

func TestEncodeHostTerminalStringNormalizationBoundaries(t *testing.T) {
	for _, text := range []string{"\xff", "\xed\xa0\x80", strings.Repeat("\xff", 1024)} {
		payload := hostTestPatch()
		payload.Rows[0].Row.Runs[0].Cells[0].Text = text
		want := encodeTestMessage(t, ScreenPatch, payload, true)
		_, wireErr := DecodeHost(want)
		got, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload)
		if (err == nil) != (wireErr == nil) || err == nil && !bytes.Equal(got, want) {
			t.Fatalf("malformed Go text differs from original wire normalization: typed %v wire %v", err, wireErr)
		}
	}
	for _, mutate := range []func(*ScreenPatchPayload){
		func(p *ScreenPatchPayload) { p.Revision = "\xff" },
		func(p *ScreenPatchPayload) { p.BaseRevision = "\xff" },
		func(p *ScreenPatchPayload) { p.Screen.HistoryEpoch = "\xff" },
		func(p *ScreenPatchPayload) { p.Screen.HistoryRevision = "\xff" },
		func(p *ScreenPatchPayload) { p.Screen.Buffer = "\xff" },
		func(p *ScreenPatchPayload) { p.Screen.WidthMethod = "\xff" },
		func(p *ScreenPatchPayload) { p.Screen.Cursor.Style = "\xff" },
		func(p *ScreenPatchPayload) { p.Rows[0].Row.Runs[0].Style.Underline = "\xff" },
	} {
		payload := hostTestPatch()
		mutate(&payload)
		if _, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload); err == nil {
			t.Fatal("malformed UTF-8 metadata accepted")
		}
	}
	for _, text := range []string{
		strings.Repeat("é", 512), // Exactly 1024 bytes, with no replacement.
		"\ufffd",                 // A real replacement scalar is valid display text.
		`\ud800`,                 // Printable backslash text must not become a surrogate escape.
		"😀",                      // A valid astral scalar must survive encoding unchanged.
	} {
		payload := hostTestPatch()
		payload.Rows[0].Row.Runs[0].Cells[0].Text = text
		message, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := DecodeHost(message)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodePayload[ScreenPatchPayload](envelope)
		if err != nil || decoded.Rows[0].Row.Runs[0].Cells[0].Text != text {
			t.Fatalf("string changed across encoding: %v", err)
		}
	}
	payload := hostTestPatch()
	payload.Rows[0].Row.Runs[0].Cells[0].Text = strings.Repeat("é", 513)
	if _, err := EncodeHost(hostTestEnvelope(ScreenPatch), payload); err == nil {
		t.Fatal("cell text above byte bound accepted")
	}
}

func TestEncodeHostMalformedOSCLinkMatchesOriginalWireNormalization(t *testing.T) {
	for _, rawLink := range []string{
		"https://example.test/?x=\xff",
		"https://example.test/?x=" + strings.Repeat("x", 2048-len("https://example.test/?x=")-1) + "\xff",
	} {
		core, err := terminal.New(context.Background(), terminal.Config{Columns: 20, Rows: 5})
		if err != nil {
			t.Fatal(err)
		}
		if err := core.WriteOutput(context.Background(), []byte("\x1b]8;;"+rawLink+"\x1b\\x\x1b]8;;\x1b\\")); err != nil {
			t.Fatal(err)
		}
		frame, err := core.ScreenSnapshot(context.Background())
		_ = core.Close()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Screen[0].Cells[0].Link != rawLink {
			t.Fatal("emulator fixture did not retain raw query bytes")
		}
		_, rows, err := Snapshot(frame, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		payload := SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "1", Rows: rows, StartRow: 0}
		want := encodeTestMessage(t, SnapshotRows, payload, true)
		_, wireErr := DecodeHost(want)
		got, err := EncodeHost(hostTestEnvelope(SnapshotRows), payload)
		if len(rawLink) < 2048 && wireErr != nil || len(rawLink) == 2048 && wireErr == nil {
			t.Fatalf("unexpected original acceptance: %v", wireErr)
		}
		if (err == nil) != (wireErr == nil) || err == nil && !bytes.Equal(got, want) {
			t.Fatalf("OSC8 normalization differs: typed %v wire %v", err, wireErr)
		}
	}
}

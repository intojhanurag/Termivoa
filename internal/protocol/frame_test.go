package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func TestSnapshotCoalescesStylesAndPreservesWideCells(t *testing.T) {
	white := terminal.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := terminal.RGBA{A: 255}
	base := terminal.Cell{Text: " ", Width: 1, Foreground: white, Background: black}
	row := terminal.Row{Cells: make([]terminal.Cell, 20)}
	for index := range row.Cells {
		row.Cells[index] = base
	}
	row.Cells[0].Text = "界"
	row.Cells[0].Width = 2
	row.Cells[1] = row.Cells[0]
	row.Cells[1].Text = ""
	row.Cells[1].Width = 0
	row.Cells[1].Continuation = true
	row.Cells[2].Text = "x"
	row.Cells[2].Bold = true

	frame := terminal.Frame{
		Revision: 7, Columns: 20, Rows: 5, WidthMethod: "unicode-graphemes",
		Foreground: white, Background: black, CursorColor: white,
		Cursor: terminal.Cursor{Style: terminal.CursorBlock, Visible: true},
		Screen: []terminal.Row{row, row, row, row, row},
	}
	metadata, rows, err := Snapshot(frame, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.HistoryEpoch != "2" || metadata.HistoryRevision != "3" || len(rows) != 5 {
		t.Fatalf("unexpected snapshot metadata: %#v rows=%d", metadata, len(rows))
	}
	if len(rows[0].Runs) != 3 {
		t.Fatalf("expected three coalesced style runs, got %d", len(rows[0].Runs))
	}
	if rows[0].Runs[0].Cells[0].Text != "界" || !rows[0].Runs[0].Cells[1].Continuation {
		t.Fatalf("wide cell was not preserved: %#v", rows[0].Runs[0].Cells[:2])
	}
}

func TestSnapshotClipsMaximumEscapedRowBelowTransportBoundary(t *testing.T) {
	white := terminal.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := terminal.RGBA{A: 255}
	blank := terminal.Cell{Text: " ", Width: 1, Foreground: white, Background: black}
	pathological := blank
	pathological.Text = strings.Repeat("<", terminal.MaxCellContentBytes)
	pathological.Link = "https://example.test/" + strings.Repeat("<", terminal.MaxLinkBytes-len("https://example.test/"))

	rows := make([]terminal.Row, 5)
	for rowIndex := range rows {
		rows[rowIndex].Cells = make([]terminal.Cell, terminal.MaxColumns)
		for column := range rows[rowIndex].Cells {
			rows[rowIndex].Cells[column] = blank
		}
	}
	for column := range rows[0].Cells {
		rows[0].Cells[column] = pathological
	}
	frame := terminal.Frame{
		Revision: 1, Columns: terminal.MaxColumns, Rows: len(rows), WidthMethod: "unicode-graphemes",
		Foreground: white, Background: black, CursorColor: white,
		Cursor: terminal.Cursor{Style: terminal.CursorBlock, Visible: true}, Screen: rows,
	}

	metadata, normalized, err := Snapshot(frame, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.ContentClipped {
		t.Fatal("pathological row was clipped without advertising contentClipped")
	}
	encoded, err := json.Marshal(normalized[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxEncodedTerminalRowBytes {
		t.Fatalf("encoded row = %d bytes, limit %d", len(encoded), MaxEncodedTerminalRowBytes)
	}
	payload, err := json.Marshal(SnapshotRowsPayload{
		SnapshotID: "44444444-4444-4444-8444-444444444444",
		Revision:   "1",
		StartRow:   0,
		Rows:       normalized[:1],
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "22222222-2222-4222-8222-222222222222"
	generation := "33333333-3333-4333-8333-333333333333"
	message, err := json.Marshal(Envelope{
		Version: Version, Type: SnapshotRows,
		RequestID: "11111111-1111-4111-8111-111111111111",
		SessionID: &sessionID, Generation: &generation, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message) > MaxOutboundMessageBytes {
		t.Fatalf("one-row snapshot message = %d bytes, limit %d", len(message), MaxOutboundMessageBytes)
	}
	if _, err := DecodeHost(message); err != nil {
		t.Fatalf("bounded one-row snapshot is not valid protocol: %v", err)
	}
	clippedCells := 0
	for _, run := range normalized[0].Runs {
		for _, cell := range run.Cells {
			if cell.ContentClipped {
				clippedCells++
			}
		}
	}
	if clippedCells == 0 {
		t.Fatal("no cells record clipping")
	}
}

func TestPatchPayloadUsesDecimalStringRevisions(t *testing.T) {
	cell := terminal.Cell{Text: " ", Width: 1, Foreground: terminal.RGBA{A: 255}, Background: terminal.RGBA{A: 255}}
	row := terminal.Row{Cells: make([]terminal.Cell, 20)}
	for index := range row.Cells {
		row.Cells[index] = cell
	}
	patch, err := PatchPayload(terminal.Patch{
		BaseRevision: 9007199254740993,
		Revision:     9007199254740994,
		Columns:      20,
		Rows:         5,
		WidthMethod:  "wcwidth",
		Cursor:       terminal.Cursor{Style: terminal.CursorBlock},
		ChangedRows:  []terminal.ChangedRow{{Index: 4, Row: row}},
	}, 1, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if patch.BaseRevision != "9007199254740993" || patch.Revision != "9007199254740994" || !patch.HistoryReset {
		t.Fatalf("unexpected patch counters: %#v", patch)
	}
}

func TestSnapshotRejectsInvalidCanonicalState(t *testing.T) {
	frame := terminal.Frame{
		Columns: 20, Rows: 5, WidthMethod: "unknown",
		Cursor: terminal.Cursor{Style: terminal.CursorBlock},
		Screen: make([]terminal.Row, 5),
	}
	if _, _, err := Snapshot(frame, 0, 0); err == nil {
		t.Fatal("expected invalid width method to be rejected")
	}
}

func TestPatchPayloadRejectsNonAdvancingRevision(t *testing.T) {
	_, err := PatchPayload(terminal.Patch{BaseRevision: 4, Revision: 4}, 0, 0, false)
	if err == nil {
		t.Fatal("expected non-advancing patch revision to be rejected")
	}
}

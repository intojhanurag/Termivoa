package protocol

import (
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

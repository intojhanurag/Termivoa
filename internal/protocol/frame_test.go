package protocol

import (
	"context"
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

func TestHistoryPageFromPreservesFrozenSnapshotBoundary(t *testing.T) {
	cell := terminal.Cell{
		Text: " ", Width: 1,
		Foreground: terminal.RGBA{A: 255}, Background: terminal.RGBA{A: 255},
	}
	row := terminal.Row{Cells: make([]terminal.Cell, 20)}
	for index := range row.Cells {
		row.Cells[index] = cell
	}
	next := 200
	result := terminal.HistoryReadResult{
		Epoch: 7, Revision: 11, Columns: 20, Offset: 0,
		Rows: make([]terminal.Row, MaxHistoryPageRows), NextOffset: &next,
	}
	for index := range result.Rows {
		result.Rows[index] = row
	}
	payload, err := HistoryPageFrom(result)
	if err != nil {
		t.Fatal(err)
	}
	if payload.HistoryEpoch != "7" || payload.HistoryRevision != "11" ||
		payload.NextOffset == nil || *payload.NextOffset != 200 || payload.Complete ||
		len(payload.Rows) != MaxHistoryPageRows {
		t.Fatalf("history page payload = %#v", payload)
	}
	if err := validateHistoryPage(payload); err != nil {
		t.Fatalf("canonical history page failed validation: %v", err)
	}

	result.Changed = true
	if _, err := HistoryPageFrom(result); err == nil {
		t.Fatal("changed history was encoded as a page")
	}
	result.Rows = nil
	result.NextOffset = nil
	changed, err := HistoryChangedFrom(result)
	if err != nil {
		t.Fatal(err)
	}
	if changed.HistoryEpoch != "7" || changed.HistoryRevision != "11" {
		t.Fatalf("history changed payload = %#v", changed)
	}
	result.Changed = false
	if _, err := HistoryChangedFrom(result); err == nil {
		t.Fatal("unchanged history was encoded as history.changed")
	}
}

func TestHistoryPageValidationRejectsSplicedPagination(t *testing.T) {
	next := int64(199)
	for name, payload := range map[string]HistoryPagePayload{
		"null rows": {
			HistoryEpoch: "1", HistoryRevision: "2", Columns: 20, Complete: true,
		},
		"wrong next offset": {
			HistoryEpoch: "1", HistoryRevision: "2", Columns: 20,
			Rows: []TerminalRow{}, NextOffset: &next,
		},
		"complete with next offset": {
			HistoryEpoch: "1", HistoryRevision: "2", Columns: 20,
			Rows: []TerminalRow{}, NextOffset: &next, Complete: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateHistoryPage(payload); err == nil {
				t.Fatalf("invalid history page accepted: %#v", payload)
			}
		})
	}
}

func TestHistoryPageFromTrimsRowsToOutboundMessageBudget(t *testing.T) {
	cell := terminal.Cell{
		Text: strings.Repeat("<", terminal.MaxCellContentBytes), Width: 1,
		Link:       "https://example.test/" + strings.Repeat("<", terminal.MaxLinkBytes-len("https://example.test/")),
		Foreground: terminal.RGBA{A: 255}, Background: terminal.RGBA{A: 255},
	}
	row := terminal.Row{Cells: make([]terminal.Cell, terminal.MaxColumns)}
	for index := range row.Cells {
		row.Cells[index] = cell
	}
	next := 3
	result := terminal.HistoryReadResult{
		Epoch: 1, Revision: 2, Columns: terminal.MaxColumns,
		Rows: []terminal.Row{row, row, row}, NextOffset: &next,
	}
	payload, err := HistoryPageFrom(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Rows) == 0 || len(payload.Rows) >= len(result.Rows) || payload.Complete ||
		payload.NextOffset == nil || *payload.NextOffset != int64(len(payload.Rows)) {
		t.Fatalf("bounded history payload = rows %d complete %t next %#v",
			len(payload.Rows), payload.Complete, payload.NextOffset)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxEncodedHistoryPagePayloadBytes {
		t.Fatalf("history payload = %d bytes, budget %d", len(encoded), maxEncodedHistoryPagePayloadBytes)
	}
	sessionID := "22222222-2222-4222-8222-222222222222"
	generation := "33333333-3333-4333-8333-333333333333"
	message, err := json.Marshal(Envelope{
		Version: Version, Type: HistoryPage,
		RequestID: "11111111-1111-4111-8111-111111111111",
		SessionID: &sessionID, Generation: &generation, Payload: encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message) > MaxOutboundMessageBytes {
		t.Fatalf("history message = %d bytes, limit %d", len(message), MaxOutboundMessageBytes)
	}
	if _, err := DecodeHost(message); err != nil {
		t.Fatalf("bounded history message failed validation: %v", err)
	}
}

func TestHistoryPageCanonicalAfterWideBoundaryResize(t *testing.T) {
	core, err := terminal.New(context.Background(), terminal.Config{
		Columns: 21, Rows: 5, ScrollbackRows: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	line := strings.Repeat("a", 19) + "界\r\n"
	if err := core.WriteOutput(context.Background(), []byte(strings.Repeat(line, 12))); err != nil {
		t.Fatal(err)
	}

	assertCanonical := func(width int, wantClipped bool) {
		t.Helper()
		if err := core.Resize(context.Background(), width, 5); err != nil {
			t.Fatal(err)
		}
		frame, err := core.ScreenSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		metadata, screenRows, err := Snapshot(frame, frame.HistoryEpoch, frame.HistoryRevision)
		if err != nil {
			t.Fatalf("screen transport at width %d: %v", width, err)
		}
		if metadata.Columns != int64(width) || len(screenRows) != frame.Rows {
			t.Fatalf("screen transport at width %d = columns %d rows %d",
				width, metadata.Columns, len(screenRows))
		}
		for _, row := range screenRows {
			if err := validateRow(row, int64(width)); err != nil {
				t.Fatalf("screen row at width %d: %v", width, err)
			}
		}
		page, err := core.ReadHistory(context.Background(), terminal.HistoryReadRequest{
			Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision,
			Limit: terminal.MaxHistoryPageRows,
		})
		if err != nil {
			t.Fatal(err)
		}
		if page.Changed || len(page.Rows) != 3 || page.Columns != width {
			t.Fatalf("history at width %d = changed %t rows %d columns %d",
				width, page.Changed, len(page.Rows), page.Columns)
		}
		payload, err := HistoryPageFrom(page)
		if err != nil {
			t.Fatalf("history transport at width %d: %v", width, err)
		}
		clipped := false
		for _, row := range payload.Rows {
			if err := validateRow(row, int64(width)); err != nil {
				t.Fatalf("history row at width %d: %v", width, err)
			}
			cells := make([]Cell, 0, width)
			for _, run := range row.Runs {
				cells = append(cells, run.Cells...)
				for _, cell := range run.Cells {
					clipped = clipped || cell.ContentClipped
				}
			}
			if wantClipped {
				last := cells[len(cells)-1]
				if last.Text != "\uFFFD" || last.Width != 1 || last.Continuation ||
					!last.ContentClipped || last.Link != nil {
					t.Fatalf("repaired boundary cell at width %d = %#v", width, last)
				}
			} else if cells[19].Text != "界" || cells[19].Width != 2 ||
				!cells[20].Continuation || cells[20].Width != 0 {
				t.Fatalf("restored wide boundary at width %d = %#v %#v",
					width, cells[19], cells[20])
			}
		}
		if clipped != wantClipped {
			t.Fatalf("history clipping at width %d = %t, want %t", width, clipped, wantClipped)
		}
	}

	assertCanonical(21, false)
	assertCanonical(20, true)
	assertCanonical(22, false)

	if err := core.WriteOutput(context.Background(), []byte("\x1b[?1049h"+strings.Repeat(line, 20))); err != nil {
		t.Fatal(err)
	}
	assertCanonical(20, true)
	assertCanonical(21, false)
	if err := core.WriteOutput(context.Background(), []byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	assertCanonical(20, true)
	assertCanonical(21, false)
}

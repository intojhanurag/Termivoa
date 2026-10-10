package protocol

import (
	"math"
	"slices"
	"testing"
)

func TestSnapshotRowsCoordinateBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		startRow int64
		rowCount int
		valid    bool
	}{
		{"first row", 0, 1, true},
		{"whole maximum screen", 0, MaxRows, true},
		{"last row", MaxRows - 1, 1, true},
		{"last two rows", MaxRows - 2, 2, true},
		{"past final row", MaxRows, 1, false},
		{"chunk exceeds screen", MaxRows - 1, 2, false},
		{"too many rows", 0, MaxRows + 1, false},
		{"empty chunk", 0, 0, false},
		{"empty chunk past final row", MaxRows, 0, false},
		{"negative offset", -1, 1, false},
		{"minimum int64", math.MinInt64, 1, false},
		{"maximum int64", math.MaxInt64, 1, false},
		{"two rows overflow", math.MaxInt64 - 1, 2, false},
		{"maximum chunk overflows", math.MaxInt64 - MaxRows + 1, MaxRows, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := SnapshotRowsPayload{SnapshotID: testSnapshotID, Revision: "1", StartRow: tc.startRow,
				Rows: slices.Repeat([]TerminalRow{testRow(MinColumns)}, tc.rowCount)}
			envelope, err := DecodeHost(encodeTestMessage(t, SnapshotRows, payload, true))
			if !tc.valid {
				if err == nil {
					t.Fatalf("accepted startRow=%d with %d rows", tc.startRow, tc.rowCount)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodePayload[SnapshotRowsPayload](envelope)
			if err != nil || decoded.StartRow != tc.startRow || len(decoded.Rows) != tc.rowCount {
				t.Fatalf("valid chunk changed on decode: %+v, %v", decoded, err)
			}
		})
	}
}

// Adjacent coordinate checks already guard arithmetic or compare directly.
// Keep their boundary behavior explicit while changing snapshot validation.
func TestAdjacentTerminalRowCoordinateBounds(t *testing.T) {
	for _, offset := range []int64{0, 4999, 5000, -1, math.MinInt64, math.MaxInt64} {
		payload := HistoryPagePayload{Columns: MinColumns, HistoryEpoch: "1", HistoryRevision: "1",
			Offset: offset, Rows: []TerminalRow{testRow(MinColumns)}, Complete: true}
		_, err := DecodeHost(encodeTestMessage(t, HistoryPage, payload, true))
		if wantValid := offset >= 0 && offset < 5000; (err == nil) != wantValid {
			t.Errorf("history offset %d: %v, want valid=%t", offset, err, wantValid)
		}
	}
	payload := HistoryPagePayload{Columns: MinColumns, HistoryEpoch: "1", HistoryRevision: "1",
		Offset: 4999, Rows: []TerminalRow{testRow(MinColumns), testRow(MinColumns)}, Complete: true}
	if _, err := DecodeHost(encodeTestMessage(t, HistoryPage, payload, true)); err == nil {
		t.Error("history chunk crossing 5000 rows was accepted")
	}
	for _, index := range []int64{0, MaxRows - 1, MaxRows, -1, math.MinInt64, math.MaxInt64} {
		screen := testScreen()
		screen.Rows = MaxRows
		payload := ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen,
			Rows: []ChangedRow{{Index: index, Row: testRow(MinColumns)}}}
		_, err := DecodeHost(encodeTestMessage(t, ScreenPatch, payload, true))
		if wantValid := index >= 0 && index < MaxRows; (err == nil) != wantValid {
			t.Errorf("patch row index %d: %v, want valid=%t", index, err, wantValid)
		}
	}
}

package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// Compare the incremental budget with real JSON encoding, including the final
// row's different completion metadata. This oracle deliberately spends extra
// work; the production path must not encode preceding rows for each candidate.
func historyPageByMarshalling(t *testing.T, source terminal.HistoryReadResult) HistoryPagePayload {
	t.Helper()
	payload := HistoryPagePayload{
		HistoryEpoch: fmt.Sprint(source.Epoch), HistoryRevision: fmt.Sprint(source.Revision),
		Columns: int64(source.Columns), Offset: int64(source.Offset), Rows: []TerminalRow{},
		Complete: source.Complete,
	}
	for i, sourceRow := range source.Rows {
		row, _, err := terminalRow(sourceRow, source.Columns)
		if err != nil {
			t.Fatal(err)
		}
		candidate := payload
		candidate.Rows = append(append([]TerminalRow{}, payload.Rows...), row)
		candidate.Complete = source.Complete && i == len(source.Rows)-1
		candidate.NextOffset = nil
		if !candidate.Complete {
			next := candidate.Offset + int64(len(candidate.Rows))
			candidate.NextOffset = &next
		}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > maxEncodedHistoryPagePayloadBytes {
			break
		}
		payload = candidate
	}
	return payload
}

func TestHistoryPageBudgetMatchesEncodedJSON(t *testing.T) {
	for _, test := range []struct {
		name     string
		columns  int
		offset   int
		complete bool
		decorate func(*terminal.Cell, int)
	}{
		{name: "plain-next-digit", columns: 80, offset: 9},
		{name: "plain-next-hundreds", columns: 80, offset: 99, complete: true},
		{name: "maximum-metadata", columns: 400, offset: 4800, complete: true},
		{name: "small-complete", columns: 20, offset: 999, complete: true},
		{name: "escapes-styles-links", columns: 80, offset: 99, complete: true, decorate: func(cell *terminal.Cell, i int) {
			cell.Text = "\"\\<&>\u2028\u2029é"
			cell.Bold = i%2 == 0
			cell.Italic = i%3 == 0
			cell.Link = "https://example.test/?value=\"<&>\\"
		}},
		{name: "clipped-row", columns: 400, offset: 999, complete: true, decorate: func(cell *terminal.Cell, i int) {
			cell.Text = strings.Repeat("<", terminal.MaxCellContentBytes)
			cell.Link = "https://example.test/" + strings.Repeat("<", terminal.MaxLinkBytes-len("https://example.test/"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := benchmarkHistorySource(test.columns)
			source.Offset, source.Complete = test.offset, test.complete
			source.Epoch, source.Revision = ^uint64(0), ^uint64(0)
			if test.decorate != nil {
				for i := range source.Rows[0].Cells {
					test.decorate(&source.Rows[0].Cells[i], i)
				}
			}
			if source.Complete {
				source.NextOffset = nil
			} else {
				next := source.Offset + len(source.Rows)
				source.NextOffset = &next
			}
			got, err := HistoryPageFrom(source)
			if err != nil {
				t.Fatal(err)
			}
			want := historyPageByMarshalling(t, source)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("incremental budget differs: got %d rows complete %t, want %d rows complete %t",
					len(got.Rows), got.Complete, len(want.Rows), want.Complete)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > maxEncodedHistoryPagePayloadBytes {
				t.Fatalf("encoded size %d exceeds budget %d", len(encoded), maxEncodedHistoryPagePayloadBytes)
			}
			if err := validateHistoryPage(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryPageBudgetEmptyAndInvalidRows(t *testing.T) {
	for _, offset := range []int{0, 4999} {
		source := terminal.HistoryReadResult{Epoch: 1, Revision: 2, Columns: 20, Offset: offset, Complete: true}
		got, err := HistoryPageFrom(source)
		if err != nil || got.Rows == nil || !got.Complete || got.NextOffset != nil {
			t.Fatalf("empty complete page: %#v, %v", got, err)
		}
	}
	source := benchmarkHistorySource(80)
	source.Rows[0].Cells[0].Width = 3
	if _, err := HistoryPageFrom(source); err == nil {
		t.Fatal("invalid row was accepted")
	}
}

func TestHistoryPageBudgetAcceptsExactLimitAndRejectsNextByte(t *testing.T) {
	source := terminal.HistoryReadResult{
		Epoch: ^uint64(0), Revision: ^uint64(0), Columns: terminal.MaxColumns,
		Offset: 998, Complete: true, Rows: make([]terminal.Row, 2),
	}
	metadata := HistoryPagePayload{
		HistoryEpoch: fmt.Sprint(source.Epoch), HistoryRevision: fmt.Sprint(source.Revision),
		Columns: int64(source.Columns), Offset: int64(source.Offset), Complete: true, Rows: []TerminalRow{},
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	remaining := maxEncodedHistoryPagePayloadBytes - len(encodedMetadata) - 1 // comma between rows
	for i := range source.Rows {
		row := benchmarkHistorySource(source.Columns).Rows[0]
		for column := range row.Cells {
			row.Cells[column].Link = "https://example.test/"
		}
		_, _, rowBytes, err := terminalRowWithSize(row, source.Columns)
		if err != nil {
			t.Fatal(err)
		}
		target := remaining / (len(source.Rows) - i)
		padding := target - rowBytes
		for column := range row.Cells {
			extra := padding / (len(row.Cells) - column)
			row.Cells[column].Link += strings.Repeat("x", extra)
			padding -= extra
		}
		source.Rows[i] = row
		remaining -= target
	}
	got, err := HistoryPageFrom(source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || !got.Complete || len(encoded) != maxEncodedHistoryPagePayloadBytes {
		t.Fatalf("exact-limit page: rows=%d complete=%t bytes=%d", len(got.Rows), got.Complete, len(encoded))
	}
	source.Rows[1].Cells[0].Link += "x"
	got, err = HistoryPageFrom(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Complete || got.NextOffset == nil || *got.NextOffset != 999 {
		t.Fatalf("one-byte-over page: rows=%d complete=%t next=%v", len(got.Rows), got.Complete, got.NextOffset)
	}
}

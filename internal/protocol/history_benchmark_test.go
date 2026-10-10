package protocol

import (
	"fmt"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func benchmarkHistorySource(columns int) terminal.HistoryReadResult {
	row := terminal.Row{Cells: make([]terminal.Cell, columns)}
	for i := range row.Cells {
		row.Cells[i] = terminal.Cell{Text: "x", Width: 1,
			Foreground: terminal.RGBA{A: 255}, Background: terminal.RGBA{A: 255}}
	}
	rows := make([]terminal.Row, terminal.MaxHistoryPageRows)
	for i := range rows {
		rows[i] = row
	}
	next := len(rows)
	return terminal.HistoryReadResult{Epoch: 1, Revision: 1, Columns: columns, Rows: rows, NextOffset: &next}
}

func BenchmarkHistoryPageFrom(b *testing.B) {
	for _, columns := range []int{80, 200, 400} {
		b.Run(fmt.Sprint(columns), func(b *testing.B) {
			source := benchmarkHistorySource(columns)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := HistoryPageFrom(source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

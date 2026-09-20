package desktop

import (
	"bytes"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestRendererPaintsOnlyNormalizedDrawing(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	screen := testScreen()
	rows := testRows(screen.Columns, screen.Rows)
	rows[0].Runs[0].Cells[0].Text = "A"
	rows[0].Runs[0].Cells[1] = protocol.Cell{Text: "界", Width: 2}
	rows[0].Runs[0].Cells[2] = protocol.Cell{Continuation: true}
	rows[0].Runs[0].Style.Bold = true
	rows[0].Runs[0].Style.Underline = protocol.Curly

	if err := renderer.RenderSnapshot(screen, rows); err != nil {
		t.Fatal(err)
	}
	drawing := output.String()
	for _, required := range []string{
		"\x1b[?7l", "\x1b[2J", "\x1b[1;1H", "A", "界",
		"\x1b[0;1;4:3;38;2;213;220;229;48;2;11;14;20m",
		"\x1b[3;5H", "\x1b[2 q", "\x1b[?25h",
	} {
		if !strings.Contains(drawing, required) {
			t.Fatalf("drawing does not contain %q: %q", required, drawing)
		}
	}
	if strings.Contains(drawing, "\x1b]") {
		t.Fatalf("renderer emitted an OSC sequence: %q", drawing)
	}
}

func TestRendererRejectsControlTextWithoutWriting(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	screen := testScreen()
	rows := testRows(screen.Columns, screen.Rows)
	rows[0].Runs[0].Cells[0].Text = "\x1b]52;c;secret\x07"

	err = renderer.RenderSnapshot(screen, rows)
	if err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("control-text error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid frame produced output %q", output.String())
	}
}

func TestRendererValidatesWideCellsAndPatchRows(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	screen := testScreen()
	row := testRows(screen.Columns, 1)[0]
	row.Runs[0].Cells[0] = protocol.Cell{Text: "界", Width: 2}
	if err := renderer.RenderPatch(screen, []protocol.ChangedRow{{Index: 0, Row: row}}); err == nil || !strings.Contains(err.Error(), "no continuation") {
		t.Fatalf("missing continuation error = %v", err)
	}

	row.Runs[0].Cells[1] = protocol.Cell{Continuation: true}
	changes := []protocol.ChangedRow{{Index: 0, Row: row}, {Index: 0, Row: row}}
	if err := renderer.RenderPatch(screen, changes); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate-row error = %v", err)
	}
}

func TestRendererIgnoresLinkMetadata(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	screen := testScreen()
	rows := testRows(screen.Columns, screen.Rows)
	link := "https://example.com/private"
	rows[0].Runs[0].Cells[0].Text = "x"
	rows[0].Runs[0].Cells[0].Link = &link
	if err := renderer.RenderSnapshot(screen, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), link) || strings.Contains(output.String(), "\x1b]8") {
		t.Fatalf("link metadata escaped into drawing: %q", output.String())
	}
}

func testScreen() protocol.ScreenMetadata {
	return protocol.ScreenMetadata{
		Columns: 20,
		Rows:    5,
		Cursor: protocol.Cursor{
			Column:  4,
			Row:     2,
			Style:   protocol.Block,
			Visible: true,
		},
		Foreground:  protocol.Color{R: 213, G: 220, B: 229, A: 255},
		Background:  protocol.Color{R: 11, G: 14, B: 20, A: 255},
		CursorColor: protocol.Color{R: 100, G: 216, B: 232, A: 255},
		WidthMethod: protocol.UnicodeGraphemes,
	}
}

func testRows(columns, rows int64) []protocol.TerminalRow {
	style := protocol.CellStyle{
		Foreground: protocol.Color{R: 213, G: 220, B: 229, A: 255},
		Background: protocol.Color{R: 11, G: 14, B: 20, A: 255},
		Underline:  protocol.UnderlineNone,
	}
	result := make([]protocol.TerminalRow, rows)
	for row := range result {
		cells := make([]protocol.Cell, columns)
		for column := range cells {
			cells[column] = protocol.Cell{Text: " ", Width: 1}
		}
		result[row] = protocol.TerminalRow{Runs: []protocol.CellRun{{Column: 0, Style: style, Cells: cells}}}
	}
	return result
}

// Package desktop implements the local terminal attachment client. It consumes
// normalized protocol frames and emits only a constrained set of drawing
// sequences to the user's outer terminal.
package desktop

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const escape = "\x1b["

// Renderer paints normalized protocol rows. It never accepts or writes child
// terminal bytes, and it deliberately ignores link metadata rather than
// emitting OSC hyperlinks into the outer terminal.
type Renderer struct {
	writer io.Writer
	mu     sync.Mutex
}

// NewRenderer creates a constrained desktop renderer.
func NewRenderer(writer io.Writer) (*Renderer, error) {
	if writer == nil {
		return nil, errors.New("desktop renderer writer must not be nil")
	}
	return &Renderer{writer: writer}, nil
}

// RenderSnapshot replaces the complete visible grid in one write.
func (renderer *Renderer) RenderSnapshot(screen protocol.ScreenMetadata, rows []protocol.TerminalRow) error {
	if err := validateScreen(screen); err != nil {
		return err
	}
	if len(rows) != int(screen.Rows) {
		return fmt.Errorf("desktop renderer: snapshot has %d rows, want %d", len(rows), screen.Rows)
	}

	var drawing strings.Builder
	drawing.WriteString(escape + "?7l") // Never autowrap while painting positioned rows.
	drawing.WriteString(escape + "0m")
	drawing.WriteString(escape + "2J")
	for index, row := range rows {
		if err := appendRow(&drawing, screen.Columns, int64(index), row); err != nil {
			return err
		}
	}
	appendCursor(&drawing, screen.Cursor)
	return renderer.write(drawing.String())
}

// RenderPatch repaints complete changed rows and restores cursor metadata.
func (renderer *Renderer) RenderPatch(screen protocol.ScreenMetadata, rows []protocol.ChangedRow) error {
	if err := validateScreen(screen); err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(rows))
	var drawing strings.Builder
	drawing.WriteString(escape + "?7l")
	for _, changed := range rows {
		if changed.Index < 0 || changed.Index >= screen.Rows {
			return fmt.Errorf("desktop renderer: changed row %d is outside screen", changed.Index)
		}
		if _, exists := seen[changed.Index]; exists {
			return fmt.Errorf("desktop renderer: changed row %d is duplicated", changed.Index)
		}
		seen[changed.Index] = struct{}{}
		if err := appendRow(&drawing, screen.Columns, changed.Index, changed.Row); err != nil {
			return err
		}
	}
	appendCursor(&drawing, screen.Cursor)
	return renderer.write(drawing.String())
}

func (renderer *Renderer) write(drawing string) error {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	_, err := io.WriteString(renderer.writer, drawing)
	if err != nil {
		return fmt.Errorf("desktop renderer: write drawing: %w", err)
	}
	return nil
}

func appendRow(drawing *strings.Builder, columns, rowIndex int64, row protocol.TerminalRow) error {
	if err := validateRow(columns, row); err != nil {
		return fmt.Errorf("desktop renderer: row %d: %w", rowIndex, err)
	}

	appendPosition(drawing, rowIndex, 0)
	drawing.WriteString(escape + "0m" + escape + "2K")
	for _, run := range row.Runs {
		drawing.WriteString(sgr(run.Style))
		for offset, cell := range run.Cells {
			column := run.Column + int64(offset)
			if cell.Continuation {
				continue
			}
			appendPosition(drawing, rowIndex, column)
			drawing.WriteString(cell.Text)
		}
	}

	return nil
}

func validateScreen(screen protocol.ScreenMetadata) error {
	if screen.Columns < protocol.MinColumns || screen.Columns > protocol.MaxColumns ||
		screen.Rows < protocol.MinRows || screen.Rows > protocol.MaxRows {
		return fmt.Errorf("desktop renderer: invalid screen dimensions %dx%d", screen.Columns, screen.Rows)
	}
	if screen.Cursor.Column < 0 || screen.Cursor.Column >= screen.Columns ||
		screen.Cursor.Row < 0 || screen.Cursor.Row >= screen.Rows {
		return fmt.Errorf("desktop renderer: cursor is outside screen")
	}
	return nil
}

func validateRow(columns int64, row protocol.TerminalRow) error {
	cells := make([]*protocol.Cell, columns)
	for runIndex := range row.Runs {
		run := &row.Runs[runIndex]
		if run.Column < 0 || run.Column >= columns || len(run.Cells) == 0 || run.Column+int64(len(run.Cells)) > columns {
			return fmt.Errorf("run at column %d exceeds row width", run.Column)
		}
		for offset := range run.Cells {
			column := run.Column + int64(offset)
			if cells[column] != nil {
				return fmt.Errorf("column %d is covered more than once", column)
			}
			cell := &run.Cells[offset]
			if err := validateCell(*cell); err != nil {
				return fmt.Errorf("column %d: %w", column, err)
			}
			cells[column] = cell
		}
	}
	for column, cell := range cells {
		if cell == nil {
			return fmt.Errorf("column %d is missing", column)
		}
		if cell.Continuation {
			if column == 0 || cells[column-1] == nil || cells[column-1].Continuation || cells[column-1].Width != 2 {
				return fmt.Errorf("column %d has an orphan continuation", column)
			}
			continue
		}
		if cell.Width == 2 {
			if column+1 >= len(cells) || cells[column+1] == nil || !cells[column+1].Continuation {
				return fmt.Errorf("column %d wide cell has no continuation", column)
			}
		}
	}
	return nil
}

func validateCell(cell protocol.Cell) error {
	if cell.Continuation {
		if cell.Width != 0 || cell.Text != "" {
			return errors.New("continuation must have empty text and zero width")
		}
		return nil
	}
	if cell.Width != 1 && cell.Width != 2 {
		return fmt.Errorf("cell width %d is invalid", cell.Width)
	}
	if cell.Text == "" || !utf8.ValidString(cell.Text) {
		return errors.New("cell text must be non-empty valid UTF-8")
	}
	for _, value := range cell.Text {
		if value == 0x7f || value < 0x20 || value >= 0x80 && value <= 0x9f {
			return errors.New("cell text contains terminal control characters")
		}
	}
	return nil
}

func appendPosition(drawing *strings.Builder, row, column int64) {
	drawing.WriteString(escape)
	drawing.WriteString(strconv.FormatInt(row+1, 10))
	drawing.WriteByte(';')
	drawing.WriteString(strconv.FormatInt(column+1, 10))
	drawing.WriteByte('H')
}

func sgr(style protocol.CellStyle) string {
	parameters := []string{"0"}
	if style.Bold {
		parameters = append(parameters, "1")
	}
	if style.Faint {
		parameters = append(parameters, "2")
	}
	if style.Italic {
		parameters = append(parameters, "3")
	}
	switch style.Underline {
	case protocol.Single:
		parameters = append(parameters, "4")
	case protocol.Double:
		parameters = append(parameters, "4:2")
	case protocol.Curly:
		parameters = append(parameters, "4:3")
	case protocol.Dotted:
		parameters = append(parameters, "4:4")
	case protocol.Dashed:
		parameters = append(parameters, "4:5")
	}
	if style.Blink {
		parameters = append(parameters, "5")
	}
	if style.Reverse {
		parameters = append(parameters, "7")
	}
	if style.Conceal {
		parameters = append(parameters, "8")
	}
	if style.Strikethrough {
		parameters = append(parameters, "9")
	}
	parameters = append(parameters, colorParameters("38", style.Foreground)...)
	parameters = append(parameters, colorParameters("48", style.Background)...)
	if style.UnderlineColor != nil {
		parameters = append(parameters, colorParameters("58", *style.UnderlineColor)...)
	}
	return escape + strings.Join(parameters, ";") + "m"
}

func colorParameters(prefix string, color protocol.Color) []string {
	return []string{
		prefix, "2",
		strconv.FormatInt(color.R, 10),
		strconv.FormatInt(color.G, 10),
		strconv.FormatInt(color.B, 10),
	}
}

func appendCursor(drawing *strings.Builder, cursor protocol.Cursor) {
	appendPosition(drawing, cursor.Row, cursor.Column)
	style := 1
	switch cursor.Style {
	case protocol.Underline:
		style = 3
	case protocol.Bar:
		style = 5
	}
	if !cursor.Blink {
		style++
	}
	drawing.WriteString(escape)
	drawing.WriteString(strconv.Itoa(style))
	drawing.WriteString(" q")
	if cursor.Visible {
		drawing.WriteString(escape + "?25h")
	} else {
		drawing.WriteString(escape + "?25l")
	}
}

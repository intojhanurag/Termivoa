package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// MaxEncodedTerminalRowBytes leaves at least half of the outbound-message
// allowance for the envelope, snapshot identity, screen metadata, and JSON
// expansion around a row. Snapshot chunking may group smaller rows, but a
// single row must always remain independently transportable.
const MaxEncodedTerminalRowBytes = MaxOutboundMessageBytes / 2

// Snapshot converts an immutable canonical frame into wire-ready metadata and
// style-coalesced rows. Chunking is deliberately left to the connection layer.
func Snapshot(frame terminal.Frame, historyEpoch, historyRevision uint64) (ScreenMetadata, []TerminalRow, error) {
	rows, rowsClipped, err := terminalRows(frame.Screen, frame.Columns)
	if err != nil {
		return ScreenMetadata{}, nil, err
	}
	metadata, err := screenMetadata(
		frame.Columns, frame.Rows, frame.Alternate, frame.WidthMethod,
		frame.Foreground, frame.Background, frame.CursorColor, frame.Cursor,
		frame.ContentClipped || rowsClipped, historyEpoch, historyRevision,
	)
	if err != nil {
		return ScreenMetadata{}, nil, err
	}
	if len(rows) != frame.Rows {
		return ScreenMetadata{}, nil, fmt.Errorf("protocol: frame has %d rows, expected %d", len(rows), frame.Rows)
	}
	return metadata, rows, nil
}

// PatchPayload converts a canonical whole-row patch into its v1 wire shape.
func PatchPayload(
	patch terminal.Patch,
	historyEpoch, historyRevision uint64,
	historyReset bool,
) (ScreenPatchPayload, error) {
	if patch.Revision <= patch.BaseRevision {
		return ScreenPatchPayload{}, fmt.Errorf("protocol: patch revision must advance")
	}
	metadata, err := screenMetadata(
		patch.Columns, patch.Rows, patch.Alternate, patch.WidthMethod,
		patch.Foreground, patch.Background, patch.CursorColor, patch.Cursor,
		patch.ContentClipped, historyEpoch, historyRevision,
	)
	if err != nil {
		return ScreenPatchPayload{}, err
	}

	changed := make([]ChangedRow, len(patch.ChangedRows))
	rowsClipped := false
	seen := make(map[int]struct{}, len(patch.ChangedRows))
	for index, source := range patch.ChangedRows {
		if source.Index < 0 || source.Index >= patch.Rows {
			return ScreenPatchPayload{}, fmt.Errorf("protocol: patch row index %d is out of bounds", source.Index)
		}
		if _, duplicate := seen[source.Index]; duplicate {
			return ScreenPatchPayload{}, fmt.Errorf("protocol: patch row index %d is duplicated", source.Index)
		}
		seen[source.Index] = struct{}{}
		row, clipped, err := terminalRow(source.Row, patch.Columns)
		if err != nil {
			return ScreenPatchPayload{}, fmt.Errorf("protocol: patch row %d: %w", source.Index, err)
		}
		rowsClipped = rowsClipped || clipped
		changed[index] = ChangedRow{Index: int64(source.Index), Row: row}
	}
	metadata.ContentClipped = metadata.ContentClipped || rowsClipped

	return ScreenPatchPayload{
		BaseRevision: strconv.FormatUint(patch.BaseRevision, 10),
		Revision:     strconv.FormatUint(patch.Revision, 10),
		Screen:       metadata,
		Rows:         changed,
		HistoryReset: historyReset,
	}, nil
}

func screenMetadata(
	columns, rows int,
	alternate bool,
	widthMethod string,
	foreground, background, cursorColor terminal.RGBA,
	cursor terminal.Cursor,
	contentClipped bool,
	historyEpoch, historyRevision uint64,
) (ScreenMetadata, error) {
	if columns < MinColumns || columns > MaxColumns || rows < MinRows || rows > MaxRows {
		return ScreenMetadata{}, fmt.Errorf("protocol: screen dimensions %dx%d are out of bounds", columns, rows)
	}
	if cursor.Column < 0 || cursor.Column >= columns || cursor.Row < 0 || cursor.Row >= rows {
		return ScreenMetadata{}, errorsf("cursor position %d,%d is out of bounds", cursor.Column, cursor.Row)
	}
	buffer := Normal
	if alternate {
		buffer = Alternate
	}
	method := WidthMethod(widthMethod)
	if method != UnicodeGraphemes && method != Wcwidth {
		return ScreenMetadata{}, errorsf("unsupported width method %q", widthMethod)
	}
	cursorStyle := Style(cursor.Style)
	if cursorStyle != Block && cursorStyle != Underline && cursorStyle != Bar {
		return ScreenMetadata{}, errorsf("unsupported cursor style %q", cursor.Style)
	}
	metadata := ScreenMetadata{
		Columns:         int64(columns),
		Rows:            int64(rows),
		Buffer:          buffer,
		WidthMethod:     method,
		Foreground:      color(foreground),
		Background:      color(background),
		CursorColor:     color(cursorColor),
		Cursor:          Cursor{Column: int64(cursor.Column), Row: int64(cursor.Row), Style: cursorStyle, Visible: cursor.Visible, Blink: cursor.Blink},
		HistoryEpoch:    strconv.FormatUint(historyEpoch, 10),
		HistoryRevision: strconv.FormatUint(historyRevision, 10),
		ContentClipped:  contentClipped,
	}
	if err := validateScreen(metadata); err != nil {
		return ScreenMetadata{}, fmt.Errorf("protocol: invalid screen metadata: %w", err)
	}
	return metadata, nil
}

func terminalRows(source []terminal.Row, columns int) ([]TerminalRow, bool, error) {
	rows := make([]TerminalRow, len(source))
	clipped := false
	for index, sourceRow := range source {
		row, rowClipped, err := terminalRow(sourceRow, columns)
		if err != nil {
			return nil, false, fmt.Errorf("protocol: row %d: %w", index, err)
		}
		rows[index] = row
		clipped = clipped || rowClipped
	}
	return rows, clipped, nil
}

func terminalRow(source terminal.Row, columns int) (TerminalRow, bool, error) {
	if len(source.Cells) != columns {
		return TerminalRow{}, false, fmt.Errorf("has %d cells, expected %d", len(source.Cells), columns)
	}
	if columns == 0 {
		return TerminalRow{}, false, fmt.Errorf("has no cells")
	}

	runs := make([]CellRun, 0, columns)
	for column, sourceCell := range source.Cells {
		style, err := cellStyle(sourceCell)
		if err != nil {
			return TerminalRow{}, false, fmt.Errorf("cell %d: %w", column, err)
		}
		cell := Cell{
			Text:           sourceCell.Text,
			Width:          int64(sourceCell.Width),
			Continuation:   sourceCell.Continuation,
			ContentClipped: sourceCell.ContentClipped,
		}
		if sourceCell.Link != "" {
			link := sourceCell.Link
			cell.Link = &link
		}
		if len(runs) == 0 || !equalCellStyle(runs[len(runs)-1].Style, style) {
			runs = append(runs, CellRun{Column: int64(column), Style: style})
		}
		runs[len(runs)-1].Cells = append(runs[len(runs)-1].Cells, cell)
	}
	row := TerminalRow{Runs: runs}
	clipped, err := boundEncodedRow(&row)
	if err != nil {
		return TerminalRow{}, false, err
	}
	if err := validateRow(row, int64(columns)); err != nil {
		return TerminalRow{}, false, err
	}
	return row, clipped, nil
}

type rowClipCandidate struct {
	run    int
	cell   int
	saving int
}

// boundEncodedRow preserves the complete column grid while clipping only
// pathological link/text payloads that would make a row impossible to place
// in any legal outbound message. Largest reductions are applied first so the
// common case is allocation-only and ordinary grapheme cells are untouched.
func boundEncodedRow(row *TerminalRow) (bool, error) {
	encoded, err := json.Marshal(row)
	if err != nil {
		return false, fmt.Errorf("encode normalized row: %w", err)
	}
	if len(encoded) <= MaxEncodedTerminalRowBytes {
		return false, nil
	}

	clipped := false
	encodedBytes := len(encoded)
	for pass, candidates := range [][]rowClipCandidate{
		rowClipCandidates(row, true),
		rowClipCandidates(row, false),
	} {
		for _, candidate := range candidates {
			cell := &row.Runs[candidate.run].Cells[candidate.cell]
			before, marshalErr := json.Marshal(cell)
			if marshalErr != nil {
				return false, fmt.Errorf("encode normalized cell before clipping: %w", marshalErr)
			}
			if pass == 0 {
				cell.Link = nil
			} else {
				cell.Text = "\uFFFD"
			}
			cell.ContentClipped = true
			clipped = true
			after, marshalErr := json.Marshal(cell)
			if marshalErr != nil {
				return false, fmt.Errorf("encode normalized cell after clipping: %w", marshalErr)
			}
			encodedBytes += len(after) - len(before)
			if encodedBytes <= MaxEncodedTerminalRowBytes {
				encoded, err = json.Marshal(row)
				if err != nil {
					return false, fmt.Errorf("encode clipped normalized row: %w", err)
				}
				if len(encoded) <= MaxEncodedTerminalRowBytes {
					return clipped, nil
				}
				encodedBytes = len(encoded)
			}
		}
	}
	encoded, err = json.Marshal(row)
	if err != nil {
		return false, fmt.Errorf("encode fully clipped normalized row: %w", err)
	}
	return false, fmt.Errorf("normalized row remains %d bytes after clipping (limit %d)", len(encoded), MaxEncodedTerminalRowBytes)
}

func rowClipCandidates(row *TerminalRow, links bool) []rowClipCandidate {
	candidates := make([]rowClipCandidate, 0)
	for runIndex := range row.Runs {
		for cellIndex := range row.Runs[runIndex].Cells {
			cell := row.Runs[runIndex].Cells[cellIndex]
			if links && cell.Link == nil || !links && len(cell.Text) <= len("\uFFFD") {
				continue
			}
			before, _ := json.Marshal(cell)
			if links {
				cell.Link = nil
			} else {
				cell.Text = "\uFFFD"
			}
			cell.ContentClipped = true
			after, _ := json.Marshal(cell)
			if saving := len(before) - len(after); saving > 0 {
				candidates = append(candidates, rowClipCandidate{run: runIndex, cell: cellIndex, saving: saving})
			}
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return candidates[left].saving > candidates[right].saving
	})
	return candidates
}

func cellStyle(cell terminal.Cell) (CellStyle, error) {
	underline, err := underlineStyle(cell.Underline)
	if err != nil {
		return CellStyle{}, err
	}
	style := CellStyle{
		Foreground:    color(cell.Foreground),
		Background:    color(cell.Background),
		Bold:          cell.Bold,
		Faint:         cell.Faint,
		Italic:        cell.Italic,
		Blink:         cell.Blink,
		Reverse:       cell.Reverse,
		Conceal:       cell.Conceal,
		Strikethrough: cell.Strikethrough,
		Underline:     underline,
	}
	if cell.UnderlineColor != (terminal.RGBA{}) {
		value := color(cell.UnderlineColor)
		style.UnderlineColor = &value
	}
	return style, nil
}

func underlineStyle(value int) (UnderlineEnum, error) {
	switch value {
	case 0:
		return UnderlineNone, nil
	case 1:
		return Single, nil
	case 2:
		return Double, nil
	case 3:
		return Curly, nil
	case 4:
		return Dotted, nil
	case 5:
		return Dashed, nil
	default:
		return "", fmt.Errorf("unsupported underline style %d", value)
	}
}

func color(value terminal.RGBA) Color {
	return Color{R: int64(value.R), G: int64(value.G), B: int64(value.B), A: int64(value.A)}
}

func equalCellStyle(left, right CellStyle) bool {
	if left.Background != right.Background || left.Foreground != right.Foreground ||
		left.Bold != right.Bold || left.Faint != right.Faint || left.Italic != right.Italic ||
		left.Blink != right.Blink || left.Reverse != right.Reverse || left.Conceal != right.Conceal ||
		left.Strikethrough != right.Strikethrough || left.Underline != right.Underline {
		return false
	}
	if left.UnderlineColor == nil || right.UnderlineColor == nil {
		return left.UnderlineColor == nil && right.UnderlineColor == nil
	}
	return *left.UnderlineColor == *right.UnderlineColor
}

func errorsf(format string, values ...any) error {
	return fmt.Errorf("protocol: "+format, values...)
}

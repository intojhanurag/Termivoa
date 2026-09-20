package terminal

import (
	"image/color"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Frame is an immutable copy of the emulator's canonical state at Revision.
// It deliberately contains cells rather than a rendered ANSI string.
type Frame struct {
	Revision    uint64
	Columns     int
	Rows        int
	Alternate   bool
	WidthMethod string
	Foreground  RGBA
	Background  RGBA
	CursorColor RGBA
	Cursor      Cursor
	Screen      []Row
	History     []Row
	// HistoryBytes is the conservative normalized retained-payload estimate.
	HistoryBytes   int
	ContentClipped bool
}

// Cursor is the canonical cursor state.
type Cursor struct {
	Column  int
	Row     int
	Style   CursorStyle
	Visible bool
	Blink   bool
}

// CursorStyle is independent of the dependency's enum so it can remain stable.
type CursorStyle string

const (
	CursorBlock     CursorStyle = "block"
	CursorUnderline CursorStyle = "underline"
	CursorBar       CursorStyle = "bar"
)

// Row is a complete terminal row. Cells retain exact column positions.
type Row struct {
	Cells []Cell
}

// Cell is a normalized terminal cell. A continuation cell occupies a column
// covered by the preceding wide cell and has no display text of its own.
type Cell struct {
	Text           string
	Width          int
	Continuation   bool
	Foreground     RGBA
	Background     RGBA
	UnderlineColor RGBA
	Bold           bool
	Faint          bool
	Italic         bool
	Blink          bool
	Reverse        bool
	Conceal        bool
	Strikethrough  bool
	Underline      int
	Link           string
	ContentClipped bool
}

// RGBA is a resolved, non-premultiplied color value.
type RGBA struct {
	R uint8
	G uint8
	B uint8
	A uint8
}

func captureFrame(emulator *vt.Emulator, revision uint64, maxCellBytes, maxHistoryBytes int) Frame {
	foreground := rgba(emulator.ForegroundColor())
	background := rgba(emulator.BackgroundColor())
	activeCursor := emulator.ActiveCursor()
	frame := Frame{
		Revision:    revision,
		Columns:     emulator.Width(),
		Rows:        emulator.Height(),
		Alternate:   emulator.IsAltScreen(),
		WidthMethod: widthMethodName(emulator.WidthMethod()),
		Foreground:  foreground,
		Background:  background,
		CursorColor: rgba(emulator.CursorColor()),
		Cursor: Cursor{
			Column:  activeCursor.X,
			Row:     activeCursor.Y,
			Style:   cursorStyle(activeCursor.Style),
			Visible: !activeCursor.Hidden,
			Blink:   !activeCursor.Steady,
		},
		Screen: make([]Row, emulator.Height()),
	}

	for y := range emulator.Height() {
		frame.Screen[y], frame.ContentClipped = normalizeRow(
			emulator.Width(),
			func(x int) *uv.Cell { return emulator.CellAt(x, y) },
			foreground,
			background,
			maxCellBytes,
			frame.ContentClipped,
		)
	}

	historyLen := min(emulator.ScrollbackLen(), MaxHistoryRows)
	frame.History = make([]Row, 0, historyLen)
	for y := historyLen - 1; y >= 0; y-- {
		row, clipped := normalizeRow(
			emulator.Width(),
			func(x int) *uv.Cell { return emulator.ScrollbackCellAt(x, y) },
			foreground,
			background,
			maxCellBytes,
			frame.ContentClipped,
		)
		rowBytes := normalizedRowBytes(row)
		if frame.HistoryBytes+rowBytes > maxHistoryBytes {
			break
		}
		frame.ContentClipped = frame.ContentClipped || clipped
		frame.History = append(frame.History, row)
		frame.HistoryBytes += rowBytes
	}
	slices.Reverse(frame.History)

	return frame
}

func normalizeRow(
	width int,
	cellAt func(int) *uv.Cell,
	defaultForeground RGBA,
	defaultBackground RGBA,
	maxCellBytes int,
	clipped bool,
) (Row, bool) {
	row := Row{Cells: make([]Cell, width)}
	continuationUntil := 0
	for x := range width {
		if x < continuationUntil {
			continue
		}
		cell := cellAt(x)
		if cell == nil || cell.IsZero() {
			row.Cells[x] = Cell{
				Text:       " ",
				Width:      1,
				Foreground: defaultForeground,
				Background: defaultBackground,
			}
			continue
		}
		text, contentClipped := safeCellText(cell.Content, maxCellBytes)
		clipped = clipped || contentClipped
		fg := defaultForeground
		if cell.Style.Fg != nil {
			fg = rgba(cell.Style.Fg)
		}
		bg := defaultBackground
		if cell.Style.Bg != nil {
			bg = rgba(cell.Style.Bg)
		}
		normalized := Cell{
			Text:           text,
			Width:          cell.Width,
			Foreground:     fg,
			Background:     bg,
			UnderlineColor: rgba(cell.Style.UnderlineColor),
			Bold:           cell.Style.Attrs&uv.AttrBold != 0,
			Faint:          cell.Style.Attrs&uv.AttrFaint != 0,
			Italic:         cell.Style.Attrs&uv.AttrItalic != 0,
			Blink:          cell.Style.Attrs&uv.AttrBlink != 0,
			Reverse:        cell.Style.Attrs&uv.AttrReverse != 0,
			Conceal:        cell.Style.Attrs&uv.AttrConceal != 0,
			Strikethrough:  cell.Style.Attrs&uv.AttrStrikethrough != 0,
			Underline:      int(cell.Style.Underline),
			Link:           safeLink(cell.Link.URL),
			ContentClipped: contentClipped,
		}
		row.Cells[x] = normalized
		if cell.Width > 1 {
			continuationUntil = min(width, x+cell.Width)
			for continuation := x + 1; continuation < continuationUntil; continuation++ {
				inherited := normalized
				inherited.Text = ""
				inherited.Width = 0
				inherited.Continuation = true
				inherited.Link = ""
				row.Cells[continuation] = inherited
			}
		}
	}
	return row, clipped
}

func safeCellText(text string, maxBytes int) (string, bool) {
	if !utf8.ValidString(text) || len(text) > maxBytes {
		return "\uFFFD", true
	}
	for _, r := range text {
		if r == 0x7f || r < 0x20 || r >= 0x80 && r <= 0x9f {
			return "\uFFFD", true
		}
	}
	return text, false
}

func safeLink(raw string) string {
	if raw == "" || len(raw) > MaxLinkBytes || strings.ContainsAny(raw, "\x00\r\n") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func rgba(value color.Color) RGBA {
	if value == nil {
		return RGBA{}
	}
	c := color.NRGBAModel.Convert(value).(color.NRGBA)
	return RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

func widthMethodName(method uv.WidthMethod) string {
	if value, ok := method.(ansi.Method); ok && value == ansi.GraphemeWidth {
		return "unicode-graphemes"
	}
	// A future WidthMethod implementation can be identified behaviorally using
	// a sequence on which the two supported profiles differ.
	if method.StringWidth("🏳️‍🌈") == ansi.GraphemeWidth.StringWidth("🏳️‍🌈") {
		return "unicode-graphemes"
	}
	return "wcwidth"
}

func normalizedRowBytes(row Row) int {
	bytes := 0
	for _, cell := range row.Cells {
		bytes += len(cell.Text) + len(cell.Link) + 48
	}
	return bytes
}

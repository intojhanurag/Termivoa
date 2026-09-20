package vt

import (
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// printableASCII reports whether r is a character that can never join the
// grapheme cluster beside it. No ASCII character is a combining mark, a ZWJ, a
// spacing mark or a regional indicator, and CR and LF are not printable, so a
// cluster boundary between two of these is certain without asking the
// segmenter.
func printableASCII(r rune) bool {
	return r >= ansi.SP && r < ansi.DEL
}

// handlePrint handles printable characters.
//
// A character is held back rather than printed straight away, because the one
// after it may be a combining mark that belongs to the same cluster. Printing
// "e" from "e\u0301" before the mark arrives puts the base in one cell and
// leaves the mark to become a zero-width cell of its own, which the next write
// or erase then destroys.
func (e *Emulator) handlePrint(r rune) {
	// No ASCII character can extend the cluster before it: none of them is
	// Extend, ZWJ, SpacingMark, Prepend, a regional indicator, or part of an
	// Indic conjunct. So an ASCII character arriving is proof that whatever is
	// buffered is finished, and the buffer can go out.
	//
	// This is also what keeps the buffer from being re-segmented as it grows.
	// Asking where the clusters are on every character costs the length of the
	// buffer each time, which is quadratic over a long run of combining marks
	// that never resolves into more than one cluster.
	if printableASCII(r) {
		// Two printable ASCII characters in a row is the common case by a wide
		// margin, and needs no segmenting at all.
		if len(e.grapheme) == 1 && printableASCII(rune(e.grapheme[0])) {
			e.handleGrapheme(string(e.grapheme[0]), 1)
			e.grapheme = utf8.AppendRune(e.grapheme[:0], r)
			return
		}
		e.flushGrapheme()
	}

	if e.maxGraphemeBytes > 0 && len(e.grapheme)+utf8.RuneLen(r) > e.maxGraphemeBytes {
		e.flushGrapheme()
	}
	e.grapheme = utf8.AppendRune(e.grapheme, r)
}

// flushGrapheme flushes the current grapheme buffer, if any, and handles the
// grapheme as a single unit.
func (e *Emulator) flushGrapheme() {
	if len(e.grapheme) == 0 {
		return
	}

	method, ok := e.WidthMethod().(ansi.Method)
	if !ok {
		method = ansi.WcWidth
	}
	graphemes := e.grapheme
	for len(graphemes) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(graphemes, method)
		if len(cluster) == 0 {
			break
		}
		e.handleGrapheme(string(cluster), width)
		graphemes = graphemes[len(cluster):]
	}
	e.grapheme = e.grapheme[:0] // Reset the grapheme buffer.
}

// handleGrapheme handles UTF-8 graphemes.
func (e *Emulator) handleGrapheme(content string, width int) {
	awm := e.isModeSet(ansi.ModeAutoWrap)
	if e.extendPreviousGrapheme(content, awm) {
		return
	}
	cell := uv.Cell{
		Content: content,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}

	x, y := e.scr.CursorPosition()
	if e.atPhantom && awm {
		// moves cursor down similar to [Terminal.linefeed] except it doesn't
		// respects [ansi.LNM] mode.
		// This will reset the phantom state i.e. pending wrap state.
		e.index()
		_, y = e.scr.CursorPosition()
		x = 0
	}

	// A single shift applies to the next character, whatever that character
	// turns out to be. Take it now, so it cannot leak onto the one after this
	// when this is a cluster no charset has a mapping for.
	single := e.gsingle
	e.gsingle = 0

	// Handle character set mappings
	if len(content) == 1 { //nolint:nestif
		var charset CharSet
		c := content[0]
		if single > 1 && single < 4 {
			charset = e.charsets[single]
		} else if c < 128 {
			charset = e.charsets[e.gl]
		} else {
			charset = e.charsets[e.gr]
		}

		if charset != nil {
			if r, ok := charset[c]; ok {
				cell.Content = r
				cell.Width = 1
			}
		}
	}

	if cell.Width == 1 && len(content) == 1 {
		e.lastChar, _ = utf8.DecodeRuneInString(content)
	}

	e.scr.SetCell(x, y, &cell)

	// Handle phantom state at the end of the line. Include the cell width so a
	// width-two cell beginning in the penultimate column behaves like a
	// width-one cell in the last column.
	endX := x + cell.Width
	e.atPhantom = awm && endX >= e.scr.Width()
	if e.atPhantom {
		x = e.scr.Width() - 1
	} else {
		x = endX
	}

	// NOTE: We don't reset the phantom state here, we handle it up above.
	e.scr.setCursor(x, y, false)
}

func (e *Emulator) extendPreviousGrapheme(content string, autoWrap bool) bool {
	if content == "" {
		return false
	}
	x, y := e.scr.CursorPosition()
	previousX := x - 1
	if e.atPhantom {
		previousX = x
	}
	for previousX >= 0 {
		cell := e.scr.CellAt(previousX, y)
		if cell == nil {
			return false
		}
		if !cell.IsZero() {
			break
		}
		previousX--
	}
	if previousX < 0 {
		return false
	}

	previous := e.scr.CellAt(previousX, y)
	if previous == nil || previous.Content == "" {
		return false
	}
	combined := previous.Content + content
	if e.maxGraphemeBytes > 0 && len(combined) > e.maxGraphemeBytes {
		return false
	}
	method, ok := e.WidthMethod().(ansi.Method)
	if !ok {
		method = ansi.WcWidth
	}
	cluster, width := ansi.FirstGraphemeCluster([]byte(combined), method)
	if len(cluster) != len(combined) {
		return false
	}

	updated := *previous
	updated.Content = combined
	updated.Width = width
	e.scr.SetCell(previousX, y, &updated)
	endX := previousX + width
	e.atPhantom = autoWrap && endX >= e.scr.Width()
	if e.atPhantom {
		endX = e.scr.Width() - 1
	}
	e.scr.setCursor(endX, y, false)
	return true
}

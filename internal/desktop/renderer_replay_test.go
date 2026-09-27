package desktop

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

var cursorPosition = regexp.MustCompile(`\x1b\[\d+;\d+H`)

// A full 120x40 frame of styled ASCII needs one position per row plus the
// final cursor restore, not one per cell.
func TestRendererPositionsOncePerRowForASCII(t *testing.T) {
	t.Parallel()
	source := emulate(t, 120, 40, styledASCII(120, 40))
	drawing := renderSnapshot(t, source)
	positions := len(cursorPosition.FindAllString(drawing, -1))
	t.Logf("120x40 styled ASCII frame: %d bytes, %d cursor positions, %d escapes",
		len(drawing), positions, strings.Count(drawing, "\x1b"))
	if positions != 40+1 {
		t.Fatalf("cursor positions = %d, want %d", positions, 40+1)
	}
	assertSameScreen(t, source, emulate(t, 120, 40, []byte(drawing)))
}

// Non-ASCII cells are repositioned after, because the outer terminal may
// disagree with the server about their width. Placement stays exact.
func TestRendererReplaysUnicodeAtExactColumns(t *testing.T) {
	t.Parallel()
	source := emulate(t, 120, 40, unicodeScreen(120, 40))
	drawing := renderSnapshot(t, source)
	t.Logf("120x40 mixed Unicode frame: %d bytes, %d cursor positions, %d escapes",
		len(drawing), len(cursorPosition.FindAllString(drawing, -1)), strings.Count(drawing, "\x1b"))
	assertSameScreen(t, source, emulate(t, 120, 40, []byte(drawing)))
	// The emulator agrees with itself on widths, so also require the explicit
	// position after every non-ASCII cell that a real terminal may disagree on.
	if match := regexp.MustCompile(`[^\x00-\x7f][ -~]`).FindString(drawing); match != "" || strings.Contains(drawing, "界界") {
		t.Fatalf("non-ASCII cell is followed by text without a cursor position: %q", match)
	}
	for _, required := range []string{"界\x1b[1;8H界\x1b[1;10H", "😀\x1b[2;9H", "e\u0301\x1b[3;12H"} {
		if !strings.Contains(drawing, required) {
			t.Fatalf("drawing does not contain %q", required)
		}
	}
}

// A patch repaints only changed rows. Replaying snapshot then patch must equal
// the new screen, including rows that went from wide to narrow text.
func TestRendererPatchReplaysChangedRows(t *testing.T) {
	t.Parallel()
	first := unicodeScreen(120, 40)
	base := emulate(t, 120, 40, first)
	second := append(append([]byte(nil), first...),
		"\x1b[1;1H\x1b[0mplain row replaces wide text\x1b[K\x1b[5;110H\x1b[31m界界界界界\x1b[40;1H\x1b[0;7mlast\x1b[3;7H"...)
	next := emulate(t, 120, 40, second)
	next.Revision = base.Revision + 1
	patch, err := terminal.Diff(base, next)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protocol.PatchPayload(patch, false)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	if err := renderer.RenderSnapshot(snapshotOf(t, base)); err != nil {
		t.Fatal(err)
	}
	if err := renderer.RenderPatch(payload.Screen, payload.Rows); err != nil {
		t.Fatal(err)
	}
	assertSameScreen(t, next, emulate(t, 120, 40, output.Bytes()))
}

func styledASCII(columns, rows int) []byte {
	var source strings.Builder
	for row := range rows {
		fmt.Fprintf(&source, "\x1b[%d;1H", row+1)
		for column := range columns {
			if column%12 == 0 {
				fmt.Fprintf(&source, "\x1b[0;%d;%dm", 31+(row+column/12)%7, 1+(column/12)%4)
			}
			source.WriteByte(byte('!' + (row*columns+column)%94))
		}
	}
	source.WriteString("\x1b[0m\x1b[20;30H")
	return []byte(source.String())
}

func unicodeScreen(columns, rows int) []byte {
	lines := []string{
		"\x1b[1mwide\x1b[0m 界界 and 日本語 then ascii",
		"emoji 😀 flag 🇯🇵 heart ❤️ family 👨‍👩‍👧 end",
		"combining e\u0301 a\u0308 n\u0303 and zero width\u200bjoin",
		"\x1b[38;2;255;193;7;48;2;30;30;30mbox ╭─────╮ │ok│ ╰─────╯\x1b[0m ▐▛███▜▌ ✳ spark",
		"\x1b[4:3;58;2;1;2;3mcurly\x1b[0m \x1b[9mstrike\x1b[0m \x1b[7mreverse\x1b[0m \x1b[2;3mfaint italic\x1b[0m",
	}
	var source strings.Builder
	for index, line := range lines {
		fmt.Fprintf(&source, "\x1b[%d;1H%s", index+1, line)
	}
	// A wide character placed so its continuation lands in the last column.
	fmt.Fprintf(&source, "\x1b[%d;%dH界", len(lines)+1, columns-1)
	fmt.Fprintf(&source, "\x1b[%d;1H\x1b[44m%s\x1b[0m", rows, strings.Repeat("x", columns/2))
	source.WriteString("\x1b[2;9H\x1b[4 q")
	return []byte(source.String())
}

func emulate(t *testing.T, columns, rows int, data []byte) terminal.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	core, err := terminal.New(ctx, terminal.Config{Columns: columns, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close() }()
	go func() {
		for range core.Output() {
		}
	}()
	if err := core.WriteOutput(ctx, data); err != nil {
		t.Fatal(err)
	}
	frame, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func snapshotOf(t *testing.T, frame terminal.Frame) (protocol.ScreenMetadata, []protocol.TerminalRow) {
	t.Helper()
	screen, rows, err := protocol.Snapshot(frame, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return screen, rows
}

func renderSnapshot(t *testing.T, frame terminal.Frame) string {
	t.Helper()
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	if err := renderer.RenderSnapshot(snapshotOf(t, frame)); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

// assertSameScreen compares every cell and the cursor. Links are ignored:
// the renderer deliberately never emits OSC 8.
func assertSameScreen(t *testing.T, want, got terminal.Frame) {
	t.Helper()
	if want.Cursor != got.Cursor {
		t.Fatalf("cursor = %#v, want %#v", got.Cursor, want.Cursor)
	}
	for row := range want.Screen {
		for column := range want.Screen[row].Cells {
			expected, actual := want.Screen[row].Cells[column], got.Screen[row].Cells[column]
			expected.Link, actual.Link = "", ""
			if expected != actual {
				t.Fatalf("cell %d,%d = %#v, want %#v", row, column, actual, expected)
			}
		}
	}
}

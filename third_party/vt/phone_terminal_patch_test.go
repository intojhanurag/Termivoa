package vt

import (
	"strings"
	"testing"
)

func TestPhoneTerminalStreamingWritePreservesGrapheme(t *testing.T) {
	whole := NewEmulator(20, 5)
	_, _ = whole.WriteStream([]byte("e\u0301"))
	whole.Flush()

	split := NewEmulator(20, 5)
	_, _ = split.WriteStream([]byte("e"))
	_, _ = split.WriteStream([]byte("\u0301"))
	split.Flush()

	if got, want := split.CellAt(0, 0), whole.CellAt(0, 0); !got.Equal(want) {
		t.Fatalf("split cell = %#v, whole cell = %#v", got, want)
	}
}

func TestPhoneTerminalGraphemeByteLimit(t *testing.T) {
	emulator := NewEmulator(20, 5)
	emulator.SetMaxGraphemeBytes(32)
	_, _ = emulator.WriteStream([]byte("e" + strings.Repeat("\u0301", 1000)))
	emulator.Flush()

	for x := range emulator.Width() {
		if cell := emulator.CellAt(x, 0); cell != nil && len(cell.Content) > 32 {
			t.Fatalf("cell %d retained %d bytes, limit 32", x, len(cell.Content))
		}
	}
}

func TestPhoneTerminalScrollbackByteLimit(t *testing.T) {
	emulator := NewEmulator(20, 5)
	emulator.SetScrollbackSize(5000)
	emulator.SetScrollbackByteLimit(12000)
	_, _ = emulator.Write([]byte(strings.Repeat("history row\r\n", 100)))

	if got := emulator.ScrollbackLen(); got == 0 || got >= 95 {
		t.Fatalf("bounded scrollback rows = %d, want a non-empty reduced history", got)
	}
	if got := emulator.Scrollback().bytes; got > 12000 {
		t.Fatalf("scrollback retained %d bytes, limit 12000", got)
	}
}

func TestPhoneTerminalScrollbackAccountsForNormalizedWidth(t *testing.T) {
	emulator := NewEmulator(20, 5)
	emulator.SetScrollbackSize(5000)
	emulator.SetScrollbackByteLimit(12000)
	_, _ = emulator.Write([]byte(strings.Repeat("row\r\n", 30)))
	if emulator.ScrollbackLen() == 0 {
		t.Fatal("narrow fixture retained no scrollback")
	}
	emulator.Resize(400, 5)
	if got := emulator.ScrollbackLen(); got != 0 {
		t.Fatalf("expanded normalized rows left %d hidden lines above byte limit", got)
	}
	if got := emulator.ScrollbackBytes(); got != 0 {
		t.Fatalf("expanded scrollback bytes = %d, want 0", got)
	}
}

func TestPhoneTerminalAlternateScreenHasNoHiddenScrollback(t *testing.T) {
	emulator := NewEmulator(20, 5)
	_, _ = emulator.Write([]byte("\x1b[?1049h" + strings.Repeat("alternate\r\n", 100)))
	if emulator.scrs[1].Scrollback() != nil {
		t.Fatalf("alternate screen retained hidden scrollback: %d rows", emulator.scrs[1].Scrollback().Len())
	}
	if got := emulator.ScrollbackLen(); got != 0 {
		t.Fatalf("alternate output contaminated %d normal history rows", got)
	}
}

func TestPhoneTerminalHyperlinkLimitDropsParamsAndOversizedURL(t *testing.T) {
	emulator := NewEmulator(20, 5)
	emulator.SetMaxLinkBytes(32)
	_, _ = emulator.Write([]byte("\x1b]8;" + strings.Repeat("p", 1000) + ";https://example.com\x1b\\x"))
	cell := emulator.CellAt(0, 0)
	if cell == nil || cell.Link.URL != "https://example.com" || cell.Link.Params != "" {
		t.Fatalf("bounded link = %#v", cell)
	}

	emulator = NewEmulator(20, 5)
	emulator.SetMaxLinkBytes(32)
	_, _ = emulator.Write([]byte("\x1b]8;;https://example.com/" + strings.Repeat("x", 32) + "\x1b\\x"))
	if cell := emulator.CellAt(0, 0); cell != nil && !cell.Link.IsZero() {
		t.Fatalf("oversized link retained: %#v", cell.Link)
	}
}

package broker

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func previewFrame(rows ...string) terminal.Frame {
	frame := terminal.Frame{}
	for _, text := range rows {
		row := terminal.Row{}
		for _, character := range text {
			row.Cells = append(row.Cells, terminal.Cell{Text: string(character), Width: 1})
		}
		frame.Screen = append(frame.Screen, row)
	}
	return frame
}

func TestScreenPreviewUsesLastNonBlankRow(t *testing.T) {
	for name, test := range map[string]struct {
		frame terminal.Frame
		want  string
	}{
		"blank":         {previewFrame("     ", "", "   "), ""},
		"no rows":       {terminal.Frame{}, ""},
		"last row":      {previewFrame("first", "  second \u00a0  line\u3000 ", "   ", ""), "second line"},
		"controls":      {previewFrame("a\x1b[31mb\x00c\x7fd\u0085e\u009bf\tg"), "a[31mbcdefg"},
		"only control":  {previewFrame("ok", "\x1b\x07"), "ok"},
		"bidi":          {previewFrame("\u202eevil\u2066x\u200bz\ufeff"), "\ufffdevil\ufffdx\ufffdz\ufffd"},
		"emoji and cjk": {previewFrame("界 e\u0301"), "界 e\u0301"},
		"private use":   {previewFrame("\ue0b0 main"), "\ufffd main"},
	} {
		if got := screenPreview(test.frame); got != test.want {
			t.Errorf("%s: preview = %q, want %q", name, got, test.want)
		}
	}
	wide := terminal.Frame{Screen: []terminal.Row{{Cells: []terminal.Cell{
		{Text: "界", Width: 2}, {Continuation: true}, {Text: "👩\u200d💻", Width: 2}, {Continuation: true}, {Text: "x", Width: 1},
	}}}}
	if got := screenPreview(wide); got != "界👩\u200d💻x" {
		t.Fatalf("wide preview = %q", got)
	}
}

func TestScreenPreviewTruncatesToRuneAndByteLimits(t *testing.T) {
	exact := strings.Repeat("a", protocol.MaxBrokerRPCPreviewRunes)
	if got := screenPreview(previewFrame(exact)); got != exact {
		t.Fatalf("160-rune preview changed: %q", got)
	}
	if got := screenPreview(previewFrame(strings.Repeat("a", 400))); got != strings.Repeat("a", 159)+"…" {
		t.Fatalf("rune-limited preview = %q", got)
	}
	if got := screenPreview(previewFrame(strings.Repeat("😀", 200))); got != strings.Repeat("😀", 119)+"…" {
		t.Fatalf("byte-limited preview = %q (%d bytes)", got, len(got))
	}
	if got := screenPreview(previewFrame(strings.Repeat("a", 158) + "   bcd")); got != strings.Repeat("a", 158)+"…" {
		t.Fatalf("space before cut was kept: %q", got)
	}
}

// Every preview, from any cell content, must pass the protocol validator,
// which mirrors the browser's safeDisplayText, maxLength and maxUtf8Bytes.
func TestScreenPreviewAlwaysPassesProtocolValidation(t *testing.T) {
	pool := []string{
		"a", "Z", "0", " ", "\t", "\n", "\r", "\x00", "\x1b", "\x7f", "\u0085", "\u009b", "\u00a0", "\u3000",
		"\u2028", "\u200b", "\u200d", "\u202e", "\u2066", "\u2069", "\ufeff", "\u0301", "e\u0301", "界", "😀",
		"👩\u200d💻", "🇮🇳", "\ue000", "\ufffd", "\U0010ffff", "\u0378", "\xff", "\xc3", "…", "\u00ad",
	}
	random := rand.New(rand.NewPCG(1, 2))
	for iteration := range 5000 {
		frame := terminal.Frame{Screen: make([]terminal.Row, 1+random.IntN(4))}
		for row := range frame.Screen {
			cells := make([]terminal.Cell, random.IntN(420))
			for index := range cells {
				var text strings.Builder
				for range 1 + random.IntN(4) {
					text.WriteString(pool[random.IntN(len(pool))])
				}
				cells[index] = terminal.Cell{Text: text.String(), Width: 1, Continuation: random.IntN(10) == 0}
			}
			frame.Screen[row].Cells = cells
		}
		got := screenPreview(frame)
		if got == "" {
			continue
		}
		if err := protocol.ValidatePreviewText(got); err != nil {
			t.Fatalf("iteration %d: preview %q failed validation: %v", iteration, got, err)
		}
		if !utf8.ValidString(got) || strings.ContainsAny(got, "\u202e\u2066\u2069\u200b\ufeff\u00ad\u2028") {
			t.Fatalf("iteration %d: unsafe preview %q", iteration, got)
		}
	}
}

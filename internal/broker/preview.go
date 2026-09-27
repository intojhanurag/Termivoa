package broker

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	previewEllipsis = "…"
	zeroWidthJoiner = '\u200d' // Kept so emoji ZWJ sequences stay whole.
)

// screenPreview returns the last non-blank row of a screen as plain text for
// the browser session list, or "" when every row is blank. The result always
// passes protocol.ValidatePreviewText.
func screenPreview(frame terminal.Frame) string {
	for row := len(frame.Screen) - 1; row >= 0; row-- {
		if text := previewRow(frame.Screen[row]); text != "" {
			return text
		}
	}
	return ""
}

// previewRow joins cell texts, drops C0/C1 controls and DEL, collapses every
// Unicode space run to one ASCII space, trims, and replaces any other
// non-printable rune (format, bidi, private-use, unassigned) with U+FFFD.
// Concealed cells (SGR 8) count as blank: the live view hides them too.
func previewRow(row terminal.Row) string {
	var text strings.Builder
	runes, space := 0, false
cells:
	for _, cell := range row.Cells {
		if cell.Continuation {
			continue
		}
		if cell.Conceal {
			space = text.Len() > 0
			continue
		}
		for _, character := range cell.Text {
			switch {
			case unicode.IsControl(character):
				continue
			case unicode.IsSpace(character):
				space = text.Len() > 0
				continue
			case !unicode.IsPrint(character) && character != zeroWidthJoiner:
				character = utf8.RuneError
			}
			if space {
				text.WriteByte(' ')
				runes, space = runes+1, false
			}
			text.WriteRune(character)
			runes++
			if runes > protocol.MaxBrokerRPCPreviewRunes {
				break cells // Already over the limit; the rest is cut anyway.
			}
		}
	}
	return truncatePreview(text.String())
}

// truncatePreview cuts on a rune boundary so the text plus "…" fits both the
// rune and the UTF-8 byte limit.
func truncatePreview(text string) string {
	if utf8.RuneCountInString(text) <= protocol.MaxBrokerRPCPreviewRunes && len(text) <= protocol.MaxBrokerRPCPreviewBytes {
		return text
	}
	end, runes := 0, 0
	for index, character := range text {
		next := index + utf8.RuneLen(character)
		if runes+1 > protocol.MaxBrokerRPCPreviewRunes-1 || next > protocol.MaxBrokerRPCPreviewBytes-len(previewEllipsis) {
			break
		}
		end, runes = next, runes+1
	}
	return strings.TrimRight(text[:end], " ") + previewEllipsis
}

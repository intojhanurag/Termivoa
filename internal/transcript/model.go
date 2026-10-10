// Package transcript reads a coding agent's own JSONL transcript, read-only,
// and turns it into the bounded chat block model of protocol revision 8
// (docs/chat-view.md). It is the only package that knows agent file formats.
// v1 reads Claude Code only.
package transcript

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// maskMargin is how much text past a cut is still masked first, so a secret
// that straddles the cut is never half shown (review S7). A PEM block longer
// than this past the cut is masked to the end of what is kept.
const maskMargin = 16 << 10

// The secret patterns are defense in depth, not a guarantee (design
// section 8). A token prefix matches at the start or after any character
// that is not a letter or digit (review S-4: "_ghp_…" or "[0mghp_…" too).
var (
	pemPattern   = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]{0,40}-----(?s:.*?)(?:-----END [A-Z0-9 ]{0,40}-----|$)`)
	tokenPattern = regexp.MustCompile(`(^|[^A-Za-z0-9])(?:` +
		`(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}` +
		`|github_pat_[A-Za-z0-9_]{20,}` +
		`|sk-ant-[A-Za-z0-9_-]{10,}` +
		`|sk-[A-Za-z0-9_-]{16,}` +
		`|(?:AKIA|ASIA)[0-9A-Z]{16}` +
		`|xox[bpas]-[A-Za-z0-9-]{10,}` +
		`|AIza[0-9A-Za-z_-]{35}` +
		`|eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,})`)
	// ansiPattern is a terminal escape sequence (CSI, OSC or a two-byte one).
	ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[@-Z\\-_])`)
)

var bearerPattern = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s"'\\]+`)

// secretHints are cheap substrings one of which every pattern needs; text
// without any of them skips the regular expressions.
var secretHints = []string{"-----BEGIN", "gh", "github_pat_", "sk-", "AKIA", "ASIA", "xox", "AIza", "eyJ", "earer", "EARER"}

const secretMark = "[secret]"

// mask replaces the fixed secret patterns with [secret]. Callers clean the
// text first, so hidden characters and escape sequences cannot split a
// token past the mask (review S-4).
func mask(text string) string {
	hinted := false
	for _, hint := range secretHints {
		if strings.Contains(text, hint) {
			hinted = true
			break
		}
	}
	if !hinted {
		return text
	}
	text = pemPattern.ReplaceAllLiteralString(text, secretMark)
	text = tokenPattern.ReplaceAllString(text, "${1}"+secretMark)
	return bearerPattern.ReplaceAllString(text, "${1}"+secretMark)
}

// clean makes text safe to show: valid UTF-8, no hidden format characters
// (bidi controls, zero-width; review S3), no control characters except tab
// and line feed when multiline (carriage returns are dropped).
func clean(text string, multiline bool) string {
	if strings.IndexByte(text, 0x1b) >= 0 {
		text = ansiPattern.ReplaceAllString(text, "")
	}
	ok := utf8.ValidString(text)
	if ok {
		for _, r := range text {
			if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) || protocol.HiddenFormatRune(r) {
				ok = false
				break
			}
		}
	}
	if ok {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range strings.ToValidUTF8(text, "�") {
		switch {
		case multiline && (r == '\n' || r == '\t'):
			out.WriteRune(r)
		case !multiline && (r == '\n' || r == '\t'):
			out.WriteByte(' ')
		case unicode.IsControl(r) || protocol.HiddenFormatRune(r):
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// cutBytes cuts text to at most limit bytes at a rune boundary.
func cutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// cutLines keeps at most limit lines.
func cutLines(text string, limit int) string {
	index := 0
	for line := 0; line < limit; line++ {
		next := strings.IndexByte(text[index:], '\n')
		if next < 0 {
			return text
		}
		index += next + 1
	}
	return strings.TrimSuffix(text[:index], "\n")
}

// cutRunes cuts text to at most limit runes.
func cutRunes(text string, limit int) string {
	count := 0
	for index := range text {
		if count == limit {
			return text[:index]
		}
		count++
	}
	return text
}

// shown masks the start of raw (with the margin), cleans it and cuts it to
// maxBytes and maxLines. It returns the text and the bytes of raw not shown.
func shown(raw string, maxBytes, maxLines int, multiline bool) (string, int) {
	window := cutBytes(raw, maxBytes+maskMargin)
	full := mask(clean(window, multiline))
	text := full
	if maxLines > 0 {
		text = cutLines(text, maxLines)
	}
	text = cutBytes(text, maxBytes)
	if len(window) == len(raw) && len(text) == len(full) {
		return text, 0
	}
	return text, max(len(raw)-len(text), 1)
}

// line is one line of summary text: masked, cleaned, at most 240 runes.
func line(raw string) string {
	if index := strings.IndexByte(raw, '\n'); index >= 0 {
		raw = raw[:index]
	}
	text, _ := shown(raw, 4*protocol.MaxChatSummaryRunes, 0, false)
	return strings.TrimSpace(cutRunes(text, protocol.MaxChatSummaryRunes))
}

// relative shows path relative to the session folder, or its base name when
// it is outside (design section 8: paths relative only).
func relative(cwd, path string) string {
	if path == "" {
		return ""
	}
	if cwd != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = rel
		} else {
			path = filepath.Base(path)
		}
	} else if filepath.IsAbs(path) {
		path = filepath.Base(path)
	}
	text := strings.TrimSpace(mask(clean(path, false)))
	return cutRunes(text, protocol.MaxChatEditPathRunes)
}

func countLines(text string) int64 {
	if text == "" {
		return 0
	}
	return int64(strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1)
}

// hunk is one bounded diff: removed lines then added lines, at most 40
// lines of at most 200 runes, masked.
func hunk(old, new string) string {
	var out bytes.Buffer
	lines := 0
	add := func(prefix byte, text string) {
		if text == "" {
			return
		}
		for _, part := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if lines == protocol.MaxChatHunkLines {
				return
			}
			if lines > 0 {
				out.WriteByte('\n')
			}
			out.WriteByte(prefix)
			out.WriteString(cutRunes(part, 200))
			lines++
		}
	}
	// Mask whole strings before any line is cut (review S7).
	add('-', mask(clean(cutBytes(old, 64<<10), true)))
	add('+', mask(clean(cutBytes(new, 64<<10), true)))
	return cutBytes(cutLines(out.String(), protocol.MaxChatHunkLines), protocol.MaxChatHunkBytes)
}

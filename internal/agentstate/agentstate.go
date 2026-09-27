// Package agentstate classifies a terminal session as working, waiting on a
// question, or idle. It reads only a sanitized screen and output timing, so
// every rule is a pure function that table tests can drive from fixtures.
//
// Screen text is untrusted child output. Every string this package returns is
// cleaned like the session-list preview: control characters removed, space
// runs collapsed, other non-printable runes replaced, and length capped.
package agentstate

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// State is the agent-level activity of a running session.
type State string

const (
	Working State = "working"
	Waiting State = "waiting"
	Idle    State = "idle"
)

const (
	// Settle is how long one question must stay on screen before it counts as
	// waiting. Claude Code blinks a dot while its dialog is open, so this is
	// measured on the parsed question, not on output silence.
	Settle = time.Second
	// IdleAfter is the output silence after which a session without a
	// question or busy line is idle ("your turn").
	IdleAfter = 3 * time.Second

	MaxOptions       = 6
	MaxQuestionRunes = 160
	MaxQuestionBytes = 480
	MaxLabelRunes    = 80
	MaxLabelBytes    = 240

	// Detail display caps. The id hashes the whole context, not only this.
	MaxDetailLines     = 6
	MaxDetailLineRunes = 120
	MaxDetailLineBytes = 360
	MaxDetailRunes     = 600
	MaxDetailBytes     = 1800

	// maxContextRows bounds how far above a menu the context search reads.
	maxContextRows = 16
)

// Screen is one sanitized copy of the visible grid. Lines hold one entry per
// row with the cell text in column order; concealed (SGR 8) cells must already
// be blank. Continuation cells of wide characters are omitted.
type Screen struct {
	Lines         []string
	CursorRow     int
	CursorVisible bool
}

// Input is the exact answer for one option, sent in field order: Text, then
// Escape, then Enter. It is derived from the current screen and never from a
// client.
type Input struct {
	Text   string
	Escape bool
	Enter  bool
}

// Option is one answer. Label is safe display text.
type Option struct {
	Label string
	Input Input
}

// Question is a recognized prompt. Text is safe display text. Detail is the
// safe, capped display form of what is being approved (tool, command, file),
// one line per "\n"; it is empty when the screen shows no such lines.
type Question struct {
	Text    string
	Detail  string
	Options []Option
	// context is every dialog line above the options (uncapped), so the id
	// changes whenever anything that is being approved changes.
	context []string
}

// ID is a stable hash of the question in one session generation. It covers the
// text, the full context and detail, and the option labels, but not the
// inputs, which follow the menu highlight. Each field is length-prefixed.
func (question Question) ID(generation string) string {
	hash := sha256.New()
	write := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		hash.Write(size[:])
		hash.Write([]byte(value))
	}
	write(generation)
	write(question.Text)
	write(question.Detail)
	write(strconv.Itoa(len(question.context)))
	for _, line := range question.context {
		write(line)
	}
	write(strconv.Itoa(len(question.Options)))
	for _, option := range question.Options {
		write(option.Label)
	}
	return hex.EncodeToString(hash.Sum(nil))[:32]
}

// newQuestion fills Detail from the full context.
func newQuestion(text string, context []string) *Question {
	return &Question{Text: text, Detail: formatDetail(context), context: context}
}

// formatDetail cleans and caps context lines for display.
func formatDetail(context []string) string {
	var kept []string
	runes, bytes := 0, 0
	for _, line := range context {
		line = Clean(line, MaxDetailLineRunes, MaxDetailLineBytes)
		if line == "" {
			continue
		}
		size := utf8.RuneCountInString(line)
		if len(kept) > 0 {
			size++ // The "\n" separator.
		}
		if len(kept) == MaxDetailLines || runes+size > MaxDetailRunes || bytes+len(line)+1 > MaxDetailBytes {
			break
		}
		kept = append(kept, line)
		runes, bytes = runes+size, bytes+len(line)+1
	}
	return strings.Join(kept, "\n")
}

// Parsed is what one screen shows, before timing is applied.
type Parsed struct {
	Question *Question
	// Busy is set when an agent status line says it can be interrupted.
	Busy bool
}

// Parse reads one screen. It prefers a numbered menu near the bottom of the
// screen, then a y/n prompt on the cursor line.
func Parse(screen Screen) Parsed {
	lines := make([]string, len(screen.Lines))
	for index, line := range screen.Lines {
		lines[index] = trimLine(line)
	}
	last := len(lines) - 1
	for last >= 0 && lines[last] == "" {
		last--
	}
	if last < 0 {
		return Parsed{}
	}
	parsed := Parsed{Busy: busy(lines, last)}
	if question := parseMenu(lines, last, screen); question != nil {
		parsed.Question = question
	} else if question := parseYesNo(lines, last, screen); question != nil {
		parsed.Question = question
	}
	return parsed
}

// Classify applies timing. quiet is the time since the last child output;
// stable is how long the parsed question has stayed the same.
func Classify(parsed Parsed, quiet, stable time.Duration) State {
	switch {
	case parsed.Busy:
		return Working
	case parsed.Question != nil && stable >= Settle:
		return Waiting
	case quiet < IdleAfter:
		return Working
	default:
		return Idle
	}
}

// Detect is Parse and Classify in one call. The question is returned only
// when the state is Waiting.
func Detect(screen Screen, quiet, stable time.Duration) (State, *Question) {
	parsed := Parse(screen)
	state := Classify(parsed, quiet, stable)
	if state != Waiting {
		return state, nil
	}
	return state, parsed.Question
}

// busy recognizes agent status lines near the bottom, such as Claude Code's
// "esc to interrupt" footer and Codex's "Working (9s • esc to interrupt)".
func busy(lines []string, last int) bool {
	for index, seen := last, 0; index >= 0 && seen < 8; index-- {
		if lines[index] == "" {
			continue
		}
		seen++
		lower := strings.ToLower(lines[index])
		if strings.Contains(lower, "esc to interrupt") || strings.Contains(lower, "ctrl+c to interrupt") {
			return true
		}
	}
	return false
}

var (
	optionPattern   = regexp.MustCompile(`^(?:([❯›>▸▶➤])\s*)?([1-9])[.)]\s+(\S.*)$`)
	shortcutPattern = regexp.MustCompile(`(?i)\s*\((esc|[a-z])\)$`)
	yesNoPattern    = regexp.MustCompile(`(?i)[\[(]\s*(y|yes)\s*/\s*(n|no)\s*[\])]\s*[:?]?$`)
	rulePattern     = regexp.MustCompile(`^[─━═-]{8,}$`)
)

// maxFooterLines is how many non-option lines may follow the last option,
// such as "Esc to cancel · Tab to amend".
const maxFooterLines = 2

// parseMenu recognizes a numbered selection menu at the bottom of the screen:
// options 1..n in order, exactly one highlighted with a cursor marker, and at
// most two continuation lines between options. Being conservative is the
// point: a missed prompt is better than a wrong "waiting".
func parseMenu(lines []string, last int, screen Screen) *Question {
	end, footers := last, 0
	for end >= 0 && !optionPattern.MatchString(lines[end]) {
		if lines[end] != "" {
			footers++
			if footers > maxFooterLines {
				return nil
			}
		}
		end--
	}
	if end < 0 {
		return nil
	}
	count := int(optionPattern.FindStringSubmatch(lines[end])[2][0] - '0')
	if count < 2 {
		return nil
	}
	type found struct {
		row    int
		match  []string
		extras []string
	}
	options := make([]found, count)
	var pending []string
	expect := count
	for row := end; row >= 0 && expect > 0; row-- {
		line := lines[row]
		if match := optionPattern.FindStringSubmatch(line); match != nil {
			if int(match[2][0]-'0') != expect {
				return nil
			}
			options[expect-1] = found{row: row, match: match, extras: pending}
			pending = nil
			expect--
			continue
		}
		if line == "" || len(pending) == 2 {
			return nil
		}
		pending = append([]string{line}, pending...)
	}
	if expect > 0 {
		return nil
	}
	highlighted := 0
	for _, option := range options {
		if option.match[1] != "" {
			highlighted++
		}
	}
	if highlighted != 1 {
		return nil
	}
	top := options[0].row
	text, textRow := menuTitle(lines, top)
	if text == "" {
		return nil
	}
	// A visible cursor below the menu is a shell prompt after printed output,
	// not an open dialog.
	if screen.CursorVisible && (screen.CursorRow < textRow || screen.CursorRow > end) {
		return nil
	}
	question := newQuestion(text, menuContext(lines, top, textRow))
	for index, option := range options {
		if index == MaxOptions {
			break
		}
		label := strings.Join(append([]string{option.match[3]}, option.extras...), " ")
		input := Input{Text: string(rune('1' + index))}
		if option.match[1] != "" {
			input = Input{Enter: true}
		}
		if shortcut := shortcutPattern.FindStringSubmatch(option.match[3]); shortcut != nil {
			label = strings.Join(append([]string{strings.TrimSuffix(option.match[3], shortcut[0])}, option.extras...), " ")
			if strings.EqualFold(shortcut[1], "esc") {
				input = Input{Escape: true}
			} else {
				input = Input{Text: strings.ToLower(shortcut[1])}
			}
		}
		label = Clean(label, MaxLabelRunes, MaxLabelBytes)
		if label == "" {
			return nil
		}
		question.Options = append(question.Options, Option{Label: label, Input: input})
	}
	return question
}

// menuTitle returns the question above a menu: the nearest line with "?"
// within six lines (cut after its first "?"), else the nearest non-blank line
// within four. A horizontal rule ends the search.
func menuTitle(lines []string, top int) (string, int) {
	fallback, fallbackRow := "", -1
	for row, seen := top-1, 0; row >= 0 && seen < 6; row, seen = row-1, seen+1 {
		line := lines[row]
		if rulePattern.MatchString(line) {
			break
		}
		if index := strings.IndexByte(line, '?'); index >= 0 {
			return Clean(line[:index+1], MaxQuestionRunes, MaxQuestionBytes), row
		}
		if fallbackRow < 0 && line != "" && seen < 4 {
			fallback, fallbackRow = Clean(line, MaxQuestionRunes, MaxQuestionBytes), row
		}
	}
	return fallback, fallbackRow
}

// menuContext returns the dialog lines above a menu, top-down: from option 1
// upward, after the blank rows right above it, until a horizontal rule, a box
// top corner, two blank rows in a row, or maxContextRows. The question row is
// replaced by what follows its "?" (Codex puts more text there); a fallback
// title row is left out because it is the question text itself.
func menuContext(lines []string, top, questionRow int) []string {
	row := top - 1
	for row >= 0 && lines[row] == "" {
		row--
	}
	var context []string
	blanks := 0
	for stop := top - maxContextRows; row >= 0 && row >= stop; row-- {
		line := lines[row]
		if rulePattern.MatchString(line) || strings.IndexAny(line, "╭┌╔") == 0 {
			break
		}
		if line == "" {
			if blanks++; blanks == 2 {
				break
			}
			continue
		}
		blanks = 0
		if row == questionRow {
			index := strings.IndexByte(line, '?')
			if index < 0 {
				continue
			}
			if line = strings.TrimSpace(line[index+1:]); line == "" {
				continue
			}
		}
		context = append([]string{line}, context...)
	}
	return context
}

// parseYesNo recognizes a [y/N], (y/n), [Y/n] or (yes/no) prompt that ends the
// visible cursor line when that line is the last content on screen.
func parseYesNo(lines []string, last int, screen Screen) *Question {
	if !screen.CursorVisible || screen.CursorRow != last {
		return nil
	}
	match := yesNoPattern.FindStringSubmatch(lines[last])
	if match == nil {
		return nil
	}
	text := Clean(lines[last], MaxQuestionRunes, MaxQuestionBytes)
	if text == "" {
		return nil
	}
	yes, no := "y", "n"
	if len(match[1]) > 1 {
		yes, no = "yes", "no"
	}
	// Up to two lines directly above belong to the same prompt (apt lists the
	// packages and the size there); a blank row ends them.
	var context []string
	for row := last - 1; row >= 0 && row >= last-2 && lines[row] != ""; row-- {
		context = append([]string{lines[row]}, context...)
	}
	question := newQuestion(text, context)
	question.Options = []Option{
		{Label: "Yes", Input: Input{Text: yes, Enter: true}},
		{Label: "No", Input: Input{Text: no, Enter: true}},
	}
	return question
}

// trimLine removes surrounding spaces and box-drawing borders.
func trimLine(line string) string {
	return strings.TrimFunc(line, func(character rune) bool {
		return unicode.IsSpace(character) || character == '│' || character == '┃' || character == '║'
	})
}

const zeroWidthJoiner = '‍'

// Clean makes untrusted text safe for display with the same rules as the
// session-list preview. It returns "" when nothing printable remains.
func Clean(text string, maxRunes, maxBytes int) string {
	var builder strings.Builder
	space := false
	for _, character := range text {
		switch {
		case unicode.IsControl(character):
			continue
		case unicode.IsSpace(character):
			space = builder.Len() > 0
			continue
		case !unicode.IsPrint(character) && character != zeroWidthJoiner:
			character = utf8.RuneError
		}
		if space {
			builder.WriteByte(' ')
			space = false
		}
		builder.WriteRune(character)
	}
	cleaned := builder.String()
	if utf8.RuneCountInString(cleaned) <= maxRunes && len(cleaned) <= maxBytes {
		return cleaned
	}
	const ellipsis = "…"
	end, runes := 0, 0
	for index, character := range cleaned {
		next := index + utf8.RuneLen(character)
		if runes+1 > maxRunes-1 || next > maxBytes-len(ellipsis) {
			break
		}
		end, runes = next, runes+1
	}
	return strings.TrimRight(cleaned[:end], " ") + ellipsis
}

// Status is the published agent state of one session.
type Status struct {
	State      State
	Since      time.Time
	Question   *Question // Set only while Waiting.
	QuestionID string    // Set only while Waiting.
	// Worked is set only on Idle: the length of the busy period this idle
	// ended, from the first Working after the previous Idle. Zero when the
	// idle ended no busy period (or only the session's startup output).
	Worked time.Duration
}

// Tracker turns successive parses into a Status. It keeps no clock of its
// own: callers pass now, so tests control time.
type Tracker struct {
	generation     string
	status         Status
	candidate      string
	candidateSince time.Time
	busySince      time.Time // First Working since the last Idle; zero when none.
}

func NewTracker(generation string) *Tracker {
	return &Tracker{generation: generation}
}

func (tracker *Tracker) Status() Status { return tracker.status }

// Observe applies one parse taken at now. lastOutput is the time of the last
// child output (or the session start). It reports whether Status changed and
// when the caller must observe again even without new output (zero: never).
func (tracker *Tracker) Observe(now time.Time, parsed Parsed, lastOutput time.Time) (bool, time.Time) {
	id := ""
	if parsed.Question != nil {
		id = parsed.Question.ID(tracker.generation)
	}
	if id != tracker.candidate {
		tracker.candidate, tracker.candidateSince = id, now
	}
	quiet, stable := now.Sub(lastOutput), now.Sub(tracker.candidateSince)
	state := Classify(parsed, quiet, stable)
	next := Status{State: state, Since: now}
	if state == Waiting {
		next.Question, next.QuestionID = parsed.Question, id
	}
	changed := next.State != tracker.status.State || next.QuestionID != tracker.status.QuestionID
	if changed {
		switch {
		// The first status of a session is its startup output (the shell
		// prompt), not work anyone waits for, so it starts no busy period.
		case state == Working && tracker.busySince.IsZero() && tracker.status.State != "":
			tracker.busySince = now
		case state == Idle && !tracker.busySince.IsZero():
			next.Worked, tracker.busySince = now.Sub(tracker.busySince), time.Time{}
		}
		tracker.status = next
	}
	var wake time.Time
	if id != "" && stable < Settle && !parsed.Busy {
		wake = tracker.candidateSince.Add(Settle)
	}
	if state == Working && !parsed.Busy && quiet < IdleAfter {
		if idle := lastOutput.Add(IdleAfter); wake.IsZero() || idle.Before(wake) {
			wake = idle
		}
	}
	return changed, wake
}

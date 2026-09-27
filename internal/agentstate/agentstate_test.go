package agentstate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// loadScreen reads a fixture: a "# cursor=ROW,COL visible=BOOL" header, then
// one line per terminal row.
func loadScreen(t *testing.T, name string) Screen {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	header, body, _ := strings.Cut(string(data), "\n")
	var screen Screen
	var column int
	if _, err := fmt.Sscanf(header, "# cursor=%d,%d visible=%t", &screen.CursorRow, &column, &screen.CursorVisible); err != nil {
		t.Fatalf("%s header: %v", name, err)
	}
	screen.Lines = strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	return screen
}

func TestDetectFixtures(t *testing.T) {
	enter, escape := Input{Enter: true}, Input{Escape: true}
	text := func(value string) Input { return Input{Text: value} }
	tests := []struct {
		fixture  string
		quiet    time.Duration
		stable   time.Duration
		state    State
		question string
		options  []Option
	}{
		// Real captures (Claude Code 2.1.283, Codex CLI 0.157.0).
		{fixture: "claude-permission.txt", quiet: 300 * time.Millisecond, stable: 2 * time.Second, state: Waiting,
			question: "Do you want to proceed?", options: []Option{
				{"Yes", enter},
				{"Yes, and always allow access to /tmp/project from this project", text("2")},
				{"Yes, and switch to auto mode · auto mode handles these prompts for you", text("3")},
				{"No", text("4")},
			}},
		{fixture: "claude-permission.txt", quiet: 300 * time.Millisecond, stable: 500 * time.Millisecond, state: Working},
		{fixture: "claude-working.txt", quiet: time.Minute, stable: time.Minute, state: Working},
		{fixture: "claude-idle-after-reject.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Idle},
		{fixture: "claude-idle-after-approve.txt", quiet: time.Second, state: Working},
		{fixture: "claude-idle-after-approve.txt", quiet: 3 * time.Second, state: Idle},
		// Unnumbered menus need arrow keys to answer, so they are not offered.
		{fixture: "claude-trust-folder.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Idle},
		{fixture: "codex-trust-folder.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Waiting,
			question: "Trust this folder?", options: []Option{{"Trust and continue", enter}, {"Quit", text("2")}}},
		{fixture: "codex-approval.txt", quiet: 2 * time.Second, stable: 2 * time.Second, state: Waiting,
			question: "Reason: Do you want to allow creating x.txt in the workspace?", options: []Option{
				{"Yes, proceed", text("y")},
				{"Yes, and don't ask again for commands that start with `touch x.txt`", text("p")},
				{"No, and tell Codex what to do differently", escape},
			}},
		{fixture: "codex-idle-after-reject.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Idle},
		{fixture: "codex-daemon-menu.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Waiting,
			question: "Experimental feature request failed", options: []Option{
				{"Run without daemon this time Restart with these settings (disabled) Restart can…", text("1")},
				{"Cancel", enter},
			}},
		// Hand-written from known UIs.
		{fixture: "claude-permission-boxed.txt", quiet: 2 * time.Second, stable: 2 * time.Second, state: Waiting,
			question: "Do you want to proceed?", options: []Option{
				{"Yes", enter},
				{"Yes, and don't ask again for rm commands in /tmp/project", text("2")},
				{"No, and tell Claude what to do differently", escape},
			}},
		{fixture: "apt-yes-no.txt", quiet: 2 * time.Second, stable: 2 * time.Second, state: Waiting,
			question: "Do you want to continue? [Y/n]", options: []Option{
				{"Yes", Input{Text: "y", Enter: true}}, {"No", Input{Text: "n", Enter: true}},
			}},
		{fixture: "yes-no-answered.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Idle},
		{fixture: "shell-printed-menu.txt", quiet: 5 * time.Second, stable: 5 * time.Second, state: Idle},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("%s/%s/%s", test.fixture, test.quiet, test.stable), func(t *testing.T) {
			state, question := Detect(loadScreen(t, test.fixture), test.quiet, test.stable)
			if state != test.state {
				t.Fatalf("state = %s, want %s", state, test.state)
			}
			if test.state != Waiting {
				if question != nil {
					t.Fatalf("question = %#v outside waiting", question)
				}
				return
			}
			if question.Text != test.question {
				t.Fatalf("question = %q, want %q", question.Text, test.question)
			}
			if !reflect.DeepEqual(question.Options, test.options) {
				t.Fatalf("options = %#v\nwant %#v", question.Options, test.options)
			}
		})
	}
}

func TestParseRejectsAmbiguousMenus(t *testing.T) {
	for name, screen := range map[string]Screen{
		"no highlight":      {Lines: []string{"Continue?", "  1. Yes", "  2. No"}},
		"two highlights":    {Lines: []string{"Continue?", "❯ 1. Yes", "❯ 2. No"}},
		"one option":        {Lines: []string{"Continue?", "❯ 1. Yes"}},
		"out of order":      {Lines: []string{"Continue?", "❯ 1. Yes", "  3. No"}},
		"not at bottom":     {Lines: []string{"Continue?", "❯ 1. Yes", "  2. No", "a", "b", "c"}},
		"blank inside":      {Lines: []string{"Continue?", "❯ 1. Yes", "", "  2. No"}},
		"no title":          {Lines: []string{"❯ 1. Yes", "  2. No"}},
		"cursor below menu": {Lines: []string{"Continue?", "❯ 1. Yes", "  2. No", "$"}, CursorRow: 3, CursorVisible: true},
		"yes/no off cursor": {Lines: []string{"Proceed? [y/N]", "$"}, CursorRow: 1, CursorVisible: true},
		"yes/no no cursor":  {Lines: []string{"Proceed? [y/N]"}},
	} {
		if parsed := Parse(screen); parsed.Question != nil {
			t.Errorf("%s: question = %#v", name, parsed.Question)
		}
	}
}

func TestParseSanitizesUntrustedText(t *testing.T) {
	screen := Screen{Lines: []string{
		"Run \x1b]52;c;x\x07 this‮ now?  " + strings.Repeat("z", 400),
		"❯ 1. Yes\x00\x7f   please",
		"  2. " + strings.Repeat("n", 300),
	}}
	question := Parse(screen).Question
	if question == nil {
		t.Fatal("menu not recognized")
	}
	if question.Text != "Run ]52;c;x this� now?" {
		t.Fatalf("text = %q", question.Text)
	}
	if question.Options[0].Label != "Yes please" {
		t.Fatalf("label = %q", question.Options[0].Label)
	}
	if label := question.Options[1].Label; len([]rune(label)) != MaxLabelRunes || !strings.HasSuffix(label, "…") {
		t.Fatalf("long label = %q", label)
	}
	if got := Clean(strings.Repeat("界", 400), MaxQuestionRunes, MaxQuestionBytes); len(got) > MaxQuestionBytes {
		t.Fatalf("clean exceeded byte cap: %d", len(got))
	}
}

func TestQuestionIDIsStableAndBound(t *testing.T) {
	question := Question{Text: "Proceed?", Options: []Option{{Label: "Yes"}, {Label: "No"}}}
	id := question.ID("gen-a")
	if len(id) != 32 || id != question.ID("gen-a") {
		t.Fatalf("id = %q", id)
	}
	moved := Question{Text: "Proceed?", Options: []Option{{Label: "Yes", Input: Input{Enter: true}}, {Label: "No"}}}
	if moved.ID("gen-a") != id {
		t.Fatal("id must not depend on the highlight-derived input")
	}
	for _, other := range []string{
		question.ID("gen-b"),
		Question{Text: "Proceed!", Options: question.Options}.ID("gen-a"),
		Question{Text: "Proceed?", Options: []Option{{Label: "Yes"}, {Label: "Never"}}}.ID("gen-a"),
	} {
		if other == id {
			t.Fatal("id did not change with generation, text or options")
		}
	}
}

func TestTrackerTransitionsAndWakeups(t *testing.T) {
	start := time.Unix(1000, 0)
	tracker := NewTracker("gen")
	prompt := Parse(Screen{Lines: []string{"Proceed?", "❯ 1. Yes", "  2. No"}})

	changed, wake := tracker.Observe(start, Parsed{}, start)
	if !changed || tracker.Status().State != Working || !wake.Equal(start.Add(IdleAfter)) {
		t.Fatalf("start: %v %#v wake %v", changed, tracker.Status(), wake)
	}
	// A question appears while output continues (Claude blinks a dot).
	at := start.Add(500 * time.Millisecond)
	changed, wake = tracker.Observe(at, prompt, at)
	if changed || !wake.Equal(at.Add(Settle)) {
		t.Fatalf("new question: %v %#v wake %v", changed, tracker.Status(), wake)
	}
	at = at.Add(Settle)
	changed, _ = tracker.Observe(at, prompt, at.Add(-100*time.Millisecond))
	status := tracker.Status()
	if !changed || status.State != Waiting || !status.Since.Equal(at) ||
		status.QuestionID != prompt.Question.ID("gen") || status.Question == nil {
		t.Fatalf("waiting: %v %#v", changed, status)
	}
	// Same question later: no change, Since kept.
	if changed, _ = tracker.Observe(at.Add(time.Minute), prompt, at); changed || !tracker.Status().Since.Equal(at) {
		t.Fatalf("repeat: %v %#v", changed, tracker.Status())
	}
	// Answered: the agent works, then goes quiet.
	at = at.Add(2 * time.Minute)
	if changed, _ = tracker.Observe(at, Parsed{Busy: true}, at); !changed || tracker.Status().State != Working ||
		tracker.Status().Question != nil {
		t.Fatalf("busy: %v %#v", changed, tracker.Status())
	}
	later := at.Add(10 * time.Second)
	if changed, wake = tracker.Observe(later, Parsed{}, at); !changed || tracker.Status().State != Idle || !wake.IsZero() {
		t.Fatalf("idle: %v %#v wake %v", changed, tracker.Status(), wake)
	}
}

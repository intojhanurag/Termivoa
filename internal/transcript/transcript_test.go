//go:build darwin || linux

package transcript_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/transcript"
	"github.com/intojhanurag/phone-terminal/internal/transcript/transcripttest"
)

const agentPID = 4242

var agentStart = time.Date(2026, 10, 10, 8, 59, 0, 0, time.UTC)

// fixture is a temp .claude with one session of a fake agent in cwd.
type fixture struct {
	t       testing.TB
	root    string
	cwd     string
	session *transcripttest.Session
	config  transcript.Config
	procs   []transcript.Process
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	base := t.TempDir()
	root, cwd := filepath.Join(base, ".claude"), filepath.Join(base, "work", "my app")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	session, err := transcripttest.New(root, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.PIDFile(agentPID, agentStart.Add(800*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, root: root, cwd: cwd, session: session,
		config: transcript.Config{Claude: root, Dir: func(_ context.Context, pid int) (string, error) {
			if pid == agentPID {
				return cwd, nil
			}
			return "", nil
		}},
		procs: []transcript.Process{{PID: 100, Start: agentStart.Add(-time.Hour)}, {PID: agentPID, Start: agentStart}},
	}
}

func (f *fixture) bind() transcript.Link {
	f.t.Helper()
	link := transcript.Bind(context.Background(), f.config, f.procs)
	if link.Transcript != nil {
		f.t.Cleanup(func() { _ = link.Transcript.Close() })
	}
	return link
}

func (f *fixture) linked() *transcript.Transcript {
	f.t.Helper()
	link := f.bind()
	if link.State != protocol.ChatLinkLinked {
		f.t.Fatalf("link = %s %s", link.State, link.Reason)
	}
	return link.Transcript
}

// valid encodes the page as the broker would and checks it with the wire
// validator.
func valid(t *testing.T, page protocol.ChatPagePayload) protocol.ChatPagePayload {
	t.Helper()
	session, generation := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	_, err := protocol.EncodeHost(protocol.Envelope{Version: 1, Type: protocol.ChatPage,
		RequestID: "33333333-3333-4333-8333-333333333333", SessionID: &session, Generation: &generation}, page)
	if err != nil {
		t.Fatalf("page does not validate: %v", err)
	}
	return page
}

func kinds(blocks []protocol.ChatBlock) string {
	var out []string
	for _, block := range blocks {
		out = append(out, string(block.Kind))
	}
	return strings.Join(out, ",")
}

func TestLatestPageShowsATurn(t *testing.T) {
	f := newFixture(t)
	f.session.Turn("Make the tests pass")
	tail := f.linked()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 5}))
	if got := kinds(page.Blocks); got != "user_text,status,tool_call,edit,assistant_text" {
		t.Fatalf("kinds = %s", got)
	}
	prompt, thought, bash, edit, text := page.Blocks[0], page.Blocks[1], page.Blocks[2], page.Blocks[3], page.Blocks[4]
	if *prompt.Text != "Make the tests pass" || *prompt.Turn != prompt.ID || *bash.Turn != prompt.ID {
		t.Fatalf("prompt/turn: %+v", prompt)
	}
	if *thought.Text != "Thought for 1s" {
		t.Fatalf("thought = %q", *thought.Text)
	}
	if bash.Tool.Name != "Bash" || *bash.Tool.Summary != "go test ./..." || bash.Tool.Status != protocol.ChatToolOK ||
		!strings.Contains(bash.Tool.Result.Preview, "0.412s") {
		t.Fatalf("bash = %+v %+v", bash.Tool, bash.Tool.Result)
	}
	if edit.Edit.Path != "app.go" || edit.Edit.Added != 1 || edit.Edit.Removed != 1 || *edit.Edit.Hunk != "-return 1\n+return 2" ||
		edit.Tool.Status != protocol.ChatToolOK {
		t.Fatalf("edit = %+v %+v", edit.Edit, edit.Tool)
	}
	if !strings.Contains(*text.Text, "**app.go**") {
		t.Fatalf("text = %q", *text.Text)
	}
	if page.Before != nil || !page.Complete {
		t.Fatalf("a whole file in one page: before=%v complete=%v", page.Before, page.Complete)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), "Let me look") || strings.Contains(string(data), f.cwd) || strings.Contains(string(data), f.session.ID) {
		t.Fatalf("thinking, the folder or the session id leaked: %s", data)
	}
}

func mustPage(t *testing.T, tail *transcript.Transcript, request transcript.PageRequest) protocol.ChatPagePayload {
	t.Helper()
	page, err := tail.Page(request)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestPollThenAfterPageCarriesNewBlocksAndResultUpdates(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("first")
	tail := f.linked()
	first := mustPage(t, tail, transcript.PageRequest{Turns: 5})
	if result, _ := tail.Poll(); result.Grew {
		t.Fatal("no new lines yet")
	}
	call := transcripttest.ToolID()
	f.session.Assistant(transcripttest.MessageID(), transcripttest.ToolUse(call, "Read", map[string]any{"file_path": "/etc/hosts"}))
	if result, _ := tail.Poll(); !result.Grew || result.Rewound {
		t.Fatalf("poll = %+v", result)
	}
	second := valid(t, mustPage(t, tail, transcript.PageRequest{After: first.After, Turns: 5}))
	if len(second.Blocks) != 1 || second.Blocks[0].Tool.Status != protocol.ChatToolRunning || *second.Blocks[0].Tool.Summary != "hosts" ||
		*second.Blocks[0].Turn != first.Blocks[0].ID {
		t.Fatalf("second = %+v", second.Blocks)
	}
	f.session.Result(call, "127.0.0.1 localhost", false)
	tail.Poll()
	third := valid(t, mustPage(t, tail, transcript.PageRequest{After: second.After, Turns: 5}))
	if len(third.Blocks) != 1 || third.Blocks[0].ID != second.Blocks[0].ID || third.Blocks[0].Tool.Status != protocol.ChatToolOK {
		t.Fatalf("the result must update its call: %+v", third.Blocks)
	}
	if _, err := tail.Page(transcript.PageRequest{After: "AAAAAAAAAAAAAAAAAAAAAA"}); err != transcript.ErrCursor {
		t.Fatalf("unknown cursor: %v", err)
	}
}

func TestBeforePagesWalkTheWholeHistoryWithoutRepeatingTurns(t *testing.T) {
	f := newFixture(t)
	for turn := range 70 {
		f.session.Turn("turn " + string(rune('A'+turn%26)) + strings.Repeat("x", turn))
	}
	tail := f.linked()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 10}))
	seen := map[string]bool{}
	pages := 0
	for {
		pages++
		for _, block := range page.Blocks {
			if block.Kind == protocol.UserText {
				seen[block.ID] = true
			}
		}
		if page.Before == nil {
			break
		}
		page = valid(t, mustPage(t, tail, transcript.PageRequest{Before: *page.Before, Turns: 10}))
		if pages > 20 {
			t.Fatal("paging does not end")
		}
	}
	if len(seen) != 70 || pages != 7 {
		t.Fatalf("prompts = %d in %d pages", len(seen), pages)
	}
}

func TestMaskingAndHiddenCharacters(t *testing.T) {
	f := newFixture(t)
	secrets := []string{
		"ghp_" + strings.Repeat("a", 36), "github_pat_" + strings.Repeat("B", 40), "sk-ant-api03-" + strings.Repeat("c", 30),
		"sk-proj-" + strings.Repeat("d", 30), "AKIA" + strings.Repeat("E", 16), "xoxb-" + strings.Repeat("1", 20),
		"AIza" + strings.Repeat("f", 35), "eyJhbGciOi.eyJzdWIiOi.c2lnbmF0dXJl",
	}
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----"
	f.session.Prompt("keys: " + strings.Join(secrets, " ") + "\n" + pem + "\nAuthorization: Bearer abc.def‮gnp.exe")
	call := transcripttest.ToolID()
	f.session.Assistant(transcripttest.MessageID(), transcripttest.ToolUse(call, "Bash",
		map[string]any{"command": "curl -H 'Authorization: Bearer " + strings.Repeat("z", 30) + "' x"}))
	// A secret straddling the preview cut is masked before the cut.
	f.session.Result(call, strings.Repeat("y", 2040)+" ghp_"+strings.Repeat("q", 36), false)
	page := valid(t, mustPage(t, f.linked(), transcript.PageRequest{Turns: 1}))
	data, _ := json.Marshal(page)
	for _, secret := range append(secrets, "b3BlbnNzaC1rZXk", "abc.def", strings.Repeat("z", 30), "‮", "ghp_qqqq") {
		if strings.Contains(string(data), secret) || strings.Contains(string(data), strings.Trim(mustJSON(secret), `"`)) {
			t.Fatalf("%q leaked: %s", secret, data)
		}
	}
	if !strings.Contains(*page.Blocks[0].Text, "Authorization: Bearer [secret]") {
		t.Fatalf("text = %q", *page.Blocks[0].Text)
	}
}

func mustJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestLinesOverTheCapAreScannedNotBuffered(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("read the big file")
	ok, failed := transcripttest.ToolID(), transcripttest.ToolID()
	message := transcripttest.MessageID()
	f.session.Assistant(message, transcripttest.ToolUse(ok, "Read", map[string]any{"file_path": "big.txt"}))
	f.session.Assistant(message, transcripttest.ToolUse(failed, "Bash", map[string]any{"command": "cat big"}))
	// Each over the 1 MiB line cap (the fixture writes the output twice), all
	// inside the 8 MiB page budget.
	f.session.Result(ok, strings.Repeat("x", 1500<<10), false)
	f.session.Result(failed, strings.Repeat("y", 1200<<10), true)
	f.session.Line("user", map[string]any{"message": map[string]any{"role": "user", "content": strings.Repeat("z", 1100<<10)}})
	page := valid(t, mustPage(t, f.linked(), transcript.PageRequest{Turns: 5}))
	got := kinds(page.Blocks)
	if got != "user_text,tool_call,tool_call,user_text" || *page.Blocks[3].Text != "Message too large to show" {
		t.Fatalf("kinds = %s", got)
	}
	if page.Blocks[1].Tool.Status != protocol.ChatToolOK || page.Blocks[2].Tool.Status != protocol.ChatToolError ||
		!page.Blocks[1].Tool.Result.Truncated || page.Blocks[1].Tool.Result.Bytes < 1500<<10 {
		t.Fatalf("statuses: %+v %+v", page.Blocks[1].Tool, page.Blocks[2].Tool)
	}
}

func TestRewindStartsANewHistoryWithoutTheAbandonedBranch(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("alpha")
	reply := f.session.Assistant(transcripttest.MessageID(), transcripttest.Text("alpha done"))
	tail := f.linked()
	first := mustPage(t, tail, transcript.PageRequest{Turns: 5})
	f.session.Prompt("beta")
	f.session.Assistant(transcripttest.MessageID(), transcripttest.Text("beta done"))
	if result, _ := tail.Poll(); result.Rewound {
		t.Fatal("not a rewind yet")
	}
	f.session.SetParent(reply)
	f.session.Prompt("gamma")
	result, _ := tail.Poll()
	if !result.Rewound || tail.History() == first.HistoryID {
		t.Fatalf("poll = %+v", result)
	}
	if _, err := tail.Page(transcript.PageRequest{After: first.After}); err != transcript.ErrCursor {
		t.Fatalf("an old cursor must be refused: %v", err)
	}
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 5}))
	var texts []string
	for _, block := range page.Blocks {
		texts = append(texts, *block.Text)
	}
	if strings.Join(texts, "|") != "alpha|alpha done|gamma" {
		t.Fatalf("texts = %v", texts)
	}
}

func TestQuietLinesAndCompaction(t *testing.T) {
	f := newFixture(t)
	f.session.Line("summary", map[string]any{"summary": "Fixing tests", "leafUuid": transcripttest.UUID()})
	f.session.Line("user", map[string]any{"isMeta": true, "message": map[string]any{"role": "user", "content": "Caveat: generated"}})
	f.session.Line("user", map[string]any{"message": map[string]any{"role": "user", "content": "<command-name>/model</command-name>"}})
	f.session.Line("user", map[string]any{"isSidechain": true, "message": map[string]any{"role": "user", "content": "sub-agent prompt"}})
	f.session.Line("system", map[string]any{"subtype": "compact_boundary", "content": "Conversation compacted"})
	f.session.Line("user", map[string]any{"isCompactSummary": true, "message": map[string]any{"role": "user", "content": "This session is being continued... secret summary"}})
	f.session.Prompt("real prompt")
	f.session.Line("user", map[string]any{"message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "look at this"}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "iVBORw0KGgo="}},
	}}})
	f.session.Line("user", map[string]any{"message": map[string]any{"role": "user", "content": "[Request interrupted by user]"}})
	tail := f.linked()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 60}))
	if got := kinds(page.Blocks); got != "compaction,user_text,user_text,omitted,status" {
		t.Fatalf("kinds = %s", got)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), "secret summary") || strings.Contains(string(data), "sub-agent") || strings.Contains(string(data), "iVBOR") {
		t.Fatalf("leaked: %s", data)
	}
	if tail.FormatUnknown() || tail.Version() != transcripttest.Version {
		t.Fatalf("format unknown=%v version=%q", tail.FormatUnknown(), tail.Version())
	}
}

func TestFormatDriftIsReported(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("hello")
	for range 40 {
		f.session.Line("brand-new-kind", map[string]any{"data": 1})
	}
	tail := f.linked()
	mustPage(t, tail, transcript.PageRequest{Turns: 5})
	if !tail.FormatUnknown() || tail.Unknown() < 0.9 {
		t.Fatalf("unknown share = %v", tail.Unknown())
	}
}

func TestTruncationAndPartialLines(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("one")
	tail := f.linked()
	first := mustPage(t, tail, transcript.PageRequest{Turns: 5})
	if err := f.session.Raw([]byte(`{"type":"user","message":{"role":"user","content":"half`)); err != nil {
		t.Fatal(err)
	}
	if result, _ := tail.Poll(); result.Grew {
		t.Fatal("a partial line is not a new line")
	}
	if err := f.session.Raw([]byte(`"},"uuid":"u","timestamp":"2026-10-10T10:00:00Z"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if result, _ := tail.Poll(); !result.Grew {
		t.Fatal("the finished line is new")
	}
	page := valid(t, mustPage(t, tail, transcript.PageRequest{After: first.After, Turns: 5}))
	if len(page.Blocks) != 1 || *page.Blocks[0].Text != "half" {
		t.Fatalf("blocks = %+v", page.Blocks)
	}
	if err := os.Truncate(f.session.Path, 10); err != nil {
		t.Fatal(err)
	}
	if result, _ := tail.Poll(); !result.Truncated {
		t.Fatal("a shrunk file must re-bind")
	}
}

func TestBackfillReadsAtMostTheBudget(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("words ", 2000)
	for range 1500 { // about 18 MiB, a long session with long answers
		f.session.Prompt("go on")
		f.session.Assistant(transcripttest.MessageID(), transcripttest.Text(big))
	}
	tail := f.linked()
	before := tail.BytesRead()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 60}))
	if read := tail.BytesRead() - before; read > 2*(8<<20)+(1<<20) {
		t.Fatalf("read %d bytes for one page", read)
	}
	if page.Before == nil || len(page.Blocks) == 0 {
		t.Fatal("a long file needs more pages")
	}
}

func TestPagesStayUnderTheMessageCap(t *testing.T) {
	f := newFixture(t)
	for range 30 {
		f.session.Prompt(strings.Repeat("p", 20<<10))
		f.session.Assistant(transcripttest.MessageID(), transcripttest.Text(strings.Repeat("é", 12<<10)))
	}
	tail := f.linked()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 60}))
	if page.Before == nil || page.Blocks[0].Kind != protocol.UserText || page.Blocks[0].TruncatedBytes == nil {
		t.Fatalf("over the cap the page starts at a line and older pages remain: %v", kinds(page.Blocks))
	}
	older := valid(t, mustPage(t, tail, transcript.PageRequest{Before: *page.Before, Turns: 60}))
	if len(older.Blocks) == 0 {
		t.Fatal("older page is empty")
	}
}

func TestMaskingSurvivesEscapesAndHiddenCharacters(t *testing.T) {
	f := newFixture(t)
	token := "ghp_" + strings.Repeat("a", 18) + "\u200b" + strings.Repeat("b", 18)
	f.session.Prompt("\x1b[0mghp_" + strings.Repeat("c", 36) + " x_" + "sk-ant-" + strings.Repeat("d", 20) + " " + token)
	page := valid(t, mustPage(t, f.linked(), transcript.PageRequest{Turns: 1}))
	text := *page.Blocks[0].Text
	for _, leaked := range []string{"cccc", "dddd", "aaaa", "bbbb"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("%q leaked: %q", leaked, text)
		}
	}
	if strings.Count(text, "[secret]") != 3 {
		t.Fatalf("text = %q", text)
	}
}

func TestResultsWithInvalidIDsAreDropped(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("go")
	call := transcripttest.ToolID()
	f.session.Assistant(transcripttest.MessageID(), transcripttest.ToolUse(call, "Bash", map[string]any{"command": "ls"}))
	tail := f.linked()
	f.session.Result("toolu_x/../"+call, "ok", false)
	tail.Poll()
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 1}))
	if page.Blocks[1].Tool.Status != protocol.ChatToolRunning {
		t.Fatalf("an invalid tool_use_id updated a call: %+v", page.Blocks[1].Tool)
	}
}

// A line over 8 MiB is read once by Poll, across ticks, and pages move past
// it (review C-1).
func TestHugeLineIsReadOnceAndPagesMove(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("dump it")
	call := transcripttest.ToolID()
	f.session.Assistant(transcripttest.MessageID(), transcripttest.ToolUse(call, "Bash", map[string]any{"command": "cat big"}))
	tail := f.linked()
	first := mustPage(t, tail, transcript.PageRequest{Turns: 5})
	line := []byte(`{"type":"user","uuid":"big1","parentUuid":"x","timestamp":"2026-10-10T10:00:00Z","message":{"role":"user","content":[{"tool_use_id":"` +
		call + `","type":"tool_result","content":"` + strings.Repeat("y", 10<<20) + `"}]}}` + "\n")
	half := len(line) / 2
	if err := f.session.Raw(line[:half]); err != nil {
		t.Fatal(err)
	}
	reads := func(poll func()) int64 { before := tail.BytesRead(); poll(); return tail.BytesRead() - before }
	if read := reads(func() { tail.Poll() }) + reads(func() { tail.Poll() }); read > int64(half)+(1<<20) {
		t.Fatalf("the unfinished line was read %d bytes, more than once", read)
	}
	if read := reads(func() { tail.Poll() }); read > 0 {
		t.Fatalf("an idle poll read %d bytes", read)
	}
	if err := f.session.Raw(line[half:]); err != nil {
		t.Fatal(err)
	}
	var result transcript.PollResult
	read := reads(func() { result, _ = tail.Poll() })
	if !result.Grew || read > int64(len(line)-half)+(1<<20) {
		t.Fatalf("finishing poll = %+v, read %d", result, read)
	}
	f.session.Prompt("after")
	tail.Poll()
	// Over the 8 MiB budget the page stops after the long line and the next
	// one goes on: no page repeats its start.
	after := valid(t, mustPage(t, tail, transcript.PageRequest{After: first.After, Turns: 5}))
	if after.Complete || len(after.Blocks) != 1 || after.Blocks[0].Tool.Status != protocol.ChatToolOK {
		t.Fatalf("after page = %v complete=%v", kinds(after.Blocks), after.Complete)
	}
	rest := valid(t, mustPage(t, tail, transcript.PageRequest{After: after.After, Turns: 5}))
	if !rest.Complete || kinds(rest.Blocks) != "user_text" {
		t.Fatalf("next page = %v complete=%v", kinds(rest.Blocks), rest.Complete)
	}
	// From the newest page back to the start, paging always moves.
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 1}))
	for steps := 0; page.Before != nil; steps++ {
		if steps > 5 {
			t.Fatal("paging back does not move")
		}
		page = valid(t, mustPage(t, tail, transcript.PageRequest{Before: *page.Before, Turns: 1}))
	}
}

func TestLongPromptOpensATurn(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("first")
	f.session.Prompt(strings.Repeat("p", 1200<<10))
	f.session.Assistant(transcripttest.MessageID(), transcripttest.Text("done"))
	page := valid(t, mustPage(t, f.linked(), transcript.PageRequest{Turns: 1}))
	if got := kinds(page.Blocks); got != "user_text,assistant_text" || page.Before == nil || *page.Blocks[1].Turn != page.Blocks[0].ID {
		t.Fatalf("kinds = %s before=%v", got, page.Before)
	}
}

func TestRewindToAMessageBeforeTheReadRange(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("very old")
	old := f.session.Assistant(transcripttest.MessageID(), transcripttest.Text("old answer"))
	for range 30 {
		f.session.Turn("later")
	}
	tail := f.linked()
	before := tail.History()
	f.session.SetParent(old)
	f.session.Prompt("back to the start")
	if result, _ := tail.Poll(); !result.Rewound || tail.History() == before {
		t.Fatalf("poll = %+v", result)
	}
}

func TestLiveNeverStartsInsideALine(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("one")
	part := `{"type":"user","uuid":"p2","timestamp":"2026-10-10T10:00:00Z","message":{"role":"user","content":"` + strings.Repeat("q", 2<<20)
	if err := f.session.Raw([]byte(part)); err != nil {
		t.Fatal(err)
	}
	tail := f.linked()
	if err := f.session.Raw([]byte(`"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if result, _ := tail.Poll(); !result.Grew {
		t.Fatal("the finished line was lost")
	}
	page := valid(t, mustPage(t, tail, transcript.PageRequest{Turns: 5}))
	if got := kinds(page.Blocks); got != "user_text,user_text" {
		t.Fatalf("kinds = %s", got)
	}
}

func TestPagesDoNotFeedTheDriftWindow(t *testing.T) {
	f := newFixture(t)
	for range 60 {
		f.session.Line("brand-new-kind", map[string]any{"data": 1})
	}
	for range 300 { // well past the 1 MiB read back at bind
		f.session.Prompt(strings.Repeat("w", 4<<10))
	}
	tail := f.linked()
	page := mustPage(t, tail, transcript.PageRequest{Turns: 60})
	for page.Before != nil {
		page = mustPage(t, tail, transcript.PageRequest{Before: *page.Before, Turns: 60})
	}
	if tail.FormatUnknown() {
		t.Fatalf("old lines read for pages counted as drift: %v", tail.Unknown())
	}
}

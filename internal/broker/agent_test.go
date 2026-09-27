package broker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// A Claude Code style dialog: option 1 highlighted, cursor hidden.
const agentDialog = "\x1b[?25lDo you want to proceed?\r\n❯ 1. Yes\r\n  2. No"

// awaitAgentState reads session.state notices until the agent state matches.
func awaitAgentState(t *testing.T, client *streamClient, state protocol.AgentState) protocol.AgentStatus {
	t.Helper()
	for {
		message := client.await(t, protocol.SessionState, "")
		payload, err := protocol.DecodePayload[protocol.SessionStatePayload](message)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Agent != nil && payload.Agent.State == state {
			return *payload.Agent
		}
	}
}

func TestAnswerQuestionThroughLiveStream(t *testing.T) {
	fixture, _, process := newStreamFixture(t)
	controller := startStreamClient(t, fixture.server, protocol.CLI)
	controller.attach(t)
	grant := controller.acquire(t)
	observer := startStreamClient(t, fixture.server, protocol.CLI)
	observer.attach(t)

	process.Emit([]byte(agentDialog))
	waiting := awaitAgentState(t, controller, protocol.Waiting)
	question := waiting.Question
	if question == nil || question.Text != "Do you want to proceed?" || len(question.Options) != 2 ||
		question.Options[0].Label != "Yes" || question.Options[1].Label != "No" {
		t.Fatalf("waiting = %#v", waiting)
	}
	answer := func(client *streamClient, lease, questionID string, option int64) protocol.Envelope {
		t.Helper()
		request := client.action(t, protocol.InputAnswer, protocol.InputAnswerPayload{
			Lease: lease, InputSequence: "1", QuestionID: questionID, OptionIndex: option,
		})
		for {
			message := client.await(t, "", request)
			if message.Type == protocol.InputAccepted || message.Type == protocol.InputRejected {
				return message
			}
		}
	}
	rejected := func(message protocol.Envelope, codes ...protocol.ErrorCode) {
		t.Helper()
		payload, err := protocol.DecodePayload[protocol.InputRejectedPayload](message)
		if err != nil || message.Type != protocol.InputRejected {
			t.Fatalf("answer was not rejected: %s %s", message.Type, message.Payload)
		}
		for _, code := range codes {
			if payload.Code == code {
				return
			}
		}
		t.Fatalf("rejection code = %s, want %v", payload.Code, codes)
	}

	// A stale or different question is refused without consuming the sequence.
	rejected(answer(controller, *grant.Lease, strings.Repeat("0", 32), 1), protocol.QuestionChanged)
	// Only the controller may answer, whatever question it names.
	rejected(answer(observer, strings.Repeat("A", 43), question.ID, 1), protocol.NotController, protocol.StaleLease)
	// An option that is not offered is refused.
	rejected(answer(controller, *grant.Lease, question.ID, 5), protocol.InvalidRequest)
	time.Sleep(50 * time.Millisecond)
	if input := process.inputText(); input != "" {
		t.Fatalf("rejected answers reached the child: %q", input)
	}

	// The current question writes exactly that option's key, once: a repeated
	// sequence is acknowledged as a duplicate without another write.
	for range 2 {
		if message := answer(controller, *grant.Lease, question.ID, 1); message.Type != protocol.InputAccepted {
			t.Fatalf("answer = %s %s", message.Type, message.Payload)
		}
	}
	waitStreamInput(t, process, "2")
	time.Sleep(50 * time.Millisecond)
	if input := process.inputText(); input != "2" {
		t.Fatalf("child input = %q, want one \"2\"", input)
	}

	// Output that replaces the question ends waiting; the old id is refused.
	process.Emit([]byte("\x1b[2J\x1b[H\x1b[?25hdone\r\n$ "))
	awaitAgentState(t, controller, protocol.Working)
	request := controller.action(t, protocol.InputAnswer, protocol.InputAnswerPayload{
		Lease: *grant.Lease, InputSequence: "2", QuestionID: question.ID, OptionIndex: 0,
	})
	rejected(controller.await(t, protocol.InputRejected, request), protocol.QuestionChanged)
	idle := awaitAgentState(t, controller, protocol.Idle)
	if idle.Question != nil {
		t.Fatalf("idle carries a question: %#v", idle)
	}
}

func TestGatewaySessionListCarriesAgentState(t *testing.T) {
	fixture, session, process, service, _ := newAuthStreamFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.Emit([]byte(agentDialog))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Agent.QuestionID != "" })

	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	response := gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(phone.Credential))
	result, err := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](response)
	if err != nil || len(result.Sessions) != 1 {
		t.Fatalf("list = %#v, %v", result, err)
	}
	agent := result.Sessions[0].Agent
	if agent == nil || agent.State != protocol.Waiting || agent.Question == nil ||
		agent.Question.Text != "Do you want to proceed?" || len(agent.StateSince) != len("2006-01-02T15:04:05.000Z") {
		t.Fatalf("agent = %#v", agent)
	}

	cli, err := fixture.server.listSessions(context.Background(), protocol.BrokerRPCSessionListRequestPayload{Limit: 10})
	if err != nil || len(cli.Sessions) != 1 || cli.Sessions[0].Agent != nil {
		t.Fatalf("cli list carries terminal-derived agent state: %#v, %v", cli, err)
	}
}

func TestAgentScreenBlanksConcealedCells(t *testing.T) {
	cell := func(text string, conceal bool) terminal.Cell {
		return terminal.Cell{Text: text, Width: 1, Conceal: conceal}
	}
	frame := terminal.Frame{
		Cursor: terminal.Cursor{Row: 1, Visible: true},
		Screen: []terminal.Row{
			{Cells: []terminal.Cell{cell("O", false), cell("k", true), cell("?", false)}},
			{Cells: []terminal.Cell{cell("界", false), {Continuation: true}, cell("", false), cell("x", false)}},
		},
	}
	screen := agentScreen(frame)
	if screen.Lines[0] != "O ?" || screen.Lines[1] != "界 x" || screen.CursorRow != 1 || !screen.CursorVisible {
		t.Fatalf("screen = %#v", screen)
	}
}

// claudeDialog draws a Claude Code permission prompt for one command.
func claudeDialog(command string) string {
	return "\x1b[2J\x1b[H\x1b[?25l" + strings.Repeat("\u2500", 40) + "\r\n Bash command\r\n\r\n   " + command +
		"\r\n\r\n Do you want to proceed?\r\n \u276f 1. Yes\r\n   2. No"
}

// awaitQuestion reads session.state notices until a waiting question other
// than skip appears.
func awaitQuestion(t *testing.T, client *streamClient, skip string) protocol.AgentQuestion {
	t.Helper()
	for {
		status := awaitAgentState(t, client, protocol.Waiting)
		if status.Question.ID != skip {
			return *status.Question
		}
	}
}

func TestAnswerForReplacedPromptIsRejected(t *testing.T) {
	fixture, _, process := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	grant := client.acquire(t)

	process.Emit([]byte(claudeDialog("rm -rf build")))
	first := awaitQuestion(t, client, "")
	if first.Detail == nil || *first.Detail != "Bash command\nrm -rf build" {
		t.Fatalf("first detail = %v", first.Detail)
	}
	// Same question text and labels, different command: a new id.
	process.Emit([]byte(claudeDialog("npm test -- --watch=false")))
	second := awaitQuestion(t, client, first.ID)
	if second.Text != first.Text || second.Detail == nil || *second.Detail != "Bash command\nnpm test -- --watch=false" {
		t.Fatalf("second = %#v", second)
	}
	request := client.action(t, protocol.InputAnswer, protocol.InputAnswerPayload{
		Lease: *grant.Lease, InputSequence: "1", QuestionID: first.ID, OptionIndex: 0,
	})
	message := client.await(t, "", request)
	payload, err := protocol.DecodePayload[protocol.InputRejectedPayload](message)
	if message.Type != protocol.InputRejected || err != nil || payload.Code != protocol.QuestionChanged {
		t.Fatalf("stale answer = %s %s", message.Type, message.Payload)
	}
	time.Sleep(50 * time.Millisecond)
	if input := process.inputText(); input != "" {
		t.Fatalf("stale answer reached the child: %q", input)
	}
}

// flipTerminal serves a replacement screen after a set number of further
// screen reads, so a test can change the question between an answer's
// admission check and its write without racing the actor.
type flipTerminal struct {
	*terminal.Core
	mu     sync.Mutex
	after  int
	armed  bool
	screen *terminal.Frame
	sent   []string
}

func (core *flipTerminal) ScreenSnapshot(ctx context.Context) (terminal.Frame, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.armed {
		if core.after == 0 {
			return *core.screen, nil
		}
		core.after--
	}
	return core.Core.ScreenSnapshot(ctx)
}

func (core *flipTerminal) SendText(ctx context.Context, text string) error {
	core.mu.Lock()
	core.sent = append(core.sent, text)
	core.mu.Unlock()
	return core.Core.SendText(ctx, text)
}

func TestDroppedAnswerRepublishesState(t *testing.T) {
	fixture := newManagementFixture(t)
	process := &streamProcess{viewerProcess: newViewerProcess()}
	var core *flipTerminal
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		ctx, cancel := context.WithCancel(context.Background())
		real, err := terminal.New(ctx, terminal.Config{Columns: config.Process.Columns, Rows: config.Process.Rows})
		if err != nil {
			cancel()
			return nil, err
		}
		config.InputQueueCapacity, config.PTYOutputQueue = DefaultInputQueueCapacity, DefaultPTYOutputQueue
		core = &flipTerminal{Core: real}
		return startSessionRuntime(ctx, cancel, parent.Done(), config, process, core)
	}
	if _, err := fixture.server.createSession(context.Background(), protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "86666666-6666-4666-8666-666666666667", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	grant := client.acquire(t)
	process.Emit([]byte(claudeDialog("rm -rf build")))
	first := awaitQuestion(t, client, "")

	// The admission check reads the real screen; the write-time check and
	// every later scan see a different prompt.
	replaced := terminal.Frame{Screen: []terminal.Row{
		textRow("Do you want to proceed?"), textRow("\u276f 1. Yes"), textRow("  2. Never"),
	}}
	core.mu.Lock()
	core.armed, core.after, core.screen = true, 1, &replaced
	core.mu.Unlock()
	request := client.action(t, protocol.InputAnswer, protocol.InputAnswerPayload{
		Lease: *grant.Lease, InputSequence: "1", QuestionID: first.ID, OptionIndex: 1,
	})
	client.await(t, protocol.InputAccepted, request)
	// A fresh session.state follows at once, without the old question.
	message := client.await(t, protocol.SessionState, "")
	payload, err := protocol.DecodePayload[protocol.SessionStatePayload](message)
	if err != nil || payload.Agent == nil || payload.Agent.Question != nil && payload.Agent.Question.ID == first.ID {
		t.Fatalf("state after drop = %s, %v", message.Payload, err)
	}
	time.Sleep(50 * time.Millisecond)
	core.mu.Lock()
	sent := core.sent
	core.mu.Unlock()
	if len(sent) != 0 || process.inputText() != "" {
		t.Fatalf("dropped answer was written: %q %q", sent, process.inputText())
	}
}

func textRow(text string) terminal.Row {
	var row terminal.Row
	for _, character := range text {
		row.Cells = append(row.Cells, terminal.Cell{Text: string(character), Width: 1})
	}
	return row
}

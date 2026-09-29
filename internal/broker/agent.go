package broker

import (
	"errors"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/agentstate"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

// agentDoneAfter is the shortest busy period that sends a "done" push when
// the agent goes idle. Shorter blips (a shell command, typing) stay silent.
// A variable only so tests can shorten it.
var agentDoneAfter = 20 * time.Second

// agentScreen copies the visible grid as plain row text for the detector.
// Concealed (SGR 8) cells are blank, as in the live view; the detector cleans
// every string it returns.
func agentScreen(frame terminal.Frame) agentstate.Screen {
	screen := agentstate.Screen{
		Lines: make([]string, len(frame.Screen)), CursorRow: frame.Cursor.Row, CursorVisible: frame.Cursor.Visible,
	}
	var line strings.Builder
	for index, row := range frame.Screen {
		line.Reset()
		for _, cell := range row.Cells {
			switch {
			case cell.Continuation:
			case cell.Conceal || cell.Text == "":
				line.WriteByte(' ')
			default:
				line.WriteString(cell.Text)
			}
		}
		screen.Lines[index] = line.String()
	}
	return screen
}

// parseAgentScreen reads the current screen in the actor.
func (session *Session) parseAgentScreen() (agentstate.Parsed, error) {
	frame, err := session.terminal.ScreenSnapshot(session.ctx)
	if err != nil {
		return agentstate.Parsed{}, err
	}
	return agentstate.Parse(agentScreen(frame)), nil
}

// observeAgent rescans the screen and returns when to scan again without new
// output (zero: not needed). A changed status reaches viewers through
// publishSessionState on the next actor loop.
func (session *Session) observeAgent(startedAt time.Time) time.Time {
	parsed, err := session.parseAgentScreen()
	if err != nil {
		return time.Time{}
	}
	lastOutput := session.lastOutput
	if lastOutput.IsZero() {
		lastOutput = startedAt
	}
	changed, wake := session.agent.Observe(time.Now(), parsed, lastOutput)
	if changed {
		session.notifyAgent(session.agent.Status())
	}
	return wake
}

// notifyAgent turns one agent state change into at most one push event:
// waiting once per question id within a busy period (a prompt that flickers
// away and back does not buzz twice), and done when idle ends a busy period
// of at least agentDoneAfter. Scans run only while the session is running,
// so an exited or lost session never notifies. The hook must not block.
func (session *Session) notifyAgent(status agentstate.Status) {
	if session.notify == nil {
		return
	}
	switch status.State {
	case agentstate.Waiting:
		if status.QuestionID != session.notifiedID {
			session.notifiedID = status.QuestionID
			session.notify(webpush.Event{SessionID: session.id, Kind: webpush.KindWaiting})
		}
	case agentstate.Idle:
		session.notifiedID = ""
		if status.Worked >= agentDoneAfter {
			session.notify(webpush.Event{SessionID: session.id, Kind: webpush.KindDone, Duration: status.Worked})
		}
	}
}

// currentAnswer returns the option for an answer only when the published
// status and a fresh parse of the screen both show the same question.
func (session *Session) currentAnswer(action InputAction) (agentstate.Option, error) {
	status := session.agent.Status()
	if status.State != agentstate.Waiting || status.QuestionID != action.QuestionID {
		return agentstate.Option{}, ErrQuestionChanged
	}
	parsed, err := session.parseAgentScreen()
	if err != nil {
		return agentstate.Option{}, err
	}
	if parsed.Question == nil || parsed.Question.ID(session.generation) != action.QuestionID {
		return agentstate.Option{}, ErrQuestionChanged
	}
	if action.OptionIndex < 0 || action.OptionIndex >= len(parsed.Question.Options) {
		return agentstate.Option{}, errAnswerOption
	}
	return parsed.Question.Options[action.OptionIndex], nil
}

func (session *Session) checkAnswer(action InputAction) error {
	_, err := session.currentAnswer(action)
	return err
}

// executeAnswer re-checks the question immediately before writing, because
// other queued input or output may have changed the screen since admission.
// A changed question drops the answer: sending it could approve a different
// prompt. The keys come from the fresh parse, never from the client.
func (session *Session) executeAnswer(action InputAction) error {
	option, err := session.currentAnswer(action)
	if errors.Is(err, ErrQuestionChanged) || errors.Is(err, errAnswerOption) {
		session.agentDropped = true
		return nil
	}
	if err != nil {
		return err
	}
	if option.Input.Text != "" {
		if err := session.terminal.SendText(session.ctx, option.Input.Text); err != nil {
			return err
		}
	}
	if option.Input.Escape {
		if err := session.terminal.SendKey(session.ctx, uv.KeyPressEvent(uv.Key{Code: uv.KeyEscape})); err != nil {
			return err
		}
	}
	if option.Input.Enter {
		return session.terminal.SendKey(session.ctx, uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
	}
	return nil
}

// agentPayload converts a status for the wire; nil before the first scan.
func agentPayload(status agentstate.Status) *protocol.AgentStatus {
	if status.State == "" {
		return nil
	}
	payload := &protocol.AgentStatus{
		State: protocol.AgentState(status.State), StateSince: status.Since.UTC().Format(millisecondRFC3339),
	}
	// Computed when sent: a growing total alone never triggers a notice.
	worked := int64(status.WorkedFor(time.Now()) / time.Second)
	payload.WorkedSeconds = &worked
	if status.Worked > 0 {
		afterWork := true
		payload.AfterWork = &afterWork
	}
	if status.Question != nil {
		question := &protocol.AgentQuestion{ID: status.QuestionID, Text: status.Question.Text}
		if detail := status.Question.Detail; detail != "" {
			question.Detail = &detail
		}
		for _, option := range status.Question.Options {
			question.Options = append(question.Options, protocol.AgentOption{Label: option.Label})
		}
		payload.Question = question
	}
	return payload
}

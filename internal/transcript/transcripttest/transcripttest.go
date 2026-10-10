// Package transcripttest writes synthetic Claude Code transcripts for tests
// and the fake agent (scripts/fakeagent). The shapes follow the public
// descriptions in docs/chat-view.md, section 3; no real transcript was read.
// Every folder it writes must be a temp folder in tests (transcript's guard).
package transcripttest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/transcript"
)

// Version is the agent version the fixtures claim.
const Version = "2.0.14"

// Session is one transcript being written, line by line.
type Session struct {
	Root    string // the agent folder (a temp .claude)
	ID      string
	Cwd     string
	Path    string
	Version string
	Step    time.Duration // added to the clock before each line
	Now     time.Time
	last    string
	file    *os.File
}

// New creates projects/<folder>/<id>.jsonl under root for a session in cwd.
func New(root, cwd string) (*Session, error) {
	session := &Session{Root: root, ID: UUID(), Cwd: cwd, Version: Version, Step: time.Second,
		Now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	folder := filepath.Join(root, "projects", transcript.ProjectFolder(cwd))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, err
	}
	session.Path = filepath.Join(folder, session.ID+".jsonl")
	file, err := os.OpenFile(session.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	session.file = file
	return session, nil
}

// Close closes the file.
func (session *Session) Close() error { return session.file.Close() }

// PIDFile writes sessions/<pid>.json as Claude Code does at start.
func (session *Session) PIDFile(pid int, started time.Time) error {
	folder := filepath.Join(session.Root, "sessions")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{
		"pid": pid, "sessionId": session.ID, "cwd": session.Cwd, "startedAt": started.UnixMilli(),
	})
	return os.WriteFile(filepath.Join(folder, strconv.Itoa(pid)+".json"), data, 0o600)
}

// Raw appends one line as is (no line feed is added when partial is true).
func (session *Session) Raw(data []byte) error {
	_, err := session.file.Write(data)
	return err
}

// Line appends a full line with the common fields; fields override them.
// It returns the line's uuid.
func (session *Session) Line(kind string, fields map[string]any) string {
	session.Now = session.Now.Add(session.Step)
	id := UUID()
	line := map[string]any{
		"parentUuid": nil, "isSidechain": false, "userType": "external", "cwd": session.Cwd,
		"sessionId": session.ID, "version": session.Version, "gitBranch": "main", "type": kind,
		"uuid": id, "timestamp": session.Now.Format("2006-01-02T15:04:05.000Z"),
	}
	if session.last != "" {
		line["parentUuid"] = session.last
	}
	for key, value := range fields {
		line[key] = value
	}
	data, err := json.Marshal(line)
	if err != nil {
		panic(err)
	}
	if _, err := session.file.Write(append(data, '\n')); err != nil {
		panic(err)
	}
	if uuid, ok := line["uuid"].(string); ok && uuid != "" {
		session.last, id = uuid, uuid
	}
	return id
}

// SetParent makes the next line's parent this uuid (a rewind).
func (session *Session) SetParent(uuid string) { session.last = uuid }

// Prompt writes a user prompt.
func (session *Session) Prompt(text string) string {
	return session.Line("user", map[string]any{"message": map[string]any{"role": "user", "content": text}})
}

// Assistant writes one assistant line of message msgID with these content
// blocks (see Text, Thinking, ToolUse).
func (session *Session) Assistant(msgID string, content ...map[string]any) string {
	return session.Line("assistant", map[string]any{
		"requestId": "req_" + msgID,
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": "claude-sonnet-4-5",
			"content": content, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 20},
		},
	})
}

// Result writes a user line with one tool result.
func (session *Session) Result(toolID string, content any, isError bool) string {
	return session.Line("user", map[string]any{
		"message": map[string]any{"role": "user", "content": []any{map[string]any{
			"tool_use_id": toolID, "type": "tool_result", "content": content, "is_error": isError,
		}}},
		"toolUseResult": map[string]any{"stdout": content, "stderr": "", "interrupted": false},
	})
}

// Text is an assistant text block.
func Text(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

// Thinking is an assistant thinking block.
func Thinking(text string) map[string]any {
	return map[string]any{"type": "thinking", "thinking": text, "signature": "sig"}
}

// ToolUse is an assistant tool call.
func ToolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

// ToolID is a fresh tool call ID.
func ToolID() string { return "toolu_" + hex.EncodeToString(random(12)) }

// MessageID is a fresh message ID.
func MessageID() string { return "msg_" + hex.EncodeToString(random(12)) }

// UUID is a fresh lowercase random UUID.
func UUID() string {
	raw := random(16)
	raw[6], raw[8] = raw[6]&0x0f|0x40, raw[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func random(size int) []byte {
	raw := make([]byte, size)
	_, _ = rand.Read(raw)
	return raw
}

// Turn writes one typical turn: a prompt, thinking, a Bash call and its
// result, an Edit and its result, and a closing text.
func (session *Session) Turn(prompt string) {
	session.Prompt(prompt)
	message := MessageID()
	session.Assistant(message, Thinking("Let me look at the tests first."))
	bash := ToolID()
	session.Assistant(message, ToolUse(bash, "Bash", map[string]any{"command": "go test ./...", "description": "Run the tests"}))
	session.Result(bash, "ok  \texample.com/app\t0.412s\n", false)
	edit := ToolID()
	second := MessageID()
	session.Assistant(second, ToolUse(edit, "Edit", map[string]any{
		"file_path": filepath.Join(session.Cwd, "app.go"), "old_string": "return 1\n", "new_string": "return 2\n",
	}))
	session.Result(edit, "The file app.go has been updated.", false)
	session.Assistant(second, Text("I changed **app.go** so the tests pass."))
}

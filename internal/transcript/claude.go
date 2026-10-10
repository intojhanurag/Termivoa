package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Claude Code transcript lines (synthetic fixtures from public docs; see
// docs/chat-view.md, section 3). Each line is one JSON object with type,
// uuid, parentUuid, timestamp, sessionId, cwd, version, isSidechain. One API
// turn is several assistant lines sharing message.id; tool results are user
// lines with tool_result blocks keyed by tool_use_id.

type lineKind uint8

const (
	kindIgnored lineKind = iota // known, shows nothing (summary, snapshots, meta, commands, sidechain)
	kindPrompt                  // a user prompt: opens a turn
	kindAssistant
	kindResults // tool results
	kindCompact // the compaction summary (its text is hidden)
	kindStatus  // a status line (for example an interrupt)
	kindBig     // a line over maxLine, read by the stream scanner
	kindBad     // not JSON, or a user or assistant line without its required fields
	kindUnknown // a type this parser does not know
)

// entry is what one line contributes.
type entry struct {
	off       int64
	kind      lineKind
	uuid      string
	parent    string
	msgID     string
	at        time.Time
	version   string
	thought   bool
	blocks    []protocol.ChatBlock
	results   []toolResult
	sessionID string
	cwd       string
	sidechain bool
}

type toolResult struct {
	id     string
	status string
	result *protocol.ChatToolResult
}

type rawMessage struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
}

type rawLine struct {
	Type              string      `json:"type"`
	Subtype           string      `json:"subtype"`
	UUID              string      `json:"uuid"`
	ParentUUID        *string     `json:"parentUuid"`
	LogicalParentUUID *string     `json:"logicalParentUuid"`
	Timestamp         string      `json:"timestamp"`
	SessionID         string      `json:"sessionId"`
	Cwd               string      `json:"cwd"`
	Version           string      `json:"version"`
	IsSidechain       bool        `json:"isSidechain"`
	IsMeta            bool        `json:"isMeta"`
	IsCompactSummary  bool        `json:"isCompactSummary"`
	Message           *rawMessage `json:"message"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type toolInput struct {
	Command      string `json:"command"`
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Content      string `json:"content"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
	NewSource    string `json:"new_source"`
	Edits        []struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	} `json:"edits"`
	Todos []json.RawMessage `json:"todos"`
}

// knownTypes are line types that carry nothing to show; they count as
// understood for the drift check.
var knownTypes = map[string]bool{
	"summary": true, "file-history-snapshot": true, "attachment": true, "progress": true,
	"queue-operation": true, "system": true, "custom-title": true, "tag": true,
}

// markupPrefixes start user text that the agent or its hooks wrote, not the
// user: slash commands, local command output, shell-mode lines, hook output.
var markupPrefixes = []string{
	"<command-name>", "<command-message>", "<command-args>", "<local-command-stdout>",
	"<local-command-stderr>", "<local-command-caveat>", "<bash-input>", "<bash-stdout>",
	"<bash-stderr>", "<user-prompt-submit-hook>", "<system-reminder>", "<task-notification>",
}

var toolIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,60}$`)

const interruptPrefix = "[Request interrupted"

// parseClaudeLine reads one complete line (without its line feed). It never
// panics; anything it cannot read is kindBad or kindUnknown.
func parseClaudeLine(data []byte, off int64, cwd string) entry {
	result := entry{off: off}
	var line rawLine
	if err := json.Unmarshal(data, &line); err != nil {
		result.kind = kindBad
		return result
	}
	result.uuid, result.version = line.UUID, line.Version
	result.sessionID, result.cwd, result.sidechain = line.SessionID, line.Cwd, line.IsSidechain
	if line.ParentUUID != nil {
		result.parent = *line.ParentUUID
	} else if line.LogicalParentUUID != nil {
		result.parent = *line.LogicalParentUUID
	}
	if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
		result.at = at.UTC()
	}
	switch line.Type {
	case "user", "assistant":
		if line.Message == nil || line.UUID == "" || line.Timestamp == "" {
			result.kind = kindBad
			return result
		}
	default:
		if knownTypes[line.Type] {
			result.kind = kindIgnored
		} else {
			result.kind = kindUnknown
		}
		return result
	}
	if line.IsSidechain || line.IsMeta {
		result.kind = kindIgnored
		return result
	}
	if line.Type == "assistant" {
		result.msgID = line.Message.ID
		parseAssistant(&result, line.Message.Content, cwd)
		return result
	}
	if line.IsCompactSummary {
		result.kind = kindCompact
		result.blocks = []protocol.ChatBlock{{ID: blockID('c', off, 0), Kind: protocol.Compaction, Text: ptr("Conversation compacted")}}
		return result
	}
	parseUser(&result, line.Message.Content)
	return result
}

func parseUser(result *entry, content json.RawMessage) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		userText(result, []string{text}, 0)
		return
	}
	var blocks []rawBlock
	if json.Unmarshal(content, &blocks) != nil {
		result.kind = kindBad
		return
	}
	var texts []string
	images, hasResults := 0, false
	for _, block := range blocks {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "image", "document":
			images++
		case "tool_result":
			hasResults = true
			// An ID the agent never issues can name no call (review S-5).
			if toolIDPattern.MatchString(block.ToolUseID) {
				result.results = append(result.results, readResult(block))
			}
		}
	}
	if hasResults {
		result.kind = kindResults
		for _, text := range texts {
			if strings.HasPrefix(text, interruptPrefix) {
				result.blocks = append(result.blocks, statusBlock(result.off, len(result.blocks), "Interrupted"))
			}
		}
		return
	}
	userText(result, texts, images)
}

func userText(result *entry, texts []string, images int) {
	var kept []string
	for _, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || hasMarkup(trimmed) {
			continue
		}
		if strings.HasPrefix(trimmed, interruptPrefix) {
			result.kind = kindStatus
			result.blocks = []protocol.ChatBlock{statusBlock(result.off, 0, "Interrupted")}
			return
		}
		kept = append(kept, text)
	}
	if len(kept) == 0 && images == 0 {
		result.kind = kindIgnored
		return
	}
	result.kind = kindPrompt
	id := blockID('u', result.off, 0)
	block := protocol.ChatBlock{ID: id, Turn: ptr(id), Kind: protocol.UserText}
	setText(&block, strings.Join(kept, "\n\n"))
	result.blocks = append(result.blocks, block)
	for index := range images {
		result.blocks = append(result.blocks, protocol.ChatBlock{
			ID: blockID('o', result.off, index+1), Kind: protocol.Omitted, Text: ptr("Image not shown"),
		})
	}
}

func hasMarkup(text string) bool {
	for _, prefix := range markupPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func parseAssistant(result *entry, content json.RawMessage, cwd string) {
	var blocks []rawBlock
	if json.Unmarshal(content, &blocks) != nil {
		var text string
		if json.Unmarshal(content, &text) != nil {
			result.kind = kindBad
			return
		}
		blocks = []rawBlock{{Type: "text", Text: text}}
	}
	result.kind = kindAssistant
	for index, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			chat := protocol.ChatBlock{ID: blockID('a', result.off, index), Kind: protocol.AssistantText}
			setText(&chat, block.Text)
			result.blocks = append(result.blocks, chat)
		case "thinking", "redacted_thinking":
			result.thought = true // never sent; only its duration may be
		case "tool_use":
			result.blocks = append(result.blocks, toolBlock(block, result.off, index, cwd))
		case "image":
			result.blocks = append(result.blocks, protocol.ChatBlock{
				ID: blockID('o', result.off, index), Kind: protocol.Omitted, Text: ptr("Image not shown"),
			})
		}
	}
}

func toolBlock(block rawBlock, off int64, index int, cwd string) protocol.ChatBlock {
	name := strings.TrimSpace(cutBytes(clean(block.Name, false), protocol.MaxChatToolNameBytes))
	if name == "" {
		name = "tool"
	}
	chat := protocol.ChatBlock{ID: toolID(block.ID, off, index), Kind: protocol.ToolCall,
		Tool: &protocol.ChatTool{Name: name, Status: protocol.ChatToolRunning}}
	var input toolInput
	_ = json.Unmarshal(block.Input, &input) // unknown shapes give an empty summary
	summary := ""
	switch block.Name {
	case "Bash":
		summary = line(input.Command)
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "NotebookRead":
		summary = relative(cwd, firstOf(input.FilePath, input.NotebookPath, input.Path))
	case "Grep", "Glob":
		summary = line(input.Pattern)
	case "LS":
		summary = relative(cwd, input.Path)
	case "WebFetch":
		summary = line(input.URL)
	case "WebSearch":
		summary = line(input.Query)
	case "Task", "Agent":
		summary = line(input.Description)
	case "TodoWrite":
		summary = fmt.Sprintf("%d items", len(input.Todos))
	}
	if summary != "" {
		chat.Tool.Summary = ptr(summary)
	}
	var old, new string
	switch block.Name {
	case "Edit":
		old, new = input.OldString, input.NewString
	case "MultiEdit":
		var olds, news []string
		for _, edit := range input.Edits {
			olds, news = append(olds, edit.OldString), append(news, edit.NewString)
		}
		old, new = strings.Join(olds, "\n"), strings.Join(news, "\n")
	case "Write":
		new = input.Content
	case "NotebookEdit":
		new = input.NewSource
	default:
		return chat
	}
	path := relative(cwd, firstOf(input.FilePath, input.NotebookPath))
	if path == "" {
		return chat
	}
	chat.Kind = protocol.Edit
	edit := &protocol.ChatEdit{Path: path, Added: countLines(new), Removed: countLines(old)}
	if text := hunk(old, new); text != "" {
		edit.Hunk = ptr(text)
	}
	chat.Edit = edit
	return chat
}

func readResult(block rawBlock) toolResult {
	result := toolResult{id: block.ToolUseID, status: protocol.ChatToolOK}
	if block.IsError {
		result.status = protocol.ChatToolError
	}
	var text string
	if json.Unmarshal(block.Content, &text) != nil {
		var parts []rawBlock
		_ = json.Unmarshal(block.Content, &parts)
		var texts []string
		for _, part := range parts {
			switch part.Type {
			case "text":
				texts = append(texts, part.Text)
			case "image":
				texts = append(texts, "[image not shown]")
			}
		}
		text = strings.Join(texts, "\n")
	}
	preview, cut := shown(text, protocol.MaxChatPreviewBytes, protocol.MaxChatPreviewLines, true)
	result.result = &protocol.ChatToolResult{Preview: preview, Bytes: int64(len(text)), Truncated: cut > 0}
	if result.result.Bytes < int64(len(preview)) {
		result.result.Bytes = int64(len(preview))
	}
	return result
}

func setText(block *protocol.ChatBlock, raw string) {
	text, cut := shown(raw, protocol.MaxChatTextBytes, 0, true)
	block.Text = &text
	if cut > 0 {
		block.TruncatedBytes = ptr(int64(cut))
	}
}

func statusBlock(off int64, index int, text string) protocol.ChatBlock {
	return protocol.ChatBlock{ID: blockID('s', off, index), Kind: protocol.Status, Text: ptr(text)}
}

// blockID is stable for a line: a letter, the line's offset and the block's
// index in it, so reconnects and repeated pages give the same IDs.
func blockID(kind byte, off int64, index int) string {
	return string(kind) + strconv.FormatInt(off, 36) + "." + strconv.Itoa(index)
}

// toolID keys a call by the agent's tool ID, so its result can update it.
func toolID(id string, off int64, index int) string {
	if toolIDPattern.MatchString(id) {
		return "t:" + id
	}
	return blockID('t', off, index)
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func ptr[T any](value T) *T { return &value }

// isUserLine is a cheap check before a full parse in the reverse scan.
func isUserLine(data []byte) bool { return bytes.Contains(data, []byte(`"type":"user"`)) }

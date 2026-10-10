package protocol

import (
	"strings"
	"testing"
)

func TestChatMessagesKeepTheirBounds(t *testing.T) {
	cursor, history := strings.Repeat("A", 22), "0123456789abcdef"
	for kind, payload := range map[MessageType]any{
		ChatOpen:  ChatOpenPayload{Limit: MaxChatTurns},
		ChatRead:  ChatReadPayload{After: &cursor, Limit: 1},
		ChatClose: EmptyPayload{},
	} {
		if _, err := DecodeClient(encodeTestMessage(t, kind, payload, true)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if _, err := DecodeClient(encodeTestMessage(t, kind, payload, false)); err == nil {
			t.Fatalf("%s without session identity was accepted", kind)
		}
	}
	bad := "short"
	for name, test := range map[string]struct {
		kind    MessageType
		payload any
	}{
		"limit 0":        {ChatOpen, ChatOpenPayload{Limit: 0}},
		"limit 61":       {ChatOpen, ChatOpenPayload{Limit: 61}},
		"no cursor":      {ChatRead, ChatReadPayload{Limit: 1}},
		"both cursors":   {ChatRead, ChatReadPayload{Before: &cursor, After: &cursor, Limit: 1}},
		"bad cursor":     {ChatRead, ChatReadPayload{Before: &bad, Limit: 1}},
		"host chat page": {ChatPage, ChatPagePayload{HistoryID: history, After: cursor, Blocks: []ChatBlock{}}},
	} {
		if _, err := DecodeClient(encodeTestMessage(t, test.kind, test.payload, true)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}

	text, turn, at, summary, hunk := "Fixed it.\n\tDone", "u:1", "2026-10-10T12:00:00.5Z", "go test ./...", "-a\n+b"
	none, linked, agent, reason := ChatLinkNone, ChatLinkLinked, Claude, ChatReasonCleared
	for kind, payload := range map[MessageType]any{
		ChatState:   ChatStatePayload{Link: linked, Agent: &agent, HistoryID: &history},
		ChatChanged: ChatChangedPayload{HistoryID: history},
		ChatPage: ChatPagePayload{HistoryID: history, After: cursor, Before: &cursor, Complete: true, Blocks: []ChatBlock{
			{ID: "u:1", Kind: UserText, Text: &text, At: &at},
			{ID: "t:toolu_1", Turn: &turn, Kind: ToolCall, Tool: &ChatTool{Name: "Bash", Summary: &summary, Status: ChatToolOK,
				Result: &ChatToolResult{Preview: "ok", Bytes: 9000, Truncated: true}}},
			{ID: "t:toolu_2", Turn: &turn, Kind: Edit, Tool: &ChatTool{Name: "Edit", Status: ChatToolRunning},
				Edit: &ChatEdit{Path: "src/a.go", Added: 1, Removed: 1, Hunk: &hunk}},
		}},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, kind, payload, true)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if _, err := DecodeHost(encodeTestMessage(t, ChatState, ChatStatePayload{Link: none, Reason: &reason}, true)); err != nil {
		t.Fatal(err)
	}

	bidi, control, long := "a‮b", "a\x1bb", strings.Repeat("a", MaxChatTextBytes+1)
	lines := strings.Repeat("x\n", MaxChatPreviewLines)
	page := func(blocks ...ChatBlock) ChatPagePayload {
		return ChatPagePayload{HistoryID: history, After: cursor, Blocks: blocks}
	}
	for name, test := range map[string]struct {
		kind    MessageType
		payload any
	}{
		"reason without none":  {ChatState, ChatStatePayload{Link: linked, Reason: &reason, Agent: &agent, HistoryID: &history}},
		"none without reason":  {ChatState, ChatStatePayload{Link: none}},
		"linked without id":    {ChatState, ChatStatePayload{Link: linked}},
		"unknown link":         {ChatState, ChatStatePayload{Link: "probable"}},
		"bidi text":            {ChatPage, page(ChatBlock{ID: "a", Kind: UserText, Text: &bidi})},
		"control text":         {ChatPage, page(ChatBlock{ID: "a", Kind: AssistantText, Text: &control})},
		"long text":            {ChatPage, page(ChatBlock{ID: "a", Kind: AssistantText, Text: &long})},
		"duplicate ids":        {ChatPage, page(ChatBlock{ID: "a", Kind: UserText, Text: &text}, ChatBlock{ID: "a", Kind: UserText, Text: &text})},
		"tool without tool":    {ChatPage, page(ChatBlock{ID: "a", Kind: ToolCall})},
		"text on a tool":       {ChatPage, page(ChatBlock{ID: "a", Kind: ToolCall, Text: &text, Tool: &ChatTool{Name: "Bash", Status: ChatToolOK}})},
		"bad status":           {ChatPage, page(ChatBlock{ID: "a", Kind: ToolCall, Tool: &ChatTool{Name: "Bash", Status: "done"}})},
		"preview of 41 lines":  {ChatPage, page(ChatBlock{ID: "a", Kind: ToolCall, Tool: &ChatTool{Name: "Bash", Status: ChatToolOK, Result: &ChatToolResult{Preview: lines, Bytes: 100, Truncated: true}}})},
		"edit without edit":    {ChatPage, page(ChatBlock{ID: "a", Kind: Edit, Tool: &ChatTool{Name: "Edit", Status: ChatToolOK}})},
		"bad time":             {ChatPage, page(ChatBlock{ID: "a", Kind: UserText, Text: &text, At: &bad})},
		"bad id":               {ChatPage, page(ChatBlock{ID: "a b", Kind: UserText, Text: &text})},
		"bad history":          {ChatChanged, ChatChangedPayload{HistoryID: "ABC"}},
		"client open as host":  {ChatOpen, ChatOpenPayload{Limit: 1}},
		"page over 256 KiB":    {ChatPage, page(bigBlocks(t, 20)...)},
		"truncated without tx": {ChatPage, page(ChatBlock{ID: "a", Kind: Omitted, TruncatedBytes: new(int64)})},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, test.kind, test.payload, true)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func bigBlocks(t *testing.T, count int) []ChatBlock {
	t.Helper()
	text := strings.Repeat("é", MaxChatTextBytes/2)
	blocks := make([]ChatBlock, count)
	for index := range blocks {
		blocks[index] = ChatBlock{ID: "b" + strings.Repeat("x", index+1), Kind: AssistantText, Text: &text}
	}
	return blocks
}

func TestChatRPCPayloads(t *testing.T) {
	if err := ValidateChatStatus(AdminRPCChatStatusPayload{Versions: []string{"2.1.0"}, Tails: 8, UnknownPercent: 100}); err != nil {
		t.Fatal(err)
	}
	for name, status := range map[string]AdminRPCChatStatusPayload{
		"nil versions": {},
		"9 tails":      {Versions: []string{}, Tails: 9},
		"bad version":  {Versions: []string{"1 0"}},
		"percent":      {Versions: []string{}, UnknownPercent: 101},
	} {
		if ValidateChatStatus(status) == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

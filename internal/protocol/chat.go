package protocol

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Chat view bounds (revision 8, docs/chat-view.md): chat.open, chat.read,
// chat.close from the client; chat.state, chat.changed, chat.page from the host.
const (
	MaxChatTurns          = 60
	MaxChatPageBlocks     = 200
	MaxChatPageBytes      = 256 << 10 // the whole chat.page message
	MaxChatTextBytes      = 16 << 10
	MaxChatPreviewBytes   = 2 << 10
	MaxChatPreviewLines   = 40
	MaxChatHunkBytes      = 8 << 10
	MaxChatHunkLines      = 40
	MaxChatSummaryRunes   = 240
	MaxChatToolNameBytes  = 64
	MaxChatEditPathRunes  = 512
	maxChatTimestampBytes = 40
)

// Chat link and reason words. They are schema patterns, not enums, so the
// generated names of older enums with the same words stay stable.
const (
	ChatLinkOff         = "off"
	ChatLinkUnsupported = "unsupported"
	ChatLinkSearching   = "searching"
	ChatLinkLinked      = "linked"
	ChatLinkNone        = "none"
	ChatLinkEnded       = "ended"

	ChatReasonCleared       = "cleared"
	ChatReasonSeveralAgents = "several_agents"
	ChatReasonCustomRoot    = "custom_root"
	ChatReasonNoFile        = "no_file"
	ChatReasonFormatUnknown = "format_unknown"
	ChatReasonFileRefused   = "file_refused"
	ChatReasonBusy          = "busy"

	ChatToolRunning = "running"
	ChatToolOK      = "ok"
	ChatToolError   = "error"
	ChatToolUnknown = "unknown"
)

var (
	chatCursorPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	chatHistoryIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
	chatBlockIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	chatVersionPattern   = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)
)

// ValidChatCursor reports whether value has the shape of a host cursor.
func ValidChatCursor(value string) bool { return chatCursorPattern.MatchString(value) }

// ValidChatHistoryID reports whether value has the shape of a history ID.
func ValidChatHistoryID(value string) bool { return chatHistoryIDPattern.MatchString(value) }

// ValidChatVersion reports whether value is a version word pterm doctor may show.
func ValidChatVersion(value string) bool { return chatVersionPattern.MatchString(value) }

// HiddenFormatRune reports the bidi controls and zero-width characters chat
// text never carries (U+200B–200F, U+202A–202E, U+2066–2069, U+FEFF): they
// can make text or a path read differently from what it is.
func HiddenFormatRune(r rune) bool {
	return r >= 0x200b && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0xfeff
}

// validChatText accepts valid UTF-8 of at most maxBytes without hidden
// format characters and without control characters, except tab and line
// feed when multiline.
func validChatText(text string, maxBytes int, multiline bool) bool {
	if len(text) > maxBytes || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) || HiddenFormatRune(r) {
			return false
		}
	}
	return true
}

func chatLines(text string) int { return strings.Count(text, "\n") + 1 }

func validateChatOpen(payload ChatOpenPayload) error {
	if payload.Limit < 1 || payload.Limit > MaxChatTurns {
		return errors.New("chat limit is outside 1..60")
	}
	return nil
}

func validateChatRead(payload ChatReadPayload) error {
	if payload.Limit < 1 || payload.Limit > MaxChatTurns {
		return errors.New("chat limit is outside 1..60")
	}
	if (payload.Before == nil) == (payload.After == nil) {
		return errors.New("chat.read needs exactly one of before and after")
	}
	for _, cursor := range []*string{payload.Before, payload.After} {
		if cursor != nil && !ValidChatCursor(*cursor) {
			return errors.New("chat cursor is invalid")
		}
	}
	return nil
}

func validateChatState(payload ChatStatePayload) error {
	switch payload.Link {
	case ChatLinkOff, ChatLinkUnsupported, ChatLinkSearching, ChatLinkLinked, ChatLinkNone, ChatLinkEnded:
	default:
		return errors.New("chat link is invalid")
	}
	if (payload.Reason != nil) != (payload.Link == ChatLinkNone) {
		return errors.New("chat reason belongs to link none only")
	}
	if payload.Reason != nil {
		switch *payload.Reason {
		case ChatReasonCleared, ChatReasonSeveralAgents, ChatReasonCustomRoot, ChatReasonNoFile,
			ChatReasonFormatUnknown, ChatReasonFileRefused, ChatReasonBusy:
		default:
			return errors.New("chat reason is invalid")
		}
	}
	history := payload.Link == ChatLinkLinked || payload.Link == ChatLinkEnded
	if (payload.HistoryID != nil) != history || (payload.Agent != nil) != history || payload.Stale != nil && !history {
		return errors.New("agent, historyId and stale belong to linked and ended only")
	}
	if payload.HistoryID != nil && !ValidChatHistoryID(*payload.HistoryID) {
		return errors.New("chat historyId is invalid")
	}
	if payload.Agent != nil && *payload.Agent != Claude {
		return errors.New("chat agent is invalid")
	}
	return nil
}

func validateChatChanged(payload ChatChangedPayload) error {
	if !ValidChatHistoryID(payload.HistoryID) {
		return errors.New("chat historyId is invalid")
	}
	return nil
}

func validateChatPage(payload ChatPagePayload) error {
	if !ValidChatHistoryID(payload.HistoryID) || !ValidChatCursor(payload.After) ||
		payload.Before != nil && !ValidChatCursor(*payload.Before) {
		return errors.New("chat page identity is invalid")
	}
	if payload.Blocks == nil || len(payload.Blocks) > MaxChatPageBlocks {
		return errors.New("chat blocks are outside bounds")
	}
	seen := make(map[string]bool, len(payload.Blocks))
	for _, block := range payload.Blocks {
		if seen[block.ID] {
			return errors.New("chat block ids must be unique in a page")
		}
		seen[block.ID] = true
		if err := ValidateChatBlock(block); err != nil {
			return err
		}
	}
	return nil
}

// ValidateChatBlock checks one block against the schema and the kind rules:
// tool for tool_call and edit, edit for edit only, text for the others.
func ValidateChatBlock(block ChatBlock) error {
	if !chatBlockIDPattern.MatchString(block.ID) || block.Turn != nil && !chatBlockIDPattern.MatchString(*block.Turn) {
		return errors.New("chat block id or turn is invalid")
	}
	if block.At != nil {
		if len(*block.At) > maxChatTimestampBytes {
			return errors.New("chat block time is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, *block.At); err != nil {
			return errors.New("chat block time is invalid")
		}
	}
	toolKind := block.Kind == ToolCall || block.Kind == Edit
	switch block.Kind {
	case UserText, AssistantText, ToolCall, Edit, Status, Compaction, Omitted:
	default:
		return errors.New("chat block kind is invalid")
	}
	if (block.Tool != nil) != toolKind || (block.Edit != nil) != (block.Kind == Edit) ||
		block.Text != nil && toolKind || block.TruncatedBytes != nil && block.Text == nil {
		return errors.New("chat block fields do not match its kind")
	}
	if block.Text != nil && !validChatText(*block.Text, MaxChatTextBytes, true) {
		return errors.New("chat block text is outside bounds")
	}
	if block.TruncatedBytes != nil && *block.TruncatedBytes < 0 {
		return errors.New("chat truncatedBytes must not be negative")
	}
	if tool := block.Tool; tool != nil {
		if tool.Name == "" || !validChatText(tool.Name, MaxChatToolNameBytes, false) {
			return errors.New("chat tool name is outside bounds")
		}
		if tool.Summary != nil && (!validChatText(*tool.Summary, 4*MaxChatSummaryRunes, false) ||
			utf8.RuneCountInString(*tool.Summary) > MaxChatSummaryRunes) {
			return errors.New("chat tool summary is outside bounds")
		}
		switch tool.Status {
		case ChatToolRunning, ChatToolOK, ChatToolError, ChatToolUnknown:
		default:
			return errors.New("chat tool status is invalid")
		}
		if result := tool.Result; result != nil {
			if !validChatText(result.Preview, MaxChatPreviewBytes, true) || chatLines(result.Preview) > MaxChatPreviewLines ||
				result.Bytes < 0 || int64(len(result.Preview)) > result.Bytes && !result.Truncated {
				return errors.New("chat tool result is outside bounds")
			}
		}
	}
	if edit := block.Edit; edit != nil {
		if edit.Path == "" || !validChatText(edit.Path, 4*MaxChatEditPathRunes, false) ||
			utf8.RuneCountInString(edit.Path) > MaxChatEditPathRunes || edit.Added < 0 || edit.Removed < 0 {
			return errors.New("chat edit is outside bounds")
		}
		if edit.Hunk != nil && (!validChatText(*edit.Hunk, MaxChatHunkBytes, true) || chatLines(*edit.Hunk) > MaxChatHunkLines) {
			return errors.New("chat edit hunk is outside bounds")
		}
	}
	return nil
}

// ValidateChatStatus checks admin.chat.status.result and admin.chat.set.result.
func ValidateChatStatus(payload AdminRPCChatStatusPayload) error {
	if payload.Tails < 0 || payload.Tails > 8 || payload.UnknownPercent < 0 || payload.UnknownPercent > 100 ||
		payload.Versions == nil || len(payload.Versions) > 8 {
		return errors.New("chat status is outside bounds")
	}
	for _, version := range payload.Versions {
		if !ValidChatVersion(version) {
			return errors.New("chat version is invalid")
		}
	}
	return nil
}

func validateGatewayChatPayload(envelope GatewayRPCEnvelope) error {
	switch envelope.Type {
	case GatewayChatUpdate:
		payload, err := DecodeRPCPayload[GatewayRPCChatUpdatePayload](envelope)
		if err != nil {
			return err
		}
		if payload.Request.Enabled {
			return errors.New("chat can be turned on only at the computer")
		}
		return ValidateSecretText(payload.Credential)
	case GatewayChatUpdateResult:
		_, err := DecodeRPCPayload[HTTPChatInfo](envelope)
		return err
	default:
		return errors.New("unknown chat message type")
	}
}

func validateAdminChatPayload(envelope AdminRPCEnvelope) error {
	switch envelope.Type {
	case AdminChatStatus:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case AdminChatSet:
		_, err := DecodeRPCPayload[AdminRPCChatSetPayload](envelope)
		return err
	case AdminChatStatusResult, AdminChatSetResult:
		payload, err := DecodeRPCPayload[AdminRPCChatStatusPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateChatStatus(payload)
	default:
		return errors.New("unknown chat message type")
	}
}

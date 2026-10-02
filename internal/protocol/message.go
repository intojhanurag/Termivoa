package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version                 = 1
	MaxInboundMessageBytes  = 256 * 1024
	MaxOutboundMessageBytes = 1024 * 1024
	MaxTextBytes            = 64 * 1024
	MaxPasteBytes           = 64 * 1024
	MinColumns              = 20
	MaxColumns              = 400
	MinRows                 = 5
	MaxRows                 = 200
	MaxHistoryPageRows      = 200
)

// Envelope contains the fields common to every v1 message. Payload remains raw
// until the type discriminator has selected its generated payload type.
type Envelope struct {
	Version    int             `json:"version"`
	Type       MessageType     `json:"type"`
	RequestID  string          `json:"requestId"`
	SessionID  *string         `json:"sessionId,omitempty"`
	Generation *string         `json:"generation,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// DecodeClient validates one client-to-host JSON message, including strict
// object fields and semantic bounds that JSON Schema cannot express in bytes.
func DecodeClient(data []byte) (Envelope, error) {
	return decode(data, true)
}

// DecodeHost validates one host-to-client JSON message.
func DecodeHost(data []byte) (Envelope, error) {
	return decode(data, false)
}

// DecodePayload strictly decodes a previously validated payload into its
// generated type. It remains useful to dispatchers without weakening bounds.
func DecodePayload[T any](envelope Envelope) (T, error) {
	var payload T
	err := strictDecode(envelope.Payload, &payload)
	return payload, err
}

func decode(data []byte, client bool) (Envelope, error) {
	limit := MaxOutboundMessageBytes
	if client {
		limit = MaxInboundMessageBytes
	}
	if len(data) == 0 || len(data) > limit {
		return Envelope{}, fmt.Errorf("protocol: message size %d is outside 1..%d bytes", len(data), limit)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Envelope{}, err
	}

	var envelope Envelope
	if err := strictDecode(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("protocol: decode envelope: %w", err)
	}
	if envelope.Version != Version {
		return Envelope{}, fmt.Errorf("protocol: version %d is not supported", envelope.Version)
	}
	if !ValidUUID(envelope.RequestID) {
		return Envelope{}, errors.New("protocol: requestId must be a canonical UUID")
	}
	if envelope.SessionID != nil && !ValidUUID(*envelope.SessionID) {
		return Envelope{}, errors.New("protocol: sessionId must be a canonical UUID")
	}
	if envelope.Generation != nil && !ValidUUID(*envelope.Generation) {
		return Envelope{}, errors.New("protocol: generation must be a canonical UUID")
	}

	if client {
		if !clientMessageTypes[envelope.Type] {
			return Envelope{}, fmt.Errorf("protocol: %q is not a client message", envelope.Type)
		}
	} else if !hostMessageTypes[envelope.Type] {
		return Envelope{}, fmt.Errorf("protocol: %q is not a host message", envelope.Type)
	}

	if sessionMessageTypes[envelope.Type] {
		if envelope.SessionID == nil || envelope.Generation == nil {
			return Envelope{}, fmt.Errorf("protocol: %q requires sessionId and generation", envelope.Type)
		}
	} else if envelope.Type != Error && (envelope.SessionID != nil || envelope.Generation != nil) {
		return Envelope{}, fmt.Errorf("protocol: %q must not carry session identity", envelope.Type)
	} else if envelope.Type == Error && (envelope.SessionID == nil) != (envelope.Generation == nil) {
		return Envelope{}, errors.New("protocol: error sessionId and generation must appear together")
	}

	if err := validatePayload(envelope); err != nil {
		return Envelope{}, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return envelope, nil
}

var clientMessageTypes = map[MessageType]bool{
	Hello: true, SessionAttach: true, ScreenResync: true, SnapshotApplied: true,
	ControlAcquire: true, ControlRelease: true, Heartbeat: true, InputKey: true,
	InputText: true, InputPaste: true, InputMouse: true, InputAnswer: true, TerminalResize: true,
	HistoryRead: true, ChangesList: true, ChangesFile: true, ChangesCommit: true, ChangesPush: true, TasksList: true,
	BranchesList: true, WorktreeRemove: true, FilesList: true, FilesRead: true, FilesSearch: true, SessionDetach: true,
}

var hostMessageTypes = map[MessageType]bool{
	Welcome: true, SnapshotBegin: true, SnapshotRows: true, SnapshotEnd: true,
	ScreenPatch: true, HistoryPage: true, MessageTypeHistoryChanged: true,
	ChangesSummary: true, ChangesPatch: true, ChangesCommitted: true, ChangesPushed: true, TasksListed: true,
	BranchesListed: true, WorktreeRemoved: true, FilesListed: true, FilesContent: true, FilesFound: true,
	ControlChanged: true, InputAccepted: true, InputRejected: true,
	SessionState: true, AccessRevoked: true, Error: true,
}

var sessionMessageTypes = map[MessageType]bool{
	SessionAttach: true, ScreenResync: true, SnapshotApplied: true,
	ControlAcquire: true, ControlRelease: true, Heartbeat: true, InputKey: true,
	InputText: true, InputPaste: true, InputMouse: true, InputAnswer: true, TerminalResize: true,
	HistoryRead: true, ChangesList: true, ChangesFile: true, ChangesCommit: true, ChangesPush: true, TasksList: true,
	BranchesList: true, WorktreeRemove: true, FilesList: true, FilesRead: true, FilesSearch: true, SessionDetach: true, SnapshotBegin: true,
	SnapshotRows: true, SnapshotEnd: true, ScreenPatch: true, HistoryPage: true,
	ChangesSummary: true, ChangesPatch: true, ChangesCommitted: true, ChangesPushed: true, TasksListed: true,
	BranchesListed: true, WorktreeRemoved: true, FilesListed: true, FilesContent: true, FilesFound: true, MessageTypeHistoryChanged: true, ControlChanged: true, InputAccepted: true,
	InputRejected: true, SessionState: true,
}

func validatePayload(envelope Envelope) error { //nolint:gocyclo
	switch envelope.Type {
	case Hello:
		payload, err := strictPayload[HelloPayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.ClientBuild) == 0 || len(payload.ClientBuild) > 128 ||
			(payload.ClientKind != Browser && payload.ClientKind != Desktop) ||
			payload.CapabilityProfile != PhoneTerminalV1 || len(payload.SupportedVersions) != 1 ||
			payload.SupportedVersions[0] != Version {
			return errors.New("invalid hello negotiation")
		}
	case SessionAttach:
		payload, err := strictPayload[SessionAttachPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateSessionAttach(payload)
	case ControlAcquire, SessionDetach:
		_, err := strictPayload[EmptyPayload](envelope)
		return err
	case ScreenResync:
		payload, err := strictPayload[ScreenResyncPayload](envelope)
		if err != nil {
			return err
		}
		if payload.LastRevision != nil {
			if _, err := parseCounter(*payload.LastRevision); err != nil {
				return fmt.Errorf("lastRevision: %w", err)
			}
		}
		if !validReason(payload.Reason) {
			return errors.New("invalid resync reason")
		}
	case SnapshotApplied:
		payload, err := strictPayload[SnapshotAppliedPayload](envelope)
		if err != nil {
			return err
		}
		return validateSnapshotIdentity(payload.SnapshotID, payload.Revision)
	case ControlRelease:
		payload, err := strictPayload[LeasePayload](envelope)
		if err != nil {
			return err
		}
		return validateSecret(payload.Lease)
	case Heartbeat:
		payload, err := strictPayload[HeartbeatPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Lease != nil {
			return validateSecret(*payload.Lease)
		}
	case InputKey:
		payload, err := strictPayload[InputKeyPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateInputIdentity(payload.Lease, payload.InputSequence); err != nil {
			return err
		}
		return ValidateKeyInput(payload.Key, payload.Character, payload.Modifiers)
	case InputText:
		payload, err := strictPayload[InputTextPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateInputIdentity(payload.Lease, payload.InputSequence); err != nil {
			return err
		}
		return ValidateTextInput(payload.Text)
	case InputPaste:
		payload, err := strictPayload[InputPastePayload](envelope)
		if err != nil {
			return err
		}
		if err := validateInputIdentity(payload.Lease, payload.InputSequence); err != nil {
			return err
		}
		return ValidatePasteInput(payload.Text)
	case InputMouse:
		payload, err := strictPayload[InputMousePayload](envelope)
		if err != nil {
			return err
		}
		if err := validateInputIdentity(payload.Lease, payload.InputSequence); err != nil {
			return err
		}
		return ValidateMouseInput(payload.Kind, payload.Button, payload.Column, payload.Row)
	case InputAnswer:
		payload, err := strictPayload[InputAnswerPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateInputIdentity(payload.Lease, payload.InputSequence); err != nil {
			return err
		}
		if !ValidAgentQuestionID(payload.QuestionID) || payload.OptionIndex < 0 || payload.OptionIndex >= MaxAgentOptions {
			return errors.New("invalid answer")
		}
	case TerminalResize:
		payload, err := strictPayload[TerminalResizePayload](envelope)
		if err != nil {
			return err
		}
		if err := validateSecret(payload.Lease); err != nil {
			return err
		}
		return validateDimensions(payload.Columns, payload.Rows)
	case HistoryRead:
		payload, err := strictPayload[HistoryReadPayload](envelope)
		if err != nil {
			return err
		}
		if _, err := parseCounter(payload.HistoryEpoch); err != nil {
			return fmt.Errorf("historyEpoch: %w", err)
		}
		if _, err := parseCounter(payload.HistoryRevision); err != nil {
			return fmt.Errorf("historyRevision: %w", err)
		}
		if payload.Offset < 0 || payload.Offset > 4999 || payload.Limit < 1 || payload.Limit > MaxHistoryPageRows {
			return errors.New("history page is outside bounds")
		}
	case ChangesList:
		_, err := strictPayload[ChangesListPayload](envelope)
		return err
	case ChangesFile:
		payload, err := strictPayload[ChangesFilePayload](envelope)
		if err != nil {
			return err
		}
		return ValidateChangePath(payload.Path)
	case ChangesCommit:
		payload, err := strictPayload[ChangesCommitPayload](envelope)
		if err != nil {
			return err
		}
		return validateChangesCommit(payload)
	case ChangesPush:
		payload, err := strictPayload[ChangesPushPayload](envelope)
		if err != nil {
			return err
		}
		return validateSecret(payload.Lease)
	case TasksList:
		_, err := strictPayload[TasksListPayload](envelope)
		return err
	case BranchesList:
		_, err := strictPayload[BranchesListPayload](envelope)
		return err
	case WorktreeRemove:
		payload, err := strictPayload[WorktreeRemovePayload](envelope)
		if err != nil {
			return err
		}
		return validateSecret(payload.Lease)
	case Welcome:
		payload, err := strictPayload[WelcomePayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.HostBuild) == 0 || len(payload.HostBuild) > 128 || !ValidUUID(payload.BrokerGeneration) ||
			payload.NegotiatedVersion != Version || payload.CapabilityProfile != PhoneTerminalV1 || payload.Limits != canonicalLimits() {
			return errors.New("invalid welcome negotiation")
		}
	case SnapshotBegin:
		payload, err := strictPayload[SnapshotBeginPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateSnapshotIdentity(payload.SnapshotID, payload.Revision); err != nil {
			return err
		}
		return validateScreen(payload.Screen)
	case SnapshotRows:
		payload, err := strictPayload[SnapshotRowsPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateSnapshotIdentity(payload.SnapshotID, payload.Revision); err != nil {
			return err
		}
		if len(payload.Rows) == 0 || len(payload.Rows) > MaxRows || payload.StartRow < 0 || payload.StartRow+int64(len(payload.Rows)) > MaxRows {
			return errors.New("snapshot row chunk is outside bounds")
		}
		return validateRows(payload.Rows, 0)
	case SnapshotEnd:
		payload, err := strictPayload[SnapshotEndPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateSnapshotIdentity(payload.SnapshotID, payload.Revision); err != nil {
			return err
		}
		if payload.RowCount < MinRows || payload.RowCount > MaxRows {
			return errors.New("rowCount is outside bounds")
		}
	case ScreenPatch:
		payload, err := strictPayload[ScreenPatchPayload](envelope)
		if err != nil {
			return err
		}
		base, err := parseCounter(payload.BaseRevision)
		if err != nil {
			return fmt.Errorf("baseRevision: %w", err)
		}
		revision, err := parseCounter(payload.Revision)
		if err != nil || revision <= base {
			return errors.New("revision must be a decimal counter greater than baseRevision")
		}
		if err := validateScreen(payload.Screen); err != nil {
			return err
		}
		seen := make(map[int64]bool, len(payload.Rows))
		for _, changed := range payload.Rows {
			if changed.Index < 0 || changed.Index >= payload.Screen.Rows || seen[changed.Index] {
				return errors.New("patch row indexes must be unique and inside the screen")
			}
			seen[changed.Index] = true
			if err := validateRow(changed.Row, payload.Screen.Columns); err != nil {
				return err
			}
		}
	case HistoryPage:
		payload, err := strictPayload[HistoryPagePayload](envelope)
		if err != nil {
			return err
		}
		return validateHistoryPage(payload)
	case ChangesSummary:
		payload, err := strictPayload[ChangesSummaryPayload](envelope)
		if err != nil {
			return err
		}
		return validateChangesSummary(payload)
	case ChangesPatch:
		payload, err := strictPayload[ChangesPatchPayload](envelope)
		if err != nil {
			return err
		}
		return validateChangesPatch(payload)
	case ChangesCommitted:
		payload, err := strictPayload[ChangesCommittedPayload](envelope)
		if err != nil {
			return err
		}
		return validateChangesCommitted(payload)
	case ChangesPushed:
		payload, err := strictPayload[ChangesPushedPayload](envelope)
		if err != nil {
			return err
		}
		return validateChangesPushed(payload)
	case TasksListed:
		payload, err := strictPayload[TasksListedPayload](envelope)
		if err != nil {
			return err
		}
		return validateTasksListed(payload)
	case BranchesListed:
		payload, err := strictPayload[BranchesListedPayload](envelope)
		if err != nil {
			return err
		}
		return validateBranchesListed(payload)
	case WorktreeRemoved:
		payload, err := strictPayload[WorktreeRemovedPayload](envelope)
		if err != nil {
			return err
		}
		return validateWorktreeRemoved(payload)
	case FilesList:
		payload, err := strictPayload[FilesListPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesList(payload)
	case FilesRead:
		payload, err := strictPayload[FilesReadPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesRead(payload)
	case FilesSearch:
		payload, err := strictPayload[FilesSearchPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesSearch(payload)
	case FilesListed:
		payload, err := strictPayload[FilesListedPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesListed(payload)
	case FilesContent:
		payload, err := strictPayload[FilesContentPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesContent(payload)
	case FilesFound:
		payload, err := strictPayload[FilesFoundPayload](envelope)
		if err != nil {
			return err
		}
		return validateFilesFound(payload)
	case MessageTypeHistoryChanged:
		payload, err := strictPayload[HistoryChangedPayload](envelope)
		if err != nil {
			return err
		}
		if _, err := parseCounter(payload.HistoryEpoch); err != nil {
			return err
		}
		_, err = parseCounter(payload.HistoryRevision)
		return err
	case ControlChanged:
		payload, err := strictPayload[ControlChangedPayload](envelope)
		if err != nil {
			return err
		}
		if payload.ControllerConnectionID != nil && !ValidUUID(*payload.ControllerConnectionID) {
			return errors.New("controllerConnectionId must be a UUID or null")
		}
		if len(valueOrEmpty(payload.ControllerLabel)) > 128 {
			return errors.New("controllerLabel is too large")
		}
		if _, err := parseCounter(payload.LeaseEpoch); err != nil {
			return err
		}
		if payload.Lease != nil {
			return validateSecret(*payload.Lease)
		}
	case InputAccepted:
		payload, err := strictPayload[InputAcceptedPayload](envelope)
		if err != nil {
			return err
		}
		_, err = parseCounter(payload.InputSequence)
		return err
	case InputRejected:
		payload, err := strictPayload[InputRejectedPayload](envelope)
		if err != nil {
			return err
		}
		if payload.InputSequence != nil {
			if _, err := parseCounter(*payload.InputSequence); err != nil {
				return err
			}
		}
		if !validErrorCode(payload.Code) || len(payload.Message) == 0 || len(payload.Message) > 512 {
			return errors.New("invalid input rejection")
		}
	case SessionState:
		payload, err := strictPayload[SessionStatePayload](envelope)
		if err != nil {
			return err
		}
		if !validState(payload.State) || (payload.ExitCode != nil && payload.State != Exited) {
			return errors.New("invalid session state")
		}
		if (payload.Agent != nil || payload.Previews != nil || payload.Cwd != nil) && payload.State != Running {
			return errors.New("agent status, previews and cwd are only valid while running")
		}
		if err := ValidatePreviewPorts(payload.Previews); err != nil {
			return err
		}
		if err := validateOptionalWorkingDirectory(payload.Cwd); err != nil {
			return err
		}
		if payload.Agent != nil {
			return validateAgentStatus(*payload.Agent)
		}
	case AccessRevoked:
		payload, err := strictPayload[AccessRevokedPayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.Message) == 0 || len(payload.Message) > 512 {
			return errors.New("invalid revocation message")
		}
	case Error:
		payload, err := strictPayload[ErrorPayload](envelope)
		if err != nil {
			return err
		}
		if !validErrorCode(payload.Code) || len(payload.Message) == 0 || len(payload.Message) > 512 {
			return errors.New("invalid error")
		}
	default:
		return errors.New("unknown message type")
	}
	return nil
}

func validateHistoryPage(payload HistoryPagePayload) error {
	if _, err := parseCounter(payload.HistoryEpoch); err != nil {
		return err
	}
	if _, err := parseCounter(payload.HistoryRevision); err != nil {
		return err
	}
	if payload.Columns < MinColumns || payload.Columns > MaxColumns ||
		payload.Offset < 0 || payload.Offset > 4999 || payload.Rows == nil ||
		len(payload.Rows) > MaxHistoryPageRows || payload.Offset+int64(len(payload.Rows)) > 5000 {
		return errors.New("history page is outside bounds")
	}
	expectedNext := payload.Offset + int64(len(payload.Rows))
	if payload.Complete {
		if payload.NextOffset != nil {
			return errors.New("complete history page must not include nextOffset")
		}
	} else if payload.NextOffset == nil || *payload.NextOffset != expectedNext || len(payload.Rows) == 0 {
		return errors.New("incomplete history page must advance nextOffset by its row count")
	}
	return validateRows(payload.Rows, payload.Columns)
}

// Changes view bounds (changes.summary and changes.patch).
const (
	MaxChangedFiles      = 300
	MaxDiffLines         = 2000
	MaxDiffLineRunes     = 1000
	maxChangePathRunes   = 4096
	maxChangeBranchRunes = 256
	MaxCommitPaths       = 300
	MaxCommitMessage     = 2000 // characters
	MaxGitOutput         = 2000 // characters of git output in a failed result
	maxLinkURLRunes      = 2048
)

// ValidateChangePath accepts a repository-relative path: 1..4096 characters
// of valid UTF-8 without NUL, not absolute, and without a ".." segment. Other
// control characters are allowed because git allows them in file names.
func ValidateChangePath(path string) error {
	if path == "" || !utf8.ValidString(path) || utf8.RuneCountInString(path) > maxChangePathRunes || strings.IndexByte(path, 0) >= 0 {
		return errors.New("change path is outside bounds")
	}
	drive := len(path) >= 3 && (path[0]|0x20 >= 'a' && path[0]|0x20 <= 'z') && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
	if path[0] == '/' || path[0] == '\\' || drive {
		return errors.New("change path must be relative")
	}
	for _, segment := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return errors.New("change path must not contain a .. segment")
		}
	}
	return nil
}

// ValidateCommitMessage accepts 1..2000 characters of UTF-8 without NUL.
func ValidateCommitMessage(message string) error {
	if message == "" || !utf8.ValidString(message) || utf8.RuneCountInString(message) > MaxCommitMessage || strings.IndexByte(message, 0) >= 0 {
		return errors.New("commit message must be 1..2000 characters without NUL")
	}
	return nil
}

// ValidateLinkURL accepts an https URL with a host, of at most 2048
// characters, without whitespace or control characters. The phone may open
// it, so nothing else passes.
func ValidateLinkURL(link string) error {
	if !strings.HasPrefix(link, "https://") || !utf8.ValidString(link) || utf8.RuneCountInString(link) > maxLinkURLRunes ||
		strings.IndexFunc(link, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("link must be an https URL without spaces or control characters")
	}
	if parsed, err := url.Parse(link); err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("link must be an https URL with a host")
	}
	return nil
}

func validateChangesCommit(payload ChangesCommitPayload) error {
	if err := validateSecret(payload.Lease); err != nil {
		return err
	}
	if err := ValidateCommitMessage(payload.Message); err != nil {
		return err
	}
	if len(payload.Paths) == 0 || len(payload.Paths) > MaxCommitPaths {
		return errors.New("commit paths are outside bounds")
	}
	for _, path := range payload.Paths {
		if err := ValidateChangePath(path); err != nil {
			return err
		}
	}
	return nil
}

func validGitOutput(output *string) bool {
	return output == nil || utf8.ValidString(*output) && utf8.RuneCountInString(*output) <= MaxGitOutput
}

func validateChangesCommitted(payload ChangesCommittedPayload) error {
	if payload.State != Committed && payload.State != Nothing && payload.State != CommitStateFailed {
		return errors.New("invalid commit state")
	}
	if (payload.Commit != nil) != (payload.State == Committed) {
		return errors.New("commit is present only when committed")
	}
	if payload.Commit != nil {
		hash := *payload.Commit
		if len(hash) < 4 || len(hash) > 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return errors.New("commit hash is invalid")
		}
	}
	if !validGitOutput(payload.Output) {
		return errors.New("commit output is outside bounds")
	}
	return nil
}

func validateChangesPushed(payload ChangesPushedPayload) error {
	if payload.State != Pushed && payload.State != NoRemote && payload.State != PushStateFailed {
		return errors.New("invalid push state")
	}
	if payload.State != Pushed && (payload.Branch != nil || payload.URL != nil) {
		return errors.New("branch and url are present only when pushed")
	}
	if payload.Branch != nil && (*payload.Branch == "" || utf8.RuneCountInString(*payload.Branch) > maxChangeBranchRunes) {
		return errors.New("branch is outside bounds")
	}
	if payload.URL != nil {
		if err := ValidateLinkURL(*payload.URL); err != nil {
			return err
		}
	}
	if !validGitOutput(payload.Output) {
		return errors.New("push output is outside bounds")
	}
	return nil
}

func validateChangesSummary(payload ChangesSummaryPayload) error {
	if payload.State != ChangesStateReady && payload.State != NotGit && payload.State != ChangesStateUnavailable {
		return errors.New("invalid changes state")
	}
	if payload.Files == nil || len(payload.Files) > MaxChangedFiles || (payload.State != ChangesStateReady && len(payload.Files) > 0) {
		return errors.New("changed files are outside bounds")
	}
	if payload.Root != nil && (*payload.Root == "" || utf8.RuneCountInString(*payload.Root) > maxChangePathRunes) {
		return errors.New("root is outside bounds")
	}
	if payload.Branch != nil && (*payload.Branch == "" || utf8.RuneCountInString(*payload.Branch) > maxChangeBranchRunes) {
		return errors.New("branch is outside bounds")
	}
	for _, file := range payload.Files {
		if err := ValidateChangePath(file.Path); err != nil {
			return err
		}
		if file.OldPath != nil {
			if err := ValidateChangePath(*file.OldPath); err != nil {
				return err
			}
		}
		switch file.Status {
		case Added, Modified, Deleted, Renamed, Untracked, Typechange:
		default:
			return errors.New("invalid change status")
		}
		if file.Additions < 0 || file.Deletions < 0 {
			return errors.New("change counts must not be negative")
		}
	}
	return nil
}

func validateChangesPatch(payload ChangesPatchPayload) error {
	if err := ValidateChangePath(payload.Path); err != nil {
		return err
	}
	if payload.State != PatchStateReady && payload.State != PatchStateBinary && payload.State != Missing && payload.State != PatchStateUnavailable {
		return errors.New("invalid patch state")
	}
	if payload.Lines == nil || len(payload.Lines) > MaxDiffLines || (payload.State != PatchStateReady && len(payload.Lines) > 0) {
		return errors.New("diff lines are outside bounds")
	}
	for _, line := range payload.Lines {
		if !utf8.ValidString(line.Text) || utf8.RuneCountInString(line.Text) > MaxDiffLineRunes {
			return errors.New("diff line text is outside bounds")
		}
		// Line numbers follow the kind: a hunk has none, a removal only the
		// old one, an addition only the new one, context both.
		wantOld, wantNew := false, false
		switch line.Kind {
		case Hunk:
		case Remove:
			wantOld = true
		case Add:
			wantNew = true
		case Context:
			wantOld, wantNew = true, true
		default:
			return errors.New("invalid diff line kind")
		}
		if (line.OldLine != nil) != wantOld || (line.NewLine != nil) != wantNew ||
			(line.OldLine != nil && *line.OldLine < 1) || (line.NewLine != nil && *line.NewLine < 1) {
			return errors.New("diff line numbers do not match the line kind")
		}
	}
	return nil
}

// Project task bounds (tasks.listed).
const (
	MaxProjectTasks     = 50
	maxTaskCommandRunes = 512
	maxTaskNameRunes    = 128
	maxTaskFolderRunes  = 4096
)

// ValidTaskName reports whether a script or target name is safe to type as
// one shell word: 1..128 of [A-Za-z0-9:._/@+-], not starting with "-" so it
// never reads as an option. The phone types the command, so nothing else
// passes.
func ValidTaskName(name string) bool {
	if name == "" || len(name) > maxTaskNameRunes || name[0] == '-' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":._/@+-", r)) {
			return false
		}
	}
	return true
}

func validateTasksListed(payload TasksListedPayload) error {
	if payload.State != TasksStateReady && payload.State != TasksStateNone && payload.State != TasksStateUnavailable {
		return errors.New("invalid tasks state")
	}
	if payload.Tasks == nil || len(payload.Tasks) > MaxProjectTasks || (payload.State != TasksStateReady && len(payload.Tasks) > 0) {
		return errors.New("tasks are outside bounds")
	}
	if payload.Folder != nil && (*payload.Folder == "" || !utf8.ValidString(*payload.Folder) || utf8.RuneCountInString(*payload.Folder) > maxTaskFolderRunes) {
		return errors.New("folder is outside bounds")
	}
	for _, task := range payload.Tasks {
		if !ValidTaskName(task.Name) {
			return errors.New("task name is not a safe shell word")
		}
		command := task.Command
		if command == "" || !utf8.ValidString(command) || utf8.RuneCountInString(command) > maxTaskCommandRunes ||
			strings.IndexFunc(command, unicode.IsControl) >= 0 {
			return errors.New("task command must be one line of 1..512 characters")
		}
		switch task.Source {
		case PackageJSON, Makefile, CargoToml, GoMod:
		default:
			return errors.New("invalid task source")
		}
		switch task.Kind {
		case Test, Dev, Build, Lint, TaskKindOther:
		default:
			return errors.New("invalid task kind")
		}
	}
	return nil
}

func strictPayload[T any](envelope Envelope) (T, error) {
	payload, err := DecodePayload[T](envelope)
	if err != nil {
		return payload, fmt.Errorf("decode: %w", err)
	}
	return payload, nil
}

func strictDecode(data []byte, destination any) error {
	// encoding/json replaces malformed UTF-8 with U+FFFD. Reject the original
	// bytes before decoding so a received user action is never silently changed.
	if !utf8.Valid(data) {
		return errors.New("protocol: message must be valid UTF-8")
	}
	if err := validateJSONUnicodeEscapes(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return validateExactJSONFields(data, destination)
}

// validateJSONUnicodeEscapes rejects lone UTF-16 surrogates before encoding/json
// can replace them. Ordinary JSON syntax remains the decoder's responsibility;
// this scan only distinguishes escaped backslashes from actual Unicode escapes.
func validateJSONUnicodeEscapes(data []byte) error {
	inString := false
	for index := 0; index < len(data); index++ {
		if data[index] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data) || data[index] != 'u' {
			continue
		}
		value, ok := jsonHexQuad(data, index+1)
		if !ok {
			return errors.New("protocol: invalid JSON Unicode escape")
		}
		index += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return errors.New("protocol: unpaired JSON Unicode surrogate")
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if index+2 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
			return errors.New("protocol: unpaired JSON Unicode surrogate")
		}
		low, ok := jsonHexQuad(data, index+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return errors.New("protocol: unpaired JSON Unicode surrogate")
		}
		index += 6
	}
	return nil
}

func jsonHexQuad(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// validateExactJSONFields closes encoding/json's case-insensitive struct-field
// matching gap while also enforcing required and non-null fields recursively.
// Protocol keys are case-sensitive: RequestID is not requestId, even when the
// standard decoder would otherwise bind it to the same Go field.
func validateExactJSONFields(data []byte, destination any) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	target := reflect.TypeOf(destination)
	if target == nil || target.Kind() != reflect.Pointer {
		return errors.New("protocol: required-field target must be a pointer")
	}
	return exactJSONFieldsPresent(value, target.Elem(), "")
}

func exactJSONFieldsPresent(value any, target reflect.Type, path string) error {
	for target.Kind() == reflect.Pointer {
		if value == nil {
			return nil
		}
		target = target.Elem()
	}
	if target == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch target.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("protocol: %s must be an object", displayPath(path))
		}
		allowed := make(map[string]struct{}, target.NumField())
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name, _, ignored := jsonField(field)
			if !ignored && name != "" {
				allowed[name] = struct{}{}
			}
		}
		for name := range object {
			if _, ok := allowed[name]; !ok {
				fieldPath := name
				if path != "" {
					fieldPath = path + "." + name
				}
				return fmt.Errorf("protocol: unknown field %q", fieldPath)
			}
		}
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name, optional, ignored := jsonField(field)
			if ignored || name == "" {
				continue
			}
			fieldValue, present := object[name]
			fieldPath := name
			if path != "" {
				fieldPath = path + "." + name
			}
			if !present {
				if !optional {
					return fmt.Errorf("protocol: required field %s is missing", fieldPath)
				}
				continue
			}
			if fieldValue == nil && (optional || field.Type.Kind() != reflect.Pointer) {
				return fmt.Errorf("protocol: field %s must not be null", fieldPath)
			}
			if err := exactJSONFieldsPresent(fieldValue, field.Type, fieldPath); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for index, item := range items {
			if err := exactJSONFieldsPresent(item, target.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func jsonField(field reflect.StructField) (name string, optional bool, ignored bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	for options != "" {
		var option string
		option, options, _ = strings.Cut(options, ",")
		if option == "omitempty" {
			optional = true
		}
	}
	return name, optional, false
}

func displayPath(path string) string {
	if path == "" {
		return "value"
	}
	return path
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("protocol: object key is not a string")
				}
				if seen[key] {
					return fmt.Errorf("protocol: duplicate JSON key %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("protocol: invalid JSON delimiter")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("protocol: multiple JSON values")
		}
		return err
	}
	return nil
}

func validateInputIdentity(lease, sequence string) error {
	if err := validateSecret(lease); err != nil {
		return err
	}
	_, err := parseCounter(sequence)
	return err
}

func validateSecret(secret string) error {
	if len(secret) < 43 || len(secret) > 128 {
		return errors.New("lease has an invalid length")
	}
	for _, value := range secret {
		if !(value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-') {
			return errors.New("lease is not base64url text")
		}
	}
	return nil
}

func parseCounter(value string) (uint64, error) {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("counter must be a canonical unsigned decimal string")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New("counter must fit uint64")
	}
	return parsed, nil
}

func validateSnapshotIdentity(snapshotID, revision string) error {
	if !ValidUUID(snapshotID) {
		return errors.New("snapshotId must be a canonical UUID")
	}
	_, err := parseCounter(revision)
	return err
}

func validateDimensions(columns, rows int64) error {
	if columns < MinColumns || columns > MaxColumns || rows < MinRows || rows > MaxRows {
		return errors.New("terminal dimensions are outside bounds")
	}
	return nil
}

func validateScreen(screen ScreenMetadata) error {
	if err := validateDimensions(screen.Columns, screen.Rows); err != nil {
		return err
	}
	if screen.Cursor.Column < 0 || screen.Cursor.Column >= screen.Columns || screen.Cursor.Row < 0 || screen.Cursor.Row >= screen.Rows ||
		!validBuffer(screen.Buffer) || !validCursorStyle(screen.Cursor.Style) || !validWidthMethod(screen.WidthMethod) {
		return errors.New("invalid screen metadata")
	}
	if _, err := parseCounter(screen.HistoryEpoch); err != nil {
		return fmt.Errorf("historyEpoch: %w", err)
	}
	if _, err := parseCounter(screen.HistoryRevision); err != nil {
		return fmt.Errorf("historyRevision: %w", err)
	}
	return errors.Join(validateColor(screen.Foreground), validateColor(screen.Background), validateColor(screen.CursorColor))
}

func validateRows(rows []TerminalRow, columns int64) error {
	for _, row := range rows {
		if err := validateRow(row, columns); err != nil {
			return err
		}
	}
	return nil
}

func validateRow(row TerminalRow, columns int64) error {
	if len(row.Runs) == 0 || len(row.Runs) > MaxColumns {
		return errors.New("row has an invalid run count")
	}
	expectedColumn := int64(0)
	var previousStyle *CellStyle
	for runIndex := range row.Runs {
		run := &row.Runs[runIndex]
		if run.Column != expectedColumn || len(run.Cells) == 0 || len(run.Cells) > MaxColumns {
			return errors.New("row runs must be non-empty and contiguous")
		}
		if previousStyle != nil && reflect.DeepEqual(*previousStyle, run.Style) {
			return errors.New("adjacent equivalent styles must be coalesced")
		}
		if err := validateStyle(run.Style); err != nil {
			return err
		}
		for cellIndex := range run.Cells {
			cell := run.Cells[cellIndex]
			if cell.Continuation {
				if cell.Text != "" || cell.Width != 0 || expectedColumn == 0 {
					return errors.New("invalid continuation cell")
				}
				previousCell := previousCellInRow(row.Runs, runIndex, cellIndex)
				if previousCell == nil || previousCell.Width != 2 || previousCell.Continuation {
					return errors.New("continuation cell has no wide leading cell")
				}
			} else {
				if cell.Width < 1 || cell.Width > 2 || len(cell.Text) == 0 || len(cell.Text) > 1024 || !safeDisplayText(cell.Text) {
					return errors.New("invalid display cell")
				}
				if cell.Width == 2 {
					next := nextCellInRow(row.Runs, runIndex, cellIndex)
					if next == nil || !next.Continuation {
						return errors.New("wide cell is missing its continuation")
					}
				}
			}
			if cell.Link != nil && !validLink(*cell.Link) {
				return errors.New("cell link must be a bounded http or https URL")
			}
			expectedColumn++
		}
		previousStyle = &run.Style
	}
	if expectedColumn > MaxColumns || columns > 0 && expectedColumn != columns {
		return errors.New("row does not cover the canonical width")
	}
	return nil
}

func previousCellInRow(runs []CellRun, runIndex, cellIndex int) *Cell {
	if cellIndex > 0 {
		return &runs[runIndex].Cells[cellIndex-1]
	}
	if runIndex > 0 && len(runs[runIndex-1].Cells) > 0 {
		return &runs[runIndex-1].Cells[len(runs[runIndex-1].Cells)-1]
	}
	return nil
}

func nextCellInRow(runs []CellRun, runIndex, cellIndex int) *Cell {
	if cellIndex+1 < len(runs[runIndex].Cells) {
		return &runs[runIndex].Cells[cellIndex+1]
	}
	if runIndex+1 < len(runs) && len(runs[runIndex+1].Cells) > 0 {
		return &runs[runIndex+1].Cells[0]
	}
	return nil
}

func validateStyle(style CellStyle) error {
	if !validUnderline(style.Underline) {
		return errors.New("invalid underline style")
	}
	if err := errors.Join(validateColor(style.Foreground), validateColor(style.Background)); err != nil {
		return err
	}
	if style.UnderlineColor != nil {
		return validateColor(*style.UnderlineColor)
	}
	return nil
}

func validateColor(color Color) error {
	if color.R < 0 || color.R > 255 || color.G < 0 || color.G > 255 || color.B < 0 || color.B > 255 || color.A < 0 || color.A > 255 {
		return errors.New("color component is outside 0..255")
	}
	return nil
}

func validLink(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.User == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// ValidUUID reports whether value is a canonical lowercase RFC 9562 UUID
// (versions 1-8, variant 10xx), the schema's uuid pattern.
func ValidUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' ||
		value[14] < '1' || value[14] > '8' || !containsByte("89ab", value[19]) {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !isLowerHex(value[index]) {
			return false
		}
	}
	return true
}

func isLowerHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

func containsByte(values string, value byte) bool {
	for index := range values {
		if values[index] == value {
			return true
		}
	}
	return false
}

func safeDisplayText(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, value := range text {
		if value == 0x7f || value < 0x20 || value >= 0x80 && value <= 0x9f {
			return false
		}
	}
	return true
}

func validText(text string) bool { return safeDisplayText(text) }

func validPaste(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, value := range text {
		if value == 0 || value == 0x1b || value == 0x7f ||
			(value < 0x20 && value != '\n' && value != '\r' && value != '\t') ||
			(value >= 0x80 && value <= 0x9f) {
			return false
		}
	}
	return true
}

func singlePrintableASCII(value string) bool {
	runeValue, size := utf8.DecodeRuneInString(value)
	return size == len(value) && runeValue >= 0x21 && runeValue <= 0x7e
}

func validKey(value KeyName) bool {
	switch value {
	case Character, Space, Enter, Backspace, Escape, Tab, ArrowUp, ArrowDown, ArrowLeft, ArrowRight,
		Home, End, Insert, Delete, PageUp, PageDown, F1, F2, F3, F4, F5, F6, F7, F8, F9, F10, F11, F12,
		KeypadEnter, KeypadEqual, KeypadMultiply, KeypadPlus, KeypadComma, KeypadMinus, KeypadDecimal,
		KeypadDivide, Keypad0, Keypad1, Keypad2, Keypad3, Keypad4, Keypad5, Keypad6, Keypad7, Keypad8, Keypad9:
		return true
	default:
		return false
	}
}

func validMouse(kind Kind, button Button) bool {
	switch button {
	case ButtonNone, Left, Middle, Right, WheelUp, WheelDown, WheelLeft, WheelRight, Backward, Forward:
	default:
		return false
	}
	wheelButton := button == WheelUp || button == WheelDown || button == WheelLeft || button == WheelRight
	switch kind {
	case Wheel:
		return wheelButton
	case Move:
		return !wheelButton
	case Press:
		return button != ButtonNone && !wheelButton
	case Release:
		return !wheelButton
	default:
		return false
	}
}

func validReason(value Reason) bool {
	return value == Initial || value == BaseMismatch || value == InvalidState || value == InterruptedSnapshot
}

func validBuffer(value Buffer) bool           { return value == Normal || value == Alternate }
func validCursorStyle(value Style) bool       { return value == Block || value == Underline || value == Bar }
func validWidthMethod(value WidthMethod) bool { return value == Wcwidth || value == UnicodeGraphemes }

func validUnderline(value UnderlineEnum) bool {
	return value == UnderlineNone || value == Single || value == Double || value == Curly || value == Dotted || value == Dashed
}

func validState(value State) bool {
	return value == Starting || value == Running || value == Exiting || value == Exited || value == Lost
}

func validErrorCode(value ErrorCode) bool {
	switch value {
	case Unauthorized, PairingExpired, PairingPending, ErrorCodeRevoked, ProtocolMismatch, SessionMissing,
		GenerationChanged, SessionLost, NotController, StaleLease, InputGap, LimitExceeded,
		HistoryChanged, ResyncRequired, HostUnavailable, InternalError, InvalidRequest,
		OperationConflict, ProjectMissing, ShellProfileMissing, QuestionChanged, PreviewMissing,
		WorktreeExists, NotARepository, WorktreeFailed:
		return true
	default:
		return false
	}
}

func canonicalLimits() ProtocolLimits {
	return ProtocolLimits{
		InboundMessageBytes: MaxInboundMessageBytes, OutboundMessageBytes: MaxOutboundMessageBytes,
		OutgoingQueueBytes: 2 * 1024 * 1024, MaxTextBytes: MaxTextBytes, MaxPasteBytes: MaxPasteBytes,
		MinColumns: MinColumns, MaxColumns: MaxColumns, MinRows: MinRows, MaxRows: MaxRows,
		MaxHistoryPageRows: MaxHistoryPageRows,
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

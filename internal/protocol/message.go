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
	if !validUUID(envelope.RequestID) {
		return Envelope{}, errors.New("protocol: requestId must be a canonical UUID")
	}
	if envelope.SessionID != nil && !validUUID(*envelope.SessionID) {
		return Envelope{}, errors.New("protocol: sessionId must be a canonical UUID")
	}
	if envelope.Generation != nil && !validUUID(*envelope.Generation) {
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
	InputText: true, InputPaste: true, InputMouse: true, TerminalResize: true,
	HistoryRead: true, SessionDetach: true,
}

var hostMessageTypes = map[MessageType]bool{
	Welcome: true, SnapshotBegin: true, SnapshotRows: true, SnapshotEnd: true,
	ScreenPatch: true, HistoryPage: true, MessageTypeHistoryChanged: true,
	ControlChanged: true, InputAccepted: true, InputRejected: true,
	SessionState: true, AccessRevoked: true, Error: true,
}

var sessionMessageTypes = map[MessageType]bool{
	SessionAttach: true, ScreenResync: true, SnapshotApplied: true,
	ControlAcquire: true, ControlRelease: true, Heartbeat: true, InputKey: true,
	InputText: true, InputPaste: true, InputMouse: true, TerminalResize: true,
	HistoryRead: true, SessionDetach: true, SnapshotBegin: true,
	SnapshotRows: true, SnapshotEnd: true, ScreenPatch: true, HistoryPage: true,
	MessageTypeHistoryChanged: true, ControlChanged: true, InputAccepted: true,
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
	case SessionAttach, ControlAcquire, SessionDetach:
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
	case Welcome:
		payload, err := strictPayload[WelcomePayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.HostBuild) == 0 || len(payload.HostBuild) > 128 || !validUUID(payload.BrokerGeneration) ||
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
		if payload.ControllerConnectionID != nil && !validUUID(*payload.ControllerConnectionID) {
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
	if !validUUID(snapshotID) {
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

func validUUID(value string) bool {
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
	case Unauthorized, PairingExpired, PairingPending, Revoked, ProtocolMismatch, SessionMissing,
		GenerationChanged, SessionLost, NotController, StaleLease, InputGap, LimitExceeded,
		HistoryChanged, ResyncRequired, HostUnavailable, InternalError, InvalidRequest,
		OperationConflict, ProjectMissing, ShellProfileMissing:
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

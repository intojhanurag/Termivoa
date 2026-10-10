package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrOutboundMessageTooLarge lets transports split a snapshot without treating
// a byte-budget failure as a semantic or authentication error.
var ErrOutboundMessageTooLarge = errors.New("protocol: outbound message exceeds byte limit")

// EncodeHost validates and encodes an application-owned host message. Generated
// terminal payloads share the decoder's semantic validators and serialize with
// exact field names and no duplicate keys by construction. Other payloads keep
// the strict wire round trip. Untrusted received bytes always use DecodeHost.
// Envelope.Payload is unused; payload must contain the message being encoded.
func EncodeHost(envelope Envelope, payload any) ([]byte, error) {
	if err := validateEnvelope(envelope, false); err != nil {
		return nil, err
	}
	typed, err := validateHostTerminalPayload(envelope.Type, payload)
	if err != nil {
		return nil, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	envelope.Payload, err = json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("protocol: encode %s payload: %w", envelope.Type, err)
	}
	message, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("protocol: encode %s envelope: %w", envelope.Type, err)
	}
	if len(message) > MaxOutboundMessageBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrOutboundMessageTooLarge, envelope.Type, len(message))
	}
	if !typed {
		if _, err := DecodeHost(message); err != nil {
			return nil, err
		}
	}
	return message, nil
}

// Exact value assertions deliberately exclude raw JSON, maps, other named types, pointers
// and custom marshalers from the terminal fast path. Non-null required arrays
// are checked by the same semantic validators as the wire decoder.
func validateHostTerminalPayload(kind MessageType, value any) (bool, error) {
	switch kind {
	case SnapshotBegin:
		payload, ok := value.(SnapshotBeginPayload)
		if ok {
			return true, validateSnapshotBegin(payload)
		}
	case SnapshotRows:
		payload, ok := value.(SnapshotRowsPayload)
		if ok {
			for _, row := range payload.Rows {
				if !terminalRowStringsUTF8(row) {
					return false, nil
				}
			}
			return true, validateSnapshotRows(payload)
		}
	case SnapshotEnd:
		payload, ok := value.(SnapshotEndPayload)
		if ok {
			return true, validateSnapshotEnd(payload)
		}
	case ScreenPatch:
		payload, ok := value.(ScreenPatchPayload)
		if ok {
			for _, changed := range payload.Rows {
				if !terminalRowStringsUTF8(changed.Row) {
					return false, nil
				}
			}
			return true, validateScreenPatch(payload)
		}
	case HistoryPage:
		payload, ok := value.(HistoryPagePayload)
		if ok {
			for _, row := range payload.Rows {
				if !terminalRowStringsUTF8(row) {
					return false, nil
				}
			}
			return true, validateHistoryPage(payload)
		}
	default:
		return false, nil
	}
	return false, errors.New("terminal message requires its exact generated payload value")
}

// encoding/json replaces malformed Go string bytes with U+FFFD. Rare malformed
// text/links therefore retain the original wire round trip: validation must see
// the normalized bytes, including any expansion past text or link byte bounds.
// Counter, UUID and enum fields already reject anything outside exact ASCII.
func terminalRowStringsUTF8(row TerminalRow) bool {
	for _, run := range row.Runs {
		for _, cell := range run.Cells {
			if !utf8.ValidString(cell.Text) || cell.Link != nil && !utf8.ValidString(*cell.Link) {
				return false
			}
		}
	}
	return true
}

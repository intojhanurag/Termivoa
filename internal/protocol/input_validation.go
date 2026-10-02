package protocol

import (
	"errors"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// ValidateKeyInput applies the v1 semantic rules shared by local and remote
// input producers. Lease and sequence validation remains a broker concern.
func ValidateKeyInput(key KeyName, character *string, modifiers Modifiers) error {
	if !validKey(key) {
		return errors.New("invalid key")
	}
	if key == Character {
		if character == nil || !singlePrintableASCII(*character) ||
			(!modifiers.Ctrl && !modifiers.Alt && !modifiers.Meta) {
			return errors.New("character key requires one printable ASCII character and a control modifier")
		}
	} else if character != nil {
		return errors.New("character is only valid for the character key")
	}
	return nil
}

// ValidateTextInput rejects control text and applies the UTF-8 byte limit.
func ValidateTextInput(text string) error {
	if len(text) == 0 || len(text) > MaxTextBytes || !terminal.ValidText(text) {
		return errors.New("text must be non-empty printable UTF-8 within the byte limit")
	}
	return nil
}

// ValidatePasteInput permits newline, carriage return, and tab but rejects
// other control bytes and applies the UTF-8 byte limit.
func ValidatePasteInput(text string) error {
	if len(text) == 0 || len(text) > MaxPasteBytes || !terminal.ValidPaste(text) {
		return errors.New("paste must be non-empty allowed UTF-8 within the byte limit")
	}
	return nil
}

// ValidateMouseInput applies the shared v1 coordinate and event rules.
func ValidateMouseInput(kind Kind, button Button, column, row int64) error {
	if column < 0 || column >= MaxColumns || row < 0 || row >= MaxRows || !validMouse(kind, button) {
		return errors.New("invalid mouse event")
	}
	return nil
}

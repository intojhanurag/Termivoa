package protocol

import (
	"errors"
	"strings"
)

// MaxWorkingDirectoryBytes bounds cwd on a session record or session.state.
const MaxWorkingDirectoryBytes = 512

// ValidateWorkingDirectory applies the workingDirectory schema plus the rule
// it does not express: the value is an absolute POSIX path ("/..."), Windows
// drive path ("C:\..." or "C:/...") or UNC path ("\\server\share"), without
// "." or ".." segments. It is display text only, derived from terminal output
// or the operating system, so it must also be safe display text.
func ValidateWorkingDirectory(value string) error {
	if err := validateBrokerRPCText(value, MaxWorkingDirectoryBytes, "cwd"); err != nil {
		return err
	}
	drive := len(value) >= 3 && (value[0]|0x20 >= 'a' && value[0]|0x20 <= 'z') && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
	if !strings.HasPrefix(value, "/") && !drive && !strings.HasPrefix(value, `\\`) {
		return errors.New("cwd must be an absolute path")
	}
	for _, segment := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == "." || segment == ".." {
			return errors.New("cwd must not contain . or .. segments")
		}
	}
	return nil
}

func validateOptionalWorkingDirectory(value *string) error {
	if value == nil {
		return nil
	}
	return ValidateWorkingDirectory(*value)
}

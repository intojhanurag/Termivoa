package cli

import "golang.org/x/sys/windows"

// This one-time ownership boundary intentionally discards pending console input
// records after startup decoding joins. It is not used during live input or resizing.
// Native Windows validation of console access rights remains a separate gate.
func discardDesktopInput(handle terminalFileHandle) error {
	return windows.FlushConsoleInputBuffer(windows.Handle(handle))
}

// Native Windows handoff input is flushed without a detach scan; see
// discardDesktopInput. Ctrl+] typed in that instant must be pressed again.
func drainDesktopDetach(terminalFileHandle) (bool, error) { return false, nil }

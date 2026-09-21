package cli

import "golang.org/x/sys/windows"

// This one-time ownership boundary intentionally discards pending console input
// records after startup decoding joins. It is not used during live input or resizing.
// Native Windows validation of console access rights remains a separate gate.
func discardDesktopInput(handle terminalFileHandle) error {
	return windows.FlushConsoleInputBuffer(windows.Handle(handle))
}

package cli

import (
	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
)

// The handoff drain's last resort: it discards pending console input records
// after startup decoding joins. It is not used during live input or resizing.
// Native Windows validation of console access rights remains a separate gate.
func discardDesktopInput(handle terminalFileHandle) error {
	return windows.FlushConsoleInputBuffer(windows.Handle(handle))
}

// drainDesktopDetach discards the console input records still queued at the
// startup/live decoder handoff and reports a Ctrl+] key press in them. It
// reads until none are pending, so every discarded record is scanned; a key
// that arrives later stays queued for the live decoder. Nothing is sent. A
// console handle has no non-blocking ReadFile, so it reads only the records
// the console reports as pending. The startup decoder is already joined, so
// nothing else consumes them and the read cannot block.
func drainDesktopDetach(handle terminalFileHandle) (bool, error) {
	var records [256]xwindows.InputRecord
	detach := false
	for range 256 {
		var pending, n uint32
		if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(handle), &pending); err != nil || pending == 0 {
			return detach, err
		}
		if err := xwindows.ReadConsoleInput(windows.Handle(handle), &records[0], min(pending, uint32(len(records))), &n); err != nil {
			return detach, err
		}
		for _, record := range records[:n] {
			if record.EventType == xwindows.KEY_EVENT {
				key := record.KeyEvent()
				detach = detach || consoleKeyDetach(key.KeyDown, key.VirtualKeyCode, key.Char, key.ControlKeyState)
			}
		}
	}
	// ponytail: only input that never stops arriving gets here; the flush
	// drops it unscanned, so a Ctrl+] inside it is lost.
	return detach, discardDesktopInput(handle)
}

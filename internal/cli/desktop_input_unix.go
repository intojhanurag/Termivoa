//go:build darwin || linux

package cli

import (
	"bytes"
	"errors"

	"golang.org/x/sys/unix"
)

// desktopDrainEmpty, when set by a test, runs each time the handoff drain
// finds the input queue empty, just before it returns.
var desktopDrainEmpty func()

// drainDesktopDetach discards the input still queued at the startup/live
// decoder handoff and reports a raw Ctrl+] in it. It reads until the queue is
// empty, so every discarded byte is scanned; a key that arrives later stays
// queued for the live decoder, which handles Ctrl+] itself. Nothing is sent.
func drainDesktopDetach(handle terminalFileHandle) (bool, error) {
	var data [4096]byte
	detach := false
	for range 256 {
		n, err := nonblockingTerminalIO(handle, func(fd int) (int, error) { return unix.Read(fd, data[:]) })
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || (err == nil && n == 0) {
			if desktopDrainEmpty != nil {
				desktopDrainEmpty()
			}
			return detach, nil
		}
		if err != nil {
			return detach, err
		}
		detach = detach || bytes.IndexByte(data[:n], desktopDetachByte) >= 0
	}
	// ponytail: only input still arriving after 256 reads gets here (at most
	// 1 MiB; a macOS terminal read returns about 1 KiB, so about 256 KiB).
	// The flush drops it unscanned, so a Ctrl+] inside it is lost.
	return detach, discardDesktopInput(handle)
}

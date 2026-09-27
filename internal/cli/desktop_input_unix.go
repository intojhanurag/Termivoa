//go:build darwin || linux

package cli

import (
	"bytes"
	"errors"

	"golang.org/x/sys/unix"
)

// drainDesktopDetach reads input still queued at the startup/live decoder
// handoff and reports a raw Ctrl+]. The bytes are discarded, never sent.
func drainDesktopDetach(handle terminalFileHandle) (bool, error) {
	var data [4096]byte
	detach := false
	for range 16 { // ponytail: bounded; the following flush discards any remainder.
		n, err := nonblockingTerminalIO(handle, func(fd int) (int, error) { return unix.Read(fd, data[:]) })
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || (err == nil && n == 0) {
			return detach, nil
		}
		if err != nil {
			return detach, err
		}
		detach = detach || bytes.IndexByte(data[:n], desktopDetachByte) >= 0
	}
	return detach, nil
}

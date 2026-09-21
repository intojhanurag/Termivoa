package cli

import "golang.org/x/sys/unix"

func discardDesktopInput(handle terminalFileHandle) error {
	return unix.IoctlSetInt(int(handle), unix.TCFLSH, unix.TCIFLUSH)
}

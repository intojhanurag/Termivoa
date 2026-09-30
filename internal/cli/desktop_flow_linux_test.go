package cli

import "golang.org/x/sys/unix"

// stopTerminalOutput is tcflow(TCOOFF) or tcflow(TCOON) on a terminal.
func stopTerminalOutput(fd uintptr, stop bool) error {
	action := unix.TCOON
	if stop {
		action = unix.TCOOFF
	}
	return unix.IoctlSetInt(int(fd), unix.TCXONC, action)
}

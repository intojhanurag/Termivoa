package cli

import "golang.org/x/sys/unix"

// stopTerminalOutput is tcflow(TCOOFF) or tcflow(TCOON) on a terminal. On
// stop it also clears IXANY, which macOS sets by default and raw mode keeps:
// with IXANY, any typed key starts the stopped output again.
func stopTerminalOutput(fd uintptr, stop bool) error {
	if !stop {
		return unix.IoctlSetInt(int(fd), unix.TIOCSTART, 0)
	}
	termios, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	if err != nil {
		return err
	}
	termios.Iflag &^= unix.IXANY
	if err := unix.IoctlSetTermios(int(fd), unix.TIOCSETA, termios); err != nil {
		return err
	}
	return unix.IoctlSetInt(int(fd), unix.TIOCSTOP, 0)
}

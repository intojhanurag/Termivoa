package cli

import "golang.org/x/sys/unix"

// The handoff drain's last resort, used only when input never stops arriving.
// TIOCFLUSH
// takes a pointer to the BSD FREAD bit, not the integer argument used on Linux.
// This discards input only; output and the caller-owned handle are untouched.
func discardDesktopInput(handle terminalFileHandle) error {
	const readQueue = 1 // FREAD from sys/fcntl.h, used by TIOCFLUSH.
	return unix.IoctlSetPointerInt(int(handle), unix.TIOCFLUSH, readQueue)
}

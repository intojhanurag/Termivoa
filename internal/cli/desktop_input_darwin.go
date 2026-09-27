package cli

import "golang.org/x/sys/unix"

// Called once after joining the startup decoder, before live input. TIOCFLUSH
// takes a pointer to the BSD FREAD bit, not the integer argument used on Linux.
// This discards input only; output and the caller-owned handle are untouched.
func discardDesktopInput(handle terminalFileHandle) error {
	const readQueue = 1 // FREAD from sys/fcntl.h, used by TIOCFLUSH.
	return unix.IoctlSetPointerInt(int(handle), unix.TIOCFLUSH, readQueue)
}

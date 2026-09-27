//go:build windows

package main

import (
	"os"
	"syscall"
)

// Go reports console close, logoff and shutdown events as SIGTERM.
func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

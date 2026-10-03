//go:build !windows

package main

import (
	"os"
	"syscall"
)

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// hangupSignals also end a pairing wait: a closed SSH session must still
// end the offer, so its grants are deleted (see endOnNewConnection).
func hangupSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}

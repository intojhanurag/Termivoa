package herdr

import (
	"errors"
	"os/exec"
)

// herdr's socket is a Unix socket; Windows is a non-goal (design Q4).
func checkSocket(string) error { return errors.New("herdr: not supported on Windows") }

func noTerminal(*exec.Cmd) {}

//go:build !windows

package lifecycle

import (
	"os"
	"syscall"
)

// DetachedAttributes starts a process in its own session so it outlives the
// launcher and its terminal.
func DetachedAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// GroupAttributes starts a child in its own process group, so a signal to one
// child's group never reaches the other child.
func GroupAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// Interrupt asks a child started with GroupAttributes to shut down cleanly.
func Interrupt(process *os.Process) error { return process.Signal(os.Interrupt) }

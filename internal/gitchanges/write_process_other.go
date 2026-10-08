//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package gitchanges

import (
	"errors"
	"os/exec"
)

type writeProcessTree struct{}

func prepareWriteProcess(*exec.Cmd) (writeProcessTree, error) {
	return writeProcessTree{}, errors.New("gitchanges: git process ownership is unsupported on this platform")
}
func (writeProcessTree) attach(*exec.Cmd) error { return nil }
func (writeProcessTree) kill(*exec.Cmd) error   { return nil }
func (writeProcessTree) close() error           { return nil }

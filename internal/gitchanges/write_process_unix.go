//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitchanges

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type writeProcessTree struct{}

func prepareWriteProcess(command *exec.Cmd) (writeProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	tree := writeProcessTree{}
	// Unlike the default CommandContext cancel, signal the entire owned group.
	// SIGKILL cannot be ignored by a hook. Deliberate setsid escapes are outside
	// this boundary: repository hooks are trusted user code, not a sandbox.
	command.Cancel = func() error { return tree.kill(command) }
	return tree, nil
}
func (writeProcessTree) attach(*exec.Cmd) error { return nil }
func (writeProcessTree) kill(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
func (writeProcessTree) close() error   { return nil }
func (writeProcessTree) release() error { return nil }

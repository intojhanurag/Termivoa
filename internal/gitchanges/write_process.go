package gitchanges

import (
	"errors"
	"os"
	"os/exec"
)

// runWriteCommand owns the process boundary before any hook can run. Wait
// reaps git, and cleanup ends remaining descendants before the caller can
// report the result or roll a failed worktree back.
func runWriteCommand(command *exec.Cmd) error {
	tree, err := prepareWriteProcess(command)
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		return errors.Join(err, tree.close())
	}
	if err = tree.attach(command); err != nil {
		killErr := tree.kill(command)
		_ = command.Process.Kill() // also handles a failed/suspended attachment
		return errors.Join(err, killErr, command.Wait(), tree.close())
	}
	err = command.Wait()
	killErr := tree.kill(command)
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	return errors.Join(err, killErr, tree.close())
}

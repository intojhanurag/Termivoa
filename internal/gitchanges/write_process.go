package gitchanges

import (
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
)

// runWriteCommand owns the process boundary before any hook can run. A
// canceled or failed git ends its remaining descendants before the caller can
// report the result or roll a failed worktree back. A successful git keeps
// them: a hook may start ordinary background work (issue #222 review).
func runWriteCommand(command *exec.Cmd) error {
	tree, err := prepareWriteProcess(command)
	if err != nil {
		return err
	}
	// Cancel ends the tree while git still runs (before Wait reaps it), so
	// the group or Job cannot have been reused yet.
	var canceled atomic.Bool
	if cancel := command.Cancel; cancel != nil {
		command.Cancel = func() error { canceled.Store(true); return cancel() }
	}
	if err = command.Start(); err != nil {
		return errors.Join(err, tree.close())
	}
	if err = tree.attach(command); err != nil {
		killErr := tree.kill(command)
		_ = command.Process.Kill() // also handles a failed/suspended attachment
		return errors.Join(err, killErr, command.Wait(), tree.close())
	}
	if err = command.Wait(); err == nil {
		return tree.release()
	}
	var killErr error
	if !canceled.Load() {
		// git failed by itself and is already reaped. Unix: a process group ID
		// is not reused while any member lives, and ESRCH means none is left.
		// ponytail: an empty group whose ID a new session leader took meanwhile
		// is not ruled out; a non-reaping wait (waitid WNOWAIT) would close it.
		if killErr = tree.kill(command); errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
	}
	return errors.Join(err, killErr, tree.close())
}

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ptyhost

import (
	"errors"
	"os/exec"
	"syscall"
)

type platformProcessTree struct {
	processGroupID int
}

func configureCommand(cmd *exec.Cmd) {
	// setsid makes the child both session leader and process-group leader.
	// Setctty then assigns the PTY slave already installed by xpty as child fd
	// zero. Setpgid must not also be requested: changing the process group of a
	// session leader fails on supported Unix systems.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
}

func attachProcessTree(cmd *exec.Cmd) (platformProcessTree, error) {
	return platformProcessTree{processGroupID: cmd.Process.Pid}, nil
}

func cleanupUnattachedProcess(cmd *exec.Cmd) (bool, error) {
	err := ignoreProcessDone(cmd.Process.Kill())
	return err == nil, err
}

func terminateProcessTree(tree platformProcessTree) error {
	// Hang up first, as closing a real terminal does, then ask to terminate.
	return errors.Join(signalSession(tree.processGroupID, syscall.SIGHUP), signalSession(tree.processGroupID, syscall.SIGTERM))
}

func forceTerminateProcessTree(tree platformProcessTree) error {
	return signalSession(tree.processGroupID, syscall.SIGKILL)
}

func cleanupExitedProcessTree(tree platformProcessTree) error {
	// A group leader may exit naturally while descendants remain in its process
	// group. Ensure those descendants cannot outlive the managed session.
	return forceTerminateProcessTree(tree)
}

// signalSession signals the managed process group and every other process in
// the PTY session. The child is the session leader, so its PID is the session
// ID. Interactive shells run background jobs in separate process groups of the
// same session and do not reliably forward hangups, so each member is signaled
// directly. Processes that called setsid are outside and are never signaled.
func signalSession(sessionID int, signal syscall.Signal) error {
	groupErr := signalProcessGroup(sessionID, signal)
	if errors.Is(groupErr, syscall.EPERM) {
		// The group was created by our own setsid. Darwin reports EPERM while
		// an exiting leader tears it down, for example right after a hangup.
		groupErr = nil
	}
	members, listErr := sessionMembers(sessionID)
	for _, pid := range members {
		// A member may exit or become an unsignalable zombie meanwhile.
		if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
			groupErr = errors.Join(groupErr, err)
		}
	}
	return errors.Join(groupErr, listErr)
}

func signalProcessGroup(processGroupID int, signal syscall.Signal) error {
	if processGroupID <= 0 {
		return errors.New("ptyhost: invalid process group")
	}
	err := syscall.Kill(-processGroupID, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func closeProcessTree(tree platformProcessTree) error {
	// The session leader's exit may already have caused the kernel to hang up
	// the controlling terminal and remove the process group. Darwin can report
	// EPERM while that teardown is in flight, so this is a best-effort safety
	// sweep. Explicit cancellation and Terminate surface their signal errors.
	_ = cleanupExitedProcessTree(tree)
	return nil
}

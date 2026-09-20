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
	return signalProcessGroup(tree.processGroupID, syscall.SIGTERM)
}

func forceTerminateProcessTree(tree platformProcessTree) error {
	return signalProcessGroup(tree.processGroupID, syscall.SIGKILL)
}

func cleanupExitedProcessTree(tree platformProcessTree) error {
	// A group leader may exit naturally while descendants remain in its process
	// group. Ensure those descendants cannot outlive the managed session.
	return forceTerminateProcessTree(tree)
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

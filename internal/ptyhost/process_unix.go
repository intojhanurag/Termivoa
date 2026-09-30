//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ptyhost

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// sessionPollInterval bounds how often the session is enumerated while its
// members use the termination grace period.
const sessionPollInterval = 10 * time.Millisecond

type platformProcessTree struct {
	processGroupID int
	hangup         *sessionHangup
}

// sessionHangup asks the session to end once. The grace period runs from the
// time its signals were sent, not from the request: listing the session can
// take hundreds of milliseconds on a loaded machine, and that time must not
// come out of the members' grace period.
type sessionHangup struct {
	once   sync.Once
	sentAt time.Time
}

// send signals the session on the first call. Every call returns when those
// signals were sent, waiting for a first call that is still sending. Only the
// call that sent them reports a signaling error: a later caller must not fail
// (and mark a session lost) because of an error the first caller already had.
func (h *sessionHangup) send(sessionID int) (time.Time, error) {
	var err error
	h.once.Do(func() {
		// Hang up first, as closing a real terminal does, then ask to terminate.
		err = errors.Join(signalSession(sessionID, syscall.SIGHUP), signalSession(sessionID, syscall.SIGTERM))
		h.sentAt = time.Now()
	})
	return h.sentAt, err
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
	return platformProcessTree{processGroupID: cmd.Process.Pid, hangup: new(sessionHangup)}, nil
}

func cleanupUnattachedProcess(cmd *exec.Cmd) (bool, error) {
	err := ignoreProcessDone(cmd.Process.Kill())
	return err == nil, err
}

func terminateProcessTree(tree platformProcessTree) error {
	_, err := tree.hangup.send(tree.processGroupID)
	return err
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

// closeProcessTree runs after the session leader was reaped. Remaining session
// members, such as background jobs, get the same grace period as a Terminate
// request: they are hung up (unless Terminate already did), then killed when
// they are still present once the grace period after that hangup ends.
func closeProcessTree(tree platformProcessTree) error {
	// The leader's exit may already have caused the kernel to hang up the
	// controlling terminal and remove the process group. Darwin can report
	// EPERM while that teardown is in flight, so these are best-effort sweeps.
	// Explicit cancellation and Terminate surface their signal errors.
	sentAt, _ := tree.hangup.send(tree.processGroupID)
	deadline := sentAt.Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if members, err := sessionMembers(tree.processGroupID); err != nil || len(members) == 0 {
			break
		}
		time.Sleep(sessionPollInterval)
	}
	_ = cleanupExitedProcessTree(tree)
	return nil
}

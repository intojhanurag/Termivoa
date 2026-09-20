//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type sessionInfo struct {
	PID            int  `json:"pid"`
	SessionID      int  `json:"sessionId"`
	ProcessGroupID int  `json:"processGroupId"`
	ControllingTTY bool `json:"controllingTty"`
}

func currentSessionInfo() (sessionInfo, error) {
	sessionID, err := unix.Getsid(0)
	if err != nil {
		return sessionInfo{}, fmt.Errorf("get session ID: %w", err)
	}
	processGroupID, err := unix.Getpgid(0)
	if err != nil {
		return sessionInfo{}, fmt.Errorf("get process group ID: %w", err)
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return sessionInfo{}, fmt.Errorf("open controlling terminal: %w", err)
	}
	if err := tty.Close(); err != nil {
		return sessionInfo{}, fmt.Errorf("close controlling terminal: %w", err)
	}

	return sessionInfo{
		PID:            os.Getpid(),
		SessionID:      sessionID,
		ProcessGroupID: processGroupID,
		ControllingTTY: true,
	}, nil
}

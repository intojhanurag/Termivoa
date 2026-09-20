//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"fmt"
	"os"
	"syscall"
)

type sessionInfo struct {
	PID            int  `json:"pid"`
	SessionID      int  `json:"sessionId"`
	ProcessGroupID int  `json:"processGroupId"`
	ControllingTTY bool `json:"controllingTty"`
}

func currentSessionInfo() (sessionInfo, error) {
	sessionID, err := syscall.Getsid(0)
	if err != nil {
		return sessionInfo{}, fmt.Errorf("get session ID: %w", err)
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
		ProcessGroupID: syscall.Getpgrp(),
		ControllingTTY: true,
	}, nil
}

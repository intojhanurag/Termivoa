//go:build windows

package main

import "errors"

type sessionInfo struct {
	PID            int  `json:"pid"`
	SessionID      int  `json:"sessionId"`
	ProcessGroupID int  `json:"processGroupId"`
	ControllingTTY bool `json:"controllingTty"`
}

func currentSessionInfo() (sessionInfo, error) {
	return sessionInfo{}, errors.New("Unix session metadata is unavailable on Windows")
}

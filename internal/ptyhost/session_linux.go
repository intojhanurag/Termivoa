package ptyhost

import (
	"os"
	"strconv"
	"strings"
)

// sessionMembers lists live processes in the PTY session sessionID by reading
// the session field of /proc/<pid>/stat. Linux is a development host only.
func sessionMembers(sessionID int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var members []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// The command name is parenthesized and may contain spaces; fields
		// after it are: state ppid pgrp session.
		closing := strings.LastIndexByte(string(stat), ')')
		if closing < 0 {
			continue
		}
		fields := strings.Fields(string(stat[closing+1:]))
		if len(fields) < 4 {
			continue
		}
		if sid, err := strconv.Atoi(fields[3]); err == nil && sid == sessionID {
			members = append(members, pid)
		}
	}
	return members, nil
}

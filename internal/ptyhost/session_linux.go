package ptyhost

import (
	"os"
	"strconv"
	"strings"
)

type processInfo struct{ pid, parent, session int }

// liveProcesses lists every process that is not a zombie by reading
// /proc/<pid>/stat. Linux is a development host only. Zombies cannot be
// signaled, must not extend the grace period, and own no sockets.
func liveProcesses() ([]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var live []processInfo
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
		if len(fields) < 4 || fields[0] == "Z" {
			continue
		}
		parent, parentErr := strconv.Atoi(fields[1])
		sid, sessionErr := strconv.Atoi(fields[3])
		if parentErr != nil || sessionErr != nil {
			continue
		}
		live = append(live, processInfo{pid: pid, parent: parent, session: sid})
	}
	return live, nil
}

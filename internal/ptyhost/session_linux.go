package ptyhost

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type processInfo struct {
	pid, parent, session int
	start                time.Time
}

// bootTime reads btime from /proc/stat: when the system booted.
func bootTime() time.Time {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "btime "); ok {
			if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return time.Unix(seconds, 0)
			}
		}
	}
	return time.Time{}
}

// liveProcesses lists every process that is not a zombie by reading
// /proc/<pid>/stat. Linux is a development host only. Zombies cannot be
// signaled, must not extend the grace period, and own no sockets.
func liveProcesses() ([]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var live []processInfo
	booted := bootTime()
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
		// after it are: state ppid pgrp session ... starttime (the 20th).
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
		info := processInfo{pid: pid, parent: parent, session: sid}
		if len(fields) > 19 && !booted.IsZero() {
			if ticks, err := strconv.ParseInt(fields[19], 10, 64); err == nil {
				info.start = booted.Add(ticksDuration(ticks))
			}
		}
		live = append(live, info)
	}
	return live, nil
}

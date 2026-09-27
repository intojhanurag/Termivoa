//go:build darwin || linux

package ptyhost

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// WorkingDirectory returns the current directory of the session's foreground
// process: the PTY's foreground process group (tcgetpgrp on the master), else
// the newest member of the PTY session, else the shell. It returns "" once the
// session has ended. The path is unvalidated display data.
func (p *Process) WorkingDirectory(ctx context.Context) (string, error) {
	if p.isDone() {
		return "", nil
	}
	directory, err := processDirectory(ctx, p.foregroundProcess())
	if err != nil || p.isDone() {
		return "", err
	}
	return directory, nil
}

func (p *Process) foregroundProcess() int {
	sessionID := p.tree.processGroupID
	// The group leader must still be in this session; a reused PID is not.
	if group := p.foregroundGroup(); group > 0 {
		if sid, err := unix.Getsid(group); err == nil && sid == sessionID {
			return group
		}
	}
	// ponytail: the highest PID stands in for the newest process; wrong only
	// after PID wraparound, and only when tcgetpgrp failed.
	newest := sessionID
	if processes, err := liveProcesses(); err == nil {
		for _, process := range processes {
			if process.session == sessionID && process.pid > newest {
				newest = process.pid
			}
		}
	}
	return newest
}

// foregroundGroup is tcgetpgrp on the PTY master, or 0. SyscallConn keeps the
// master non-blocking, unlike File.Fd.
func (p *Process) foregroundGroup() int {
	pty, ok := p.pty.(interface{ Master() *os.File })
	if !ok || pty.Master() == nil {
		return 0
	}
	raw, err := pty.Master().SyscallConn()
	if err != nil {
		return 0
	}
	group := 0
	_ = raw.Control(func(fd uintptr) {
		if value, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
			group = value
		}
	})
	return group
}

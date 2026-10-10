package ptyhost

import (
	"time"

	"golang.org/x/sys/unix"
)

// sZomb is the Darwin p_stat value of an exited, unreaped process.
const sZomb = 5

type processInfo struct {
	pid, parent, session int
	start                time.Time
}

// liveProcesses lists every process that is not a zombie, from one
// kern.proc.all snapshot. Zombies cannot be signaled, must not extend the
// grace period, and own no sockets. A process whose session cannot be read
// has session -1.
func liveProcesses() ([]processInfo, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var live []processInfo
	for _, process := range processes {
		pid := int(process.Proc.P_pid)
		if pid <= 0 || process.Proc.P_stat == sZomb {
			continue
		}
		sid, err := unix.Getsid(pid)
		if err != nil {
			sid = -1
		}
		started := process.Proc.P_starttime
		live = append(live, processInfo{pid: pid, parent: int(process.Eproc.Ppid), session: sid,
			start: time.Unix(int64(started.Sec), int64(started.Usec)*1000)})
	}
	return live, nil
}

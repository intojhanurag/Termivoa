package ptyhost

import "golang.org/x/sys/unix"

// sessionMembers lists live processes in the PTY session sessionID. Only the
// session created for a managed child qualifies; a descendant that calls
// setsid leaves it deliberately and is outside the managed boundary.
func sessionMembers(sessionID int) ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var members []int
	for _, process := range processes {
		pid := int(process.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		if sid, err := unix.Getsid(pid); err == nil && sid == sessionID {
			members = append(members, pid)
		}
	}
	return members, nil
}

//go:build darwin || linux

package ptyhost

// sessionMembers lists live processes in the PTY session sessionID. Only the
// session created for a managed child qualifies; a descendant that calls
// setsid leaves it deliberately and is outside the termination boundary.
func sessionMembers(sessionID int) ([]int, error) {
	processes, err := liveProcesses()
	if err != nil {
		return nil, err
	}
	var members []int
	for _, process := range processes {
		if process.session == sessionID {
			members = append(members, process.pid)
		}
	}
	return members, nil
}

// sessionTree lists the processes a session's preview may expose: the
// members of session sessionID and all their descendants by parent PID,
// including dev servers that a coding agent detached with setsid. Parent
// links always name a live or zombie parent, so an unrelated process cannot
// join the tree through PID reuse.
func sessionTree(sessionID int) ([]int, error) {
	processes, err := liveProcesses()
	if err != nil {
		return nil, err
	}
	parents := make(map[int]int, len(processes))
	roots := []int{sessionID}
	for _, process := range processes {
		parents[process.pid] = process.parent
		if process.session == sessionID {
			roots = append(roots, process.pid)
		}
	}
	return descendants(parents, roots), nil
}

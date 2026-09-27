//go:build linux

package ptyhost

import "context"

// sessionListeners returns the session's process tree and its TCP listeners,
// read from /proc without lsof, which many Linux servers lack: LISTEN sockets
// of the broker's network namespace (net/tcp and net/tcp6) are matched by
// socket inode to the open files of the tree's processes. Another user's
// /proc/<pid>/fd cannot be read, so it can only hide listeners.
// ponytail: a tree PID that exits and is reused between the snapshot and the
// fd scan could be reported; the window is milliseconds and PID reuse is slow.
func sessionListeners(_ context.Context, tree platformProcessTree) ([]int, []Listener, error) {
	pids, err := sessionTree(tree.processGroupID)
	if err != nil {
		return nil, nil, err
	}
	if len(pids) > maxTreeProcesses {
		pids = pids[:maxTreeProcesses]
	}
	listeners, err := procListeners("/proc", pids)
	if err != nil {
		return nil, nil, err
	}
	return pids, listeners, nil
}

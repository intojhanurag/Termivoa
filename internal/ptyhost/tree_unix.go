//go:build darwin || linux

package ptyhost

import (
	"context"
	"time"
)

// TreeProcesses lists the session's process tree (as for preview ports) with
// start times, at most maxTreeProcesses. It returns nil once the session has
// ended. The chat view binds an agent by its PID and start time
// (docs/chat-view.md, section 4.3).
func (p *Process) TreeProcesses(ctx context.Context) ([]TreeProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.isDone() {
		return nil, nil
	}
	processes, err := liveProcesses()
	if err != nil {
		return nil, err
	}
	parents := make(map[int]int, len(processes))
	started := make(map[int]time.Time, len(processes))
	roots := []int{p.tree.processGroupID}
	for _, process := range processes {
		parents[process.pid], started[process.pid] = process.parent, process.start
		if process.session == p.tree.processGroupID {
			roots = append(roots, process.pid)
		}
	}
	pids := descendants(parents, roots)
	if len(pids) > maxTreeProcesses {
		pids = pids[:maxTreeProcesses]
	}
	tree := make([]TreeProcess, 0, len(pids))
	for _, pid := range pids {
		if start, ok := started[pid]; ok {
			tree = append(tree, TreeProcess{PID: pid, Start: start})
		}
	}
	if p.isDone() {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tree, nil
}

// ProcessDirectory is the OS working folder of pid (macOS: lsof, Linux:
// /proc), or "" when the process is gone. Unvalidated.
func ProcessDirectory(ctx context.Context, pid int) (string, error) {
	return processDirectory(ctx, pid)
}

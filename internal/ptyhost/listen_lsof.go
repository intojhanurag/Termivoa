//go:build darwin || linux

package ptyhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	lsofTimeout   = 2 * time.Second
	lsofBatch     = 200
	maxLsofOutput = 256 << 10
)

// lsofPaths are absolute so a session cannot shadow lsof through PATH. Darwin
// ships /usr/sbin/lsof; Linux distributions use either.
var lsofPaths = []string{"/usr/sbin/lsof", "/usr/bin/lsof"}

// sessionListeners returns the session's process tree and the TCP listeners
// lsof reports for it. Without lsof there is nothing to report.
// ponytail: a tree PID that exits and is reused between the snapshot and lsof
// could be reported; the window is milliseconds and PID reuse is slow.
func sessionListeners(ctx context.Context, tree platformProcessTree) ([]int, []Listener, error) {
	lsof := ""
	for _, path := range lsofPaths {
		if _, err := os.Stat(path); err == nil {
			lsof = path
			break
		}
	}
	if lsof == "" {
		return nil, nil, nil
	}
	pids, err := sessionTree(tree.processGroupID)
	if err != nil {
		return nil, nil, err
	}
	if len(pids) > maxTreeProcesses {
		pids = pids[:maxTreeProcesses]
	}
	ctx, cancel := context.WithTimeout(ctx, lsofTimeout)
	defer cancel()
	var listeners []Listener
	for start := 0; start < len(pids); start += lsofBatch {
		batch := pids[start:min(start+lsofBatch, len(pids))]
		found, err := runLsof(ctx, lsof, batch)
		if err != nil {
			return nil, nil, err
		}
		listeners = append(listeners, found...)
	}
	return pids, listeners, nil
}

func runLsof(ctx context.Context, lsof string, pids []int) ([]Listener, error) {
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	// -a ANDs the filters: TCP, LISTEN state, and only these PIDs.
	cmd := exec.CommandContext(ctx, lsof, "-w", "-a", "-nP", "-iTCP", "-sTCP:LISTEN", "-p", strings.Join(list, ","), "-F", "ptn")
	cmd.Env = []string{"LC_ALL=C"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ptyhost: run lsof: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxLsofOutput+1))
	if len(output) > maxLsofOutput {
		_ = cmd.Process.Kill()
		readErr = errors.New("ptyhost: lsof output is too large")
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ptyhost: run lsof: %w", err)
	}
	// lsof exits 1 when nothing matched or when a listed PID has exited
	// meanwhile; the output for the remaining PIDs is still complete.
	var exitErr *exec.ExitError
	if waitErr != nil && !(errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1) {
		return nil, fmt.Errorf("ptyhost: run lsof: %w", waitErr)
	}
	return parseLsof(output), nil
}

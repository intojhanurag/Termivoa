package ptyhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

const maxLsofDirectoryOutput = 64 << 10

// processDirectory asks lsof, by absolute path so a session cannot shadow it,
// for the cwd of pid. Darwin has no /proc and this avoids cgo.
func processDirectory(ctx context.Context, pid int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lsofTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-w", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn")
	cmd.Env = []string{"LC_ALL=C"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("ptyhost: run lsof: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxLsofDirectoryOutput))
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", readErr
	}
	// lsof exits 1 when the process exited meanwhile: no directory.
	var exitErr *exec.ExitError
	if waitErr != nil && !(errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1) {
		return "", fmt.Errorf("ptyhost: run lsof: %w", waitErr)
	}
	return parseLsofDirectory(output), nil
}

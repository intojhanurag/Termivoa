package ptyhost

import (
	"context"
	"os"
	"strconv"
)

// processDirectory reads /proc/<pid>/cwd; a process that exited meanwhile
// has none.
func processDirectory(_ context.Context, pid int) (string, error) {
	directory, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return "", nil
	}
	return directory, nil
}

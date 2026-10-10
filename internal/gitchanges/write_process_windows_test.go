//go:build windows

package gitchanges

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func writeProcessGone(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(process)
	state, err := windows.WaitForSingleObject(process, 0)
	return err == nil && state == windows.WAIT_OBJECT_0
}
func assertWriteProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writeProcessGone(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("write descendant PID %d survived Job cleanup", pid)
}

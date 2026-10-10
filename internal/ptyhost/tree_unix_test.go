//go:build darwin || linux

package ptyhost

import (
	"os"
	"testing"
	"time"
)

func TestLiveProcessesReportStartTimes(t *testing.T) {
	processes, err := liveProcesses()
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		if process.pid == os.Getpid() {
			if age := time.Since(process.start); age < 0 || age > 10*time.Minute {
				t.Fatalf("this test process started %v ago", age)
			}
			return
		}
	}
	t.Fatal("this process is not listed")
}

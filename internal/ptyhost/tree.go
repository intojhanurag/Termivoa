package ptyhost

import "time"

// TreeProcess is one live process of a session tree and when it started.
type TreeProcess struct {
	PID   int
	Start time.Time
}

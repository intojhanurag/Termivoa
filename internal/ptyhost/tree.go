package ptyhost

import "time"

// TreeProcess is one live process of a session tree and when it started.
type TreeProcess struct {
	PID   int
	Start time.Time
}

// clockTicks is Linux USER_HZ, the unit of /proc/<pid>/stat starttime.
// ponytail: 100 on every mainstream Linux build; read sysconf(_SC_CLK_TCK)
// (cgo) if a host with another value shows up.
const clockTicks = 100

// ticksDuration converts clock ticks without overflow: the multiplication
// by a whole second first would overflow after about 3 years of uptime.
func ticksDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * (time.Second / clockTicks)
}

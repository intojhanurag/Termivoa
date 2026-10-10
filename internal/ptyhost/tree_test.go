package ptyhost

import (
	"testing"
	"time"
)

func TestTicksDurationDoesNotOverflow(t *testing.T) {
	// 4 years of uptime in USER_HZ ticks.
	ticks := int64(4 * 365 * 24 * 3600 * clockTicks)
	if got, want := ticksDuration(ticks), 4*365*24*time.Hour; got != want {
		t.Fatalf("ticksDuration = %v, want %v", got, want)
	}
}

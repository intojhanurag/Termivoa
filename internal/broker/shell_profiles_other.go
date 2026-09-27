//go:build !windows

package broker

// wslDistroRegistered is Windows-only; other hosts never offer WSL.
func wslDistroRegistered() bool { return false }

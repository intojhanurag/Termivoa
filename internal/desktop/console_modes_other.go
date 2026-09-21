//go:build !windows

package desktop

func newDisplayModes(uintptr) displayModes { return nil }

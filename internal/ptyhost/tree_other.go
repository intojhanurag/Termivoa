//go:build !darwin && !linux

package ptyhost

import "context"

// ProcessDirectory is not read here: the Chat view is macOS and Linux only.
func ProcessDirectory(context.Context, int) (string, error) { return "", nil }

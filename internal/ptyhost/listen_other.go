//go:build !darwin && !linux && !windows

package ptyhost

import "context"

// sessionListeners is not implemented here; no port is ever previewable.
func sessionListeners(context.Context, platformProcessTree) ([]int, []Listener, error) {
	return nil, nil, nil
}

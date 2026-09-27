//go:build !windows

package ptyhost

import "runtime"

// hostEnvironment keeps only the allowlisted host variables.
func hostEnvironment(host []string) []string {
	return inheritedEnvironment(host, runtime.GOOS)
}

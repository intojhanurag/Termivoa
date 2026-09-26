//go:build dragonfly || freebsd || netbsd || openbsd || solaris

package ptyhost

// sessionMembers is not implemented on hosts outside the supported set; the
// managed process group is still signaled.
func sessionMembers(int) ([]int, error) { return nil, nil }

//go:build !windows

package tailnet

import (
	"os"
	"path/filepath"
	"syscall"
)

// safeBinary accepts an existing regular file only when it, the folder it is
// listed in and, through symlinks, the real file's folder are owned by root
// or the current user and writable by neither group nor others. Otherwise
// another account could replace the tailscale the broker runs.
func safeBinary(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return false
	}
	for _, name := range []string{resolved, filepath.Dir(resolved), filepath.Dir(path)} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm()&0o022 != 0 {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 && int(stat.Uid) != os.Getuid() {
			return false
		}
	}
	return true
}

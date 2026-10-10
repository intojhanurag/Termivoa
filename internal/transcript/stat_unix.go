//go:build darwin || linux

package transcript

import (
	"io/fs"
	"os"
	"syscall"
)

// fileID returns the device and inode of a file, and whether it is safe to
// read: owned by this user, one name only, not writable by group or others
// (review S5).
func fileID(info fs.FileInfo) (dev, ino uint64, safe bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	safe = int(stat.Uid) == os.Getuid() && stat.Nlink == 1 && info.Mode().Perm()&0o022 == 0
	return uint64(stat.Dev), stat.Ino, safe //nolint:unconvert // Dev is int32 on darwin
}

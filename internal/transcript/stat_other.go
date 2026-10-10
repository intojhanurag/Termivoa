//go:build !darwin && !linux

package transcript

import "io/fs"

// fileID refuses every file: Chat view is unsupported off macOS and Linux.
func fileID(fs.FileInfo) (dev, ino uint64, safe bool) { return 0, 0, false }

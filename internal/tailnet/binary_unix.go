//go:build !windows

package tailnet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// maxLinks bounds the symlink chain checkBinary follows.
const maxLinks = 40

// checkBinary accepts an existing regular file only when another account
// cannot replace the tailscale the broker runs: the file, every link in its
// symlink chain, and every folder up to / on the way to each of them (as
// listed and as resolved) must be owned by root or the current user, and no
// folder may be writable by group or others unless it has the sticky bit.
// The file itself may not be writable by group or others at all. A folder
// writable only by group root, or by group admin on macOS, passes: members
// of those groups can already become root.
func checkBinary(path string) error {
	for range maxLinks {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err := checkOwner(path, info); err != nil {
			return err
		}
		for _, folder := range []string{filepath.Dir(path), resolved(filepath.Dir(path))} {
			if err := checkFolders(folder); err != nil {
				return err
			}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", path)
			}
			if info.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("%s is writable by group or others", path)
			}
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			// The kernel follows ".." from the real folder, not the listed one.
			target = filepath.Join(resolved(filepath.Dir(path)), target)
		}
		path = target
	}
	return errors.New("too many symlinks")
}

// resolved is folder with every symlink resolved, or folder when that fails
// (the Lstat that follows reports the error).
func resolved(folder string) string {
	if real, err := filepath.EvalSymlinks(folder); err == nil {
		return real
	}
	return folder
}

// checkFolders checks folder and each parent up to /. Lstat checks who owns
// the entry (a symlink's owner can replace it in a sticky folder); Stat checks
// the folder it leads to.
func checkFolders(folder string) error {
	for {
		info, err := os.Lstat(folder)
		if err != nil {
			return err
		}
		if err := checkOwner(folder, info); err != nil {
			return err
		}
		if info, err = os.Stat(folder); err != nil {
			return err
		}
		if err := checkOwner(folder, info); err != nil {
			return err
		}
		if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 && !trustedGroupOnly(info) {
			return fmt.Errorf("%s is writable by group or others", folder)
		}
		parent := filepath.Dir(folder)
		if parent == folder {
			return nil
		}
		folder = parent
	}
}

func checkOwner(name string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by another user", name)
	}
	return nil
}

// trustedGroupOnly: writable by group root (gid 0), or admin (gid 80) on
// macOS, and not by others. /Applications is root:admin 0775.
func trustedGroupOnly(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().Perm()&0o002 == 0 && (stat.Gid == 0 || runtime.GOOS == "darwin" && stat.Gid == 80)
}

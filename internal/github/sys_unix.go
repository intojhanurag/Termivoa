//go:build darwin || linux

package github

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// ownedByMe: the file's owner is the user this broker runs as.
func ownedByMe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

// freeSpace is the space an unprivileged user can still write at path.
func freeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// ownGroup starts command in its own session with no terminal, and makes
// its context cancel send SIGTERM to the whole group (git and its
// git-remote-https child). WaitDelay then kills git.
func ownGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
}

// killGroup ends what is left of command's group.
func killGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}

// openNoFollow makes an open fail on a symlink.
const openNoFollow = syscall.O_NOFOLLOW

// renameNoReplace is the system's exclusive rename (a seam for tests).
var renameNoReplace = renameNoReplaceSystem

// renameExclusive renames from to to and fails with EEXIST when to exists
// (os.Rename would replace an empty folder). A file system without the
// exclusive rename gets a check and a plain rename.
func renameExclusive(from, to string) error {
	err := renameNoReplace(from, to)
	if !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOSYS) &&
		!errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EOPNOTSUPP) {
		return err
	}
	// ponytail: a folder made at to between this Lstat and the rename is
	// replaced when it is empty; only on file systems without
	// RENAME_EXCL/RENAME_NOREPLACE (some network and FUSE ones).
	if _, statErr := os.Lstat(to); !errors.Is(statErr, os.ErrNotExist) {
		return syscall.EEXIST
	}
	return os.Rename(from, to)
}

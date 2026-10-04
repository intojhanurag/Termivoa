//go:build !windows

package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// checkSocket is design S3 before every dial: a socket owned by the current
// user with no group or other access, in a folder owned by the user that
// group and others cannot write, whose parent chain up to / passes the
// folder check of the binary lookup (tailnet.CheckFolders). After connect the
// caller also checks the peer credential.
func checkSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("herdr: %s is not a socket", path)
	}
	if err := ownedByUser(info); err != nil || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("herdr: socket %s is not private to this user", path)
	}
	folder := filepath.Dir(path)
	folderInfo, err := os.Lstat(folder)
	if err != nil {
		return err
	}
	if !folderInfo.IsDir() || ownedByUser(folderInfo) != nil || folderInfo.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("herdr: socket folder %s is not private to this user", folder)
	}
	return tailnet.CheckFolders(folder)
}

func ownedByUser(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("herdr: %s is not owned by this user", info.Name())
	}
	return nil
}

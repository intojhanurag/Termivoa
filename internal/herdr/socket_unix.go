//go:build !windows

package herdr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// checkSocket is design S3 before every dial: a socket owned by the current
// user with no group or other access, in a folder owned by the user that
// group and others cannot write, whose parent chain up to / passes the
// folder check of the binary lookup (tailnet.CheckFolders). After connect the
// caller also checks the peer credential.
//
// A missing socket is a plain error (herdr is not running). Every other
// failure is a *socketRefused whose text names the fix where the user can
// make it. A group-writable folder is refused even when the group is the
// user's private group (Ubuntu's umask 002 makes ~/.config 0775): Termivoa
// cannot prove that a group has no other members (docs/herdr-provider.md,
// section 21).
func checkSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		return &socketRefused{err.Error()}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return &socketRefused{path + " is not a socket"}
	}
	if err := ownedByUser(info); err != nil {
		return &socketRefused{path + " belongs to another user"}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return &socketRefused{fmt.Sprintf("other users can open %s; run: chmod 600 %s", path, path)}
	}
	folder := filepath.Dir(path)
	folderInfo, err := os.Lstat(folder)
	if err != nil {
		return &socketRefused{err.Error()}
	}
	if !folderInfo.IsDir() || ownedByUser(folderInfo) != nil {
		return &socketRefused{folder + " is not this user's folder"}
	}
	// Every folder of the chain this user owns and could fix at once.
	var open []string
	for current := folder; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil && ownedByUser(info) == nil && info.Mode().Perm()&0o022 != 0 &&
			(current == folder || info.Mode()&os.ModeSticky == 0) {
			open = append(open, current)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if len(open) > 0 {
		return &socketRefused{fmt.Sprintf("group or others can write %s; run: chmod go-w %s",
			strings.Join(open, " and "), strings.Join(open, " "))}
	}
	if err := tailnet.CheckFolders(folder); err != nil {
		return &socketRefused{err.Error()}
	}
	// Also the chain behind a link (~/.config linked into another folder), as the binary check does.
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return &socketRefused{err.Error()}
	}
	if resolved != folder {
		if err := tailnet.CheckFolders(resolved); err != nil {
			return &socketRefused{err.Error()}
		}
	}
	return nil
}

func ownedByUser(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("herdr: %s is not owned by this user", info.Name())
	}
	return nil
}

// noTerminal runs a herdr probe in its own session: it has no controlling
// terminal, so it cannot start herdr's UI.
func noTerminal(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

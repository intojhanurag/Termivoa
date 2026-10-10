//go:build darwin || linux

package ipc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func privateUnixFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("synthetic non-secret fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireUnixMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("mode = %04o, want %04o", info.Mode().Perm(), mode)
	}
}

func TestPrivateFileRefusesUnsafeObjectsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			outside, path := filepath.Join(dir, "outside"), filepath.Join(dir, "private")
			privateUnixFixture(t, outside)
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(outside, path)
			case "hardlink":
				err = os.Link(outside, path)
			case "directory":
				err = os.Mkdir(path, 0o755)
			case "fifo":
				err = unix.Mkfifo(path, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := EnsurePrivateFile(path); err == nil {
				t.Fatal("unsafe object accepted")
			}
			requireUnixMode(t, outside, 0o644)
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if before.Mode() != after.Mode() || !os.SameFile(before, after) {
				t.Fatal("refusal mutated unsafe object")
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "synthetic non-secret fixture" {
				t.Fatal("refusal changed outside contents")
			}
		})
	}
}

func TestPrivateFileRepairsOwnedObjectWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	privateUnixFixture(t, path)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("repair replaced object")
	}
	requireUnixMode(t, path, 0o600)
	if err := validatePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateFile(path); err != nil {
		t.Fatal("already private object rejected", err)
	}
}

func TestPrivateFileReplacementCannotRedirectRepair(t *testing.T) {
	for _, replacement := range []string{"regular", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			dir := t.TempDir()
			path, outside, original := filepath.Join(dir, "private"), filepath.Join(dir, "outside"), filepath.Join(dir, "original")
			privateUnixFixture(t, path)
			privateUnixFixture(t, outside)
			fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(fd), path)
			defer file.Close()
			if err := os.Rename(path, original); err != nil {
				t.Fatal(err)
			}
			if replacement == "regular" {
				privateUnixFixture(t, path)
			} else if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			if err := securePrivateUnixFile(file, path); err == nil {
				t.Fatal("replaced path accepted")
			}
			requireUnixMode(t, outside, 0o644)
			if replacement == "regular" {
				requireUnixMode(t, path, 0o644)
			}
			requireUnixMode(t, original, 0o600)
		})
	}
}

type privateForeignOwnerInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func TestPrivateFileHardLinkAddedAfterOpenRefusesRepair(t *testing.T) {
	dir := t.TempDir()
	path, outside := filepath.Join(dir, "private"), filepath.Join(dir, "outside")
	privateUnixFixture(t, path)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Link(path, outside); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateUnixFile(file, path); err == nil {
		t.Fatal("new shared link accepted")
	}
	requireUnixMode(t, path, 0o644)
	requireUnixMode(t, outside, 0o644)
}

func (info privateForeignOwnerInfo) Sys() any { return &info.stat }

func TestPrivateFileRejectsForeignOwnerBeforeRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	privateUnixFixture(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid++
	if err := validatePrivateUnixObject(privateForeignOwnerInfo{info, stat}); err == nil {
		t.Fatal("foreign owner accepted")
	}
	requireUnixMode(t, path, 0o644)
}

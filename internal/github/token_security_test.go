//go:build darwin || linux

package github

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenFileUnsafeLinksDoNotChangeOutsidePermissions(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(dir, "outside")
			if err := os.WriteFile(outside, []byte("synthetic non-secret fixture"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(outside, 0o644); err != nil {
				t.Fatal(err)
			}
			dataDir := filepath.Join(dir, "data")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dataDir, tokenFileName)
			var err error
			if kind == "symlink" {
				err = os.Symlink(outside, path)
			} else {
				err = os.Link(outside, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loadAccount(dataDir); err == nil {
				t.Fatal("unsafe token file accepted")
			}
			info, err := os.Stat(outside)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Fatalf("outside mode = %04o, want 0644", info.Mode().Perm())
			}
		})
	}
}

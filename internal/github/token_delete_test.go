package github

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDeleteAccountPreservesOtherFileNames(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink", "replaced-hardlink", "replaced-symlink"} {
		t.Run(kind, func(t *testing.T) {
			// One parent keeps both entries on the same volume, including NTFS.
			root := t.TempDir()
			dir := filepath.Join(root, "data")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "outside")
			contents := []byte(`{"login":"fixture","accessToken":"fixture-access"}`)
			if err := os.WriteFile(outside, contents, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, tokenFileName)
			link := path
			if kind == "replaced-hardlink" || kind == "replaced-symlink" {
				if err := saveAccount(dir, &account{login: "fixture", access: newSecret("obsolete-fixture")}); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(dir, "replacement")
			}
			var err error
			if kind == "hardlink" || kind == "replaced-hardlink" {
				err = os.Link(outside, link)
			} else {
				err = os.Symlink(outside, link)
			}
			if err != nil {
				if runtime.GOOS == "windows" && os.IsPermission(err) && (kind == "symlink" || kind == "replaced-symlink") {
					t.Skip("Windows symlink privilege unavailable; native symlink gate remains pending")
				}
				t.Fatal(err)
			}
			if link != path {
				if err := os.Rename(link, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := deleteAccount(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("stored-login entry still exists: %v", err)
			}
			after, err := os.ReadFile(outside)
			if err != nil || !bytes.Equal(after, contents) {
				t.Fatalf("outside file unchanged=%v error=%v", bytes.Equal(after, contents), err)
			}
			if err := deleteAccount(dir); err != nil {
				t.Fatalf("repeated removal: %v", err)
			}
		})
	}
}

func TestDeleteAccountHandlesNormalMissingAndInvalidEntries(t *testing.T) {
	for _, kind := range []string{"normal", "missing", "damaged-file", "empty-directory", "nonempty-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tokenFileName)
			switch kind {
			case "normal":
				if err := saveAccount(dir, &account{login: "fixture", access: newSecret("fixture-access")}); err != nil {
					t.Fatal(err)
				}
			case "damaged-file":
				if err := os.WriteFile(path, []byte("damaged fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty-directory", "nonempty-directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "nonempty-directory" {
					if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := deleteAccount(dir)
			if kind == "nonempty-directory" {
				if err == nil {
					t.Fatal("nonempty directory silently removed")
				}
				if contents, err := os.ReadFile(filepath.Join(path, "keep")); err != nil || string(contents) != "keep" {
					t.Fatal("failed removal changed directory contents")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if acc, err := loadAccount(dir); err != nil || acc != nil {
				t.Fatalf("removed login did not load as absent: present=%v error=%v", acc != nil, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("removed entry still present: %v", err)
			}
		})
	}
}

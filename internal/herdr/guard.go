package herdr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardTestConfig is design S16: in a test binary every path a Config names
// must sit in a temp folder, so no test can run the owner's herdr or dial
// his socket. It panics, which fails the test that built the config.
func guardTestConfig(config Config) {
	if !testing.Testing() {
		return
	}
	paths := append([]string{config.Home}, config.BinaryPaths...)
	paths = append(paths, filepath.SplitList(config.PathEnv)...)
	for _, path := range paths {
		if !insideTemp(path) {
			panic(fmt.Sprintf("herdr: test config names %q outside a temp folder (design S16)", path))
		}
	}
}

// insideTemp resolves every symlink first and then requires the resolved
// path inside a resolved temp folder: a link in /tmp to the real home or a
// real herdr is outside.
func insideTemp(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	resolved := resolveExisting(path)
	for _, root := range []string{os.TempDir(), "/tmp"} {
		if root, err := filepath.EvalSymlinks(root); err == nil && resolved != "" &&
			strings.HasPrefix(resolved, filepath.Clean(root)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveExisting resolves the symlinks of path's longest existing prefix;
// the rest does not exist yet, so it holds no symlink. "" when a part exists
// but cannot be resolved (a dangling link).
func resolveExisting(path string) string {
	path, rest := filepath.Clean(path), ""
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(resolved, rest)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return ""
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		rest, path = filepath.Join(filepath.Base(path), rest), parent
	}
}

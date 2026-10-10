package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardRoot: in a test binary the agent folder must sit in a temp folder,
// so no test can open the owner's real transcripts (~/.claude). It panics,
// which fails the test that named the folder.
func guardRoot(root string) {
	if testing.Testing() && !insideTemp(root) {
		panic(fmt.Sprintf("transcript: test names agent folder %q outside a temp folder", root))
	}
}

// insideTemp resolves every symlink first and then requires the resolved
// path inside a resolved temp folder: a link in /tmp to the real home is
// outside.
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

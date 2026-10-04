package herdr

import (
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

func insideTemp(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	forms := []string{filepath.Clean(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		forms = append(forms, resolved)
	}
	roots := []string{os.TempDir(), "/tmp"}
	for _, root := range roots[:2] {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			roots = append(roots, resolved)
		}
	}
	for _, form := range forms {
		for _, root := range roots {
			if root = filepath.Clean(root); strings.HasPrefix(form, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

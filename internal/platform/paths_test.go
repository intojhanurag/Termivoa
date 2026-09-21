package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProfilePathBoundaryRejectsCaseAliases(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case-insensitive profile boundary is required on macOS and Windows")
	}
	root := t.TempDir()
	production := filepath.Join(root, "Example Parent", "Phone Terminal")
	if err := os.MkdirAll(production, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalProduction, err := canonicalComparisonPath(production)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "ancestor", path: filepath.Join(root, "example parent", "Phone Terminal")},
		{name: "final component", path: filepath.Join(root, "Example Parent", "phone terminal")},
	} {
		t.Run(test.name, func(t *testing.T) {
			alias, err := canonicalComparisonPath(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if !samePath(alias, canonicalProduction) {
				t.Fatal("case alias escaped the production profile boundary")
			}
			for _, candidate := range []string{alias, filepath.Join(alias, "child"), filepath.Dir(alias)} {
				if !pathsOverlap(candidate, canonicalProduction) || !pathsOverlap(canonicalProduction, candidate) {
					t.Fatalf("overlapping case alias %q escaped the production profile boundary", candidate)
				}
			}
		})
	}
	development, err := canonicalComparisonPath(filepath.Join(root, "Example Parent", "Phone Terminal Development"))
	if err != nil {
		t.Fatal(err)
	}
	if pathsOverlap(development, canonicalProduction) {
		t.Fatal("separate development sibling was rejected as overlapping production")
	}
}

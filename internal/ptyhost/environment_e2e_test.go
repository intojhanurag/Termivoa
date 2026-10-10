//go:build e2e

package ptyhost

import (
	"slices"
	"testing"
)

func TestE2ESessionInheritsFakeGitHub(t *testing.T) {
	host := []string{"HOME=/h", "PTERM_E2E_GITHUB=http://127.0.0.1:4000", "PTERM_E2E_GITHUB_CLIENT=Iv1.e2e"}
	for _, goos := range []string{"darwin", "linux"} {
		got := inheritedEnvironment(host, goos)
		if !slices.Equal(got, host[:2]) {
			t.Fatalf("%s: inherited = %q, want %q", goos, got, host[:2])
		}
	}
}

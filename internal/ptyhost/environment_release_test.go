//go:build !e2e

package ptyhost

import "testing"

func TestReleaseSessionDropsFakeGitHub(t *testing.T) {
	host := []string{"HOME=/h", "PTERM_E2E_GITHUB=http://127.0.0.1:4000"}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if got := inheritedEnvironment(host, goos); len(got) > 1 || (len(got) == 1 && got[0] != "HOME=/h") {
			t.Fatalf("%s: inherited = %q, want no PTERM_E2E_GITHUB", goos, got)
		}
	}
}

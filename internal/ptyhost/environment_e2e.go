//go:build e2e

package ptyhost

// In e2e builds only (the multi-computer harness), a session also inherits
// PTERM_E2E_GITHUB, the fake GitHub's address: git in a session runs
// `pterm github-credential`, which answers only for that host in an e2e
// build. Production builds do not compile this file; their helper answers
// for github.com, which needs no variable.
func init() {
	unixInheritedNames = append(unixInheritedNames, "PTERM_E2E_GITHUB")
}

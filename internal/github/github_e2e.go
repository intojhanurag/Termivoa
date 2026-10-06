//go:build e2e

package github

import "os"

// In e2e builds only (the multi-computer harness), PTERM_E2E_GITHUB is the
// base URL of the harness's fake GitHub (for example http://127.0.0.1:41234):
// it serves the login endpoints, the API and the clones. http and loopback
// addresses are allowed for the client and for git. PTERM_E2E_GITHUB_CLIENT
// and PTERM_E2E_GITHUB_APP give the app's client ID and slug. Production
// builds do not compile this file, so nothing can change the hosts there.
func init() {
	if base := os.Getenv("PTERM_E2E_GITHUB"); base != "" {
		defaultOptions.web, defaultOptions.api, defaultOptions.git = base, base, base
		defaultOptions.open = true
	}
	if client := os.Getenv("PTERM_E2E_GITHUB_CLIENT"); client != "" {
		ClientID = client
	}
	if app := os.Getenv("PTERM_E2E_GITHUB_APP"); app != "" {
		AppSlug = app
	}
}

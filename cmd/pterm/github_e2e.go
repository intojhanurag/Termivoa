//go:build e2e

package main

import (
	"net/url"
	"os"
)

// githubE2EHost is, in e2e builds only, the host:port of the fake GitHub in
// PTERM_E2E_GITHUB (a base URL such as https://127.0.0.1:PORT): the
// credential helper also answers for it. Production builds do not compile
// this file.
func githubE2EHost() string {
	base, err := url.Parse(os.Getenv("PTERM_E2E_GITHUB"))
	if err != nil {
		return ""
	}
	return base.Host
}

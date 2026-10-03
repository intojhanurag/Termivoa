//go:build e2e

package main

import "os"

// In e2e builds only (scripts/e2e-multicomputer, check F-setup), `pterm
// setup` runs against a development stack: with PTERM_E2E_GATEWAY set, it
// checks that gateway URL instead of the installed one on 127.0.0.1:8787,
// which the harness sandbox blocks. Everything else setup does stays the
// production code. Production builds do not compile this file.
func init() {
	gateway := os.Getenv("PTERM_E2E_GATEWAY")
	if gateway == "" {
		return
	}
	load := loadLifecycleEnv
	loadLifecycleEnv = func() (lifecycleEnv, error) {
		env, err := load()
		env.gateway = gateway
		return env, err
	}
}

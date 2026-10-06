//go:build !e2e

package main

// githubE2EHost: a release build answers for github.com only.
func githubE2EHost() string { return "" }

// Package ptyhost isolates native PTY and process-lifecycle behavior.
//
// A Process owns one pseudo-terminal and the command attached to it. Commands
// are always started from an executable plus an argument slice; this package
// never constructs a shell command. The context passed to Start represents the
// broker-owned session lifetime, not a viewer or request lifetime.
package ptyhost

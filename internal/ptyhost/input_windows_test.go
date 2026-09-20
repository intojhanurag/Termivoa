//go:build windows

package ptyhost

// ConPTY's input stream represents the Enter key as carriage return. The
// attached console translates it for line-oriented child reads.
var fixtureEnter = []byte{'\r'}

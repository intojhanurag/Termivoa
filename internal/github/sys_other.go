//go:build !darwin && !linux

package github

import (
	"errors"
	"os"
	"os/exec"
)

// New never builds the service here; these keep the package compiling.

var errUnsupported = errors.New("github: not supported on this system")

func ownedByMe(os.FileInfo) bool           { return false }
func freeSpace(string) (uint64, error)     { return 0, errUnsupported }
func ownGroup(*exec.Cmd)                   {}
func killGroup(*exec.Cmd)                  {}
func renameExclusive(string, string) error { return errUnsupported }

// openNoFollow: no flag here (New never builds the service).
const openNoFollow = 0

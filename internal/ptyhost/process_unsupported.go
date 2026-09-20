//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package ptyhost

import (
	"errors"
	"os/exec"
)

type platformProcessTree struct{}

func configureCommand(*exec.Cmd) {}

func attachProcessTree(*exec.Cmd) (platformProcessTree, error) {
	return platformProcessTree{}, errors.New("ptyhost: process lifecycle is unsupported on this platform")
}

func cleanupUnattachedProcess(cmd *exec.Cmd) (bool, error) {
	err := ignoreProcessDone(cmd.Process.Kill())
	return err == nil, err
}

func terminateProcessTree(platformProcessTree) error {
	return errors.New("ptyhost: process lifecycle is unsupported on this platform")
}

func forceTerminateProcessTree(platformProcessTree) error {
	return errors.New("ptyhost: process lifecycle is unsupported on this platform")
}

func cleanupExitedProcessTree(platformProcessTree) error { return nil }

func closeProcessTree(platformProcessTree) error { return nil }

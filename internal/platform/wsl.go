package platform

import (
	"os"
	"runtime"
	"strings"
)

// IsWSL reports whether this Linux runs inside Windows Subsystem for Linux.
func IsWSL() bool {
	return runtime.GOOS == "linux" && isWSL(os.Getenv, os.ReadFile)
}

// isWSL checks the variable WSL sets for every process, the interop handler
// WSL registers (WSLInterop, or WSLInterop-late on newer releases), and the
// kernel version string Microsoft builds carry.
func isWSL(getenv func(string) string, readFile func(string) ([]byte, error)) bool {
	if getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	for _, interop := range []string{"/proc/sys/fs/binfmt_misc/WSLInterop", "/proc/sys/fs/binfmt_misc/WSLInterop-late"} {
		if _, err := readFile(interop); err == nil {
			return true
		}
	}
	version, err := readFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(version)), "microsoft")
}

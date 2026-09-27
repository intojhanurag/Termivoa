package broker

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// wslDistroRegistered reports whether the current user has a WSL distro. WSL
// registers each distro as a {GUID} subkey of Lxss; reading the registry
// avoids running wsl.exe, which can be slow or start the WSL VM.
func wslDistroRegistered() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Lxss`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false
	}
	defer key.Close()
	names, _ := key.ReadSubKeyNames(0)
	for _, name := range names {
		if strings.HasPrefix(name, "{") {
			return true
		}
	}
	return false
}

package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const applicationDirectoryName = "Phone Terminal"

// Paths are the per-user persistent and runtime locations used by the two
// installed companion processes. Callers may override them only through the
// explicit development profile in cmd/pterm.
type Paths struct {
	DataDir    string
	RuntimeDir string
}

func DefaultPaths() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("host: resolve user application-data directory: %w", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("host: resolve user runtime directory: %w", err)
	}
	if configDir == "" || cacheDir == "" {
		return Paths{}, errors.New("host: operating system returned an empty user directory")
	}
	dataBase := configDir
	if runtime.GOOS == "windows" {
		dataBase = cacheDir
	}
	return Paths{
		DataDir:    filepath.Join(dataBase, applicationDirectoryName),
		RuntimeDir: filepath.Join(cacheDir, applicationDirectoryName, "runtime"),
	}, nil
}

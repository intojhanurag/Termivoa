package host

import "github.com/intojhanurag/phone-terminal/internal/platform"

// Paths are the per-user persistent and runtime locations used by the two
// installed companion processes. Callers may override them only through the
// explicit development profile in cmd/pterm.
type Paths = platform.Paths
type Profile = platform.Profile

const (
	ProductionProfile  = platform.ProductionProfile
	DevelopmentProfile = platform.DevelopmentProfile
)

func DefaultPaths() (Paths, error) {
	return platform.DefaultPaths()
}

func DevelopmentPaths() (Paths, error) {
	return platform.DevelopmentPaths()
}

func ValidatePaths(profile Profile, paths Paths) (Paths, error) {
	return platform.ValidatePaths(profile, paths)
}

func ValidateRuntimeDir(profile Profile, path string) (string, error) {
	return platform.ValidateRuntimeDir(profile, path)
}

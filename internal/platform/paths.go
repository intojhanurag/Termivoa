package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	applicationDirectoryName            = "Phone Terminal"
	developmentApplicationDirectoryName = "Phone Terminal Development"
	// Linux uses lower-case XDG directory names.
	linuxApplicationDirectoryName            = "phone-terminal"
	linuxDevelopmentApplicationDirectoryName = "phone-terminal-development"
)

// Profile identifies whether process paths belong to the installed product or
// an explicitly isolated development instance.
type Profile string

const (
	ProductionProfile  Profile = "production"
	DevelopmentProfile Profile = "development"
)

type Paths struct {
	DataDir    string
	RuntimeDir string
	// LogDir holds the broker and gateway logs of the installed product. It is
	// set by DefaultPaths and DevelopmentPaths only; ValidatePaths leaves it
	// empty because only startup registration uses it.
	LogDir string
}

func DefaultPaths() (Paths, error) {
	if runtime.GOOS == "linux" {
		return linuxDefaultPaths(linuxApplicationDirectoryName)
	}
	return pathsForDirectory(applicationDirectoryName)
}

func DevelopmentPaths() (Paths, error) {
	if runtime.GOOS == "linux" {
		return linuxDefaultPaths(linuxDevelopmentApplicationDirectoryName)
	}
	return pathsForDirectory(developmentApplicationDirectoryName)
}

func linuxDefaultPaths(directory string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("platform: resolve home directory: %w", err)
	}
	if !filepath.IsAbs(home) {
		return Paths{}, fmt.Errorf("platform: home directory %q is not absolute", home)
	}
	return linuxPaths(directory, os.Getenv, home, os.Getuid(), func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.IsDir()
	}), nil
}

// linuxPaths follows the XDG Base Directory specification: data in
// $XDG_DATA_HOME (~/.local/share) and logs in $XDG_STATE_HOME
// (~/.local/state); relative values are ignored, as the specification says.
// The runtime directory, which holds the sockets every pterm process must
// agree on, prefers systemd's /run/user/<uid> over $XDG_RUNTIME_DIR: they are
// normally the same, but su/sudo shells lack the variable and WSLg points it
// elsewhere, while the systemd user service always uses /run/user/<uid>.
// Without either, it falls back to a directory inside the private data
// directory. The IPC layer checks ownership and permissions of whichever is
// used when it opens it.
func linuxPaths(directory string, getenv func(string) string, home string, uid int, isDir func(string) bool) Paths {
	base := func(variable, fallback string) string {
		if value := getenv(variable); filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Join(home, fallback)
	}
	paths := Paths{
		DataDir: filepath.Join(base("XDG_DATA_HOME", ".local/share"), directory),
		LogDir:  filepath.Join(base("XDG_STATE_HOME", ".local/state"), directory, "logs"),
	}
	systemdRuntime := "/run/user/" + strconv.Itoa(uid)
	switch runtimeBase := getenv("XDG_RUNTIME_DIR"); {
	case uid >= 0 && isDir(systemdRuntime):
		paths.RuntimeDir = filepath.Join(systemdRuntime, directory)
	case filepath.IsAbs(runtimeBase):
		paths.RuntimeDir = filepath.Join(filepath.Clean(runtimeBase), directory)
	default:
		paths.RuntimeDir = filepath.Join(paths.DataDir, "runtime")
	}
	return paths
}

func pathsForDirectory(directory string) (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("platform: resolve user application-data directory: %w", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("platform: resolve user runtime directory: %w", err)
	}
	if configDir == "" || cacheDir == "" {
		return Paths{}, errors.New("platform: operating system returned an empty user directory")
	}
	dataBase := configDir
	if runtime.GOOS == "windows" {
		dataBase = cacheDir
	}
	return Paths{
		DataDir:    filepath.Join(dataBase, directory),
		RuntimeDir: filepath.Join(cacheDir, directory, "runtime"),
		LogDir:     filepath.Join(dataBase, directory, "logs"),
	}, nil
}

// ValidatePaths keeps the production profile fixed and ensures development
// paths neither reuse nor contain any production data. Canonical comparison
// resolves aliases before testing overlap; the original clean absolute paths
// are returned for the stricter platform ownership checks at open time.
func ValidatePaths(profile Profile, paths Paths) (Paths, error) {
	if err := validateProfile(profile); err != nil {
		return Paths{}, err
	}
	dataDir, err := validateAbsoluteClean(paths.DataDir, "data")
	if err != nil {
		return Paths{}, err
	}
	runtimeDir, err := validateAbsoluteClean(paths.RuntimeDir, "runtime")
	if err != nil {
		return Paths{}, err
	}
	if err := validateProfilePath(profile, dataDir, "data"); err != nil {
		return Paths{}, err
	}
	if err := validateProfilePath(profile, runtimeDir, "runtime"); err != nil {
		return Paths{}, err
	}
	return Paths{DataDir: dataDir, RuntimeDir: runtimeDir}, nil
}

func ValidateRuntimeDir(profile Profile, path string) (string, error) {
	if err := validateProfile(profile); err != nil {
		return "", err
	}
	runtimeDir, err := validateAbsoluteClean(path, "runtime")
	if err != nil {
		return "", err
	}
	if err := validateProfilePath(profile, runtimeDir, "runtime"); err != nil {
		return "", err
	}
	return runtimeDir, nil
}

func validateProfile(profile Profile) error {
	if profile != ProductionProfile && profile != DevelopmentProfile {
		return errors.New("platform: profile must be production or development")
	}
	return nil
}

func validateProfilePath(profile Profile, path, kind string) error {
	production, err := DefaultPaths()
	if err != nil {
		return err
	}
	canonical, err := canonicalComparisonPath(path)
	if err != nil {
		return fmt.Errorf("platform: resolve %s path: %w", kind, err)
	}
	productionData, err := canonicalComparisonPath(production.DataDir)
	if err != nil {
		return fmt.Errorf("platform: resolve production data path: %w", err)
	}
	productionRuntime, err := canonicalComparisonPath(production.RuntimeDir)
	if err != nil {
		return fmt.Errorf("platform: resolve production runtime path: %w", err)
	}
	if profile == ProductionProfile {
		want := productionRuntime
		if kind == "data" {
			want = productionData
		}
		if !samePath(canonical, want) {
			return fmt.Errorf("platform: production %s path is fixed", kind)
		}
		return nil
	}
	if pathsOverlap(canonical, productionData) || pathsOverlap(canonical, productionRuntime) {
		return fmt.Errorf("platform: development %s path overlaps the production profile", kind)
	}
	return nil
}

func validateAbsoluteClean(path, kind string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("platform: %s path must not be empty", kind)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("platform: resolve %s path: %w", kind, err)
	}
	if filepath.Clean(path) != absolute {
		return "", fmt.Errorf("platform: %s path must be absolute and clean", kind)
	}
	return absolute, nil
}

func canonicalComparisonPath(path string) (string, error) {
	existing := path
	missing := make([]string, 0, 4)
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", errors.New("no existing path ancestor")
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(left, right string) bool {
	left = comparablePath(left)
	right = comparablePath(right)
	if left == right {
		return true
	}
	relation, err := filepath.Rel(left, right)
	if err == nil && relation != ".." && !strings.HasPrefix(relation, ".."+string(filepath.Separator)) {
		return true
	}
	relation, err = filepath.Rel(right, left)
	return err == nil && relation != ".." && !strings.HasPrefix(relation, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	return comparablePath(left) == comparablePath(right)
}

func comparablePath(path string) string {
	path = filepath.Clean(path)
	// macOS commonly uses a case-insensitive filesystem, and EvalSymlinks
	// preserves the spelling of ordinary path components. Conservatively
	// reject case-only profile aliases even on a case-sensitive macOS volume.
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

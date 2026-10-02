package broker

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

type shellCandidate struct {
	name       string
	executable string
	arguments  []string
	platform   store.Platform
}

// hostShellCandidates lists absolute shell executables for the host. macOS
// and Linux use login shells; Windows uses resolved absolute PowerShell
// paths, then WSL when wslDistroRegistered reports a distro. Other platforms
// are not broker hosts and yield nothing.
func hostShellCandidates(
	platform store.Platform, getenv func(string) string, lookPath func(string) (string, error), wslDistroRegistered func() bool,
) []shellCandidate {
	switch platform {
	case store.PlatformDarwin:
		candidates := []shellCandidate{
			{"zsh (login)", "/bin/zsh", []string{"-l"}, platform},
			{"bash (login)", "/bin/bash", []string{"-l"}, platform},
		}
		// macOS paths are POSIX; path (not filepath) keeps host-independent tests.
		// The user's login shell comes first so it is the default choice.
		if shell := path.Clean(getenv("SHELL")); path.IsAbs(shell) {
			index := slices.IndexFunc(candidates, func(candidate shellCandidate) bool { return candidate.executable == shell })
			switch {
			case index > 0:
				candidates[0], candidates[index] = candidates[index], candidates[0]
			case index < 0:
				candidates = append([]shellCandidate{{path.Base(shell) + " (login)", shell, []string{"-l"}, platform}}, candidates...)
			}
		}
		return candidates
	case store.PlatformWindows:
		var candidates []shellCandidate
		root := getenv("SystemRoot")
		rootValid := root != "" && filepath.IsAbs(root)
		if rootValid {
			candidates = append(candidates, shellCandidate{"Windows PowerShell",
				filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoLogo"}, platform})
		}
		if pwsh, err := lookPath("pwsh.exe"); err == nil && filepath.IsAbs(pwsh) {
			candidates = append(candidates, shellCandidate{"PowerShell 7", filepath.Clean(pwsh), []string{"-NoLogo"}, platform})
		}
		// WSL is a separate, explicit profile and never stands in for the
		// native shells above. Windows 11 ships wsl.exe before WSL is set up,
		// so a registered distro is required too. With no arguments wsl.exe
		// starts the default distro's shell in the translated session
		// directory, so the session opens in the chosen project.
		if rootValid && wslDistroRegistered() {
			candidates = append(candidates, shellCandidate{"WSL bash",
				filepath.Join(root, "System32", "wsl.exe"), []string{}, store.PlatformWSL})
		}
		return candidates
	case store.PlatformLinux:
		return linuxShellCandidates(getenv, readEtcShells())
	default:
		return nil
	}
}

// linuxShellCandidates lists login shells for a Linux host, including one
// inside WSL: the user's $SHELL first, then bash, zsh and fish. allowed is
// the /etc/shells list; a shell must be on it, unless the list is missing
// (minimal containers). The -l argument works for all three. saveShellProfiles
// skips paths that do not exist. A built-in candidate is dropped when $SHELL
// is the same program at another path, such as /bin/zsh and /usr/bin/zsh on
// a merged-/usr system.
func linuxShellCandidates(getenv func(string) string, allowed []string) []shellCandidate {
	permitted := func(shell string) bool { return allowed == nil || slices.Contains(allowed, shell) }
	var candidates []shellCandidate
	login := path.Clean(getenv("SHELL"))
	if path.IsAbs(login) && permitted(login) {
		candidates = append(candidates, shellCandidate{path.Base(login) + " (login)", login, []string{"-l"}, store.PlatformLinux})
	}
	for _, shell := range []string{"/bin/bash", "/usr/bin/zsh", "/usr/bin/fish"} {
		if permitted(shell) && !slices.ContainsFunc(candidates, func(candidate shellCandidate) bool {
			return path.Base(candidate.executable) == path.Base(shell)
		}) {
			candidates = append(candidates, shellCandidate{path.Base(shell) + " (login)", shell, []string{"-l"}, store.PlatformLinux})
		}
	}
	return candidates
}

// readEtcShells returns the absolute shell paths in /etc/shells, or nil when
// the file cannot be read.
func readEtcShells() []string {
	data, err := os.ReadFile("/etc/shells")
	if err != nil {
		return nil
	}
	return parseEtcShells(string(data))
}

func parseEtcShells(data string) []string {
	shells := []string{}
	for _, line := range strings.Split(data, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "/") {
			shells = append(shells, path.Clean(line))
		}
	}
	return shells
}

// DetectShellProfiles idempotently saves each existing host shell that is
// not already saved with the same executable and arguments. It never runs a
// shell and never removes or edits existing profiles.
func DetectShellProfiles(ctx context.Context, persistence *store.Store, platform store.Platform, now time.Time) error {
	return saveShellProfiles(ctx, persistence, hostShellCandidates(platform, os.Getenv, exec.LookPath, wslDistroRegistered), now)
}

func saveShellProfiles(ctx context.Context, persistence *store.Store, candidates []shellCandidate, now time.Time) error {
	existing, err := persistence.ListShellProfiles(ctx, protocol.MaxAdminProfiles)
	if err != nil {
		return err
	}
	for index, candidate := range candidates {
		// Lists are ordered by creation time; one millisecond apart keeps the
		// preferred order instead of leaving it to random IDs.
		created := now.UTC().Add(time.Duration(index) * time.Millisecond)
		info, err := os.Stat(candidate.executable)
		if err != nil || !info.Mode().IsRegular() || len(existing) >= protocol.MaxAdminProfiles {
			continue
		}
		if slices.ContainsFunc(existing, func(profile store.ShellProfile) bool {
			return profile.Platform == candidate.platform && profile.ExecutablePath == candidate.executable &&
				slices.Equal(profile.Arguments, candidate.arguments)
		}) {
			continue
		}
		profile := store.ShellProfile{
			ID: uuid.New().String(), DisplayName: candidate.name, Platform: candidate.platform, ExecutablePath: candidate.executable,
			Arguments: candidate.arguments, CreatedAt: created, UpdatedAt: created,
		}
		if err := persistence.PutShellProfile(ctx, profile); err != nil {
			return err
		}
		existing = append(existing, profile)
	}
	return nil
}

package broker

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"time"

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
// uses login shells; Windows uses resolved absolute PowerShell paths, then WSL
// when wslDistroRegistered reports a distro. Other platforms are not v0
// broker hosts and yield nothing.
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
	default:
		return nil
	}
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
		id, err := randomUUID()
		if err != nil {
			return err
		}
		profile := store.ShellProfile{
			ID: id, DisplayName: candidate.name, Platform: candidate.platform, ExecutablePath: candidate.executable,
			Arguments: candidate.arguments, CreatedAt: created, UpdatedAt: created,
		}
		if err := persistence.PutShellProfile(ctx, profile); err != nil {
			return err
		}
		existing = append(existing, profile)
	}
	return nil
}

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
}

// hostShellCandidates lists absolute shell executables for the host. macOS
// uses login shells; Windows uses resolved absolute PowerShell paths. Other
// platforms are not v0 broker hosts and yield nothing.
func hostShellCandidates(platform store.Platform, getenv func(string) string, lookPath func(string) (string, error)) []shellCandidate {
	switch platform {
	case store.PlatformDarwin:
		candidates := []shellCandidate{
			{"zsh (login)", "/bin/zsh", []string{"-l"}},
			{"bash (login)", "/bin/bash", []string{"-l"}},
		}
		// macOS paths are POSIX; path (not filepath) keeps host-independent tests.
		if shell := path.Clean(getenv("SHELL")); path.IsAbs(shell) && shell != "/bin/zsh" && shell != "/bin/bash" {
			candidates = append(candidates, shellCandidate{path.Base(shell) + " (login)", shell, []string{"-l"}})
		}
		return candidates
	case store.PlatformWindows:
		var candidates []shellCandidate
		if root := getenv("SystemRoot"); root != "" && filepath.IsAbs(root) {
			candidates = append(candidates, shellCandidate{"Windows PowerShell",
				filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoLogo"}})
		}
		if pwsh, err := lookPath("pwsh.exe"); err == nil && filepath.IsAbs(pwsh) {
			candidates = append(candidates, shellCandidate{"PowerShell 7", filepath.Clean(pwsh), []string{"-NoLogo"}})
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
	existing, err := persistence.ListShellProfiles(ctx, protocol.MaxAdminProfiles)
	if err != nil {
		return err
	}
	for _, candidate := range hostShellCandidates(platform, os.Getenv, exec.LookPath) {
		info, err := os.Stat(candidate.executable)
		if err != nil || !info.Mode().IsRegular() || len(existing) >= protocol.MaxAdminProfiles {
			continue
		}
		if slices.ContainsFunc(existing, func(profile store.ShellProfile) bool {
			return profile.Platform == platform && profile.ExecutablePath == candidate.executable &&
				slices.Equal(profile.Arguments, candidate.arguments)
		}) {
			continue
		}
		id, err := randomUUID()
		if err != nil {
			return err
		}
		profile := store.ShellProfile{
			ID: id, DisplayName: candidate.name, Platform: platform, ExecutablePath: candidate.executable,
			Arguments: candidate.arguments, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
		}
		if err := persistence.PutShellProfile(ctx, profile); err != nil {
			return err
		}
		existing = append(existing, profile)
	}
	return nil
}

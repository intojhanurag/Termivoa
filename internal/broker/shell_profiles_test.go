package broker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/store"
)

func TestHostShellCandidatesAreAbsolutePerPlatform(t *testing.T) {
	env := map[string]string{"SHELL": "/opt/homebrew/bin/fish", "SystemRoot": t.TempDir()}
	getenv := func(key string) string { return env[key] }
	noPwsh := func(string) (string, error) { return "", errors.New("missing") }
	darwin := hostShellCandidates(store.PlatformDarwin, getenv, noPwsh)
	if len(darwin) != 3 || darwin[0].executable != "/bin/zsh" || !reflect.DeepEqual(darwin[0].arguments, []string{"-l"}) ||
		darwin[2].executable != "/opt/homebrew/bin/fish" {
		t.Fatalf("darwin candidates = %#v", darwin)
	}
	env["SHELL"] = "relative-shell"
	if got := hostShellCandidates(store.PlatformDarwin, getenv, noPwsh); len(got) != 2 {
		t.Fatalf("relative SHELL accepted: %#v", got)
	}
	windows := hostShellCandidates(store.PlatformWindows, getenv, func(string) (string, error) {
		return filepath.Join(env["SystemRoot"], "pwsh", "pwsh.exe"), nil
	})
	if len(windows) != 2 || windows[0].name != "Windows PowerShell" || windows[1].name != "PowerShell 7" {
		t.Fatalf("windows candidates = %#v", windows)
	}
	if got := hostShellCandidates("linux", getenv, noPwsh); got != nil {
		t.Fatalf("unsupported host produced candidates: %#v", got)
	}
}

func TestDetectShellProfilesIsIdempotent(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("requires the macOS system zsh")
	}
	persistence, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "profiles.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	for range 2 {
		if err := DetectShellProfiles(context.Background(), persistence, store.PlatformDarwin, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := persistence.ListShellProfiles(context.Background(), 64)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles = %#v, %v", profiles, err)
	}
	seen := map[string]bool{}
	for _, profile := range profiles {
		if seen[profile.ExecutablePath] || !filepath.IsAbs(profile.ExecutablePath) {
			t.Fatalf("duplicate or relative profile: %#v", profile)
		}
		seen[profile.ExecutablePath] = true
	}
	if !seen["/bin/zsh"] {
		t.Fatal("zsh login profile was not detected")
	}
}

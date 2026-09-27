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
	noWSL := func() bool { return false }
	darwin := hostShellCandidates(store.PlatformDarwin, getenv, noPwsh, noWSL)
	if len(darwin) != 3 || darwin[0].executable != "/opt/homebrew/bin/fish" || !reflect.DeepEqual(darwin[0].arguments, []string{"-l"}) ||
		darwin[1].executable != "/bin/zsh" || darwin[2].executable != "/bin/bash" {
		t.Fatalf("darwin candidates = %#v", darwin)
	}
	env["SHELL"] = "/bin/bash"
	if got := hostShellCandidates(store.PlatformDarwin, getenv, noPwsh, noWSL); len(got) != 2 || got[0].executable != "/bin/bash" || got[1].executable != "/bin/zsh" {
		t.Fatalf("login shell is not first: %#v", got)
	}
	env["SHELL"] = "relative-shell"
	if got := hostShellCandidates(store.PlatformDarwin, getenv, noPwsh, noWSL); len(got) != 2 {
		t.Fatalf("relative SHELL accepted: %#v", got)
	}
	windows := hostShellCandidates(store.PlatformWindows, getenv, func(string) (string, error) {
		return filepath.Join(env["SystemRoot"], "pwsh", "pwsh.exe"), nil
	}, noWSL)
	if len(windows) != 2 || windows[0].name != "Windows PowerShell" || windows[1].name != "PowerShell 7" {
		t.Fatalf("windows candidates = %#v", windows)
	}
	if got := hostShellCandidates("linux", getenv, noPwsh, func() bool { return true }); got != nil {
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

func TestDetectShellProfilesAddsWSLAfterPowerShell(t *testing.T) {
	for _, test := range []struct {
		name   string
		wslExe bool
		distro bool
		want   []string
	}{
		{"distro registered", true, true, []string{"Windows PowerShell", "PowerShell 7", "WSL bash"}},
		{"wsl.exe missing", false, true, []string{"Windows PowerShell", "PowerShell 7"}},
		{"no distro", true, false, []string{"Windows PowerShell", "PowerShell 7"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			pwsh := filepath.Join(root, "pwsh", "pwsh.exe")
			files := []string{filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), pwsh}
			if test.wslExe {
				files = append(files, filepath.Join(root, "System32", "wsl.exe"))
			}
			for _, file := range files {
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			persistence, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "profiles.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer persistence.Close()
			candidates := hostShellCandidates(store.PlatformWindows,
				func(key string) string { return map[string]string{"SystemRoot": root}[key] },
				func(string) (string, error) { return pwsh, nil },
				func() bool { return test.distro })
			for range 2 {
				if err := saveShellProfiles(context.Background(), persistence, candidates, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			profiles, err := persistence.ListShellProfiles(context.Background(), 64)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, profile := range profiles {
				names = append(names, profile.DisplayName)
				wantPlatform := store.PlatformWindows
				if profile.DisplayName == "WSL bash" {
					wantPlatform = store.PlatformWSL
					if profile.ExecutablePath != filepath.Join(root, "System32", "wsl.exe") || len(profile.Arguments) != 0 {
						t.Fatalf("WSL profile = %#v", profile)
					}
				}
				if profile.Platform != wantPlatform {
					t.Fatalf("profile %q platform = %q, want %q", profile.DisplayName, profile.Platform, wantPlatform)
				}
			}
			if !reflect.DeepEqual(names, test.want) {
				t.Fatalf("profiles = %q, want %q", names, test.want)
			}
		})
	}
}

package platform

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxPathsFollowXDG(t *testing.T) {
	for _, test := range []struct {
		name     string
		env      map[string]string
		runUser  bool
		wantData string
		wantLogs string
		wantRun  string
	}{
		{
			name:     "defaults with systemd runtime directory",
			env:      map[string]string{},
			runUser:  true,
			wantData: "/home/ada/.local/share/phone-terminal",
			wantLogs: "/home/ada/.local/state/phone-terminal/logs",
			wantRun:  "/run/user/1000/phone-terminal",
		},
		{
			name:     "XDG variables, WSLg runtime ignored while /run/user exists",
			env:      map[string]string{"XDG_DATA_HOME": "/data/", "XDG_STATE_HOME": "/state", "XDG_RUNTIME_DIR": "/mnt/wslg/runtime-dir"},
			runUser:  true,
			wantData: "/data/phone-terminal",
			wantLogs: "/state/phone-terminal/logs",
			wantRun:  "/run/user/1000/phone-terminal",
		},
		{
			name:     "no systemd: XDG_RUNTIME_DIR",
			env:      map[string]string{"XDG_RUNTIME_DIR": "/tmp/xdg-1000"},
			wantData: "/home/ada/.local/share/phone-terminal",
			wantLogs: "/home/ada/.local/state/phone-terminal/logs",
			wantRun:  "/tmp/xdg-1000/phone-terminal",
		},
		{
			name:     "relative values are ignored; runtime falls back into data",
			env:      map[string]string{"XDG_DATA_HOME": "rel", "XDG_STATE_HOME": "rel", "XDG_RUNTIME_DIR": "rel"},
			wantData: "/home/ada/.local/share/phone-terminal",
			wantLogs: "/home/ada/.local/state/phone-terminal/logs",
			wantRun:  "/home/ada/.local/share/phone-terminal/runtime",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := linuxPaths("phone-terminal", func(key string) string { return test.env[key] }, "/home/ada", 1000,
				func(path string) bool { return test.runUser && path == "/run/user/1000" })
			if paths.DataDir != test.wantData || paths.LogDir != test.wantLogs || paths.RuntimeDir != test.wantRun {
				t.Fatalf("paths = %+v", paths)
			}
			// The socket must fit the portable 103-byte limit with room to spare.
			if len(paths.RuntimeDir+"/broker-management.sock") > 103 {
				t.Fatalf("runtime path too long: %s", paths.RuntimeDir)
			}
		})
	}
}

func TestLinuxDefaultPathsOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real XDG paths only on Linux")
	}
	production, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	development, err := DevelopmentPaths()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(production.DataDir, "/phone-terminal") || !strings.HasSuffix(development.DataDir, "/phone-terminal-development") ||
		!strings.HasSuffix(production.LogDir, "/phone-terminal/logs") {
		t.Fatalf("production %+v development %+v", production, development)
	}
	if pathsOverlap(production.DataDir, development.DataDir) || pathsOverlap(production.RuntimeDir, development.RuntimeDir) {
		t.Fatal("development paths overlap production")
	}
	if _, err := ValidatePaths(ProductionProfile, production); err != nil {
		t.Fatal(err)
	}
}

func TestIsWSL(t *testing.T) {
	files := map[string]string{}
	env := map[string]string{}
	readFile := func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return []byte(data), nil
		}
		return nil, os.ErrNotExist
	}
	getenv := func(key string) string { return env[key] }
	files["/proc/version"] = "Linux version 6.8.0-45-generic (buildd@lcy02-amd64-075) (gcc Ubuntu 13.2.0)"
	if isWSL(getenv, readFile) {
		t.Fatal("plain Ubuntu detected as WSL")
	}
	files["/proc/version"] = "Linux version 5.15.153.1-microsoft-standard-WSL2 (root@941d701f84f1)"
	if !isWSL(getenv, readFile) {
		t.Fatal("WSL2 kernel not detected")
	}
	delete(files, "/proc/version")
	files["/proc/sys/fs/binfmt_misc/WSLInterop-late"] = "enabled"
	if !isWSL(getenv, readFile) {
		t.Fatal("WSL interop not detected")
	}
	delete(files, "/proc/sys/fs/binfmt_misc/WSLInterop-late")
	env["WSL_DISTRO_NAME"] = "Ubuntu-24.04"
	if !isWSL(getenv, readFile) {
		t.Fatal("WSL_DISTRO_NAME not detected")
	}
	if isWSL(func(string) string { return "" }, func(string) ([]byte, error) { return nil, errors.New("no /proc") }) {
		t.Fatal("no evidence detected as WSL")
	}
}

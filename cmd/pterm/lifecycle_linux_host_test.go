//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
)

// systemdFake models `systemctl --user` and `loginctl` for one user and
// records every call.
type systemdFake struct {
	mu      sync.Mutex
	calls   []string
	enabled map[string]bool
	linger  string
}

func (fake *systemdFake) Run(_ context.Context, name string, args ...string) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	fake.calls = append(fake.calls, call)
	switch {
	case name == "loginctl" && args[0] == "show-user":
		return fake.linger + "\n", nil
	case name == "loginctl" && args[0] == "enable-linger":
		fake.linger = "yes"
	case name != "systemctl":
		return "", errors.New("unexpected command " + call)
	case args[1] == "is-enabled":
		if !fake.enabled[args[2]] {
			return "disabled\n", errors.New("exit status 1")
		}
		return "enabled\n", nil
	case args[1] == "enable":
		fake.enabled[args[2]] = true
	case args[1] == "disable":
		delete(fake.enabled, args[len(args)-1])
	}
	return "", nil
}

func (fake *systemdFake) mutations() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var result []string
	for _, call := range fake.calls {
		if !strings.Contains(call, " is-enabled ") && !strings.HasPrefix(call, "loginctl show-user") {
			result = append(result, call)
		}
	}
	return result
}

// newLinuxLifecycleTestEnv points the lifecycle commands at temporary XDG
// directories and fake systemctl/loginctl. It runs on macOS as well; nothing
// real changes.
func newLinuxLifecycleTestEnv(t *testing.T, systemd bool) (lifecycleEnv, *systemdFake) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pterm-linux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	fake := &systemdFake{enabled: map[string]bool{}, linger: "no"}
	paths := host.Paths{
		DataDir:    filepath.Join(root, "share", "phone-terminal"),
		RuntimeDir: filepath.Join(root, "run", "phone-terminal"),
		LogDir:     filepath.Join(root, "state", "phone-terminal", "logs"),
	}
	env := lifecycleEnv{
		host: lifecycle.Host{
			OS: "linux", Executable: "/opt/pterm/bin/pterm", DataDir: paths.DataDir, Logs: paths.LogDir,
			HomeDir: filepath.Join(root, "home"), ConfigDir: filepath.Join(root, "config"), User: "ada", Systemd: systemd,
		},
		paths: paths, runner: fake, gateway: "http://" + freeLoopbackAddress(t),
	}
	if !systemd {
		env.noSystemd, env.wsl = "offline", true
	}
	previous, previousWait := loadLifecycleEnv, waitTimeout
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	waitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { loadLifecycleEnv, waitTimeout = previous, previousWait })
	return env, fake
}

func TestLinuxSetupAndUninstallUseSystemdUserUnits(t *testing.T) {
	env, fake := newLinuxLifecycleTestEnv(t, true)
	ctx := context.Background()
	broker := env.host.File(lifecycle.Services[0])

	var output bytes.Buffer
	if err := runWithInput(ctx, []string{"setup"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"No changes made", "write " + broker + ":", "ExecStart=/opt/pterm/bin/pterm __broker",
		"run systemctl --user daemon-reload", "run systemctl --user enable phone-terminal-broker.service",
		"linger: off; Termivoa stops when you log out",
		"asks separately whether to keep running after you log out: loginctl enable-linger ada",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("setup output lacks %q:\n%s", want, output.String())
		}
	}
	if len(fake.mutations()) != 0 {
		t.Fatalf("setup without --apply ran %q", fake.mutations())
	}

	output.Reset()
	if err := runWithInput(ctx, []string{"setup", "--apply", "--yes"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable phone-terminal-broker.service", "systemctl --user restart phone-terminal-broker.service",
		"systemctl --user enable phone-terminal-gateway.service", "systemctl --user restart phone-terminal-gateway.service",
	}
	if got := fake.mutations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("setup --apply --yes ran %q", got)
	}
	if !strings.Contains(output.String(), "run it yourself: loginctl enable-linger ada") {
		t.Errorf("--yes does not name the linger command:\n%s", output.String())
	}
	if info, err := os.Stat(env.host.LogDir()); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("log directory = %v, %v", info, err)
	}

	fake.calls = nil
	output.Reset()
	if err := runWithInput(ctx, []string{"uninstall", "--yes", "--delete-data"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want = []string{
		"systemctl --user disable --now phone-terminal-gateway.service",
		"systemctl --user disable --now phone-terminal-broker.service",
		"systemctl --user daemon-reload",
	}
	if got := fake.mutations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstall ran %q", got)
	}
	for _, path := range []string{broker, env.paths.DataDir, filepath.Dir(env.paths.LogDir)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived uninstall --delete-data", path)
		}
	}
	if !strings.Contains(output.String(), "delete /opt/pterm/bin/pterm yourself") {
		t.Errorf("uninstall does not say how to remove the program:\n%s", output.String())
	}
}

// Without a systemd user manager (WSL without systemd, containers), setup
// says so and explains the fallback instead of registering anything.
func TestLinuxSetupWithoutSystemdExplainsTheFallback(t *testing.T) {
	_, fake := newLinuxLifecycleTestEnv(t, false)
	var output bytes.Buffer
	if err := runWithInput(context.Background(), []string{"setup"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"startup: not available: systemd user services are not running (offline)",
		"Start Termivoa now in the background, as `pterm launch` does",
		"After each restart, run `pterm launch` again.",
		"systemd=true", "/etc/wsl.conf",
		"Windows stops this Linux VM when no Windows program uses it",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("setup output lacks %q:\n%s", want, output.String())
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("setup without systemd ran %q", fake.calls)
	}
	if strings.Contains(output.String(), "startup broker: not registered") {
		t.Errorf("startup reported as a problem although it cannot exist:\n%s", output.String())
	}
}

func TestOfferLingerRunsOnlyAfterItsOwnYes(t *testing.T) {
	env, fake := newLinuxLifecycleTestEnv(t, true)
	for _, answer := range []string{"n\n", "\n"} {
		var output bytes.Buffer
		if err := offerLinger(context.Background(), env, fileConfirmer{input: confirmationPipe(t, answer), output: &output}, &output); err != nil {
			t.Fatal(err)
		}
		if len(fake.mutations()) != 0 || !strings.Contains(output.String(), "This runs: loginctl enable-linger ada") ||
			!strings.Contains(output.String(), "Linger not changed") {
			t.Fatalf("answer %q ran %q:\n%s", answer, fake.mutations(), output.String())
		}
	}
	var output bytes.Buffer
	if err := offerLinger(context.Background(), env, fileConfirmer{input: confirmationPipe(t, "y\n"), output: &output}, &output); err != nil {
		t.Fatal(err)
	}
	if got := fake.mutations(); len(got) != 1 || got[0] != "loginctl enable-linger ada" || strings.Contains(output.String(), "sudo") {
		t.Fatalf("approved linger ran %q:\n%s", got, output.String())
	}
}

func TestLinuxTailscaleOperatorIsExplainedNotBypassed(t *testing.T) {
	env, _ := newLinuxLifecycleTestEnv(t, true)
	env.tailscale = "tailscale-fake"
	previous := tailscaleRun
	t.Cleanup(func() { tailscaleRun = previous })
	var serveCalls []string
	operator := ""
	tailscaleRun = func(_ context.Context, _ string, stream io.Writer, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "debug prefs":
			return []byte(`{"OperatorUser":"` + operator + `"}`), nil
		case "serve --bg --https=443 http://127.0.0.1:8787":
			serveCalls = append(serveCalls, strings.Join(args, " "))
			_, _ = io.WriteString(stream, "Access denied: serve config denied\n\nTo not require root, use 'sudo tailscale set --operator=$USER' once.\n")
			return nil, errors.New("exit status 1")
		}
		return nil, errors.New("unexpected tailscale command")
	}
	report := tailscaleReport{installed: true}
	if !needsTailscaleOperator(context.Background(), env, report) {
		t.Fatal("missing operator not detected")
	}
	operator = "ada"
	if needsTailscaleOperator(context.Background(), env, report) {
		t.Fatal("operator ada reported as missing for user ada")
	}

	// Detected beforehand: the question is not asked at all.
	var output bytes.Buffer
	report.needsOperator = true
	configured, err := offerTailscaleServe(context.Background(), env, report, "https://vps.example.ts.net",
		fileConfirmer{input: confirmationPipe(t, "y\n"), output: &output}, &output)
	if err != nil || configured || len(serveCalls) != 0 || !strings.Contains(output.String(), lifecycle.TailscaleOperatorCommand) {
		t.Fatalf("operator missing: configured=%v err=%v calls=%q\n%s", configured, err, serveCalls, output.String())
	}

	// Refused only when run: the refusal names the one-time fix.
	output.Reset()
	report.needsOperator = false
	configured, err = offerTailscaleServe(context.Background(), env, report, "https://vps.example.ts.net",
		fileConfirmer{input: confirmationPipe(t, "y\n"), output: &output}, &output)
	if err != nil || configured || len(serveCalls) != 1 ||
		!strings.Contains(output.String(), "tailscale serve was refused") || !strings.Contains(output.String(), lifecycle.TailscaleOperatorCommand) {
		t.Fatalf("refused serve: configured=%v err=%v calls=%q\n%s", configured, err, serveCalls, output.String())
	}
}

func TestDataDirectoriesNeverChooseAParent(t *testing.T) {
	for name, test := range map[string]struct {
		paths host.Paths
		want  []string
	}{
		"linux": {
			host.Paths{DataDir: "/home/ada/.local/share/phone-terminal", RuntimeDir: "/run/user/1000/phone-terminal",
				LogDir: "/home/ada/.local/state/phone-terminal/logs"},
			[]string{"/home/ada/.local/share/phone-terminal", "/home/ada/.local/state/phone-terminal", "/run/user/1000/phone-terminal"},
		},
		"linux runtime fallback inside data": {
			host.Paths{DataDir: "/home/ada/.local/share/phone-terminal", RuntimeDir: "/home/ada/.local/share/phone-terminal/runtime"},
			[]string{"/home/ada/.local/share/phone-terminal"},
		},
		"home named like the product": {
			host.Paths{DataDir: "/home/phone-terminal/.local/share/phone-terminal", RuntimeDir: "/run/user/1001/phone-terminal",
				LogDir: "/home/phone-terminal/.local/state/phone-terminal/logs"},
			[]string{"/home/phone-terminal/.local/share/phone-terminal", "/home/phone-terminal/.local/state/phone-terminal", "/run/user/1001/phone-terminal"},
		},
		"macos": {
			host.Paths{DataDir: "/Users/ada/Library/Application Support/Phone Terminal",
				RuntimeDir: "/Users/ada/Library/Caches/Phone Terminal/runtime", LogDir: "/Users/ada/Library/Application Support/Phone Terminal/logs"},
			[]string{"/Users/ada/Library/Application Support/Phone Terminal", "/Users/ada/Library/Caches/Phone Terminal"},
		},
	} {
		if got := dataDirectories(test.paths); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %q, want %q", name, got, test.want)
		}
	}
}

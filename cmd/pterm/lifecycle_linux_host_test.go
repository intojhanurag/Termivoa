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

	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/platform"
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
	paths := platform.Paths{
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
	previous, previousWait, previousRoot := loadLifecycleEnv, waitTimeout, isRoot
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	waitTimeout = 300 * time.Millisecond
	isRoot = func() bool { return false }
	t.Cleanup(func() { loadLifecycleEnv, waitTimeout, isRoot = previous, previousWait, previousRoot })
	return env, fake
}

// After an install into ~/.local/bin that PATH does not have yet, the last
// thing setup prints is the command that makes `pterm` work in this terminal.
// PATH finding this pterm through a symlink needs no hint. New logins have it
// only when the login shell is bash and ~/.profile adds ~/.local/bin.
func TestLinuxSetupEndsWithThePathHint(t *testing.T) {
	env, _ := newLinuxLifecycleTestEnv(t, true)
	bin := filepath.Join(env.host.HomeDir, ".local", "bin")
	env.host.Executable = filepath.Join(bin, "pterm")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.host.Executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	setup := func() string {
		t.Helper()
		var output bytes.Buffer
		if err := runWithInput(context.Background(), []string{"setup"}, nil, &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	hint := "\n`pterm` is not on your PATH in this terminal yet. To use it here, run this now:\n  export PATH=\"" + bin + ":$PATH\"\n"

	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/usr/bin/zsh")
	if text := setup(); !strings.HasSuffix(text, hint+"To keep it for new logins, add that line to your shell's startup file (bash: ~/.profile, zsh: ~/.zshrc).\n") ||
		strings.Count(text, "not on your PATH") != 1 {
		t.Errorf("setup does not end with the PATH hint, once:\n%s", text)
	}
	t.Setenv("SHELL", "/bin/bash")
	if err := os.WriteFile(filepath.Join(env.host.HomeDir, ".profile"), []byte(`PATH="$HOME/.local/bin:$PATH"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text := setup(); !strings.HasSuffix(text, hint+"New logins have it already: your ~/.profile adds ~/.local/bin when it exists.\n") {
		t.Errorf("bash with ~/.profile:\n%s", text)
	}

	t.Setenv("PATH", "/usr/bin:"+bin)
	if text := setup(); strings.Contains(text, "PATH") {
		t.Errorf("PATH hint although pterm is on PATH:\n%s", text)
	}
	linked := t.TempDir()
	if err := os.Symlink(env.host.Executable, filepath.Join(linked, "pterm")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:"+linked)
	if text := setup(); strings.Contains(text, "PATH") {
		t.Errorf("PATH hint although a symlink on PATH is this pterm:\n%s", text)
	}
}

// As root, setup changes nothing and says how to make a normal user with
// the administrators' group this system has; run with sudo, it only says to
// drop sudo.
func TestLinuxSetupRefusesRootAndSaysHowToMakeAUser(t *testing.T) {
	_, fake := newLinuxLifecycleTestEnv(t, true)
	isRoot = func() bool { return true }
	previous := groupFile
	t.Cleanup(func() { groupFile = previous })
	groupFile = filepath.Join(t.TempDir(), "group")
	t.Setenv("SUDO_USER", "")
	for group, file := range map[string]string{"sudo": "root:x:0:\nwheel:x:10:\nsudo:x:27:\n", "wheel": "root:x:0:\nwheel:x:10:\n"} {
		if err := os.WriteFile(groupFile, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"setup"}, {"setup", "--apply", "--yes"}} {
			var output bytes.Buffer
			err := runWithInput(context.Background(), args, nil, &output)
			if err == nil || output.Len() != 0 || len(fake.calls) != 0 {
				t.Fatalf("%q as root: err=%v calls=%q output=%q", args, err, fake.calls, output.String())
			}
			for _, want := range []string{
				"refusing to set up as root", "\n  adduser <name>\n", "\n  usermod -aG " + group + " <name>\n",
				"\n  install -d -m 700 -o <name> -g <name> /home/<name>/.ssh && install -m 600 -o <name> -g <name> ~/.ssh/authorized_keys /home/<name>/.ssh/\n",
				"\n  install -m 755 -o <name> -g <name> /opt/pterm/bin/pterm /home/<name>/\n", "ssh <name>@<this-server>) and run there: ~/pterm setup",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%q: root refusal lacks %q:\n%v", args, want, err)
				}
			}
		}
	}
	t.Setenv("SUDO_USER", "ada")
	err := runWithInput(context.Background(), []string{"setup"}, nil, &bytes.Buffer{})
	if err == nil || !strings.HasSuffix(err.Error(), "\nRun it without sudo, as ada: pterm setup") || strings.Contains(err.Error(), "adduser") {
		t.Fatalf("setup with sudo: %v", err)
	}
}

// The short plan still shows every warning the checks found that it does
// not state itself (other ports that get the cookie, Funnel), says when the
// phone cannot reach this computer and how to fix it, and names the
// pairing question only when it will come.
func TestShortSetupPlanShowsWarningsAndThePhoneFix(t *testing.T) {
	const (
		self    = `{"BackendState":"Running","MagicDNSSuffix":"example.ts.net","Self":{"ID":"nVps","DNSName":"vps.example.ts.net.","UserID":1},"User":{"1":{"LoginName":"ada@example.com"}}`
		withMac = self + `,"Peer":{"a":{"ID":"nMac","DNSName":"mac.example.ts.net.","UserID":1,"OS":"macOS"}}}`
		served  = `{"Web":{"vps.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}}}}`
		connect = "  - Then connect your phone (pair it, or add this computer to your app).\n"
		cannot  = "  - Your phone cannot reach this computer yet: "
	)
	for name, test := range map[string]struct {
		status, serve, prefs string
		want, unwanted       []string
	}{
		"other port gets the cookie": {status: withMac, serve: `{"Web":{"vps.example.ts.net:8080":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`,
			want: []string{"Warnings:\n  ! tailscale serve (warning): other ports of this computer's host receive the Termivoa cookie",
				"Share Termivoa with your tailnet devices at https://vps.example.ts.net (port 443). It asks first.", connect},
			unwanted: []string{cannot, "Funnel", "not configured for Termivoa"}},
		"funnel": {status: withMac, serve: strings.TrimSuffix(served, "}") + `,"AllowFunnel":{"vps.example.ts.net:443":true}}`,
			want: []string{"Warnings:\n  ! tailscale serve: Tailscale Funnel is enabled for vps.example.ts.net:443",
				"Never use Tailscale Funnel: it would put your terminal on the public internet.", connect}},
		"no other computer": {status: self + "}", serve: served,
			want: []string{"  - Then pair your phone.\n"}, unwanted: []string{"Warnings:", connect, cannot, "Funnel"}},
		"operator needed": {status: withMac, serve: "{}", prefs: `{"OperatorUser":""}`,
			want: []string{cannot + "on Linux only root or the Tailscale operator may share it on port 443. Run this once: " +
				lifecycle.TailscaleOperatorCommand + ", then run `pterm setup` again.\n"},
			unwanted: []string{"Warnings:", connect, "It asks first.\n  - Share", "(port 443)"}},
	} {
		env, _ := newLinuxLifecycleTestEnv(t, true)
		useTailscaleFake(t, &env, &tailscaleFake{status: test.status, serve: test.serve, prefs: test.prefs})
		var output bytes.Buffer
		if err := runWithInput(context.Background(), []string{"setup"}, nil, &output); err != nil {
			t.Fatal(err)
		}
		plan, _, _ := strings.Cut(output.String(), "No changes made")
		for _, want := range test.want {
			if !strings.Contains(plan, want) {
				t.Errorf("%s: plan lacks %q:\n%s", name, want, plan)
			}
		}
		for _, unwanted := range test.unwanted {
			if strings.Contains(plan, unwanted) {
				t.Errorf("%s: plan contains %q:\n%s", name, unwanted, plan)
			}
		}
	}
}

// Moving startup to this pterm, or restarting the broker, gets its own line
// in the short plan.
func TestShortSetupPlanSaysWhenItReplacesStartup(t *testing.T) {
	env, _ := newLinuxLifecycleTestEnv(t, true)
	var output bytes.Buffer
	status := lifecycle.Status{Service: lifecycle.Service{Name: "broker", Mode: lifecycle.BrokerMode}, Registered: true}
	plan := lifecycle.Plan{Steps: []lifecycle.Step{{Mkdir: "x"}}, ChangesRunningBroker: true}
	describeSetup(env, plan, hostReport{statuses: []lifecycle.Status{status}}, "", "Tailscale is not installed.", false, false, &output)
	if want := "\n    This replaces the startup of another pterm and restarts the broker, which ends running sessions " +
		"(setup stops first if any is active).\n"; !strings.Contains(output.String(), want) {
		t.Errorf("plan lacks %q:\n%s", want, output.String())
	}
	output.Reset()
	status.Registered, status.FileMatches = true, true
	describeSetup(env, lifecycle.Plan{Steps: plan.Steps}, hostReport{statuses: []lifecycle.Status{status}}, "", "", false, false, &output)
	if strings.Contains(output.String(), "This ") {
		t.Errorf("plan for a plain registration says it replaces something:\n%s", output.String())
	}
}

func TestLinuxSetupAndUninstallUseSystemdUserUnits(t *testing.T) {
	env, fake := newLinuxLifecycleTestEnv(t, true)
	ctx := context.Background()
	broker := env.host.File(lifecycle.Services[0])

	var output bytes.Buffer
	if err := runWithInput(ctx, []string{"setup"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	// The default plan is short: one line for each thing, no checks, no unit
	// files, no commands.
	short := output.String()
	for _, want := range []string{
		"No changes made", "  - Start Termivoa when you log in (your user only, no administrator rights).\n",
		"  - Keep its data in " + env.paths.DataDir + " and its logs in " + env.paths.LogDir + ".\n",
		"  - Keep running after you log out (Linux linger). It asks first.\n",
		"run `pterm setup --verbose`",
	} {
		if !strings.Contains(short, want) {
			t.Errorf("setup output lacks %q:\n%s", want, short)
		}
	}
	for _, unwanted := range []string{"Checks (read-only)", "ExecStart", "systemctl", "loginctl"} {
		if strings.Contains(short, unwanted) {
			t.Errorf("short plan contains %q:\n%s", unwanted, short)
		}
	}
	plan, _, _ := strings.Cut(short, "No changes made")
	if lines := strings.Count(plan, "\n"); lines > 12 {
		t.Errorf("short plan has %d lines:\n%s", lines, short)
	}
	output.Reset()
	if err := runWithInput(ctx, []string{"setup", "--verbose"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"No changes made", "write " + broker + ":", "ExecStart=/opt/pterm/bin/pterm __broker",
		"run systemctl --user daemon-reload", "run systemctl --user enable phone-terminal-broker.service",
		"linger: off; Termivoa stops when you log out",
		"Keep running after you log out (Linux linger). It asks first. Runs: loginctl enable-linger ada",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("setup --verbose output lacks %q:\n%s", want, output.String())
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
	if err := runWithInput(context.Background(), []string{"setup", "--verbose"}, nil, &output); err != nil {
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
		paths platform.Paths
		want  []string
	}{
		"linux": {
			platform.Paths{DataDir: "/home/ada/.local/share/phone-terminal", RuntimeDir: "/run/user/1000/phone-terminal",
				LogDir: "/home/ada/.local/state/phone-terminal/logs"},
			[]string{"/home/ada/.local/share/phone-terminal", "/home/ada/.local/state/phone-terminal", "/run/user/1000/phone-terminal"},
		},
		"linux runtime fallback inside data": {
			platform.Paths{DataDir: "/home/ada/.local/share/phone-terminal", RuntimeDir: "/home/ada/.local/share/phone-terminal/runtime"},
			[]string{"/home/ada/.local/share/phone-terminal"},
		},
		"home named like the product": {
			platform.Paths{DataDir: "/home/phone-terminal/.local/share/phone-terminal", RuntimeDir: "/run/user/1001/phone-terminal",
				LogDir: "/home/phone-terminal/.local/state/phone-terminal/logs"},
			[]string{"/home/phone-terminal/.local/share/phone-terminal", "/home/phone-terminal/.local/state/phone-terminal", "/run/user/1001/phone-terminal"},
		},
		"macos": {
			platform.Paths{DataDir: "/Users/ada/Library/Application Support/Phone Terminal",
				RuntimeDir: "/Users/ada/Library/Caches/Phone Terminal/runtime", LogDir: "/Users/ada/Library/Application Support/Phone Terminal/logs"},
			[]string{"/Users/ada/Library/Application Support/Phone Terminal", "/Users/ada/Library/Caches/Phone Terminal"},
		},
	} {
		if got := dataDirectories(test.paths); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %q, want %q", name, got, test.want)
		}
	}
}

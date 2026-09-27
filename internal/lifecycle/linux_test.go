package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var linuxHost = Host{
	OS:         "linux",
	Executable: "/home/ada/.local/bin/pterm",
	DataDir:    "/home/ada/.local/share/phone-terminal",
	Logs:       "/home/ada/.local/state/phone-terminal/logs",
	HomeDir:    "/home/ada",
	ConfigDir:  "/home/ada/.config",
	User:       "ada",
	Systemd:    true,
}

const wantGatewayUnit = `[Unit]
Description=Phone Terminal gateway for this user
Wants=phone-terminal-broker.service
After=phone-terminal-broker.service

[Service]
Type=simple
ExecStart=/home/ada/.local/bin/pterm __gateway
Restart=on-failure
RestartSec=5
StandardOutput=append:/home/ada/.local/state/phone-terminal/logs/gateway.log
StandardError=inherit

[Install]
WantedBy=default.target
`

func TestSystemdUnitExactContent(t *testing.T) {
	data, text, err := linuxHost.Content(Services[1])
	if err != nil {
		t.Fatal(err)
	}
	if text != wantGatewayUnit || string(data) != text {
		t.Fatalf("gateway unit mismatch:\n%s", text)
	}
	_, broker, _ := linuxHost.Content(Services[0])
	if !strings.Contains(broker, "ExecStart=/home/ada/.local/bin/pterm __broker\n") || strings.Contains(broker, "Wants=") ||
		!strings.Contains(broker, "logs/broker.log") {
		t.Fatalf("broker unit:\n%s", broker)
	}
	if got := linuxHost.File(Services[0]); got != "/home/ada/.config/systemd/user/phone-terminal-broker.service" {
		t.Fatalf("broker unit path = %q", got)
	}
	if linuxHost.LogDir() != "/home/ada/.local/state/phone-terminal/logs" {
		t.Fatalf("log dir = %q", linuxHost.LogDir())
	}
}

func TestLinuxHostRejectsPathsUnitFilesWouldMisread(t *testing.T) {
	for _, executable := range []string{"/home/ada/My Apps/pterm", "/home/ada/100%/pterm", "/home/$USER/pterm", `/home/ada/"x"/pterm`, "relative/pterm"} {
		host := linuxHost
		host.Executable = executable
		if _, _, err := host.Content(Services[0]); err == nil {
			t.Errorf("executable %q accepted", executable)
		}
	}
	for _, user := range []string{"", "-rf", "a b", "a/b"} {
		host := linuxHost
		host.User = user
		if err := host.Validate(); err == nil {
			t.Errorf("user %q accepted", user)
		}
	}
}

func TestSystemdInstallAndUninstallCommands(t *testing.T) {
	broker, gateway := "phone-terminal-broker.service", "phone-terminal-gateway.service"
	fresh, err := linuxHost.InstallPlan(statuses(linuxHost, false, false))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", broker}, {"systemctl", "--user", "restart", broker},
		{"systemctl", "--user", "enable", gateway}, {"systemctl", "--user", "restart", gateway},
	}
	if !reflect.DeepEqual(commands(fresh), want) || fresh.ChangesRunningBroker {
		t.Fatalf("fresh install = %q changes=%v", commands(fresh), fresh.ChangesRunningBroker)
	}
	if fresh.Steps[0].Mkdir != linuxHost.Logs || fresh.Steps[1].Write != linuxHost.File(Services[0]) || fresh.Steps[2].Write != linuxHost.File(Services[1]) {
		t.Fatalf("fresh install must create the log directory, then write both units before reloading: %+v", fresh.Steps[:3])
	}

	if current, _ := linuxHost.InstallPlan(statuses(linuxHost, true, true)); len(current.Steps) != 0 {
		t.Fatalf("current registration planned changes: %v", current.Steps)
	}

	// Units written but not enabled (for example disabled by hand): enable
	// and start; start never restarts a running broker.
	disabled := statuses(linuxHost, true, false)
	disabled[0].Disabled, disabled[1].Disabled = true, true
	plan, _ := linuxHost.InstallPlan(disabled)
	want = [][]string{
		{"systemctl", "--user", "enable", broker}, {"systemctl", "--user", "start", broker},
		{"systemctl", "--user", "enable", gateway}, {"systemctl", "--user", "start", gateway},
	}
	if !reflect.DeepEqual(commands(plan), want) || plan.ChangesRunningBroker {
		t.Fatalf("re-enable = %q changes=%v", commands(plan), plan.ChangesRunningBroker)
	}

	// A new pterm: rewrite and restart; that ends the broker's sessions.
	changed, _ := linuxHost.InstallPlan(statuses(linuxHost, false, true))
	want = [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "restart", broker}, {"systemctl", "--user", "restart", gateway},
	}
	if !reflect.DeepEqual(commands(changed), want) || !changed.ChangesRunningBroker {
		t.Fatalf("changed install = %q changes=%v", commands(changed), changed.ChangesRunningBroker)
	}

	removal, err := linuxHost.UninstallPlan(statuses(linuxHost, true, true),
		[]string{linuxHost.DataDir, "/home/ada/.local/state/phone-terminal", "/run/user/1000/phone-terminal"})
	if err != nil {
		t.Fatal(err)
	}
	want = [][]string{
		{"systemctl", "--user", "disable", "--now", gateway},
		{"systemctl", "--user", "disable", "--now", broker},
		{"systemctl", "--user", "daemon-reload"},
	}
	if !reflect.DeepEqual(commands(removal), want) || !removal.ChangesRunningBroker {
		t.Fatalf("uninstall = %q", commands(removal))
	}
	if removal.Steps[1].Remove != linuxHost.File(Services[1]) || removal.Steps[len(removal.Steps)-1].RemoveTree != "/run/user/1000/phone-terminal" {
		t.Fatalf("uninstall steps: %+v", removal.Steps)
	}
	if _, err := linuxHost.UninstallPlan(nil, []string{"/run/user/1000"}); err == nil {
		t.Fatal("uninstall would delete the whole runtime directory")
	}
	if got := linuxHost.UnloadCommand(Services[1]); !reflect.DeepEqual(got, []string{"systemctl", "--user", "disable", "--now", gateway}) {
		t.Fatalf("unload = %q", got)
	}
	if got := linuxHost.StartCommand(Services[0]); !reflect.DeepEqual(got, []string{"systemctl", "--user", "start", broker}) {
		t.Fatalf("start = %q", got)
	}
}

func TestWithoutSystemdNothingIsRegistered(t *testing.T) {
	host := linuxHost
	host.Systemd = false
	if _, err := host.InstallPlan(statuses(host, false, false)); err == nil {
		t.Fatal("install planned without a systemd user manager")
	}
	runner := &systemctlFake{enabled: map[string]string{}}
	found, err := host.Inspect(context.Background(), runner)
	if err != nil || len(runner.calls) != 0 || found[0].Registered || found[0].Disabled {
		t.Fatalf("inspect ran %q: %+v %v", runner.calls, found, err)
	}
	// Leftover unit files are still removed, without systemctl.
	found[0].FileExists = true
	removal, _ := host.UninstallPlan(found, nil)
	if len(commands(removal)) != 0 || len(removal.Steps) != 1 || removal.Steps[0].Remove != host.File(Services[0]) {
		t.Fatalf("uninstall without systemd = %+v", removal.Steps)
	}
}

// systemctlFake models `systemctl --user` and `loginctl` for one user.
type systemctlFake struct {
	calls   [][]string
	enabled map[string]string // unit -> is-enabled state
	state   string            // is-system-running output
	linger  string
}

func (fake *systemctlFake) Run(_ context.Context, name string, args ...string) (string, error) {
	fake.calls = append(fake.calls, append([]string{name}, args...))
	if name == "loginctl" {
		if args[0] == "enable-linger" {
			fake.linger = "yes"
			return "", nil
		}
		return fake.linger + "\n", nil
	}
	switch args[1] {
	case "is-system-running":
		if fake.state == "running" {
			return "running\n", nil
		}
		return fake.state + "\n", errors.New("exit status 1")
	case "is-enabled":
		state, ok := fake.enabled[args[2]]
		if !ok {
			return "Failed to get unit file state for " + args[2] + ": No such file or directory\n", errors.New("exit status 1")
		}
		if state != "enabled" {
			return state + "\n", errors.New("exit status 1")
		}
		return "enabled\n", nil
	case "enable":
		fake.enabled[args[2]] = "enabled"
	case "disable":
		fake.enabled[args[len(args)-1]] = "disabled"
	}
	return "", nil
}

func TestSystemdApplyRoundTripInTemporaryHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix paths")
	}
	root := t.TempDir()
	host := Host{
		OS: "linux", Executable: "/opt/pterm", User: "ada", Systemd: true, HomeDir: filepath.Join(root, "home"),
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "share", "phone-terminal"),
		Logs: filepath.Join(root, "state", "phone-terminal", "logs"),
	}
	runner := &systemctlFake{enabled: map[string]string{}}
	ctx := context.Background()
	found, err := host.Inspect(ctx, runner)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := host.InstallPlan(found)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, runner, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(host.LogDir()); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("log directory = %v, %v", info, err)
	}
	written, _ := os.ReadFile(host.File(Services[1]))
	if _, want, _ := host.Content(Services[1]); string(written) != want {
		t.Fatal("installed unit differs from generated content")
	}
	found, _ = host.Inspect(ctx, runner)
	if !found[0].Registered || !found[1].Registered {
		t.Fatalf("not registered after install: %+v", found)
	}
	if plan, _ := host.InstallPlan(found); len(plan.Steps) != 0 {
		t.Fatalf("second install is not idempotent: %v", plan.Steps)
	}
	plan, _ = host.UninstallPlan(found, []string{host.DataDir})
	if err := Apply(ctx, runner, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{host.File(Services[0]), host.File(Services[1]), host.DataDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists", path)
		}
	}
	found, _ = host.Inspect(ctx, runner)
	if found[0].Registered || !found[0].Disabled {
		t.Fatalf("after uninstall: %+v", found[0])
	}
}

func TestSystemdUserAvailability(t *testing.T) {
	for state, want := range map[string]bool{
		"running": true, "degraded": true, "starting": true, "offline": false, "": false,
	} {
		available, reason := SystemdUser(context.Background(), &systemctlFake{state: state})
		if available != want || (!want && reason == "") {
			t.Errorf("state %q: available=%v reason=%q", state, available, reason)
		}
	}
	missing := runnerFunc(func(context.Context, string, ...string) (string, error) {
		return "", errors.New(`exec: "systemctl": executable file not found in $PATH`)
	})
	if available, reason := SystemdUser(context.Background(), missing); available || !strings.Contains(reason, "not found") {
		t.Fatalf("missing systemctl: %v %q", available, reason)
	}
}

func TestLingerQueryAndCommand(t *testing.T) {
	runner := &systemctlFake{linger: "no"}
	if enabled, known := linuxHost.Linger(context.Background(), runner); enabled || !known {
		t.Fatalf("linger off = %v, %v", enabled, known)
	}
	if !reflect.DeepEqual(runner.calls[0], []string{"loginctl", "show-user", "ada", "--property=Linger", "--value"}) {
		t.Fatalf("query = %q", runner.calls[0])
	}
	if !reflect.DeepEqual(linuxHost.LingerCommand(), []string{"loginctl", "enable-linger", "ada"}) {
		t.Fatalf("command = %q", linuxHost.LingerCommand())
	}
	runner.linger = "yes"
	if enabled, known := linuxHost.Linger(context.Background(), runner); !enabled || !known {
		t.Fatalf("linger on = %v, %v", enabled, known)
	}
	runner.linger = "Failed to connect to bus"
	if _, known := linuxHost.Linger(context.Background(), runner); known {
		t.Fatal("unknown linger reported as known")
	}
}

func TestTailscaleOperatorParsing(t *testing.T) {
	if operator, err := TailscaleOperator([]byte(`{"ControlURL":"https://controlplane.tailscale.com","OperatorUser":"ada"}`)); err != nil || operator != "ada" {
		t.Fatalf("operator = %q, %v", operator, err)
	}
	if operator, err := TailscaleOperator([]byte(`{"ControlURL":"x"}`)); err != nil || operator != "" {
		t.Fatalf("no operator = %q, %v", operator, err)
	}
	if _, err := TailscaleOperator([]byte(`not json`)); err == nil {
		t.Fatal("bad JSON accepted")
	}
	denied := "Access denied: serve config denied\n\nUse 'sudo tailscale serve --bg --https=443 http://127.0.0.1:8787'.\n" +
		"To not require root, use 'sudo tailscale set --operator=$USER' once.\n"
	if !NeedsTailscaleOperator(denied) || NeedsTailscaleOperator("serve: port 443 is already in use") {
		t.Fatal("operator error detection is wrong")
	}
}

type runnerFunc func(context.Context, string, ...string) (string, error)

func (run runnerFunc) Run(ctx context.Context, name string, args ...string) (string, error) {
	return run(ctx, name, args...)
}

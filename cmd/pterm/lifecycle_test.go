package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

func TestLifecycleModesMatchInternalModes(t *testing.T) {
	if lifecycle.BrokerMode != brokerMode || lifecycle.GatewayMode != gatewayMode {
		t.Fatal("startup registration would start the wrong process mode")
	}
}

// launchctlFake records commands and models launchd registration state.
type launchctlFake struct {
	mu         sync.Mutex
	calls      []string
	registered map[string]bool
}

func (fake *launchctlFake) bootstrapped() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	count := 0
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "launchctl bootstrap ") {
			count++
		}
	}
	return count
}

func (fake *launchctlFake) Run(_ context.Context, name string, args ...string) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, name+" "+strings.Join(args, " "))
	switch args[0] {
	case "print":
		if !fake.registered[args[1]] {
			return "", errors.New("exit status 113")
		}
	case "bootstrap":
		fake.registered[args[1]+"/"+strings.TrimSuffix(filepath.Base(args[2]), ".plist")] = true
	case "bootout":
		delete(fake.registered, args[1])
	}
	return "", nil
}

func (fake *launchctlFake) mutations() []string {
	var result []string
	for _, call := range fake.calls {
		if !strings.HasPrefix(call, "launchctl print ") {
			result = append(result, call)
		}
	}
	return result
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

// newLifecycleTestEnv points the lifecycle commands at a temporary home, a
// free loopback gateway address and the fake launchctl. Nothing real changes.
func newLifecycleTestEnv(t *testing.T) (lifecycleEnv, *launchctlFake) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("exercises the macOS plan against a temporary home")
	}
	root, err := os.MkdirTemp("/tmp", "pterm-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	fake := &launchctlFake{registered: map[string]bool{}}
	data := filepath.Join(root, "Application Support", "Phone Terminal")
	env := lifecycleEnv{
		host:    lifecycle.Host{OS: "darwin", Executable: "/Applications/Termivoa.app/Contents/MacOS/pterm", DataDir: data, HomeDir: filepath.Join(root, "home"), UID: 501},
		paths:   platform.Paths{DataDir: data, RuntimeDir: filepath.Join(root, "Caches", "Phone Terminal", "runtime")},
		runner:  fake,
		gateway: "http://" + freeLoopbackAddress(t),
	}
	previous, previousWait := loadLifecycleEnv, waitTimeout
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	waitTimeout = time.Second
	t.Cleanup(func() { loadLifecycleEnv, waitTimeout = previous, previousWait })
	return env, fake
}

// startLifecycleBroker runs a real development broker on env's temporary
// data and runtime directories, as `pterm launch` would outside launchd.
func startLifecycleBroker(t *testing.T, paths platform.Paths) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- host.RunBroker(ctx, host.BrokerConfig{
			DataDir: paths.DataDir, RuntimeDir: paths.RuntimeDir, HostBuild: buildID, Profile: platform.DevelopmentProfile,
			Ready: func(host.BrokerReady) { close(ready) },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("broker shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("test broker failed to shut down")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		done <- err
		t.Fatalf("broker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("broker was not ready")
	}
}

func assertExists(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestSetupAndUninstallChangeStartupOnlyWithConsent(t *testing.T) {
	env, fake := newLifecycleTestEnv(t)
	data := env.paths.DataDir
	plist := env.host.File(lifecycle.Services[0])
	ctx := context.Background()

	var output bytes.Buffer
	if err := runWithInput(ctx, []string{"setup"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No changes made", "Tailscale is not installed", "Never use Tailscale Funnel", "run launchctl bootstrap gui/501 " + plist} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("setup output lacks %q:\n%s", want, output.String())
		}
	}
	if len(fake.mutations()) != 0 {
		t.Fatalf("setup without --apply ran %q", fake.mutations())
	}
	if err := runWithInput(ctx, []string{"setup", "--apply"}, nil, &output); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("setup --apply without confirmation input = %v", err)
	}
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) || len(fake.mutations()) != 0 {
		t.Fatal("unconfirmed setup changed startup registration")
	}

	if err := runWithInput(ctx, []string{"setup", "--apply", "--yes"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if got := fake.mutations(); len(got) != 2 || !strings.HasPrefix(got[0], "launchctl bootstrap gui/501 ") {
		t.Fatalf("setup --apply --yes ran %q", got)
	}
	if _, err := os.Stat(plist); err != nil {
		t.Fatal(err)
	}

	fake.calls = nil
	if err := runWithInput(ctx, []string{"uninstall", "--dry-run", "--delete-data"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if len(fake.mutations()) != 0 {
		t.Fatalf("uninstall --dry-run ran %q", fake.mutations())
	}
	if err := runWithInput(ctx, []string{"uninstall", "--yes", "--delete-data"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want := []string{"launchctl bootout gui/501/dev.phone-terminal.gateway", "launchctl bootout gui/501/dev.phone-terminal.broker"}
	if got := fake.mutations(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("uninstall ran %q", got)
	}
	for _, path := range []string{plist, data} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived uninstall --delete-data", path)
		}
	}
}

// An unregistered broker and gateway (started by `pterm launch`) hold the data
// lock and port 8787, so registered copies would crash-loop forever.
func TestSetupRefusesProcessesRunningOutsideStartup(t *testing.T) {
	env, fake := newLifecycleTestEnv(t)
	ctx := context.Background()
	gateway, err := net.Listen("tcp", strings.TrimPrefix(env.gateway, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runWithInput(ctx, []string{"setup", "--apply", "--yes"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("setup --apply with a busy gateway port = %v", err)
	}
	_ = gateway.Close()

	startLifecycleBroker(t, env.paths)
	err = runWithInput(ctx, []string{"setup", "--apply", "--yes"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "outside startup registration") || !strings.Contains(err.Error(), "rerun `pterm setup`") {
		t.Fatalf("setup --apply with an unregistered broker = %v", err)
	}
	if got := fake.mutations(); len(got) != 0 {
		t.Fatalf("refused setup ran %q", got)
	}
	if _, err := os.Stat(env.host.File(lifecycle.Services[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused setup wrote a LaunchAgent")
	}
}

func TestUninstallKeepsDataWhileABrokerRuns(t *testing.T) {
	env, fake := newLifecycleTestEnv(t)
	ctx := context.Background()
	startLifecycleBroker(t, env.paths)
	database := filepath.Join(env.paths.DataDir, "phone-terminal.db")

	var output bytes.Buffer
	for _, args := range [][]string{{"uninstall", "--dry-run", "--delete-data"}, {"uninstall", "--yes", "--delete-data"}} {
		err := runWithInput(ctx, args, nil, &output)
		if err == nil || !strings.Contains(err.Error(), "refusing to delete data") {
			t.Fatalf("%q with an unregistered broker = %v", args, err)
		}
	}
	if got := fake.mutations(); len(got) != 0 {
		t.Fatalf("refused uninstall ran %q", got)
	}
	assertExists(t, database, env.paths.RuntimeDir)
	// The shared read-only checks read the live broker's bound app origins.
	if !strings.Contains(output.String(), "  multi-computer: no bound app origins (CORS off)\n") ||
		strings.Contains(output.String(), "! multi-computer") {
		t.Fatalf("multi-computer checks:\n%s", output.String())
	}

	// Registered in launchd, but the live broker is not launchd's copy:
	// booting out the registration must not be taken as stopping it.
	for _, service := range lifecycle.Services {
		fake.registered["gui/501/"+service.Label] = true
	}
	err := runWithInput(ctx, []string{"uninstall", "--yes", "--delete-data"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "refusing to delete data") {
		t.Fatalf("uninstall --delete-data with a live broker after bootout = %v", err)
	}
	assertExists(t, database, env.paths.RuntimeDir)
}

// A broker that holds its locks but never answers (hung, or a different
// protocol version) must not be treated as "not running".
func TestLifecycleRefusesABrokerThatDoesNotAnswer(t *testing.T) {
	env, fake := newLifecycleTestEnv(t)
	ctx := context.Background()
	_, dataLock, err := ipc.LockBrokerDataDirectory(env.paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataLock.Close() })
	if _, err := ipc.EnsurePrivateDirectory(env.paths.RuntimeDir); err != nil {
		t.Fatal(err)
	}
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{RuntimeDir: env.paths.RuntimeDir, BrokerGeneration: "33333333-3333-4333-8333-333333333333"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	for _, service := range lifecycle.Services {
		fake.registered["gui/501/"+service.Label] = true
	}

	var output bytes.Buffer
	err = runWithInput(ctx, []string{"uninstall", "--yes", "--delete-data"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("uninstall with a silent broker = %v", err)
	}
	if got := fake.mutations(); len(got) != 0 {
		t.Fatalf("refused uninstall ran %q", got)
	}
	assertExists(t, env.paths.DataDir, env.paths.RuntimeDir)

	// The data lock alone, with no endpoint at all, is still a running broker.
	_ = listener.Close()
	err = runWithInput(ctx, []string{"uninstall", "--dry-run", "--delete-data"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("uninstall with only the data lock held = %v", err)
	}
}

func TestUpRequiresExplicitDevelopmentProfile(t *testing.T) {
	if err := runUpCommand(context.Background(), nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--dev") {
		t.Fatalf("up without --dev = %v", err)
	}
}

// TestInspectTailscaleReportsPreviewFindingsSeparately proves the dev-server
// preview (HTTPS 8443) lines in `pterm doctor` output are independent of the
// Termivoa's own HTTPS 443 status, and that a preview conflict never
// counts as a doctor problem (previews are optional).
func TestInspectTailscaleReportsPreviewFindingsSeparately(t *testing.T) {
	const status = `{"BackendState":"Running","Self":{"DNSName":"h.ts.net."}}`
	for name, test := range map[string]struct {
		serve    string
		want     []string
		lacks    []string
		problems int
	}{
		"both configured": {
			serve: `{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}},` +
				`"h.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8789"}}}}}`,
			want:     []string{"tailscale serve: HTTPS 443 proxies the Termivoa gateway", "tailscale serve: HTTPS 8443 proxies dev-server previews"},
			problems: 0,
		},
		"neither configured": {
			serve: `{}`,
			want: []string{
				"! tailscale serve: not configured for Termivoa",
				"tailscale serve: dev-server previews not shared (optional): " + lifecycle.PreviewServeCommand,
			},
			problems: 1,
		},
		"preview conflict does not block or fail 443": {
			serve: `{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}},` +
				`"h.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`,
			want: []string{
				"tailscale serve: HTTPS 443 proxies the Termivoa gateway",
				"! tailscale serve: HTTPS 8443 path / on h.ts.net:8443 already serves http://127.0.0.1:3000; pterm will not overwrite it",
			},
			lacks:    []string{"dev-server previews not shared"},
			problems: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			previous := tailscaleRun
			t.Cleanup(func() { tailscaleRun = previous })
			tailscaleRun = func(_ context.Context, _ string, _ io.Writer, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "status --json":
					return []byte(status), nil
				case "serve status --json":
					return []byte(test.serve), nil
				}
				return nil, errors.New("unexpected tailscale command")
			}
			var output bytes.Buffer
			_, problems := inspectTailscale(context.Background(), "tailscale-fake", nil, &output)
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, output.String())
				}
			}
			for _, lacks := range test.lacks {
				if strings.Contains(output.String(), lacks) {
					t.Errorf("output should not contain %q:\n%s", lacks, output.String())
				}
			}
			if problems != test.problems {
				t.Errorf("problems = %d, want %d", problems, test.problems)
			}
		})
	}
}

func TestProgramLeftoverNamesWhatToDelete(t *testing.T) {
	cases := []struct{ os, executable, want string }{
		{"darwin", "/Users/ada/.local/bin/pterm", "The pterm program itself stays: delete /Users/ada/.local/bin/pterm yourself."},
		{"darwin", "/Applications/Termivoa.app/Contents/MacOS/pterm", "The application itself stays: move /Applications/Termivoa.app to the Trash."},
		{"linux", "/home/ada/.local/bin/pterm", "The pterm program itself stays: delete /home/ada/.local/bin/pterm yourself."},
		{"windows", `C:\Users\ada\AppData\Local\Programs\Termivoa\pterm.exe`, `The pterm program itself stays: delete C:\Users\ada\AppData\Local\Programs\Termivoa\pterm.exe yourself.`},
	}
	for _, c := range cases {
		if got := programLeftover(c.os, c.executable); got != c.want {
			t.Errorf("programLeftover(%q, %q) = %q, want %q", c.os, c.executable, got, c.want)
		}
	}
}

const tailnetStatusJSON = `{"BackendState":"Running",
  "Self":{"ID":"nVps","DNSName":"vps.tailx.ts.net.","UserID":1,"Tags":null},
  "Peer":{
    "nodekey:a":{"ID":"nMac","DNSName":"mac.tailx.ts.net.","UserID":1},
    "nodekey:b":{"ID":"nEve","DNSName":"eve.tailx.ts.net.","UserID":2},
    "nodekey:c":{"ID":"nSrv","DNSName":"srv.tailx.ts.net.","UserID":3,"Tags":["tag:server"]},
    "nodekey:d":{"ID":"nNew","DNSName":"old.tailx.ts.net.","UserID":1}},
  "User":{"1":{"LoginName":"ada@example.com"},"2":{"LoginName":"eve@example.com"}},
  "CurrentTailnet":{"MagicDNSSuffix":"tailx.ts.net"}}`

func TestTailnetFindingsReportBindingMismatches(t *testing.T) {
	status, err := tailnet.ParseStatus([]byte(tailnetStatusJSON))
	if err != nil {
		t.Fatal(err)
	}
	device := func(origin, node string, revoked bool, tags ...string) protocol.AdminRPCDevice {
		return protocol.AdminRPCDevice{AppOrigin: &origin, AppNode: &protocol.TailnetNode{ID: node, Tags: append([]string{}, tags...)}, Revoked: revoked}
	}
	devices := []protocol.AdminRPCDevice{
		device("https://mac.tailx.ts.net", "nMac", false),               // same owner: fine
		device("https://mac.tailx.ts.net", "nMac", false),               // duplicate: checked once
		device("https://srv.tailx.ts.net", "nSrv", false, "tag:server"), // tagged: owners not compared
		device("https://eve.tailx.ts.net", "nEve", false),               // another user
		device("https://gone.tailx.ts.net", "nGone", false),             // node removed
		device("https://old.tailx.ts.net", "nOld", false),               // name now on another node
		device("https://mac.other.ts.net", "nX", false),                 // another tailnet
		device("https://eve.tailx.ts.net", "nEve2", true),               // revoked: skipped
		{}, // own origin: skipped
	}
	got := tailnetFindings(status, devices)
	want := []string{
		`https://eve.tailx.ts.net: owner mismatch ("eve@example.com"; this computer: "ada@example.com")`,
		"https://gone.tailx.ts.net: pinned node nGone is gone; phones bound to it stop working",
		"https://old.tailx.ts.net: node ID changed (reused name?)",
		"https://mac.other.ts.net: different tailnet; not supported",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A node pinned untagged that is tagged now is refused (SL4).
	if got := tailnetFindings(status, []protocol.AdminRPCDevice{device("https://srv.tailx.ts.net", "nSrv", false)}); len(got) != 1 ||
		got[0] != "https://srv.tailx.ts.net: tags changed since pairing; phones bound to it are refused until paired again" {
		t.Fatalf("tag change findings = %v", got)
	}
	status.Self.Tags = []string{"tag:server"} // a tagged home never compares owners
	if got := tailnetFindings(status, devices[3:4]); len(got) != 0 {
		t.Fatalf("tagged self findings = %v", got)
	}
}

// Logged out, Tailscale reports no MagicDNS suffix: doctor says so once
// instead of calling every bound app "different tailnet".
func TestTailnetFindingsWithUnknownTailnet(t *testing.T) {
	status, err := tailnet.ParseStatus([]byte(`{"BackendState":"NeedsLogin"}`))
	if err != nil {
		t.Fatal(err)
	}
	origin, other := "https://mac.tailx.ts.net", "https://srv.tailx.ts.net"
	devices := []protocol.AdminRPCDevice{
		{AppOrigin: &origin, AppNode: &protocol.TailnetNode{ID: "nMac"}},
		{AppOrigin: &other, AppNode: &protocol.TailnetNode{ID: "nSrv"}},
	}
	want := "this computer's tailnet is unknown (is Tailscale logged out?); bound app origins cannot be checked"
	if got := tailnetFindings(status, devices); strings.Join(got, "\n") != want {
		t.Fatalf("findings = %q", got)
	}
	if got := tailnetFindings(status, []protocol.AdminRPCDevice{{}}); len(got) != 0 {
		t.Fatalf("own-origin device findings = %q", got)
	}
}

func TestOtherServeHandlersOnlyOnThisOrigin(t *testing.T) {
	const gateway = `"/":{"Proxy":"http://127.0.0.1:8787"}`
	for name, test := range map[string]struct {
		serve string
		want  []string
	}{
		"only the gateway":      {serve: `{"Web":{"h.ts.net:443":{"Handlers":{` + gateway + `}}}}`},
		"path on own host":      {serve: `{"Web":{"H.ts.net:443":{"Handlers":{` + gateway + `,"/grafana":{"Proxy":"http://127.0.0.1:3000"}}}}}`, want: []string{"/grafana"}},
		"root to another app":   {serve: `{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`, want: []string{"/"}},
		"other host":            {serve: `{"Web":{"svc.ts.net:443":{"Handlers":{"/x":{"Proxy":"http://127.0.0.1:3000"}}}}}`},
		"preview port":          {serve: `{"Web":{"h.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`},
		"TCP forward on 443":    {serve: `{"TCP":{"443":{"TCPForward":"127.0.0.1:22"}}}`, want: []string{"TCP forward to 127.0.0.1:22"}},
		"TCP forward elsewhere": {serve: `{"TCP":{"8443":{"TCPForward":"127.0.0.1:22"}}}`},
		"empty":                 {serve: ``},
	} {
		if got := otherServeHandlers([]byte(test.serve), "h.ts.net."); strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}

// Doctor reports other handlers on this computer's own origin as a problem
// line that says how to move them. ServeFindings already counts each one, so
// the explanation does not count it twice.
func TestDoctorReportsOtherHandlersAsAProblem(t *testing.T) {
	previous := tailscaleRun
	t.Cleanup(func() { tailscaleRun = previous })
	tailscaleRun = func(_ context.Context, _ string, _ io.Writer, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"h.ts.net."},"CurrentTailnet":{"MagicDNSSuffix":"ts.net"}}`), nil
		case "serve status --json":
			return []byte(`{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"},"/admin":{"Proxy":"http://127.0.0.1:9000"}}}}}`), nil
		}
		return nil, errors.New("unexpected tailscale command")
	}
	var output bytes.Buffer
	report, problems := inspectTailscale(context.Background(), "tailscale-fake", nil, &output)
	want := "  ! tailscale serve: other handlers share this computer's Termivoa origin https://h.ts.net: /admin. " +
		"Any handler there is same-origin with the Termivoa app and could control this computer and, if it is home, " +
		"every computer added to it. Move each one to another computer or to its own Tailscale Service hostname, " +
		"or stop serving it; another port on this computer does not help, because browsers send the Termivoa cookie " +
		"to every port of a host. Then remove it here: `tailscale serve --https=443 --set-path=/admin off`.\n"
	if problems != 1 || strings.Count(output.String(), want) != 1 || report.tailnet == nil ||
		report.tailnet.Suffix != "ts.net" || strings.Join(report.otherHandlers, ",") != "/admin" {
		t.Fatalf("problems=%d report=%+v output:\n%s", problems, report, output.String())
	}
}

// Handlers on other ports of this computer's host receive the Termivoa cookie
// (cookies ignore ports): doctor warns without counting a problem. Termivoa's
// own preview handler on 8443 strips the cookie and is not listed.
func TestDoctorWarnsAboutOtherPortsOfThisHost(t *testing.T) {
	const preview = `"h.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8789"}}}`
	for name, test := range map[string]struct {
		serve string
		want  []string
	}{
		"preview only":         {serve: `{"Web":{` + preview + `}}`},
		"443 is not this list": {serve: `{"Web":{"h.ts.net:443":{"Handlers":{"/admin":{"Proxy":"http://127.0.0.1:9000"}}}}}`},
		"other port":           {serve: `{"Web":{` + preview + `,"H.ts.net:8080":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`, want: []string{"HTTPS 8080 /"}},
		"8443 not the preview": {serve: `{"Web":{"h.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`, want: []string{"HTTPS 8443 /"}},
		"other host":           {serve: `{"Web":{"svc.ts.net:8080":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`},
		"TCP forward":          {serve: `{"TCP":{"443":{"HTTPS":true},"5432":{"TCPForward":"127.0.0.1:5432"}}}`, want: []string{"TCP 5432 forward to 127.0.0.1:5432"}},
	} {
		if got := otherPortHandlers([]byte(test.serve), "h.ts.net."); strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
	previous := tailscaleRun
	t.Cleanup(func() { tailscaleRun = previous })
	tailscaleRun = func(_ context.Context, _ string, _ io.Writer, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"h.ts.net."},"CurrentTailnet":{"MagicDNSSuffix":"ts.net"}}`), nil
		case "serve status --json":
			return []byte(`{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}},` + preview +
				`,"h.ts.net:8080":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`), nil
		}
		return nil, errors.New("unexpected tailscale command")
	}
	var output bytes.Buffer
	if _, problems := inspectTailscale(context.Background(), "tailscale-fake", nil, &output); problems != 0 ||
		!strings.Contains(output.String(), "  ! tailscale serve (warning): other ports of this computer's host receive the Termivoa cookie, "+
			"because browsers send it to every port of a host: HTTPS 8080 /.") {
		t.Fatalf("problems=%d output:\n%s", problems, output.String())
	}
}

// A tailscale that FindBinary refused is named with the reason, instead of
// "not installed".
func TestDoctorSaysWhyTailscaleWasRefused(t *testing.T) {
	reason := "tailscale at /opt/homebrew/bin/tailscale: /opt/homebrew/bin/tailscale is owned by another user; Termivoa will not run it"
	var output bytes.Buffer
	report, problems := inspectTailscale(context.Background(), "", []string{reason}, &output)
	if problems != 1 || output.String() != "  ! "+reason+"\n" {
		t.Fatalf("problems=%d output:\n%s", problems, output.String())
	}
	if _, problem := report.phoneOrigin(); !strings.Contains(problem, reason) {
		t.Fatalf("setup problem = %q", problem)
	}
}

func TestOtherHandlersProblemNamesTheRemoveCommands(t *testing.T) {
	got := otherHandlersProblem("h.ts.net.", []string{"/", "TCP forward to 127.0.0.1:22"})
	for _, want := range []string{"origin https://h.ts.net: /, TCP forward to 127.0.0.1:22.",
		"remove it here: `tailscale serve --https=443 --set-path=/ off`, `tailscale serve --tcp=443 off`."} {
		if !strings.Contains(got, want) {
			t.Errorf("problem lacks %q:\n%s", want, got)
		}
	}
}

// The phone address comes from the one parsed tailnet status.
func TestPhoneOriginUsesTheParsedStatus(t *testing.T) {
	for status, want := range map[string]struct{ origin, problem string }{
		`{"BackendState":"Running","Self":{"DNSName":"Mac.example.ts.net."}}`: {origin: "https://mac.example.ts.net"},
		`{"BackendState":"NeedsLogin"}`:                                       {problem: "Tailscale is not connected (NeedsLogin)"},
		`{"BackendState":"Running","Self":{}}`:                                {problem: "Tailscale has no DNS name for this computer"},
	} {
		parsed, err := tailnet.ParseStatus([]byte(status))
		if err != nil {
			t.Fatal(err)
		}
		origin, problem := tailscaleReport{installed: true, tailnet: parsed}.phoneOrigin()
		if origin != want.origin || !strings.HasPrefix(problem, want.problem) || (want.problem == "") != (problem == "") {
			t.Errorf("%s: origin=%q problem=%q", status, origin, problem)
		}
	}
	if _, problem := (tailscaleReport{installed: true}).phoneOrigin(); !strings.HasPrefix(problem, "Tailscale did not report its status") {
		t.Errorf("no status: problem=%q", problem)
	}
}

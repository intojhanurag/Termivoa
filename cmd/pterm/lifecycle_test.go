package main

import (
	"bytes"
	"context"
	"errors"
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
		host:    lifecycle.Host{OS: "darwin", Executable: "/Applications/Phone Terminal.app/Contents/MacOS/pterm", DataDir: data, HomeDir: filepath.Join(root, "home"), UID: 501},
		paths:   host.Paths{DataDir: data, RuntimeDir: filepath.Join(root, "Caches", "Phone Terminal", "runtime")},
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
func startLifecycleBroker(t *testing.T, paths host.Paths) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- host.RunBroker(ctx, host.BrokerConfig{
			DataDir: paths.DataDir, RuntimeDir: paths.RuntimeDir, HostBuild: buildID, Profile: host.DevelopmentProfile,
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

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
)

func TestLifecycleModesMatchInternalModes(t *testing.T) {
	if lifecycle.BrokerMode != brokerMode || lifecycle.GatewayMode != gatewayMode {
		t.Fatal("startup registration would start the wrong process mode")
	}
}

// launchctlFake records commands and models launchd registration state.
type launchctlFake struct {
	calls      []string
	registered map[string]bool
}

func (fake *launchctlFake) Run(_ context.Context, name string, args ...string) (string, error) {
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

func TestSetupAndUninstallChangeStartupOnlyWithConsent(t *testing.T) {
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
		gateway: "http://127.0.0.1:1",
	}
	previous := loadLifecycleEnv
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	t.Cleanup(func() { loadLifecycleEnv = previous })
	plist := env.host.File(lifecycle.Services[0])
	ctx := context.Background()

	var output bytes.Buffer
	if err := runWithInput(ctx, []string{"setup"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No changes made", lifecycle.ServeCommand, "Never use Tailscale Funnel", "run launchctl bootstrap gui/501 " + plist} {
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

func TestUpRequiresExplicitDevelopmentProfile(t *testing.T) {
	if err := runUpCommand(context.Background(), nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--dev") {
		t.Fatalf("up without --dev = %v", err)
	}
}

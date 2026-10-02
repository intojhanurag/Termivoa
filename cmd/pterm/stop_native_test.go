//go:build darwin || linux || windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestStopKeepsSessionsAndShutdownEndsEverything(t *testing.T) {
	runtimeDir, client := startCommandBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runCommand := func(command string, input *os.File) (string, error) {
		t.Helper()
		var output bytes.Buffer
		err := runWithInput(ctx, []string{command, "--dev", "--runtime-dir", runtimeDir}, input, &output)
		return output.String(), err
	}
	startGateway := func() <-chan error {
		t.Helper()
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runGateway(ctx, gateway.Config{
				RuntimeDir: runtimeDir, HostBuild: buildID, ListenAddress: "127.0.0.1:0",
				Profile: host.DevelopmentProfile, Ready: func(gateway.ReadyState) { close(ready) },
			})
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("gateway stopped before ready: %v", err)
		case <-ctx.Done():
			t.Fatal("gateway was not ready")
		}
		return done
	}
	waitExit := func(done <-chan error, name string) {
		t.Helper()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s exit: %v", name, err)
			}
		case <-ctx.Done():
			t.Fatalf("%s did not exit", name)
		}
	}
	operationID := cli.NewOperationID()
	created, err := client.Create(ctx, operationID, commandProjectID, commandProfileID)
	if err != nil {
		t.Fatal(err)
	}
	ref := localclient.SessionRef{ID: created.Session.ID, Generation: created.Session.Generation}

	gatewayDone := startGateway()
	output, err := runCommand("stop", nil)
	if err != nil || !strings.Contains(output, "Gateway stopped.") ||
		!strings.Contains(output, "keeps running with 1 active session(s); nothing was ended") {
		t.Fatalf("stop = %q, %v", output, err)
	}
	waitExit(gatewayDone, "stopped gateway")
	if status, err := client.Status(ctx, ref); err != nil || status.State != protocol.Running {
		t.Fatalf("session after gateway stop = %#v, %v", status, err)
	}
	if output, err := runCommand("stop", nil); err != nil || !strings.Contains(output, "The gateway is not running.") {
		t.Fatalf("second stop = %q, %v", output, err)
	}

	gatewayDone = startGateway()
	if output, err := runCommand("shutdown", confirmationPipe(t, "no\n")); err != nil ||
		!strings.Contains(output, "This ends 1 active session(s)") || !strings.Contains(output, "Shutdown cancelled.") {
		t.Fatalf("declined shutdown = %q, %v", output, err)
	}
	if status, err := client.Status(ctx, ref); err != nil || status.State != protocol.Running {
		t.Fatalf("declined shutdown changed the session: %#v, %v", status, err)
	}
	output, err = runCommand("shutdown", confirmationPipe(t, "yes\n"))
	if err != nil || !strings.Contains(output, "Ended 1 session(s). Broker stopped.") || !strings.Contains(output, "Gateway stopped.") {
		t.Fatalf("shutdown = %q, %v", output, err)
	}
	waitExit(gatewayDone, "gateway after shutdown")
	if _, _, err := ipc.ValidateBrokerEndpoint(ctx, runtimeDir, protocol.CLI, buildID); err == nil {
		t.Fatal("broker still answers after shutdown")
	}
}

func TestSupervisorNotePrintsUnloadCommandOnlyForRegisteredServices(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("uses the macOS registration layout")
	}
	home := t.TempDir()
	runner := &launchctlFake{registered: map[string]bool{"gui/501/dev.phone-terminal.gateway": true}}
	restore := loadLifecycleEnv
	t.Cleanup(func() { loadLifecycleEnv = restore })
	loadLifecycleEnv = func() (lifecycleEnv, error) {
		return lifecycleEnv{runner: runner, host: lifecycle.Host{
			OS: "darwin", Executable: "/opt/pterm", DataDir: home + "/data", HomeDir: home, UID: 501,
		}}, nil
	}
	var text strings.Builder
	supervisorNote(context.Background(), &text, lifecycle.Services)
	if got := text.String(); strings.Count(got, "registered for startup") != 1 ||
		!strings.Contains(got, "  launchctl bootout gui/501/dev.phone-terminal.gateway\n") || strings.Contains(got, "broker") {
		t.Fatalf("supervisor note = %q", got)
	}
	for _, call := range runner.mutations() {
		t.Fatalf("supervisor note changed startup state: %s", call)
	}
}

// schtasksFake models registered, enabled Windows tasks. /End would only end
// the headless console host, so it leaves the processes running.
type schtasksFake struct{ calls []string }

func (fake *schtasksFake) Run(_ context.Context, name string, args ...string) (string, error) {
	if args[0] != "/Query" {
		fake.calls = append(fake.calls, name+" "+strings.Join(args, " "))
	}
	return "<Settings><Enabled>true</Enabled></Settings>", nil
}

func TestWindowsUninstallStopsProcessesOnlyWithoutSessions(t *testing.T) {
	runtimeDir, client := startCommandBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fake := &schtasksFake{}
	restore, restoreWait := loadLifecycleEnv, waitTimeout
	t.Cleanup(func() { loadLifecycleEnv, waitTimeout = restore, restoreWait })
	waitTimeout = 5 * time.Second
	loadLifecycleEnv = func() (lifecycleEnv, error) {
		return lifecycleEnv{runner: fake, gateway: "http://" + freeLoopbackAddress(t),
			paths: host.Paths{DataDir: filepath.Join(filepath.Dir(runtimeDir), "data"), RuntimeDir: runtimeDir},
			host: lifecycle.Host{OS: "windows", User: `PC\ada`, Executable: `C:\Users\ada\AppData\Local\Programs\Termivoa\pterm.exe`,
				DataDir: `C:\Users\ada\AppData\Local\Phone Terminal`},
		}, nil
	}
	gatewayReady, gatewayDone := make(chan struct{}), make(chan error, 1)
	go func() {
		gatewayDone <- runGateway(ctx, gateway.Config{
			RuntimeDir: runtimeDir, HostBuild: buildID, ListenAddress: "127.0.0.1:0",
			Profile: host.DevelopmentProfile, Ready: func(gateway.ReadyState) { close(gatewayReady) },
		})
	}()
	<-gatewayReady
	operationID := cli.NewOperationID()
	created, err := client.Create(ctx, operationID, commandProjectID, commandProfileID)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err = runWithInput(ctx, []string{"uninstall", "--yes"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "1 active session(s)") || len(fake.calls) != 0 {
		t.Fatalf("uninstall with a live session = %v, ran %q", err, fake.calls)
	}

	ref := localclient.SessionRef{ID: created.Session.ID, Generation: created.Session.Generation}
	operationID = cli.NewOperationID()
	if _, err := client.Terminate(ctx, ref, operationID); err != nil {
		t.Fatal(err)
	}
	if err := waitFor(ctx, func() error {
		if count, _, err := activeSessions(ctx, host.Paths{RuntimeDir: runtimeDir}); err != nil || count != 0 {
			return errors.Join(err, errors.New("session still active"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := runWithInput(ctx, []string{"uninstall", "--yes"}, nil, &output); err != nil {
		t.Fatalf("uninstall = %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "  shut down the broker") || len(fake.calls) != 4 {
		t.Fatalf("uninstall ran %q:\n%s", fake.calls, output.String())
	}
	if err := <-gatewayDone; err != nil {
		t.Fatalf("gateway exit: %v", err)
	}
	if _, _, err := ipc.ValidateBrokerEndpoint(ctx, runtimeDir, protocol.CLI, buildID); err == nil {
		t.Fatal("broker still answers after uninstall")
	}
}

//go:build darwin || windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// tailscaleFake answers the read-only status commands and records every call.
type tailscaleFake struct {
	mu     sync.Mutex
	serve  string // `serve status --json` output
	status string // `status --json` output
	calls  []string
}

func (fake *tailscaleFake) run(_ context.Context, _ string, _ io.Writer, args ...string) ([]byte, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, strings.Join(args, " "))
	switch strings.Join(args, " ") {
	case "status --json":
		return []byte(fake.status), nil
	case "serve status --json":
		return []byte(fake.serve), nil
	case "serve --bg --https=443 http://127.0.0.1:8787":
		return nil, nil
	case "serve --bg --https=8443 http://127.0.0.1:8789":
		return nil, nil
	}
	return nil, errors.New("unexpected tailscale command")
}

func (fake *tailscaleFake) changes() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var changes []string
	for _, call := range fake.calls {
		if !strings.HasSuffix(call, "--json") {
			changes = append(changes, call)
		}
	}
	return changes
}

func useTailscaleFake(t *testing.T, env *lifecycleEnv, fake *tailscaleFake) {
	t.Helper()
	env.tailscale = "tailscale-fake"
	previousEnv, previousRun := loadLifecycleEnv, tailscaleRun
	current := *env
	loadLifecycleEnv = func() (lifecycleEnv, error) { return current, nil }
	tailscaleRun = fake.run
	t.Cleanup(func() { loadLifecycleEnv, tailscaleRun = previousEnv, previousRun })
}

// TestSetupWizardGoesFromInstallToPairedPhone runs the interactive wizard
// against fake launchctl and tailscale, a real development broker and a real
// gateway: one confirmation registers startup, the Tailscale name becomes the
// phone address, Serve runs only after its own yes, Home is added, and the
// phone is approved after its code is shown.
func TestSetupWizardGoesFromInstallToPairedPhone(t *testing.T) {
	env, launchd := newLifecycleTestEnv(t)
	fake := &tailscaleFake{status: `{"BackendState":"Running","Self":{"DNSName":"mac.example.ts.net."}}`, serve: `{}`}
	useTailscaleFake(t, &env, fake)
	if err := os.MkdirAll(env.host.HomeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// launchd would start both processes after bootstrap; the test starts
	// them itself once the fake registration exists.
	registered := make(chan struct{})
	go func() {
		for launchd.bootstrapped() < 2 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		close(registered)
	}()
	output := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- runWithInput(ctx, []string{"setup"}, confirmationPipe(t, "y\ny\ny\ny\n"), output)
	}()
	select {
	case <-registered:
	case err := <-done:
		t.Fatalf("setup ended before registering: %v\n%s", err, output.String())
	}
	startLifecycleBroker(t, env.paths)
	gatewayCtx, stopGateway := context.WithCancel(ctx)
	gatewayDone := make(chan error, 1)
	go func() {
		gatewayDone <- runGateway(gatewayCtx, gateway.Config{
			RuntimeDir: env.paths.RuntimeDir, HostBuild: buildID, ListenAddress: strings.TrimPrefix(env.gateway, "http://"),
			Profile: host.DevelopmentProfile,
		})
	}()
	t.Cleanup(func() {
		stopGateway()
		<-gatewayDone
	})

	phone, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: env.paths.RuntimeDir, ClientBuild: "setup-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	redeemed, err := redeemPrintedLink(ctx, t, phone, output)
	if err != nil {
		t.Fatalf("%v\n%s", err, output.String())
	}
	if err := <-done; err != nil {
		t.Fatalf("setup: %v\n%s", err, output.String())
	}
	text := output.String()
	for _, want := range []string{
		"Continue? [y/N]", "Pairing origin set to https://mac.example.ts.net",
		"This runs: tailscale serve --bg --https=443 http://127.0.0.1:8787", "never uses Funnel",
		"Also share dev-server previews with your tailnet devices at https://mac.example.ts.net:8443/ ?",
		"This runs: tailscale serve --bg --https=8443 http://127.0.0.1:8789", "Previews are a separate origin",
		`Saved project "Home"`, "https://mac.example.ts.net/pair#",
		"shows code " + redeemed.VerificationCode + ". Approve? [y/N]",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("setup output lacks %q", want)
		}
	}
	want := []string{"serve --bg --https=443 http://127.0.0.1:8787", "serve --bg --https=8443 http://127.0.0.1:8789"}
	if got := fake.changes(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tailscale changes = %q", got)
	}
	poll, err := phone.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret})
	if err != nil || poll.Response.Status != protocol.Approved {
		t.Fatalf("poll = %#v, %v\n%s", poll, err, text)
	}
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	projects, err := admin.Projects(ctx)
	home, _ := filepath.EvalSymlinks(env.host.HomeDir)
	if err != nil || len(projects) != 1 || projects[0].Name != "Home" || projects[0].Path != home {
		t.Fatalf("projects = %#v, %v", projects, err)
	}
}

// The installer path never asks, never touches Tailscale Serve and never
// waits for a phone; a Serve conflict is reported, not overwritten.
func TestSetupApplyYesNeverChangesTailscale(t *testing.T) {
	env, launchd := newLifecycleTestEnv(t)
	fake := &tailscaleFake{
		status: `{"BackendState":"Running","Self":{"DNSName":"mac.example.ts.net."}}`,
		serve:  `{"Web":{"mac.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`,
	}
	useTailscaleFake(t, &env, fake)
	var output bytes.Buffer
	if err := runWithInput(context.Background(), []string{"setup", "--apply", "--yes"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if launchd.bootstrapped() != 2 || len(fake.changes()) != 0 {
		t.Fatalf("launchd %d bootstraps, tailscale changes %q", launchd.bootstrapped(), fake.changes())
	}
	for _, want := range []string{
		"already serves http://127.0.0.1:3000", "starts at your next login",
		"Dev-server previews over your tailnet (run it yourself): " + lifecycle.PreviewServeCommand,
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, output.String())
		}
	}
}

// The interactive preview question runs only after its own explicit yes; an
// "n" or empty answer never touches Tailscale, and a conflict on 8443 is
// reported and not overwritten. offerTailscaleServe's own consent is
// exercised end-to-end by TestSetupWizardGoesFromInstallToPairedPhone; this
// test isolates the second, separate preview question.
func TestOfferTailscalePreviewServe(t *testing.T) {
	fake := &tailscaleFake{}
	previousRun := tailscaleRun
	tailscaleRun = fake.run
	t.Cleanup(func() { tailscaleRun = previousRun })
	env := lifecycleEnv{tailscale: "tailscale-fake"}
	origin := "https://mac.example.ts.net"

	for _, answer := range []string{"n\n", "\n"} {
		fake.calls = nil
		var output bytes.Buffer
		confirmer := fileConfirmer{input: confirmationPipe(t, answer), output: &output}
		if err := offerTailscalePreviewServe(context.Background(), env, tailscaleReport{}, origin, confirmer, &output); err != nil {
			t.Fatal(err)
		}
		if len(fake.changes()) != 0 {
			t.Fatalf("answer %q ran %q", answer, fake.changes())
		}
		for _, want := range []string{
			"Also share dev-server previews with your tailnet devices at https://mac.example.ts.net:8443/ ?",
			"This runs: " + lifecycle.PreviewServeCommand,
		} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("answer %q: output lacks %q:\n%s", answer, want, output.String())
			}
		}
	}

	fake.calls = nil
	var approved bytes.Buffer
	confirmer := fileConfirmer{input: confirmationPipe(t, "y\n"), output: &approved}
	if err := offerTailscalePreviewServe(context.Background(), env, tailscaleReport{}, origin, confirmer, &approved); err != nil {
		t.Fatal(err)
	}
	if got := fake.changes(); len(got) != 1 || got[0] != "serve --bg --https=8443 http://127.0.0.1:8789" {
		t.Fatalf("approved preview ran %q", got)
	}

	fake.calls = nil
	var conflicted bytes.Buffer
	report := tailscaleReport{serve: lifecycle.ServeStatus{
		PreviewProblems: []string{"HTTPS 8443 path / on mac.example.ts.net:8443 already serves http://127.0.0.1:3000; pterm will not overwrite it"},
	}}
	confirmer = fileConfirmer{input: confirmationPipe(t, "y\n"), output: &conflicted}
	if err := offerTailscalePreviewServe(context.Background(), env, report, origin, confirmer, &conflicted); err != nil {
		t.Fatal(err)
	}
	if len(fake.changes()) != 0 {
		t.Fatalf("conflicted preview ran %q", fake.changes())
	}
	if !strings.Contains(conflicted.String(), "already serves http://127.0.0.1:3000") {
		t.Errorf("conflicted output lacks the finding:\n%s", conflicted.String())
	}
}

//go:build darwin || linux || windows

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

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// tailscaleFake answers the read-only status commands and records every call.
type tailscaleFake struct {
	mu     sync.Mutex
	serve  string // `serve status --json` output
	status string // `status --json` output
	prefs  string // `debug prefs` output; empty fails
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
	case "debug prefs":
		if fake.prefs != "" {
			return []byte(fake.prefs), nil
		}
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
// phone address, Serve runs only after its own yes, the Home folder project is added, and the
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
			Profile: platform.DevelopmentProfile,
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
		`Saved project "Home folder"`, "https://mac.example.ts.net/pair#",
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
	if err != nil || len(projects) != 1 || projects[0].Name != "Home folder" || projects[0].Path != home {
		t.Fatalf("projects = %#v, %v", projects, err)
	}
}

// The installer path never asks, never touches Tailscale Serve and never
// waits for a phone; a Serve conflict is reported, not overwritten.
func TestSetupApplyYesNeverChangesTailscale(t *testing.T) {
	env, launchd := newLifecycleTestEnv(t)
	fake := &tailscaleFake{
		status: `{"BackendState":"Running","Self":{"DNSName":"mac.example.ts.net."}}`,
		serve:  `{"Web":{"svc.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`,
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

// Setup refuses, before it changes anything, while other Tailscale Serve
// handlers share this computer's origin: each would be same-origin with the
// Termivoa app. This holds even before any other computer is added, and in
// installer mode too.
func TestSetupRefusesOtherServeHandlersOnItsOrigin(t *testing.T) {
	env, launchd := newLifecycleTestEnv(t)
	fake := &tailscaleFake{
		status: `{"BackendState":"Running","Self":{"DNSName":"mac.example.ts.net."}}`,
		serve:  `{"Web":{"mac.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"},"/grafana":{"Proxy":"http://127.0.0.1:3000"}}}}}`,
	}
	useTailscaleFake(t, &env, fake)
	for _, args := range [][]string{{"setup"}, {"setup", "--apply", "--yes"}} {
		var output bytes.Buffer
		input := confirmationPipe(t, "y\ny\ny\ny\n")
		if args[len(args)-1] == "--yes" {
			input = nil
		}
		err := runWithInput(context.Background(), args, input, &output)
		if err == nil || !strings.Contains(err.Error(), "refusing to set up") || !strings.Contains(err.Error(), "/grafana") ||
			exitCode(err) != exitOtherServeHandlers {
			t.Fatalf("%q: err = %v\n%s", args, err, output.String())
		}
		if !strings.Contains(output.String(), "`tailscale serve --https=443 --set-path=/grafana off`") ||
			strings.Contains(output.String(), "Continue? [y/N]") {
			t.Errorf("%q output:\n%s", args, output.String())
		}
		if launchd.bootstrapped() != 0 || len(fake.changes()) != 0 {
			t.Fatalf("%q: launchd %d bootstraps, tailscale changes %q", args, launchd.bootstrapped(), fake.changes())
		}
	}
}

// devicesAdmin answers only Devices; offerPairing's pairing itself is faked.
type devicesAdmin struct {
	cli.Admin
	devices []protocol.AdminRPCDevice
}

func (admin devicesAdmin) Devices(context.Context) ([]protocol.AdminRPCDevice, error) {
	return admin.devices, nil
}

// On an interactive terminal setup asks whether Termivoa is already on the
// phone: no pairs a phone as before; yes lists this owner's other computers
// (no phones, no other owners) and runs the `pterm pair --via` flow for the
// chosen one, asking again after a wrong answer. A tagged computer needs its
// own yes. Without a terminal, or without other computers, it pairs at once
// as before; when the phone cannot reach this computer, it pairs nothing.
func TestOfferPairingAsksWhetherTermivoaIsOnThePhone(t *testing.T) {
	parse := func(self, peers string) *tailnet.Status {
		status, err := tailnet.ParseStatus([]byte(`{"BackendState":"Running","MagicDNSSuffix":"example.ts.net",
			"Self":{"ID":"nSelf","DNSName":"vps.example.ts.net.",` + self + `},
			"User":{"1":{"LoginName":"ada@example.com"},"2":{"LoginName":"bob@example.com"}},
			"Peer":{` + peers + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	mac := `"a":{"ID":"nMac","DNSName":"mac.example.ts.net.","UserID":1,"OS":"macOS"}`
	others := mac + `,"b":{"ID":"nAir","DNSName":"air.example.ts.net.","UserID":1,"OS":"macOS"},
		"c":{"ID":"nPhone","DNSName":"iphone.example.ts.net.","UserID":1,"OS":"iOS"},
		"d":{"ID":"nPi","DNSName":"pi.example.ts.net.","UserID":2,"OS":"linux"}`
	several := parse(`"UserID":1`, others)
	tagged := parse(`"UserID":9,"Tags":["tag:vps"]`, others)
	var pairs []cli.PairOptions
	var pairErr error
	previous := pairPhone
	pairPhone = func(_ context.Context, _ cli.Admin, options cli.PairOptions, _ io.Writer) error {
		pairs = append(pairs, options)
		return pairErr
	}
	t.Cleanup(func() { pairPhone = previous })
	paired := []protocol.AdminRPCDevice{{}}
	const (
		question = "Is Termivoa already on your phone?\n  1) No, this is my first computer\n  2) Yes, add this computer to it\n"
		which    = "Which computer did you first pair your phone with?\n"
		another  = "Pair another phone? [y/N]"
		skipped  = "Setup is done. Next: to pair a phone, run `pterm pair`; to add this computer to a Termivoa app you already use, " +
			"run `pterm pair --via <name>` (<name>: air, mac)."
		added = "Setup is done. Next: in the Termivoa app on your phone, switch to this computer and start a session."
	)

	for name, test := range map[string]struct {
		answers        string
		status         *tailnet.Status
		devices        []protocol.AdminRPCDevice
		notTerminal    bool
		phoneFix       string
		pairErr        error
		pairs          int
		via            string
		allowTagged    bool
		want, unwanted []string
		last           string
	}{
		"first computer": {answers: "1\n", status: several, pairs: 1, want: []string{question, "Pair your phone:"},
			last: "Setup is done. Next: open Termivoa on your phone and start a session."},
		"add by number": {answers: "2\n2\n", status: several, pairs: 1, via: "mac",
			want:     []string{question, which + "  1) air\n  2) mac\nType a number or a name (Enter skips): ", "`pterm pair --via mac`"},
			unwanted: []string{"iphone", ") pi", "Pair your phone:"}, last: added},
		"add by name, any case": {answers: "2\nMac\n", status: several, pairs: 1, via: "mac", last: added},
		"a number out of range asks again": {answers: "2\n0\n5\n1\n", status: several, pairs: 1, via: "air",
			want: []string{"Type a number from 1 to 2, or a name.\n"}},
		"an unknown name asks again": {answers: "2\nstudio\nMy Mac\n\n", status: several,
			want:     []string{`No computer in your tailnet is named "studio".`, `No computer in your tailnet is named "My Mac".`},
			unwanted: []string{"invalid --via"}, last: skipped},
		"another owner's computer asks again": {answers: "2\npi\nmac\n", status: several, pairs: 1, via: "mac",
			want: []string{"pi belongs to another Tailscale user"}},
		"one candidate still asks": {answers: "2\n1\n", status: parse(`"UserID":1`, mac), pairs: 1, via: "mac",
			want: []string{question, "  1) mac\nType a number"}},
		"a typo asks again":   {answers: "first\n1\n", status: several, pairs: 1, want: []string{question}},
		"enter skips pairing": {answers: "\n", status: several, want: []string{question}, last: skipped},
		"one candidate names it when skipped": {answers: "\n", status: parse(`"UserID":1`, mac),
			last: "Setup is done. Next: to pair a phone, run `pterm pair`; to add this computer to a Termivoa app you already use, run `pterm pair --via mac`."},
		"not approved": {answers: "2\n2\n", status: several, pairErr: cli.ErrNotPaired, pairs: 1, via: "mac",
			unwanted: []string{added}, last: "Setup is done, but your phone is not connected yet. To connect it, run `pterm pair --via mac`."},
		"tagged computer, allowed": {answers: "2\nmac\ny\n", status: tagged, pairs: 1, via: "mac", allowTagged: true,
			want: []string{which + "  1) air\n  2) mac\n  3) pi\n", "Tailscale cannot show that you own both computers: this computer has tag:vps.",
				"(the same as `--allow-tagged`). [y/N] ", "`pterm pair --via mac --allow-tagged`"}, last: added},
		"tagged computer, refused": {answers: "2\n2\nn\n", status: tagged,
			last: "Setup is done, but your phone is not connected yet. To connect it, run `pterm pair --via mac --allow-tagged`."},
		"no other computer":   {status: parse(`"UserID":1`, ""), pairs: 1, unwanted: []string{question}},
		"no Tailscale status": {pairs: 1, unwanted: []string{question}},
		"not a terminal":      {status: several, devices: paired, notTerminal: true, pairs: 1, unwanted: []string{question, another}},
		"already paired, declined": {answers: "n\n", status: several, devices: paired, want: []string{another},
			unwanted: []string{question}, last: skipped},
		"already paired, another": {answers: "y\n1\n", status: several, devices: paired, pairs: 1, want: []string{another, question}},
		"phone cannot reach this computer": {answers: "1\n", status: several, phoneFix: "run `pterm setup` again and say yes to sharing on port 443.",
			unwanted: []string{question}, last: "Setup is done, but your phone cannot reach this computer yet: run `pterm setup` again and say yes to sharing on port 443."},
	} {
		pairs, pairErr = nil, test.pairErr
		var output bytes.Buffer
		confirmer := fileConfirmer{input: confirmationPipe(t, test.answers), output: &output}
		err := offerPairing(context.Background(), lifecycleEnv{}, devicesAdmin{devices: test.devices}, test.status, test.phoneFix, !test.notTerminal, confirmer, &output)
		text := output.String()
		if err != nil || len(pairs) != test.pairs ||
			test.pairs == 1 && (pairs[0].Via != test.via || pairs[0].AllowTagged != test.allowTagged || pairs[0].Confirmer == nil || pairs[0].FirstComputerWarning) {
			t.Fatalf("%s: err=%v pairs=%+v\n%s", name, err, pairs, text)
		}
		for _, want := range test.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s: output lacks %q:\n%s", name, want, text)
			}
		}
		for _, unwanted := range test.unwanted {
			if strings.Contains(text, unwanted) {
				t.Errorf("%s: output contains %q:\n%s", name, unwanted, text)
			}
		}
		if test.last != "" && !strings.HasSuffix(text, test.last+"\n") {
			t.Errorf("%s: last line is not %q:\n%s", name, test.last, text)
		}
	}
}

// When the phone cannot reach this computer over HTTPS 443, setup pairs
// nothing and ends with the fix for the reason it found.
func TestPhoneAccessFixNamesTheNextStep(t *testing.T) {
	again := ", then run `pterm setup` again."
	for name, test := range map[string]struct {
		report tailscaleReport
		origin string
		served bool
		want   string
	}{
		"served":         {origin: "https://vps.example.ts.net", served: true},
		"no address":     {want: "Tailscale is not installed. Install it" + again},
		"operator":       {report: tailscaleReport{needsOperator: true}, origin: "https://vps.example.ts.net", want: lifecycle.TailscaleOperatorCommand + again},
		"said no":        {origin: "https://vps.example.ts.net", want: "run `pterm setup` again and say yes to sharing on port 443."},
		"443 is in use":  {report: tailscaleReport{serve: lifecycle.ServeStatus{Problems: []string{"x"}}}, origin: "https://vps.example.ts.net", want: "(see the warnings). Fix it" + again},
		"status unknown": {report: tailscaleReport{serveErr: errors.New("x")}, origin: "https://vps.example.ts.net", want: lifecycle.ServeCommand + again},
	} {
		got := test.report.phoneAccessFix(test.origin, "Tailscale is not installed. Install it"+again, test.served)
		if test.want == "" && got != "" || !strings.HasSuffix(got, test.want) {
			t.Errorf("%s: %q, want …%q", name, got, test.want)
		}
	}
}

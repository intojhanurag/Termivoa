//go:build darwin || linux || windows

package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// syncBuffer is command output that a test goroutine reads while the
// command still writes.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *syncBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *syncBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

// redeemPrintedLink plays the phone: it waits for the pairing link in output,
// redeems its grant through the real gateway client and returns the result.
func redeemPrintedLink(ctx context.Context, t *testing.T, client *gatewayclient.Client, output *syncBuffer,
) (protocol.HTTPPairRedeemResponse, error) {
	for {
		text := output.String()
		if index := strings.Index(text, "/pair#"); index >= 0 && len(text) >= index+6+protocol.SecretTextBytes {
			grant := text[index+6 : index+6+protocol.SecretTextBytes]
			return client.RedeemPairing(ctx, protocol.HTTPPairRedeemRequest{Grant: grant, DeviceLabel: "Test phone"})
		}
		select {
		case <-ctx.Done():
			return protocol.HTTPPairRedeemResponse{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestPairWaitsForThePhoneAndApprovesOnlyOnYes drives `pterm pair` against a
// real development broker: a phone redeems the printed link, the command
// shows its code, and only a "y" answer approves it.
func TestPairWaitsForThePhoneAndApprovesOnlyOnYes(t *testing.T) {
	for _, answer := range []string{"y", "n"} {
		t.Run(answer, func(t *testing.T) {
			runtimeDir, _ := startCommandBroker(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			phone, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: runtimeDir, ClientBuild: "pair-test"})
			if err != nil {
				t.Fatal(err)
			}
			defer phone.Close()
			output := &syncBuffer{}
			done := make(chan error, 1)
			go func() {
				done <- runWithInput(ctx, []string{"pair", "--dev", "--runtime-dir", runtimeDir}, confirmationPipe(t, answer+"\n"), output)
			}()
			redeemed, err := redeemPrintedLink(ctx, t, phone, output)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("pair: %v\n%s", err, output.String())
			}
			text := output.String()
			if !strings.Contains(text, `Phone "Test phone" shows code `+redeemed.VerificationCode+". Approve? [y/N]") {
				t.Fatalf("pair did not show the phone's code:\n%s", text)
			}
			poll, err := phone.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret})
			if err != nil {
				t.Fatal(err)
			}
			if approved := poll.Response.Status == protocol.Approved; approved != (answer == "y") {
				t.Fatalf("answer %q gave poll status %s", answer, poll.Response.Status)
			}
		})
	}
}

// TestNoWaitPairAndFlagsAfterArguments keeps the scriptable path: --no-wait
// prints the link and returns, and approval works with flags after the ID.
func TestNoWaitPairAndFlagsAfterArguments(t *testing.T) {
	runtimeDir, _ := startCommandBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	phone, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: runtimeDir, ClientBuild: "pair-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	output := &syncBuffer{}
	if err := runWithInput(ctx, []string{"pair", "--dev", "--runtime-dir", runtimeDir, "--no-wait"}, nil, output); err != nil {
		t.Fatal(err)
	}
	redeemed, err := redeemPrintedLink(ctx, t, phone, output)
	if err != nil {
		t.Fatal(err)
	}
	var approve bytes.Buffer
	if err := runWithInput(ctx, []string{"pair", "approve", redeemed.PendingID, "--dev", "--runtime-dir", runtimeDir}, nil, &approve); err != nil {
		t.Fatal(err)
	}
	poll, err := phone.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret})
	if err != nil || poll.Response.Status != protocol.Approved {
		t.Fatalf("poll after approve = %#v, %v", poll, err)
	}
	var rename bytes.Buffer
	if err := runWithInput(ctx, []string{"devices", "rename", "--dev", "--runtime-dir", runtimeDir, poll.Response.Device.ID, "Work", "phone"}, nil, &rename); err != nil {
		t.Fatal(err)
	}
	principal, err := phone.Authenticate(ctx, poll.Credential)
	if err != nil || principal.DeviceLabel != "Work phone" {
		t.Fatalf("renamed device = %#v, %v", principal, err)
	}
}

// TestRunningGatewayFollowsConfigOrigin proves `pterm config origin` reaches
// an already running gateway (the real process mode) without a restart.
func TestRunningGatewayFollowsConfigOrigin(t *testing.T) {
	runtimeDir, _ := startCommandBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := make(chan gateway.ReadyState, 1)
	done := make(chan error, 1)
	gatewayCtx, stopGateway := context.WithCancel(ctx)
	go func() {
		done <- runGateway(gatewayCtx, gateway.Config{
			RuntimeDir: runtimeDir, HostBuild: buildID, ListenAddress: "127.0.0.1:0",
			Profile: host.DevelopmentProfile, Ready: func(state gateway.ReadyState) { ready <- state },
		})
	}()
	defer func() {
		stopGateway()
		<-done
	}()
	var address string
	select {
	case state := <-ready:
		address = state.Address
	case err := <-done:
		t.Fatalf("gateway stopped: %v", err)
	}
	status := func(hostHeader string) int {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/healthz", nil)
		request.Host = hostHeader
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	const configured = "localhost:4545"
	if code := status(configured); code != http.StatusMisdirectedRequest {
		t.Fatalf("host before config origin = %d", code)
	}
	var output bytes.Buffer
	if err := runWithInput(ctx, []string{"config", "origin", "http://" + configured, "--dev", "--runtime-dir", runtimeDir}, nil, &output); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for status(configured) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("running gateway did not accept the configured origin")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code := status("attacker.example"); code != http.StatusMisdirectedRequest {
		t.Fatalf("unrelated host = %d", code)
	}
}

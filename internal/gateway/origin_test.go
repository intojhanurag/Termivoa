package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/platform"
)

// The origin saved in the broker joins the exact allow-list on refresh, only
// if it is valid for the profile, and replaces the previous one.
func TestConfiguredOriginIsExactAndProfileChecked(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	const desk = "desk.tailnet.ts.net"
	health := func(host string) int {
		return do(handler, http.MethodGet, "/healthz", "", requestOptions{host: host}).Code
	}
	if code := health(desk); code != http.StatusMisdirectedRequest {
		t.Fatalf("unconfigured host = %d", code)
	}
	broker.setOrigin("https://" + desk)
	handler.refreshOrigin(context.Background())
	if code := health(desk); code != http.StatusOK {
		t.Fatalf("configured host = %d", code)
	}
	for _, host := range []string{desk + ".evil", "evil." + desk, desk + ":8443", "attacker.example"} {
		if code := health(host); code != http.StatusMisdirectedRequest {
			t.Fatalf("near-match host %q = %d", host, code)
		}
	}
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{host: desk, origin: "https://" + desk}); response.Code != http.StatusOK {
		t.Fatalf("redeem from configured origin = %d %s", response.Code, response.Body)
	}
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{host: desk, origin: "http://" + desk}); response.Code != http.StatusForbidden {
		t.Fatalf("redeem from http origin = %d", response.Code)
	}
	// The --origin flag keeps working next to the configured origin.
	if code := health(testHost); code != http.StatusOK {
		t.Fatalf("flag origin host = %d", code)
	}

	// Production never accepts an http origin, even from the broker.
	broker.setOrigin("http://127.0.0.1:8788")
	handler.refreshOrigin(context.Background())
	if code := health(desk); code != http.StatusMisdirectedRequest {
		t.Fatalf("replaced origin still accepted: %d", code)
	}
	if code := health("127.0.0.1:8788"); code != http.StatusMisdirectedRequest {
		t.Fatalf("http origin from broker accepted in production: %d", code)
	}
}

// The page on the bound loopback address can pair in production too.
func TestLoopbackOriginIsAlwaysAllowed(t *testing.T) {
	handler := newTestHandler(t, newFakeBroker())
	for _, host := range []string{testBound, "localhost:8787"} {
		response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{host: host, origin: "http://" + host})
		if response.Code != http.StatusOK {
			t.Fatalf("loopback origin %s = %d %s", host, response.Code, response.Body)
		}
	}
	for _, origin := range []string{"http://127.0.0.1:9999", "https://127.0.0.1:8787", "http://localhost"} {
		if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{host: testBound, origin: origin}); response.Code != http.StatusForbidden {
			t.Fatalf("origin %s = %d", origin, response.Code)
		}
	}
}

// A running gateway picks up `pterm config origin` from the broker within a
// few probe intervals, without a restart.
func TestRunningGatewayPicksUpConfiguredOrigin(t *testing.T) {
	runtimeDir := shortGatewayRuntimeDir(t)
	brokerCtx, cancelBroker := context.WithCancel(context.Background())
	listener, err := ipc.ListenBroker(brokerCtx, ipc.ListenerConfig{RuntimeDir: runtimeDir, BrokerGeneration: gatewayTestGeneration})
	if err != nil {
		t.Fatal(err)
	}
	brokerDone := serveHandshakes(brokerCtx, listener)
	t.Cleanup(func() {
		cancelBroker()
		_ = listener.Close()
		<-brokerDone
	})
	broker := newFakeBroker()
	gatewayCtx, cancelGateway := context.WithCancel(context.Background())
	ready := make(chan ReadyState, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(gatewayCtx, Config{
			RuntimeDir: runtimeDir, HostBuild: "gateway-test", ListenAddress: "127.0.0.1:0",
			Profile: platform.DevelopmentProfile, Broker: broker,
			ProbeInterval: 25 * time.Millisecond, Ready: func(state ReadyState) { ready <- state },
		})
	}()
	t.Cleanup(func() {
		cancelGateway()
		<-done
	})
	state := waitReady(t, ready)
	client := &http.Client{Timeout: time.Second}
	status := func(host string) int {
		request, _ := http.NewRequest(http.MethodGet, "http://"+state.Address+"/healthz", nil)
		request.Host = host
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	const configured = "localhost:4444"
	if code := status(configured); code != http.StatusMisdirectedRequest {
		t.Fatalf("host before configuration = %d", code)
	}
	broker.setOrigin("http://" + configured)
	deadline := time.Now().Add(2 * time.Second)
	for status(configured) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("gateway did not pick up the configured origin")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code := status("attacker.example"); code != http.StatusMisdirectedRequest {
		t.Fatalf("unrelated host = %d", code)
	}
}

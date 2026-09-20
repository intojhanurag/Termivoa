package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
)

const gatewayTestGeneration = "91111111-1111-4111-8111-111111111111"

func TestRunServesRestrictedHealthAndStopsIndependently(t *testing.T) {
	runtimeDir := shortGatewayRuntimeDir(t)
	brokerCtx, cancelBroker := context.WithCancel(context.Background())
	listener, err := ipc.ListenBroker(brokerCtx, ipc.ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: gatewayTestGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	brokerDone := serveHandshakes(brokerCtx, listener)
	t.Cleanup(func() {
		cancelBroker()
		_ = listener.Close()
		<-brokerDone
	})

	gatewayCtx, cancelGateway := context.WithCancel(context.Background())
	ready := make(chan ReadyState, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(gatewayCtx, Config{
			RuntimeDir: runtimeDir, HostBuild: "gateway-test", ListenAddress: "127.0.0.1:0",
			ProbeInterval: 25 * time.Millisecond, Ready: func(state ReadyState) { ready <- state },
		})
	}()
	state := waitReady(t, ready)
	if state.BrokerGeneration != gatewayTestGeneration {
		t.Fatalf("broker generation = %q", state.BrokerGeneration)
	}

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + state.Address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	var payload struct {
		Status           string `json:"status"`
		BrokerGeneration string `json:"brokerGeneration"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if payload.Status != "ok" || payload.BrokerGeneration != gatewayTestGeneration {
		t.Fatalf("health payload = %#v", payload)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("health security headers = %#v", response.Header)
	}

	request, _ := http.NewRequest(http.MethodGet, "http://"+state.Address+"/healthz", nil)
	request.Host = "attacker.example"
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("mismatched Host status = %d", response.StatusCode)
	}
	response.Body.Close()

	cancelGateway()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gateway shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down")
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancelProbe()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", state.Address)
	if err == nil {
		_ = connection.Close()
		t.Fatal("gateway listener remained reachable after shutdown")
	}
}

func TestRunRejectsNonLoopbackListener(t *testing.T) {
	runtimeDir := shortGatewayRuntimeDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: gatewayTestGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := serveHandshakes(ctx, listener)
	defer func() {
		_ = listener.Close()
		<-done
	}()
	err = Run(context.Background(), Config{
		RuntimeDir: runtimeDir, HostBuild: "gateway-test", ListenAddress: "0.0.0.0:0",
	})
	if err == nil || err.Error() != "gateway: listener must bind an IPv4 loopback address" {
		t.Fatalf("non-loopback error = %v", err)
	}
}

func TestHealthHandlerRejectsWrongPathMethodAndHost(t *testing.T) {
	handler := &healthHandler{
		broker:       brokerState{available: true, generation: gatewayTestGeneration},
		allowedHosts: map[string]struct{}{"127.0.0.1:8787": {}},
	}
	for _, test := range []struct {
		name, method, path, host string
		want                     int
	}{
		{name: "host", method: http.MethodGet, path: "/healthz", host: "bad.example", want: http.StatusMisdirectedRequest},
		{name: "path", method: http.MethodGet, path: "/api/v1/sessions", host: "127.0.0.1:8787", want: http.StatusNotFound},
		{name: "method", method: http.MethodPost, path: "/healthz", host: "127.0.0.1:8787", want: http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://127.0.0.1:8787"+test.path, nil)
			request.Host = test.host
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func serveHandshakes(ctx context.Context, listener *ipc.BrokerListener) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = ipc.ServerHandshake(ctx, connection, gatewayTestGeneration, "broker-test")
			}()
		}
	}()
	return done
}

func waitReady(t *testing.T, ready <-chan ReadyState) ReadyState {
	t.Helper()
	select {
	case state := <-ready:
		return state
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not become ready")
		return ReadyState{}
	}
}

func TestProbeBrokerReportsMissingEndpoint(t *testing.T) {
	_, err := probeBroker(context.Background(), filepath.Join(t.TempDir(), "missing"), "test")
	if !errors.Is(err, ipc.ErrEndpointMissing) {
		t.Fatalf("missing endpoint error = %v", err)
	}
}

func shortGatewayRuntimeDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pterm-gateway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return filepath.Join(root, "runtime")
}

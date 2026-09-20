package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	ProductionListenAddress = "127.0.0.1:8787"
	defaultProbeInterval    = time.Second
	probeTimeout            = 750 * time.Millisecond
	shutdownTimeout         = 3 * time.Second
)

type Config struct {
	RuntimeDir    string
	HostBuild     string
	ListenAddress string
	ProbeInterval time.Duration

	// AllowedHosts adds exact private hostnames after they are deliberately
	// configured. The bound loopback address and its localhost spelling are
	// always accepted; forwarded identity headers are never consulted.
	AllowedHosts []string

	// Ready is an internal integration-test observation point. The callback
	// must return promptly and receives no server ownership.
	Ready func(ReadyState)
}

type ReadyState struct {
	Address          string
	BrokerGeneration string
}

type brokerState struct {
	available  bool
	generation string
}

type healthHandler struct {
	mu           sync.RWMutex
	broker       brokerState
	allowedHosts map[string]struct{}
}

// Run serves the loopback gateway independently from the broker. It receives
// no Registry, Store, PTY, or process capability; the only host connection is
// the restricted, versioned broker IPC handshake.
func Run(ctx context.Context, config Config) error {
	if ctx == nil {
		return errors.New("gateway: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.RuntimeDir == "" {
		return errors.New("gateway: runtime directory is required")
	}
	if len(config.HostBuild) == 0 || len(config.HostBuild) > 128 {
		return errors.New("gateway: build must be 1..128 bytes")
	}
	if config.ListenAddress == "" {
		config.ListenAddress = ProductionListenAddress
	}
	if config.ProbeInterval == 0 {
		config.ProbeInterval = defaultProbeInterval
	}
	if config.ProbeInterval < 25*time.Millisecond || config.ProbeInterval > time.Minute {
		return errors.New("gateway: broker probe interval must be between 25ms and 1m")
	}

	initial, err := probeBroker(ctx, config.RuntimeDir, config.HostBuild)
	if err != nil {
		return fmt.Errorf("gateway: connect to broker: %w", err)
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("gateway: listen: %w", err)
	}
	if err := requireIPv4Loopback(listener.Addr()); err != nil {
		_ = listener.Close()
		return err
	}

	allowedHosts, err := exactAllowedHosts(listener.Addr().String(), config.AllowedHosts)
	if err != nil {
		_ = listener.Close()
		return err
	}
	handler := &healthHandler{
		broker:       brokerState{available: true, generation: initial.BrokerGeneration},
		allowedHosts: allowedHosts,
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		monitorBroker(runCtx, handler, config)
	}()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	if config.Ready != nil {
		config.Ready(ReadyState{Address: listener.Addr().String(), BrokerGeneration: initial.BrokerGeneration})
	}

	var serveErr error
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		serveErr = errors.Join(server.Shutdown(shutdownCtx), <-serveDone)
		shutdownCancel()
	case serveErr = <-serveDone:
		cancel()
		_ = listener.Close()
	}
	cancel()
	<-monitorDone
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return serveErr
}

func probeBroker(ctx context.Context, runtimeDir, build string) (protocol.IPCWelcomeClass, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, welcome, err := ipc.ValidateBrokerEndpoint(probeCtx, runtimeDir, protocol.Gateway, build)
	return welcome, err
}

func monitorBroker(ctx context.Context, handler *healthHandler, config Config) {
	ticker := time.NewTicker(config.ProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			welcome, err := probeBroker(ctx, config.RuntimeDir, config.HostBuild)
			handler.mu.Lock()
			if err != nil {
				handler.broker.available = false
			} else {
				handler.broker = brokerState{available: true, generation: welcome.BrokerGeneration}
			}
			handler.mu.Unlock()
		}
	}
}

func (handler *healthHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	if _, ok := handler.allowedHosts[request.Host]; !ok {
		http.Error(response, "misdirected request", http.StatusMisdirectedRequest)
		return
	}
	if request.URL.Path != "/healthz" {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handler.mu.RLock()
	state := handler.broker
	handler.mu.RUnlock()
	response.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	payload := struct {
		Status           string `json:"status"`
		Broker           string `json:"broker"`
		BrokerGeneration string `json:"brokerGeneration,omitempty"`
	}{Status: "ok", Broker: "available", BrokerGeneration: state.generation}
	if !state.available {
		status = http.StatusServiceUnavailable
		payload.Status = "unavailable"
		payload.Broker = "unavailable"
		payload.BrokerGeneration = ""
	}
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func requireIPv4Loopback(address net.Addr) error {
	tcpAddress, ok := address.(*net.TCPAddr)
	if !ok || tcpAddress.IP == nil || !tcpAddress.IP.IsLoopback() || tcpAddress.IP.To4() == nil {
		return errors.New("gateway: listener must bind an IPv4 loopback address")
	}
	return nil
}

func exactAllowedHosts(bound string, configured []string) (map[string]struct{}, error) {
	address, err := netip.ParseAddrPort(bound)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsLoopback() {
		return nil, errors.New("gateway: bound address is not canonical IPv4 loopback")
	}
	allowed := map[string]struct{}{
		bound: {},
		net.JoinHostPort("localhost", fmt.Sprint(address.Port())): {},
	}
	for _, host := range configured {
		if host == "" || strings.TrimSpace(host) != host || strings.ContainsAny(host, "/\\\r\n\t ") {
			return nil, errors.New("gateway: allowed hosts must be exact authority values")
		}
		allowed[host] = struct{}{}
	}
	return allowed, nil
}

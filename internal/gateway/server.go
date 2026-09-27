package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	ProductionListenAddress  = "127.0.0.1:8787"
	DevelopmentListenAddress = "127.0.0.1:0"
	defaultProbeInterval     = time.Second
	probeTimeout             = 750 * time.Millisecond
	shutdownTimeout          = 3 * time.Second
	defaultBrokerWait        = 2 * time.Minute
	maxBrokerRetry           = 5 * time.Second
)

type Config struct {
	RuntimeDir    string
	HostBuild     string
	ListenAddress string
	// PreviewListenAddress is the loopback listener of the separate
	// dev-server preview origin; "" serves no previews. See preview.go.
	PreviewListenAddress string
	Profile              platform.Profile
	ProbeInterval        time.Duration
	ShutdownTimeout      time.Duration

	// BrokerWait bounds how long startup retries an unreachable broker; zero
	// means two minutes. At logon the broker may start after the gateway.
	BrokerWait time.Duration

	// Origins lists exact browser origins such as
	// "https://mac.tailnet.ts.net". Their hosts join the bound loopback
	// address as the only accepted Host values. Production accepts https
	// origins only; development accepts only loopback origins. The bound
	// loopback http origins are always accepted, and the origin configured in
	// the broker (`pterm config origin`) is added and refreshed on every broker
	// probe, so it needs no restart. Forwarded headers are never consulted.
	Origins []string

	// Broker authorizes every browser request. Nil serves only health and the
	// static site; the API and WebSocket routes then return 404.
	Broker gatewayapi.Broker

	// Ready is an internal integration-test observation point. The callback
	// must return promptly and receives no server ownership.
	Ready func(ReadyState)

	// connState is a package-test observation point. It must not block and
	// receives no authority to change gateway or broker state.
	connState func(net.Conn, http.ConnState)
}

type ReadyState struct {
	Address          string
	PreviewAddress   string
	BrokerGeneration string
}

type brokerState struct {
	available  bool
	generation string
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
	runtimeDir, err := platform.ValidateRuntimeDir(config.Profile, config.RuntimeDir)
	if err != nil {
		return err
	}
	config.RuntimeDir = runtimeDir
	if len(config.HostBuild) == 0 || len(config.HostBuild) > 128 {
		return errors.New("gateway: build must be 1..128 bytes")
	}
	if err := ValidateListenAddress(config.Profile, config.ListenAddress); err != nil {
		return err
	}
	if config.PreviewListenAddress != "" {
		if err := ValidatePreviewListenAddress(config.Profile, config.PreviewListenAddress); err != nil {
			return err
		}
	}
	if config.ProbeInterval == 0 {
		config.ProbeInterval = defaultProbeInterval
	}
	if config.ProbeInterval < 25*time.Millisecond || config.ProbeInterval > time.Minute {
		return errors.New("gateway: broker probe interval must be between 25ms and 1m")
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = shutdownTimeout
	}
	if config.ShutdownTimeout < 25*time.Millisecond || config.ShutdownTimeout > time.Minute {
		return errors.New("gateway: shutdown timeout must be between 25ms and 1m")
	}

	if config.BrokerWait == 0 {
		config.BrokerWait = defaultBrokerWait
	}
	initial, err := waitForBroker(ctx, config)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("gateway: listen: %w", err)
	}
	if err := requireIPv4Loopback(listener.Addr()); err != nil {
		_ = listener.Close()
		return err
	}

	handler, err := newServer(config, listener.Addr().String(), initial.BrokerGeneration)
	if err != nil {
		_ = listener.Close()
		return err
	}
	var preview *http.Server
	var previewListener net.Listener
	var previewHandle *previewHandler
	if config.PreviewListenAddress != "" && handler.api != nil {
		previewListener, err = net.Listen("tcp", config.PreviewListenAddress)
		if err == nil {
			err = requireIPv4Loopback(previewListener.Addr())
		}
		if err == nil {
			previewHandle, err = newPreviewHandler(handler, previewListener.Addr().String())
		}
		if err != nil {
			// Previews are optional: a busy port must not take the app down.
			if previewListener != nil {
				_ = previewListener.Close()
			}
			fmt.Fprintf(os.Stderr, "pterm gateway: dev-server previews are off: %v\n", err)
		} else {
			preview = &http.Server{
				Handler:           previewHandle,
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       60 * time.Second,
				MaxHeaderBytes:    64 * 1024,
			}
			preview.RegisterOnShutdown(previewHandle.stop)
		}
	}
	handler.refreshOrigin(ctx)
	server := &http.Server{
		Handler:           handler,
		ConnState:         config.connState,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Hijacked WebSocket connections are invisible to Shutdown; stop them
	// explicitly and wait for their handlers before returning.
	server.RegisterOnShutdown(handler.stopLive)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		monitorBroker(runCtx, handler, config)
	}()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	ready := ReadyState{Address: listener.Addr().String(), BrokerGeneration: initial.BrokerGeneration}
	previewDone := make(chan error, 1)
	if preview != nil {
		go func() { previewDone <- preview.Serve(previewListener) }()
		ready.PreviewAddress = previewListener.Addr().String()
	}
	if config.Ready != nil {
		config.Ready(ready)
	}

	var serveErr error
	select {
	case serveErr = <-previewDone:
		// The preview listener failed: stop the whole gateway rather than run
		// half of it; the supervisor restarts it.
		serveErr = errors.Join(errors.New("gateway: preview listener stopped"), filterServeError(serveErr), server.Close())
		<-serveDone
		preview = nil
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		shutdownErr := server.Shutdown(shutdownCtx)
		shutdownCancel()
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		serveErr = errors.Join(shutdownErr, filterServeError(<-serveDone))
	case serveErr = <-serveDone:
		serveErr = filterServeError(serveErr)
		cancel()
		_ = listener.Close()
	}
	if preview != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		if preview.Shutdown(shutdownCtx) != nil {
			_ = preview.Close()
		}
		shutdownCancel()
		previewHandle.stop()
		<-previewDone
	}
	cancel()
	<-monitorDone
	handler.stopLive()
	handler.live.Wait()
	return serveErr
}

func ValidateListenAddress(profile platform.Profile, address string) error {
	if profile != platform.ProductionProfile && profile != platform.DevelopmentProfile {
		return errors.New("gateway: profile must be production or development")
	}
	if profile == platform.ProductionProfile && address != ProductionListenAddress {
		return errors.New("gateway: production listener is fixed")
	}
	if profile == platform.DevelopmentProfile && address == ProductionListenAddress {
		return errors.New("gateway: development listener must not reuse the production address")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("gateway: listener must be a literal IPv4 loopback address and port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() || !ip.IsLoopback() || port == "" {
		return errors.New("gateway: listener must be a literal IPv4 loopback address and port")
	}
	parsed, err := netip.ParseAddrPort(address)
	if err != nil || parsed.Addr() != ip {
		return errors.New("gateway: listener must be a literal IPv4 loopback address and port")
	}
	return nil
}

func filterServeError(err error) error {
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func probeBroker(ctx context.Context, runtimeDir, build string) (protocol.IPCWelcomeClass, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, welcome, err := ipc.ValidateBrokerEndpoint(probeCtx, runtimeDir, protocol.Gateway, build)
	return welcome, err
}

// waitForBroker probes at once, then retries with backoff until the broker
// answers, ctx ends, or config.BrokerWait passes.
func waitForBroker(ctx context.Context, config Config) (protocol.IPCWelcomeClass, error) {
	deadline := time.Now().Add(config.BrokerWait)
	delay := 100 * time.Millisecond
	for {
		welcome, err := probeBroker(ctx, config.RuntimeDir, config.HostBuild)
		if err == nil {
			return welcome, nil
		}
		if ctx.Err() != nil {
			return welcome, ctx.Err()
		}
		if time.Until(deadline) <= 0 {
			return welcome, fmt.Errorf("gateway: connect to broker (waited %s): %w", config.BrokerWait, err)
		}
		timer := time.NewTimer(min(delay, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return welcome, ctx.Err()
		case <-timer.C:
		}
		delay = min(2*delay, maxBrokerRetry)
	}
}

func monitorBroker(ctx context.Context, handler *gatewayHandler, config Config) {
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
			if err == nil {
				handler.refreshOrigin(ctx)
			}
		}
	}
}

func requireIPv4Loopback(address net.Addr) error {
	tcpAddress, ok := address.(*net.TCPAddr)
	if !ok || tcpAddress.IP == nil || !tcpAddress.IP.IsLoopback() || tcpAddress.IP.To4() == nil {
		return errors.New("gateway: listener must bind an IPv4 loopback address")
	}
	return nil
}

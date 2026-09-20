// Command pterm is the Phone Terminal companion entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/host"
)

const (
	brokerMode  = "__broker"
	gatewayMode = "__gateway"
)

var buildID = "private-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "pterm: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprintln(output, "Phone Terminal private development build")
		return err
	}
	switch args[0] {
	case brokerMode:
		config, err := parseBrokerConfig(args[1:])
		if err != nil {
			return err
		}
		return host.RunBroker(ctx, config)
	case gatewayMode:
		config, err := parseGatewayConfig(args[1:])
		if err != nil {
			return err
		}
		return gateway.Run(ctx, config)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseBrokerConfig(args []string) (host.BrokerConfig, error) {
	paths, err := host.DefaultPaths()
	if err != nil {
		return host.BrokerConfig{}, err
	}
	flags := flag.NewFlagSet(brokerMode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	dataDir := flags.String("data-dir", paths.DataDir, "broker data directory")
	runtimeDir := flags.String("runtime-dir", paths.RuntimeDir, "broker runtime directory")
	if err := flags.Parse(args); err != nil {
		return host.BrokerConfig{}, fmt.Errorf("broker options: %w", err)
	}
	if flags.NArg() != 0 {
		return host.BrokerConfig{}, errors.New("broker mode does not accept positional arguments")
	}
	if !*dev && (*dataDir != paths.DataDir || *runtimeDir != paths.RuntimeDir) {
		return host.BrokerConfig{}, errors.New("broker path overrides require explicit --dev")
	}
	return host.BrokerConfig{DataDir: *dataDir, RuntimeDir: *runtimeDir, HostBuild: buildID}, nil
}

func parseGatewayConfig(args []string) (gateway.Config, error) {
	paths, err := host.DefaultPaths()
	if err != nil {
		return gateway.Config{}, err
	}
	flags := flag.NewFlagSet(gatewayMode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", paths.RuntimeDir, "broker runtime directory")
	listen := flags.String("listen", gateway.ProductionListenAddress, "gateway loopback address")
	if err := flags.Parse(args); err != nil {
		return gateway.Config{}, fmt.Errorf("gateway options: %w", err)
	}
	if flags.NArg() != 0 {
		return gateway.Config{}, errors.New("gateway mode does not accept positional arguments")
	}
	if !*dev && (*runtimeDir != paths.RuntimeDir || *listen != gateway.ProductionListenAddress) {
		return gateway.Config{}, errors.New("gateway path or listener overrides require explicit --dev")
	}
	return gateway.Config{
		RuntimeDir: *runtimeDir, HostBuild: buildID, ListenAddress: *listen,
	}, nil
}

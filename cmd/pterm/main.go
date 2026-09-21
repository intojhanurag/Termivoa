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
	return runWithInput(ctx, args, os.Stdin, output)
}

func runWithInput(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	if ctx == nil || output == nil {
		return errors.New("command context and output must not be nil")
	}
	if len(args) == 0 {
		_, err := fmt.Fprintln(output, "Phone Terminal private development build")
		return err
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, err := io.WriteString(output, commandHelp)
		return err
	case "list", "terminate":
		return runManagementCommand(ctx, args[0], args[1:], input, output)
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
	production, err := host.DefaultPaths()
	if err != nil {
		return host.BrokerConfig{}, err
	}
	development, err := host.DevelopmentPaths()
	if err != nil {
		return host.BrokerConfig{}, err
	}
	flags := flag.NewFlagSet(brokerMode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	dataDir := flags.String("data-dir", "", "broker data directory")
	runtimeDir := flags.String("runtime-dir", "", "broker runtime directory")
	if err := flags.Parse(args); err != nil {
		return host.BrokerConfig{}, fmt.Errorf("broker options: %w", err)
	}
	if flags.NArg() != 0 {
		return host.BrokerConfig{}, errors.New("broker mode does not accept positional arguments")
	}
	profile := host.ProductionProfile
	defaults := production
	if *dev {
		profile = host.DevelopmentProfile
		defaults = development
	}
	if *dataDir == "" {
		*dataDir = defaults.DataDir
	}
	if *runtimeDir == "" {
		*runtimeDir = defaults.RuntimeDir
	}
	paths, err := host.ValidatePaths(profile, host.Paths{DataDir: *dataDir, RuntimeDir: *runtimeDir})
	if err != nil {
		if !*dev {
			return host.BrokerConfig{}, fmt.Errorf("broker path overrides require explicit --dev: %w", err)
		}
		return host.BrokerConfig{}, err
	}
	return host.BrokerConfig{
		DataDir: paths.DataDir, RuntimeDir: paths.RuntimeDir, HostBuild: buildID, Profile: profile,
	}, nil
}

func parseGatewayConfig(args []string) (gateway.Config, error) {
	production, err := host.DefaultPaths()
	if err != nil {
		return gateway.Config{}, err
	}
	development, err := host.DevelopmentPaths()
	if err != nil {
		return gateway.Config{}, err
	}
	flags := flag.NewFlagSet(gatewayMode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", "", "broker runtime directory")
	listen := flags.String("listen", "", "gateway loopback address")
	if err := flags.Parse(args); err != nil {
		return gateway.Config{}, fmt.Errorf("gateway options: %w", err)
	}
	if flags.NArg() != 0 {
		return gateway.Config{}, errors.New("gateway mode does not accept positional arguments")
	}
	profile := host.ProductionProfile
	defaultRuntime := production.RuntimeDir
	defaultListen := gateway.ProductionListenAddress
	if *dev {
		profile = host.DevelopmentProfile
		defaultRuntime = development.RuntimeDir
		defaultListen = gateway.DevelopmentListenAddress
	}
	if *runtimeDir == "" {
		*runtimeDir = defaultRuntime
	}
	if *listen == "" {
		*listen = defaultListen
	}
	validatedRuntime, err := host.ValidateRuntimeDir(profile, *runtimeDir)
	if err != nil {
		if !*dev {
			return gateway.Config{}, fmt.Errorf("gateway path overrides require explicit --dev: %w", err)
		}
		return gateway.Config{}, err
	}
	if err := gateway.ValidateListenAddress(profile, *listen); err != nil {
		if !*dev {
			return gateway.Config{}, fmt.Errorf("gateway listener overrides require explicit --dev: %w", err)
		}
		return gateway.Config{}, err
	}
	return gateway.Config{
		RuntimeDir: validatedRuntime, HostBuild: buildID, ListenAddress: *listen, Profile: profile,
	}, nil
}

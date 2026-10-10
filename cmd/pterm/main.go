// Command pterm is the Termivoa companion entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	brokerMode  = "__broker"
	gatewayMode = "__gateway"
)

var buildID = "private-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == githubCredentialMode {
		// A clone made by a development broker names its profile in the helper line.
		args, dev, runtimeDir := githubCredentialFlags(os.Args[2:])
		os.Exit(runGitHubCredential(ctx, args, os.Stdin, os.Stdout, os.Stderr, func(ctx context.Context) (protocol.AdminRPCGitHubCredentialPayload, error) {
			return brokerGitHubCredential(ctx, dev, runtimeDir)
		}))
	}
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "pterm: %v\n", err)
		os.Exit(exitCode(err))
	}
}

// exitOtherServeHandlers is pterm setup's exit code when it refuses because
// other Tailscale Serve handlers share this computer's origin, so the Windows
// installer can name that cause. Every other error exits 1.
const exitOtherServeHandlers = 3

// exitCodeError makes pterm exit with code instead of 1.
type exitCodeError struct {
	code int
	err  error
}

func (failure exitCodeError) Error() string { return failure.err.Error() }
func (failure exitCodeError) Unwrap() error { return failure.err }

func exitCode(err error) int {
	var coded exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

func run(ctx context.Context, args []string, output io.Writer) error {
	return runWithInput(ctx, args, os.Stdin, output)
}

func runWithInput(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	if ctx == nil || output == nil {
		return errors.New("command context and output must not be nil")
	}
	if len(args) == 0 {
		return writeCommandText(ctx, output, "Termivoa, free and open-source beta\n")
	}
	switch args[0] {
	case "help", "--help", "-h":
		return writeCommandText(ctx, output, commandHelp)
	case "list", "terminate":
		return runManagementCommand(ctx, args[0], args[1:], input, output)
	case "attach":
		return runAttachCommand(ctx, args[1:], input, output)
	case "new":
		return runNewCommand(ctx, args[1:], input, output)
	case "pair", "devices", "revoke", "computers", "project", "profile", "config", "github":
		return runAdminCommand(ctx, args[0], args[1:], input, output)
	case "setup":
		return runSetupCommand(ctx, args[1:], input, output)
	case "doctor":
		return runDoctorCommand(ctx, args[1:], output)
	case "uninstall":
		return runUninstallCommand(ctx, args[1:], input, output)
	case "launch":
		return runLaunchCommand(ctx, args[1:], output)
	case "up":
		return runUpCommand(ctx, args[1:], output)
	case "stop":
		return runStopCommand(ctx, args[1:], output)
	case "shutdown":
		return runShutdownCommand(ctx, args[1:], input, output)
	case brokerMode:
		config, err := parseBrokerConfig(args[1:])
		if err != nil {
			return err
		}
		useServiceLog("broker", config.Profile)
		return host.RunBroker(ctx, config)
	case gatewayMode:
		config, err := parseGatewayConfig(args[1:])
		if err != nil {
			return err
		}
		useServiceLog("gateway", config.Profile)
		return runGateway(ctx, config)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runGateway serves the gateway until ctx ends or `pterm stop` asks it to
// exit through its private control endpoint. Either way it exits cleanly.
func runGateway(ctx context.Context, config gateway.Config) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	control, err := host.ListenGatewayControl(ctx, config.RuntimeDir, buildID, stop)
	if err != nil {
		return err
	}
	client, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: config.RuntimeDir, ClientBuild: buildID})
	if err != nil {
		return errors.Join(err, control.Close())
	}
	config.Broker = client
	err = gateway.Run(ctx, config)
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		err = nil // A stop during startup is still a clean exit.
	}
	return errors.Join(err, client.Close(), control.Close())
}

// useServiceLog appends a production broker's or gateway's output and crash
// report to <data>\logs\<name>.log on Windows, where the task's headless
// console discards it. launchd redirects both streams on macOS, and `pterm
// launch` hands the child that same file, so nothing is written twice.
func useServiceLog(name string, profile platform.Profile) {
	if runtime.GOOS != "windows" || profile != platform.ProductionProfile {
		return
	}
	file, err := openServiceLog(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pterm: open %s log: %v\n", name, err)
		return
	}
	os.Stdout, os.Stderr = file, file
	_ = debug.SetCrashOutput(file, debug.CrashOptions{})
	fmt.Fprintf(file, "%s pterm %s started (build %s)\n", time.Now().Format(time.RFC3339), name, buildID)
}

func openServiceLog(name string) (*os.File, error) {
	paths, err := platform.DefaultPaths()
	if err != nil {
		return nil, err
	}
	directory, err := ipc.EnsurePrivateDirectory(lifecycle.Host{OS: runtime.GOOS, DataDir: paths.DataDir}.LogDir())
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(directory, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func parseBrokerConfig(args []string) (host.BrokerConfig, error) {
	production, err := platform.DefaultPaths()
	if err != nil {
		return host.BrokerConfig{}, err
	}
	development, err := platform.DevelopmentPaths()
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
	profile := platform.ProductionProfile
	defaults := production
	if *dev {
		profile = platform.DevelopmentProfile
		defaults = development
	}
	if *dataDir == "" {
		*dataDir = defaults.DataDir
	}
	if *runtimeDir == "" {
		*runtimeDir = defaults.RuntimeDir
	}
	paths, err := platform.ValidatePaths(profile, platform.Paths{DataDir: *dataDir, RuntimeDir: *runtimeDir})
	if err != nil {
		if !*dev {
			return host.BrokerConfig{}, fmt.Errorf("broker path overrides require explicit --dev: %w", err)
		}
		return host.BrokerConfig{}, err
	}
	return host.BrokerConfig{
		DataDir: paths.DataDir, RuntimeDir: paths.RuntimeDir, HostBuild: buildID, Profile: profile,
		Herdr: herdrConfig(), GitHub: githubConfig(),
	}, nil
}

func parseGatewayConfig(args []string) (gateway.Config, error) {
	production, err := platform.DefaultPaths()
	if err != nil {
		return gateway.Config{}, err
	}
	development, err := platform.DevelopmentPaths()
	if err != nil {
		return gateway.Config{}, err
	}
	flags := flag.NewFlagSet(gatewayMode, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", "", "broker runtime directory")
	listen := flags.String("listen", "", "gateway loopback address")
	previewListen := flags.String("preview-listen", "", `dev-server preview loopback address, or "off"`)
	var origins []string
	flags.Func("origin", "exact browser origin, such as https://mac.tailnet.ts.net (repeatable)", func(value string) error {
		origins = append(origins, value)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return gateway.Config{}, fmt.Errorf("gateway options: %w", err)
	}
	if flags.NArg() != 0 {
		return gateway.Config{}, errors.New("gateway mode does not accept positional arguments")
	}
	profile := platform.ProductionProfile
	defaultRuntime := production.RuntimeDir
	defaultListen := gateway.ProductionListenAddress
	defaultPreview := gateway.ProductionPreviewListenAddress
	if *dev {
		profile = platform.DevelopmentProfile
		defaultRuntime = development.RuntimeDir
		defaultListen = gateway.DevelopmentListenAddress
		defaultPreview = gateway.DevelopmentPreviewListenAddress
	}
	switch *previewListen {
	case "":
		*previewListen = defaultPreview
	case "off":
		*previewListen = ""
	}
	if *runtimeDir == "" {
		*runtimeDir = defaultRuntime
	}
	if *listen == "" {
		*listen = defaultListen
	}
	validatedRuntime, err := platform.ValidateRuntimeDir(profile, *runtimeDir)
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
	if *previewListen != "" {
		if err := gateway.ValidatePreviewListenAddress(profile, *previewListen); err != nil {
			if !*dev {
				return gateway.Config{}, fmt.Errorf("gateway preview listener overrides require explicit --dev: %w", err)
			}
			return gateway.Config{}, err
		}
	}
	if err := gateway.ValidateOrigins(profile, origins); err != nil {
		return gateway.Config{}, err
	}
	return gateway.Config{
		RuntimeDir: validatedRuntime, HostBuild: buildID, ListenAddress: *listen, Profile: profile,
		PreviewListenAddress: *previewListen, Origins: origins,
	}, nil
}

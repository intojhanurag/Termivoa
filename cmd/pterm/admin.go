package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const adminProfileFlags = "[--dev] [--runtime-dir <directory>]"

// Administration commands take subcommand words first; flags and positional
// arguments may follow in any order (see parseInterspersed).
var adminUsages = map[string]string{
	"pair":             "pterm pair " + adminProfileFlags + " [--origin <https-origin>] [--no-qr] [--no-wait] [--via <name|https-origin> [--allow-tagged]]",
	"pair pending":     "pterm pair pending " + adminProfileFlags,
	"pair approve":     "pterm pair approve " + adminProfileFlags + " <pending-id>",
	"devices":          "pterm devices " + adminProfileFlags,
	"devices rename":   "pterm devices rename " + adminProfileFlags + " <device-id> <label>",
	"revoke":           "pterm revoke " + adminProfileFlags + " <device-id> | --via <https-origin>",
	"computers":        "pterm computers " + adminProfileFlags,
	"computers remove": "pterm computers remove " + adminProfileFlags + " <https-origin>",
	"project add":      "pterm project add " + adminProfileFlags + " [--name <name>] <path>",
	"project list":     "pterm project list " + adminProfileFlags,
	"profile list":     "pterm profile list " + adminProfileFlags,
	"profile detect":   "pterm profile detect " + adminProfileFlags,
	"config origin":    "pterm config origin " + adminProfileFlags + " [<https-origin>]",
}

var adminSubcommands = map[string][]string{
	"project": {"add", "list"},
	"profile": {"list", "detect"},
	"config":  {"origin"},
}

type adminCommandConfig struct {
	action     string
	runtimeDir string
	dev        bool
	arguments  []string
	origin     string
	noQR       bool
	noWait     bool
	name       *string
	// via is --via: the app origin (or short name for pair) to bind to.
	via         string
	allowTagged bool
}

func parseAdminCommand(command string, args []string) (adminCommandConfig, error) {
	action := command
	if command == "pair" && len(args) > 0 && (args[0] == "pending" || args[0] == "approve") ||
		command == "devices" && len(args) > 0 && args[0] == "rename" ||
		command == "computers" && len(args) > 0 && args[0] == "remove" {
		action, args = command+" "+args[0], args[1:]
	}
	if subcommands, ok := adminSubcommands[command]; ok {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
			return adminCommandConfig{}, flag.ErrHelp
		}
		action = ""
		for _, subcommand := range subcommands {
			if len(args) > 0 && args[0] == subcommand {
				action, args = command+" "+subcommand, args[1:]
			}
		}
		if action == "" {
			return adminCommandConfig{}, fmt.Errorf("usage: pterm %s <%s> ...; run pterm help", command, strings.Join(subcommands, "|"))
		}
	}
	usage, ok := adminUsages[action]
	if !ok {
		return adminCommandConfig{}, fmt.Errorf("unknown command %q", command)
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", "", "isolated broker runtime directory (requires --dev)")
	config := adminCommandConfig{action: action}
	if action == "pair" {
		flags.StringVar(&config.origin, "origin", "", "https origin for this pairing link")
		flags.BoolVar(&config.noQR, "no-qr", false, "print only the link, without a terminal QR code")
		flags.BoolVar(&config.noWait, "no-wait", false, "print the link and return without waiting to approve")
		flags.BoolVar(&config.allowTagged, "allow-tagged", false, "accept a tagged node as the app computer")
	}
	if action == "pair" || action == "revoke" {
		flags.StringVar(&config.via, "via", "", "the computer whose Termivoa app the phone uses")
	}
	if action == "project add" {
		flags.Func("name", "project display name", func(value string) error {
			if value == "" {
				return errors.New("project name must not be empty")
			}
			config.name = &value
			return nil
		})
	}
	arguments, err := parseInterspersed(flags, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return adminCommandConfig{}, err
		}
		return adminCommandConfig{}, fmt.Errorf("%w; usage: %s", err, usage)
	}
	minimum, maximum := 0, 0
	switch action {
	case "pair approve", "project add", "computers remove":
		minimum, maximum = 1, 1
	case "revoke":
		if config.via == "" {
			minimum, maximum = 1, 1
		}
	case "config origin":
		maximum = 1
	case "devices rename":
		minimum, maximum = 2, 2
		if len(arguments) > 2 { // An unquoted label with spaces.
			arguments = []string{arguments[0], strings.Join(arguments[1:], " ")}
		}
	}
	if len(arguments) < minimum || len(arguments) > maximum {
		return adminCommandConfig{}, fmt.Errorf("usage: %s", usage)
	}
	// Reject a malformed origin before contacting the broker or creating a grant.
	origin := config.origin
	if action == "config origin" && len(arguments) == 1 {
		origin = arguments[0]
	}
	if origin != "" {
		if err := protocol.ValidatePairingOrigin(origin); err != nil {
			return adminCommandConfig{}, fmt.Errorf("invalid pairing origin: %w", err)
		}
	}
	if err := validateComputerFlags(config, arguments, usage); err != nil {
		return adminCommandConfig{}, err
	}
	validated, err := adminRuntimeDir(*dev, *runtimeDir)
	if err != nil {
		return adminCommandConfig{}, err
	}
	config.runtimeDir, config.dev, config.arguments = validated, *dev, arguments
	return config, nil
}

// validateComputerFlags rejects a malformed --via, --allow-tagged without
// --via, or a malformed computer origin before the broker is contacted.
func validateComputerFlags(config adminCommandConfig, arguments []string, usage string) error {
	switch {
	case config.allowTagged && config.via == "":
		return fmt.Errorf("--allow-tagged needs --via; usage: %s", usage)
	case config.action == "pair" && config.via != "":
		if err := protocol.ValidatePairVia(config.via); err != nil {
			return fmt.Errorf("invalid --via: %w", err)
		}
	case config.action == "revoke" && config.via != "":
		if err := protocol.ValidateComputerOrigin(config.via); err != nil {
			return fmt.Errorf("invalid --via: %w", err)
		}
	case config.action == "computers remove":
		if err := protocol.ValidateComputerOrigin(arguments[0]); err != nil {
			return fmt.Errorf("invalid computer origin: %w", err)
		}
	}
	return nil
}

// parseInterspersed parses flags that appear before, between or after
// positional arguments, so `pterm pair approve <id> --dev` works like
// `pterm pair approve --dev <id>`. Everything after "--" is positional.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		rest := flags.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" || len(rest) == 0 {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// terminalQR reports whether output is a terminal, and its width. The QR code
// uses terminal colors, so redirected output gets only the link.
func terminalQR(output io.Writer) (show bool, columns int) {
	file, isFile := output.(*os.File)
	if !isFile || !term.IsTerminal(file.Fd()) {
		return false, 0
	}
	columns, _, _ = term.GetSize(file.Fd())
	return true, columns
}

// localOrigin is the default gateway page on this computer, used for a
// pairing link when no phone origin is configured.
func localOrigin(dev bool) string {
	if dev {
		return "http://" + devListenAddress
	}
	return strings.TrimSuffix(lifecycle.LocalURL, "/")
}

// adminRuntimeDir applies the same profile rules as parseLocalCommand: a
// runtime-dir override is accepted only inside an explicit --dev profile.
func adminRuntimeDir(dev bool, runtimeDir string) (string, error) {
	profile := platform.ProductionProfile
	defaults, err := platform.DefaultPaths()
	if err != nil {
		return "", err
	}
	if dev {
		profile = platform.DevelopmentProfile
		defaults, err = platform.DevelopmentPaths()
		if err != nil {
			return "", err
		}
	}
	if runtimeDir == "" {
		runtimeDir = defaults.RuntimeDir
	}
	validated, err := platform.ValidateRuntimeDir(profile, runtimeDir)
	if err != nil {
		return "", fmt.Errorf("invalid local command profile: %w", err)
	}
	return validated, nil
}

func runAdminCommand(ctx context.Context, command string, args []string, input *os.File, output io.Writer) (returnErr error) {
	config, parseErr := parseAdminCommand(command, args)
	if parseErr != nil && !errors.Is(parseErr, flag.ErrHelp) {
		return parseErr
	}
	showQR, columns := terminalQR(output)
	showQR = showQR && !config.noQR
	managedOutput, err := newManagementOutput(ctx, output)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, managedOutput.Close()) }()
	output = managedOutput
	if errors.Is(parseErr, flag.ErrHelp) {
		_, err = io.WriteString(output, commandHelp)
		return err
	}
	client, err := localclient.DialAdmin(ctx, config.runtimeDir, buildID)
	if err != nil {
		return fmt.Errorf("cannot connect to the managed-session broker: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, client.Close()) }()
	switch config.action {
	case "pair":
		options := cli.PairOptions{
			Origin: config.origin, Fallback: localOrigin(config.dev), ShowQR: showQR, QRColumns: columns,
			Via: config.via, AllowTagged: config.allowTagged, FirstComputerWarning: true,
		}
		if !config.noWait && input != nil {
			options.Confirmer = fileConfirmer{input: input, output: output}
			// Only a person at a terminal is offered to home (Found on your
			// network); piped input still waits for the link.
			options.NoOffer = !term.IsTerminal(input.Fd())
		}
		// A declined phone is the owner's answer, not a failure; Pair said so.
		if err := cli.Pair(ctx, client, options, output); !errors.Is(err, cli.ErrNotPaired) {
			return err
		}
		return nil
	case "pair pending":
		return cli.PairPending(ctx, client, output)
	case "pair approve":
		return cli.PairApprove(ctx, client, config.arguments[0], output)
	case "devices":
		return cli.Devices(ctx, client, output)
	case "devices rename":
		return cli.RenameDevice(ctx, client, config.arguments[0], config.arguments[1], output)
	case "revoke":
		if config.via != "" {
			return cli.RevokeVia(ctx, client, config.via, fileConfirmer{input: input, output: output}, output)
		}
		return cli.Revoke(ctx, client, config.arguments[0], fileConfirmer{input: input, output: output}, output)
	case "computers":
		return cli.Computers(ctx, client, output)
	case "computers remove":
		return cli.RemoveComputer(ctx, client, config.arguments[0], output)
	case "project add":
		return cli.ProjectAdd(ctx, client, config.arguments[0], config.name, output)
	case "project list":
		return cli.ProjectList(ctx, client, output)
	case "profile list":
		return cli.ProfileList(ctx, client, output)
	case "profile detect":
		return cli.ProfileDetect(ctx, client, output)
	case "config origin":
		var origin *string
		if len(config.arguments) == 1 {
			origin = &config.arguments[0]
		}
		return cli.ConfigOrigin(ctx, client, origin, output)
	default:
		return errors.New("unsupported administration command")
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const adminProfileFlags = "[--dev] [--runtime-dir <directory>]"

// Administration commands take subcommand words first, then flags, then
// positional arguments, because Go's flag package stops at the first
// positional: pterm project add [--dev] [--runtime-dir D] [--name N] <path>.
var adminUsages = map[string]string{
	"pair":           "pterm pair " + adminProfileFlags + " [--origin <https-origin>]",
	"pair pending":   "pterm pair pending " + adminProfileFlags,
	"pair approve":   "pterm pair approve " + adminProfileFlags + " <pending-id>",
	"devices":        "pterm devices " + adminProfileFlags,
	"revoke":         "pterm revoke " + adminProfileFlags + " <device-id>",
	"project add":    "pterm project add " + adminProfileFlags + " [--name <name>] <path>",
	"project list":   "pterm project list " + adminProfileFlags,
	"profile list":   "pterm profile list " + adminProfileFlags,
	"profile detect": "pterm profile detect " + adminProfileFlags,
	"config origin":  "pterm config origin " + adminProfileFlags + " [<https-origin>]",
}

var adminSubcommands = map[string][]string{
	"project": {"add", "list"},
	"profile": {"list", "detect"},
	"config":  {"origin"},
}

type adminCommandConfig struct {
	action     string
	runtimeDir string
	arguments  []string
	origin     string
	name       *string
}

func parseAdminCommand(command string, args []string) (adminCommandConfig, error) {
	action := command
	if command == "pair" && len(args) > 0 && (args[0] == "pending" || args[0] == "approve") {
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
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return adminCommandConfig{}, err
		}
		return adminCommandConfig{}, fmt.Errorf("%w; usage: %s", err, usage)
	}
	minimum, maximum := 0, 0
	switch action {
	case "pair approve", "revoke", "project add":
		minimum, maximum = 1, 1
	case "config origin":
		maximum = 1
	}
	if flags.NArg() < minimum || flags.NArg() > maximum {
		return adminCommandConfig{}, fmt.Errorf("usage: %s", usage)
	}
	// Reject a malformed origin before contacting the broker or creating a grant.
	origin := config.origin
	if action == "config origin" && flags.NArg() == 1 {
		origin = flags.Arg(0)
	}
	if origin != "" {
		if err := protocol.ValidatePairingOrigin(origin); err != nil {
			return adminCommandConfig{}, fmt.Errorf("invalid pairing origin: %w", err)
		}
	}
	validated, err := adminRuntimeDir(*dev, *runtimeDir)
	if err != nil {
		return adminCommandConfig{}, err
	}
	config.runtimeDir, config.arguments = validated, flags.Args()
	return config, nil
}

// adminRuntimeDir applies the same profile rules as parseLocalCommand: a
// runtime-dir override is accepted only inside an explicit --dev profile.
func adminRuntimeDir(dev bool, runtimeDir string) (string, error) {
	profile := host.ProductionProfile
	defaults, err := host.DefaultPaths()
	if err != nil {
		return "", err
	}
	if dev {
		profile = host.DevelopmentProfile
		defaults, err = host.DevelopmentPaths()
		if err != nil {
			return "", err
		}
	}
	if runtimeDir == "" {
		runtimeDir = defaults.RuntimeDir
	}
	validated, err := host.ValidateRuntimeDir(profile, runtimeDir)
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
		return cli.Pair(ctx, client, config.origin, output)
	case "pair pending":
		return cli.PairPending(ctx, client, output)
	case "pair approve":
		return cli.PairApprove(ctx, client, config.arguments[0], output)
	case "devices":
		return cli.Devices(ctx, client, output)
	case "revoke":
		return cli.Revoke(ctx, client, config.arguments[0], fileConfirmer{input: input, output: output}, output)
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

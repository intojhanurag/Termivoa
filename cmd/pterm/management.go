package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
)

const commandHelp = `Phone Terminal private development build

Available commands:
  pterm list                         Show managed sessions and lifecycle state.
  pterm terminate <session-id>        Confirm and end one managed process tree.
  pterm help                         Show this help.

Local commands talk to the existing broker; they never start or replace it.
Development tests may use --dev and --runtime-dir <isolated-directory> before
the session ID. Production and development profiles are never mixed.
Phone access, installation, and the remaining CLI commands are not ready yet.
`

type localCommandConfig struct {
	runtimeDir string
	arguments  []string
}

func parseLocalCommand(command string, args []string) (localCommandConfig, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", "", "isolated broker runtime directory (requires --dev)")
	if err := flags.Parse(args); err != nil {
		return localCommandConfig{}, err
	}
	if (command == "list" && flags.NArg() != 0) || (command == "terminate" && flags.NArg() != 1) {
		return localCommandConfig{}, fmt.Errorf("usage: pterm %s [--dev] [--runtime-dir <directory>]%s", command, commandArgumentHelp(command))
	}
	profile := host.ProductionProfile
	defaults, err := host.DefaultPaths()
	if err != nil {
		return localCommandConfig{}, err
	}
	if *dev {
		profile = host.DevelopmentProfile
		defaults, err = host.DevelopmentPaths()
		if err != nil {
			return localCommandConfig{}, err
		}
	}
	if *runtimeDir == "" {
		*runtimeDir = defaults.RuntimeDir
	}
	validated, err := host.ValidateRuntimeDir(profile, *runtimeDir)
	if err != nil {
		return localCommandConfig{}, fmt.Errorf("invalid local command profile: %w", err)
	}
	return localCommandConfig{runtimeDir: validated, arguments: flags.Args()}, nil
}

func commandArgumentHelp(command string) string {
	if command == "terminate" {
		return " <session-id>"
	}
	return ""
}

func runManagementCommand(ctx context.Context, command string, args []string, input *os.File, output io.Writer) (returnErr error) {
	config, parseErr := parseLocalCommand(command, args)
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
	client, err := localclient.DialManagement(ctx, config.runtimeDir, buildID)
	if err != nil {
		return fmt.Errorf("cannot connect to the managed-session broker: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, client.Close()) }()
	app, err := cli.NewApp(client, fileConfirmer{input: input, output: output}, cli.NewOperationID, output)
	if err != nil {
		return err
	}
	switch command {
	case "list":
		return app.List(ctx)
	case "terminate":
		return app.Terminate(ctx, config.arguments[0])
	default:
		return errors.New("unsupported management command")
	}
}

// Confirmation owns only a duplicate, opened lazily after resolving a live
// session. Listing and already-exited sessions never touch stdin.
type fileConfirmer struct {
	input  *os.File
	output io.Writer
}

func (confirmer fileConfirmer) Confirm(ctx context.Context, prompt string) (confirmed bool, returnErr error) {
	reader, err := cli.NewFileConfirmationReader(confirmer.input)
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, reader.Close()) }()
	line, err := cli.NewLineConfirmer(reader, confirmer.output)
	if err != nil {
		return false, err
	}
	return line.Confirm(ctx, prompt)
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/platform"
)

const commandHelp = `Termivoa, free and open-source beta

Available commands:
  pterm list                         Show managed sessions and lifecycle state.
  pterm terminate <session-id>        Confirm and end one managed process tree.
  pterm attach [--control] <id>       View a session; --control explicitly takes control.
  pterm new --project <id> --shell <id> Create and take control of a new session.
  pterm pair [--origin <url>] [--no-qr] [--no-wait]
                                     Print a two-minute, single-use phone pairing link
                                     and, on a terminal, its QR code; then wait for the
                                     phone and approve it after you compare its code.
                                     For a first computer.
  pterm pair --via <name|url> [--allow-tagged] [--no-qr]
                                     Add this computer to the Termivoa app at that
                                     computer. While it waits (up to 10 minutes),
                                     the app shows this computer under Settings →
                                     Found on your network: tap Add.
                                     It also prints a two-minute add-computer link
                                     and, on a terminal, its QR code for the app's
                                     own scanner (not the phone's Camera app). It
                                     approves the phone after you compare its code.
                                     --allow-tagged accepts a tagged app computer
                                     (link and QR code only; no Add button).
  pterm pair pending                 Show phones waiting for approval and their codes.
  pterm pair approve <pending-id>    Approve a phone after comparing its code.
  pterm devices                      Show paired devices, status, last use and app (VIA).
  pterm devices rename <id> <label>  Rename a paired device.
  pterm revoke <device-id>           Confirm and revoke one device's browser access.
  pterm revoke --via <url>           Confirm and revoke every device that uses this
                                     computer from the Termivoa app at <url>.
  pterm computers                    On the home computer: show each phone's computers.
  pterm computers remove <url>       Remove a computer from every phone's list.
  pterm project add [--name <name>] <path> Save a project directory.
  pterm project list                 Show saved projects.
  pterm profile list                 Show saved shell profiles.
  pterm profile detect               Detect host shells and save them as profiles.
  pterm config origin [<url>]        Show or set the private HTTPS pairing origin.
  pterm github status                Show whether this computer is connected to GitHub.
  pterm github disconnect            Remove this computer's GitHub login.
  pterm chat on|off|status           Turn Chat view on (asks y/N first) or off, or show it.
  pterm setup [--verbose]            First run: a short plan, one confirmation, startup,
                                     phone address, Tailscale Serve (asks), pairing.
                                     --verbose also shows the checks, the startup
                                     files and every command.
  pterm setup --apply --yes          Installer mode: register startup, ask nothing.
  pterm doctor                       Read-only diagnostics; never prints secrets.
  pterm uninstall [--dry-run] [--yes] [--delete-data]
                                     Remove startup registration; data only with --delete-data.
  pterm launch [--no-open]           Start broker and gateway if needed; open the local page.
  pterm stop                         Stop the gateway only; sessions keep running.
  pterm shutdown                     Confirm, end all sessions, stop broker and gateway.
  pterm up --dev [--listen <addr>]   Development harness: broker and gateway as separate processes.
  pterm help                         Show this help.

Local commands talk to the existing broker; they never start or replace it.
Development tests may use --dev and --runtime-dir <isolated-directory>
anywhere after the command words. Production and development
profiles are never mixed. The pairing link and its QR code are a secret.
Attach: Ctrl+] detaches, never terminates. With control, Ctrl+V then Ctrl+]
sends literal Ctrl+]. Ordinary typing in observer mode is ignored, not queued.
`

type localCommandConfig struct {
	runtimeDir string
	dev        bool
	arguments  []string
	control    bool
	projectID  string
	profileID  string
}

func parseLocalCommand(command string, args []string) (localCommandConfig, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use an isolated development profile")
	runtimeDir := flags.String("runtime-dir", "", "isolated broker runtime directory (requires --dev)")
	var control bool
	var projectID, profileID string
	if command == "attach" {
		flags.BoolVar(&control, "control", false, "explicitly take control after the initial screen is applied")
	}
	if command == "new" {
		flags.StringVar(&projectID, "project", "", "saved project ID")
		flags.StringVar(&profileID, "shell", "", "saved shell profile ID")
	}
	arguments, err := parseInterspersed(flags, args)
	if err != nil {
		return localCommandConfig{}, err
	}
	if ((command == "list" || command == "stop" || command == "shutdown") && len(arguments) != 0) || ((command == "terminate" || command == "attach") && len(arguments) != 1) {
		return localCommandConfig{}, fmt.Errorf("usage: pterm %s [--dev] [--runtime-dir <directory>]%s", command, commandArgumentHelp(command))
	}
	if command == "new" && (len(arguments) != 0 || projectID == "" || profileID == "") {
		return localCommandConfig{}, errors.New("usage: pterm new [--dev] [--runtime-dir <directory>] --project <saved-project-id> --shell <saved-shell-profile-id>")
	}
	profile := platform.ProductionProfile
	defaults, err := platform.DefaultPaths()
	if err != nil {
		return localCommandConfig{}, err
	}
	if *dev {
		profile = platform.DevelopmentProfile
		defaults, err = platform.DevelopmentPaths()
		if err != nil {
			return localCommandConfig{}, err
		}
	}
	if *runtimeDir == "" {
		*runtimeDir = defaults.RuntimeDir
	}
	validated, err := platform.ValidateRuntimeDir(profile, *runtimeDir)
	if err != nil {
		return localCommandConfig{}, fmt.Errorf("invalid local command profile: %w", err)
	}
	return localCommandConfig{
		runtimeDir: validated, dev: *dev, arguments: arguments, control: control, projectID: projectID, profileID: profileID,
	}, nil
}

func commandArgumentHelp(command string) string {
	if command == "terminate" || command == "attach" {
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
	return withLine(confirmer, func(line *cli.LineConfirmer) (bool, error) { return line.Confirm(ctx, prompt) })
}

// Ask reads one free-text line: a menu choice or a computer name.
func (confirmer fileConfirmer) Ask(ctx context.Context, prompt string) (string, error) {
	return withLine(confirmer, func(line *cli.LineConfirmer) (string, error) { return line.Ask(ctx, prompt) })
}

func withLine[T any](confirmer fileConfirmer, read func(*cli.LineConfirmer) (T, error)) (answer T, returnErr error) {
	reader, err := cli.NewFileConfirmationReader(confirmer.input)
	if err != nil {
		return answer, err
	}
	defer func() { returnErr = errors.Join(returnErr, reader.Close()) }()
	line, err := cli.NewLineConfirmer(reader, confirmer.output)
	if err != nil {
		return answer, err
	}
	return read(line)
}

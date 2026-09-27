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
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func runNewCommand(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	config, err := parseLocalCommand("new", args)
	if errors.Is(err, flag.ErrHelp) {
		return writeCommandText(ctx, output, commandHelp)
	}
	if err != nil {
		return err
	}
	terminalOutput, ok := output.(*os.File)
	if !ok {
		return errors.New("new requires terminal stdin and stdout; no session was created")
	}
	columns, rows, err := cli.ValidateDesktopFiles(input, terminalOutput)
	if err != nil {
		return fmt.Errorf("new terminal preflight failed; no session was created: %w", err)
	}
	// The plan fixes creation at 80x24; control is acquired only after that
	// complete screen is applied. Do not create an invisible session just to
	// discover the initial screen cannot fit. A later resize is rechecked.
	if columns < 80 || rows < 24 {
		return errors.New("new requires an initial terminal of at least 80 columns by 24 rows; enlarge the window and try again (no session was created)")
	}
	session, err := createNewManagedSession(ctx, config)
	if err != nil {
		if session.ID != "" {
			return fmt.Errorf("session %s was created but management cleanup failed; inspect pterm list before continuing: %w", session.ID, err)
		}
		return err
	}
	if err := writeCommandText(ctx, output, fmt.Sprintf("Created session %s. Taking control after its initial screen is applied.\r\n", session.ID)); err != nil {
		return fmt.Errorf("session %s was created but its status could not be displayed; use pterm list before creating another: %w", session.ID, err)
	}
	if session.State != protocol.Running && session.State != protocol.Starting {
		return writeCommandText(ctx, output, fmt.Sprintf("Session %s is already %s; no replacement process was started.\r\n", session.ID, session.State))
	}
	// The explicit new command creates and controls this new identity. It is
	// not reconnect behavior: neither creation nor attachment is retried.
	if err := cli.AttachDesktop(ctx, cli.DesktopConfig{
		RuntimeDir: config.runtimeDir, ClientBuild: buildID, Session: session,
		Input: input, Output: terminalOutput, Control: true,
	}); err != nil {
		return fmt.Errorf("session %s was created; attachment failed without terminating it. Inspect pterm list, then explicitly attach to that session: %w", session.ID, err)
	}
	return nil
}

func createNewManagedSession(ctx context.Context, config localCommandConfig) (record protocol.BrokerRPCSessionRecord, returnErr error) {
	client, err := localclient.DialManagement(ctx, config.runtimeDir, buildID)
	if err != nil {
		return record, fmt.Errorf("cannot connect to the managed-session broker: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, client.Close()) }()
	app, err := cli.NewApp(client, fileConfirmer{output: io.Discard}, cli.NewOperationID, io.Discard)
	if err != nil {
		return record, err
	}
	return app.Create(ctx, config.projectID, config.profileID)
}

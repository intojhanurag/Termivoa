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
)

func runAttachCommand(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	config, err := parseLocalCommand("attach", args)
	if errors.Is(err, flag.ErrHelp) {
		return writeCommandText(ctx, output, commandHelp)
	}
	if err != nil {
		return err
	}
	terminalOutput, ok := output.(*os.File)
	if !ok || input == nil {
		return errors.New("attach requires terminal stdin and stdout; use pterm list for redirected output")
	}
	client, err := localclient.DialManagement(ctx, config.runtimeDir, buildID)
	if err != nil {
		return fmt.Errorf("cannot connect to the managed-session broker: %w", err)
	}
	app, err := cli.NewApp(client, fileConfirmer{input: input, output: io.Discard}, cli.NewOperationID, io.Discard)
	if err != nil {
		return errors.Join(err, client.Close())
	}
	session, err := app.ResolveSession(ctx, config.arguments[0])
	closeErr := client.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return cli.AttachDesktop(ctx, cli.DesktopConfig{
		RuntimeDir: config.runtimeDir, ClientBuild: buildID, Session: session,
		Input: input, Output: terminalOutput, Control: config.control,
	})
}

func writeCommandText(ctx context.Context, output io.Writer, value string) (returnErr error) {
	managed, err := newManagementOutput(ctx, output)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, managed.Close()) }()
	_, err = io.WriteString(managed, value)
	return err
}

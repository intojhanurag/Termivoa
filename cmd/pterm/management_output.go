package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/intojhanurag/phone-terminal/internal/cli"
)

// managementOutput binds only short-lived management output to its command
// context. Desktop drawing/restoration must use its separately owned writer
// and a fresh bounded restoration context after the live context is canceled.
type managementOutput struct {
	ctx    context.Context
	output io.Writer
	owned  *cli.FileTerminalWriter
}

func newManagementOutput(ctx context.Context, output io.Writer) (*managementOutput, error) {
	if ctx == nil || output == nil {
		return nil, errors.New("management output requires context and writer")
	}
	writer := &managementOutput{ctx: ctx, output: output}
	file, ok := output.(*os.File)
	if !ok {
		// In-memory/custom writers retain their caller-defined behavior. We
		// cannot promise to interrupt an arbitrary io.Writer implementation.
		return writer, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect command output: %w", err)
	}
	interruptible := info.Mode()&os.ModeNamedPipe != 0
	if !interruptible && info.Mode()&os.ModeCharDevice != 0 {
		raw, err := file.SyscallConn()
		if err != nil {
			return nil, fmt.Errorf("access command output: %w", err)
		}
		if err := raw.Control(func(fd uintptr) { interruptible = term.IsTerminal(fd) }); err != nil {
			return nil, fmt.Errorf("inspect output terminal: %w", err)
		}
	}
	if !interruptible {
		// Preserve file redirection and non-terminal sinks such as /dev/null.
		// Their writes are synchronous: cancellation is observed between
		// calls, but an in-flight filesystem/device write cannot be stopped.
		return writer, nil
	}
	writer.owned, err = cli.NewFileTerminalWriter(file)
	if err != nil {
		return nil, fmt.Errorf("own command output: %w", err)
	}
	return writer, nil
}

func (writer *managementOutput) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	if writer.owned != nil {
		return writer.owned.WriteContext(writer.ctx, data)
	}
	n, err := writer.output.Write(data)
	return n, errors.Join(err, writer.ctx.Err())
}

func (writer *managementOutput) Close() error {
	if writer.owned != nil {
		return writer.owned.Close()
	}
	return nil
}

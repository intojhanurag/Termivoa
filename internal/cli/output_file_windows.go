//go:build windows

package cli

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sys/windows"
)

func writeTerminalOutput(ctx context.Context, handle terminalFileHandle, data []byte) (int, error) {
	return interruptibleWindowsTerminalIO(ctx, func() (int, error) {
		written := 0
		for written < len(data) {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			var count uint32
			err := windows.WriteFile(windows.Handle(handle), data[written:], &count, nil)
			written += int(count)
			if err != nil {
				return written, fmt.Errorf("write terminal output: %w", err)
			}
			if count == 0 {
				return written, io.ErrShortWrite
			}
		}
		return written, nil
	})
}

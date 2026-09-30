//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

func writeTerminalOutput(ctx context.Context, handle terminalFileHandle, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, err := nonblockingTerminalIO(handle, func(fd int) (int, error) {
			n, err := unix.Write(fd, data[written:])
			if n == len(data)-written {
				runTerminalIOHook(ctx)
			}
			return n, err
		})
		if n > 0 {
			written += n
		}
		if errors.Is(err, errTerminalIORestore) {
			return written, err
		}
		if err == nil {
			if n == 0 {
				return written, io.ErrShortWrite
			}
			continue
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return written, fmt.Errorf("write terminal output: %w", err)
		}
		_, err = unix.Poll([]unix.PollFd{{Fd: int32(handle), Events: unix.POLLOUT}}, 20)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return written, fmt.Errorf("wait for terminal output: %w", err)
		}
	}
	return written, nil
}

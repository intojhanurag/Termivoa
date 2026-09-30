//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

type terminalFileHandle int

// File-status flags are shared by dup and may also be shared by input/output
// descriptors for one terminal. Serialize only the short nonblocking syscall
// window across owned adapters, never the wait for input or output readiness.
var (
	terminalIOMu         sync.Mutex
	errTerminalIORestore = errors.New("restore terminal descriptor flags")
)

func duplicateTerminalFile(source *os.File) (terminalFileHandle, error) {
	connection, err := source.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("access terminal I/O source: %w", err)
	}
	fd := -1
	var duplicateErr error
	if err := connection.Control(func(sourceFD uintptr) {
		fd, duplicateErr = unix.FcntlInt(sourceFD, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return -1, fmt.Errorf("access terminal I/O descriptor: %w", err)
	}
	if duplicateErr != nil {
		return -1, fmt.Errorf("duplicate terminal I/O descriptor: %w", duplicateErr)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("inspect terminal I/O source: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFIFO && !term.IsTerminal(uintptr(fd)) {
		_ = unix.Close(fd)
		return -1, errors.New("terminal I/O source must be a terminal or pipe")
	}
	return terminalFileHandle(fd), nil
}

func nonblockingTerminalIO(handle terminalFileHandle, operation func(int) (int, error)) (count int, operationErr error) {
	terminalIOMu.Lock()
	defer terminalIOMu.Unlock()
	fd := int(handle)
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return 0, fmt.Errorf("inspect terminal descriptor flags: %w", err)
	}
	// Restore flags before releasing the shared lock or waiting in Poll.
	if flags&unix.O_NONBLOCK == 0 {
		if err := unix.SetNonblock(fd, true); err != nil {
			return 0, fmt.Errorf("make terminal I/O interruptible: %w", err)
		}
		defer func() {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags); err != nil {
				operationErr = errors.Join(operationErr, fmt.Errorf("%w: %v", errTerminalIORestore, err))
			}
		}()
	}
	return operation(fd)
}

func readConfirmationInput(ctx context.Context, handle terminalFileHandle, data []byte) (int, error) {
	fd := int(handle)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := nonblockingTerminalIO(handle, func(fd int) (int, error) {
			n, err := unix.Read(fd, data)
			if n > 0 {
				runTerminalIOHook(ctx)
			}
			return n, err
		})
		if errors.Is(err, errTerminalIORestore) {
			return 0, err
		}
		if err == nil {
			if n == 0 {
				return 0, io.EOF
			}
			return n, nil
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return 0, fmt.Errorf("read confirmation input: %w", err)
		}
		// A short bounded poll makes cancellation independent of input arrival
		// without a goroutine blocked in Read. Nonblocking Read also covers
		// readiness becoming stale before the syscall.
		_, err = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 20)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return 0, fmt.Errorf("wait for confirmation input: %w", err)
		}
	}
}

func closeTerminalFile(handle terminalFileHandle) error {
	return unix.Close(int(handle))
}

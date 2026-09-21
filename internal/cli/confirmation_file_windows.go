//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type terminalFileHandle windows.Handle

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func duplicateTerminalFile(source *os.File) (terminalFileHandle, error) {
	if err := cancelSynchronousIO.Find(); err != nil {
		return 0, fmt.Errorf("synchronous terminal I/O cancellation unavailable: %w", err)
	}
	connection, err := source.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access terminal I/O source: %w", err)
	}
	var handle windows.Handle
	var duplicateErr error
	if err := connection.Control(func(sourceHandle uintptr) {
		duplicateErr = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(sourceHandle),
			windows.CurrentProcess(), &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
	}); err != nil {
		return 0, fmt.Errorf("access terminal I/O handle: %w", err)
	}
	if duplicateErr != nil {
		return 0, fmt.Errorf("duplicate terminal I/O handle: %w", duplicateErr)
	}
	kind, err := windows.GetFileType(handle)
	var mode uint32
	if err != nil || (kind != windows.FILE_TYPE_PIPE && windows.GetConsoleMode(handle, &mode) != nil) {
		_ = windows.CloseHandle(handle)
		return 0, errors.New("terminal I/O source must be a console or synchronous pipe")
	}
	if kind == windows.FILE_TYPE_PIPE {
		// CancelSynchronousIo cannot cancel overlapped I/O. Query the actual
		// mode instead of assuming every redirected standard handle is sync.
		// FILE_MODE_INFORMATION contains one ULONG; its information class is
		// 16 in Microsoft's NtQueryInformationFile contract.
		const fileModeInformation = 16
		var fileMode uint32
		var status windows.IO_STATUS_BLOCK
		if err := windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&fileMode)),
			uint32(unsafe.Sizeof(fileMode)), fileModeInformation); err != nil {
			_ = windows.CloseHandle(handle)
			return 0, fmt.Errorf("inspect terminal pipe I/O mode: %w", err)
		}
		if fileMode&(windows.FILE_SYNCHRONOUS_IO_ALERT|windows.FILE_SYNCHRONOUS_IO_NONALERT) == 0 {
			_ = windows.CloseHandle(handle)
			return 0, errors.New("terminal I/O source uses unsupported asynchronous pipe operations")
		}
	}
	return terminalFileHandle(handle), nil
}

func interruptibleWindowsTerminalIO(ctx context.Context, operation func() (int, error)) (int, error) {
	// CancelSynchronousIo targets the issuing OS thread. Keep that thread
	// dedicated to this I/O until cancellation has fully stopped so it can
	// never cancel unrelated I/O on a reused runtime thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		return 0, fmt.Errorf("open terminal I/O thread: %w", err)
	}
	defer windows.CloseHandle(thread)
	readDone := make(chan struct{})
	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-readDone:
				return
			default:
			}
			// Cancellation can race with entry into ReadFile/WriteFile. Retry
			// until I/O returns, including ERROR_NOT_FOUND before it begins.
			_, _, _ = cancelSynchronousIO.Call(uintptr(thread))
			select {
			case <-readDone:
				return
			case <-ticker.C:
			}
		}
	})
	defer func() {
		close(readDone)
		if !stopCancel() {
			<-cancelDone
		}
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := operation()
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	return n, err
}

func readConfirmationInput(ctx context.Context, handle terminalFileHandle, data []byte) (int, error) {
	read, err := interruptibleWindowsTerminalIO(ctx, func() (int, error) {
		var count uint32
		err := windows.ReadFile(windows.Handle(handle), data, &count, nil)
		return int(count), err
	})
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) || (err == nil && read == 0) {
		return 0, io.EOF
	}
	if err != nil {
		return read, fmt.Errorf("read confirmation input: %w", err)
	}
	return read, nil
}

func closeTerminalFile(handle terminalFileHandle) error {
	return windows.CloseHandle(windows.Handle(handle))
}

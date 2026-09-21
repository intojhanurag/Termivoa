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

	"golang.org/x/sys/windows"
)

type confirmationHandle windows.Handle

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func duplicateConfirmationInput(source *os.File) (confirmationHandle, error) {
	if err := cancelSynchronousIO.Find(); err != nil {
		return 0, fmt.Errorf("synchronous input cancellation unavailable: %w", err)
	}
	connection, err := source.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access confirmation input: %w", err)
	}
	var handle windows.Handle
	var duplicateErr error
	if err := connection.Control(func(sourceHandle uintptr) {
		duplicateErr = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(sourceHandle),
			windows.CurrentProcess(), &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
	}); err != nil {
		return 0, fmt.Errorf("access confirmation handle: %w", err)
	}
	if duplicateErr != nil {
		return 0, fmt.Errorf("duplicate confirmation handle: %w", duplicateErr)
	}
	kind, err := windows.GetFileType(handle)
	var mode uint32
	if err != nil || (kind != windows.FILE_TYPE_PIPE && windows.GetConsoleMode(handle, &mode) != nil) {
		_ = windows.CloseHandle(handle)
		return 0, errors.New("confirmation input must be a console or synchronous pipe")
	}
	return confirmationHandle(handle), nil
}

func readConfirmationInput(ctx context.Context, handle confirmationHandle, data []byte) (int, error) {
	// CancelSynchronousIo targets the issuing OS thread. Keep that thread
	// dedicated to this read until cancellation has fully stopped so it can
	// never cancel unrelated I/O on a reused runtime thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		return 0, fmt.Errorf("open confirmation reader thread: %w", err)
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
			// Cancellation can race with entry into ReadFile. Retrying until
			// that one read returns covers ERROR_NOT_FOUND before I/O begins.
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
	var read uint32
	err = windows.ReadFile(windows.Handle(handle), data, &read, nil)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) || (err == nil && read == 0) {
		return 0, io.EOF
	}
	if err != nil {
		return int(read), fmt.Errorf("read confirmation input: %w", err)
	}
	return int(read), nil
}

func closeConfirmationInput(handle confirmationHandle) error {
	return windows.CloseHandle(windows.Handle(handle))
}

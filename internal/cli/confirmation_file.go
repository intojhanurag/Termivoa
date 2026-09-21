package cli

import (
	"context"
	"errors"
	"os"
	"sync"
)

// FileConfirmationReader owns a duplicate of a terminal or pipe descriptor.
// Close cancels its active read and releases only that duplicate. The source
// file, including os.Stdin, stays open. It must be the sole reader of the
// source while in use; duplicated descriptors share the same input stream.
// On Unix, each read syscall temporarily changes the shared nonblocking flag
// under a shared adapter lock and restores it before any readiness wait. No
// code outside the owned adapters may read or reconfigure the source while
// in use. An owned output adapter may share a terminal through the same lock.
type FileConfirmationReader struct {
	file *ownedTerminalFile
}

type ownedTerminalFile struct {
	handle    terminalFileHandle
	closed    context.Context
	cancel    context.CancelFunc
	slot      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewFileConfirmationReader duplicates a terminal or pipe. Regular files and
// unsupported device types are rejected rather than treated as interruptible.
func NewFileConfirmationReader(source *os.File) (*FileConfirmationReader, error) {
	file, err := newOwnedTerminalFile(source)
	if err != nil {
		return nil, err
	}
	return &FileConfirmationReader{file: file}, nil
}

func newOwnedTerminalFile(source *os.File) (*ownedTerminalFile, error) {
	if source == nil {
		return nil, errors.New("terminal I/O source must not be nil")
	}
	handle, err := duplicateTerminalFile(source)
	if err != nil {
		return nil, err
	}
	closed, cancel := context.WithCancel(context.Background())
	reader := &ownedTerminalFile{
		handle: handle, closed: closed, cancel: cancel, slot: make(chan struct{}, 1),
	}
	reader.slot <- struct{}{}
	return reader, nil
}

func (reader *FileConfirmationReader) ReadContext(ctx context.Context, data []byte) (int, error) {
	n, err := reader.file.withContext(ctx, data, readConfirmationInput)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrClosed) {
		return 0, err
	}
	return n, err
}

func (reader *FileConfirmationReader) Close() error { return reader.file.Close() }

func (reader *ownedTerminalFile) withContext(
	ctx context.Context,
	data []byte,
	operation func(context.Context, terminalFileHandle, []byte) (int, error),
) (int, error) {
	if ctx == nil {
		return 0, errors.New("terminal I/O context must not be nil")
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-reader.closed.Done():
		return 0, os.ErrClosed
	case <-reader.slot:
	}
	defer func() { reader.slot <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if reader.closed.Err() != nil {
		return 0, os.ErrClosed
	}
	if len(data) == 0 {
		return 0, nil
	}
	readContext, cancel := context.WithCancel(ctx)
	stopClose := context.AfterFunc(reader.closed, cancel)
	defer func() {
		stopClose()
		cancel()
	}()
	n, err := operation(readContext, reader.handle, data)
	if ctx.Err() != nil {
		if errors.Is(err, ctx.Err()) {
			return n, err
		}
		return n, errors.Join(ctx.Err(), err)
	}
	if reader.closed.Err() != nil {
		return n, errors.Join(os.ErrClosed, err)
	}
	return n, err
}

func (reader *ownedTerminalFile) Close() error {
	reader.closeOnce.Do(func() {
		reader.cancel()
		// Cancellation wakes the active I/O. Wait for its OS operations and
		// cleanup before releasing the handle; no caller-owned file is closed.
		<-reader.slot
		reader.closeErr = closeTerminalFile(reader.handle)
		reader.slot <- struct{}{}
	})
	return reader.closeErr
}

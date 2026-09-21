package cli

import (
	"context"
	"os"
)

// FileTerminalWriter owns a duplicate of a terminal or pipe output handle.
// WriteContext completes the supplied bytes or returns the written count and
// an error. Cancellation and Close interrupt blocked writes; Close waits for
// them to finish before releasing the duplicate. The caller's file stays open.
// Other writers must not interleave terminal drawing with this adapter.
type FileTerminalWriter struct {
	file *ownedTerminalFile
}

func NewFileTerminalWriter(source *os.File) (*FileTerminalWriter, error) {
	file, err := newOwnedTerminalFile(source)
	if err != nil {
		return nil, err
	}
	return &FileTerminalWriter{file: file}, nil
}

func (writer *FileTerminalWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	return writer.file.withContext(ctx, data, writeTerminalOutput)
}

func (writer *FileTerminalWriter) Close() error { return writer.file.Close() }

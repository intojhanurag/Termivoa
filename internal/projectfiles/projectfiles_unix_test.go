//go:build !windows

package projectfiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestAFIFONeverBlocksARead(t *testing.T) {
	dir := plainDir(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	files, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	// platform.OpenRegular is what Read opens with after its Stat: a FIFO swapped in
	// between must not block the open.
	done := make(chan error, 1)
	go func() {
		file, _, err := platform.OpenRegular(files, "pipe", errNotFile)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotFile) {
			t.Fatalf("open of a FIFO = %v, want errNotFile", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
	if got := Read(context.Background(), dir, "pipe"); got.State != protocol.NotFile {
		t.Fatalf("read of a FIFO = %#v", got)
	}
}

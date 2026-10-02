//go:build !windows

package projecttasks

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/platform"
)

func TestAFIFONeverBlocksARead(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "go.mod"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// platform.OpenRegular is what readFile opens with after its Lstat: a FIFO swapped
	// in between must not block the open.
	done := make(chan error, 1)
	go func() {
		file, _, err := platform.OpenRegular(root, "go.mod", errSkipped)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errSkipped) {
			t.Fatalf("open of a FIFO = %v, want errSkipped", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}

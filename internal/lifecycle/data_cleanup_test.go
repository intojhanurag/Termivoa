package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
)

func TestDataCleanupPreservesOwnershipNamespace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Phone Terminal")
	canonical, lock, err := ipc.LockBrokerDataDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "github.json"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(canonical, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	// Symlink cleanup must remove only the entry, never its target.
	if err := os.Symlink(outside, filepath.Join(canonical, "linked")); err != nil {
		t.Logf("symlink subcase unavailable: %v; ownership check still runs", err)
	}
	if err := Apply(context.Background(), nil, Plan{Steps: []Step{{RemoveTree: canonical, KeepDataLock: true}}}, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(canonical)
	if err != nil || len(entries) != 1 || entries[0].Name() != ipc.BrokerDataLockFile {
		t.Fatal("ownership namespace was removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("symlink target was touched")
	}
	_, replacement, err := ipc.LockBrokerDataDirectory(canonical)
	if err == nil {
		replacement.Close()
		t.Fatal("replacement broker acquired ownership during cleanup")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	_, replacement, err = ipc.LockBrokerDataDirectory(canonical)
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
}
func TestDataCleanupCanceledBeforeDeletion(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fixture")
	os.WriteFile(file, []byte("fixture"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := removeDataContents(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup=%v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("canceled cleanup removed data")
	}
}

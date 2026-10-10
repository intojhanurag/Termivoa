//go:build darwin || linux

package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The real GitHub service and its fake client support macOS/Linux only. The
// file-removal regressions remain platform-neutral in token_delete_test.go.
func TestDisconnectReportsLocalRemovalFailure(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	path := filepath.Join(h.config.DataDir, tokenFileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, revoked, err := h.service.Disconnect(context.Background(), "a")
	if err == nil || !revoked {
		t.Fatalf("local removal failure was hidden: revoked=%v error=%v", revoked, err)
	}
	if contents, err := os.ReadFile(filepath.Join(path, "keep")); err != nil || string(contents) != "keep" {
		t.Fatal("failed Disconnect changed entry contents")
	}
}

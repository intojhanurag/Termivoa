//go:build darwin || linux

package broker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// A real shell session: the published cwd starts at the project folder and
// follows `cd /tmp` (lsof on macOS, /proc on Linux), with no hook injected.
func TestSessionPublishesLiveFolderAfterCd(t *testing.T) {
	start, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	session, err := StartSession(context.Background(), SessionConfig{
		ID: testSessionID, Generation: testGeneration,
		Process: ptyhost.Spec{
			Executable: "/bin/sh", Args: []string{"-c", "sleep 1.5; cd /tmp && echo moved; exec sleep 30"},
			Directory: start, Columns: 80, Rows: 24,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd == start })
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd == tmp })
}

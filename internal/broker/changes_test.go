package broker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestSessionStreamReadsChangesOfTheLiveFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{"init -q", "add a.txt", "-c user.name=t -c user.email=t@example.com commit -q -m one"} {
		cmd := exec.Command("git", strings.Fields(args)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, session, process := newStreamFixture(t)
	session.directory = t.TempDir() // The project folder is not a repository.
	process.Emit([]byte("\x1b]7;file://host/" + strings.TrimPrefix(filepath.ToSlash(repo), "/") + "\x07"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd != "" })

	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	request := client.action(t, protocol.ChangesList, protocol.ChangesListPayload{})
	message := client.await(t, protocol.ChangesSummary, request)
	summary, _ := protocol.DecodePayload[protocol.ChangesSummaryPayload](message)
	if *message.SessionID != rpcSessionID || *message.Generation != rpcSessionGen || summary.State != protocol.ChangesStateReady ||
		summary.Root == nil || filepath.Base(*summary.Root) != filepath.Base(repo) || len(summary.Files) != 1 ||
		summary.Files[0].Path != "a.txt" || summary.Files[0].Status != protocol.Modified {
		t.Fatalf("summary = %#v", summary)
	}
	request = client.action(t, protocol.ChangesFile, protocol.ChangesFilePayload{Path: "a.txt"})
	patch, _ := protocol.DecodePayload[protocol.ChangesPatchPayload](client.await(t, protocol.ChangesPatch, request))
	if patch.State != protocol.PatchStateReady || patch.Path != "a.txt" || len(patch.Lines) != 3 {
		t.Fatalf("patch = %#v", patch)
	}
	request = client.action(t, protocol.ChangesFile, protocol.ChangesFilePayload{Path: "b.txt"})
	patch, _ = protocol.DecodePayload[protocol.ChangesPatchPayload](client.await(t, protocol.ChangesPatch, request))
	if patch.State != protocol.Missing || len(patch.Lines) != 0 {
		t.Fatalf("unlisted patch = %#v", patch)
	}
}

func TestSessionStreamLimitsChangesAndCancelsOnDetach(t *testing.T) {
	started, cancelled := make(chan string, 1), make(chan struct{})
	previous := readChangesSummary
	readChangesSummary = func(ctx context.Context, dir string) protocol.ChangesSummaryPayload {
		started <- dir
		<-ctx.Done()
		close(cancelled)
		return protocol.ChangesSummaryPayload{State: protocol.ChangesStateUnavailable, Files: []protocol.ChangedFile{}}
	}
	t.Cleanup(func() { readChangesSummary = previous })
	fixture, session, _ := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	client.action(t, protocol.ChangesList, protocol.ChangesListPayload{})
	select {
	case dir := <-started:
		if dir != session.directory {
			t.Fatalf("changes folder = %q, want project folder %q", dir, session.directory)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("changes request never started")
	}
	request := client.action(t, protocol.ChangesFile, protocol.ChangesFilePayload{Path: "a.txt"})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("second changes request = %#v", failure)
	}
	_ = client.conn.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("detaching did not cancel git")
	}
}

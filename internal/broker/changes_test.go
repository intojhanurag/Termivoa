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

func TestSessionStreamCommitsForTheControllerOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	fixture, session, _ := newStreamFixture(t)
	repo := t.TempDir()
	session.directory = repo
	for _, args := range []string{"init -q", "config user.name t", "config user.email t@example.com", "config commit.gpgsign false"} {
		cmd := exec.Command("git", strings.Fields(args)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	viewer := startStreamClient(t, fixture.server, protocol.CLI)
	viewer.attach(t)
	controller := startStreamClient(t, fixture.server, protocol.CLI)
	controller.attach(t)
	grant := controller.acquire(t)

	commit := protocol.ChangesCommitPayload{Lease: *grant.Lease, Message: "first", Paths: []string{"a.txt"}}
	for name, test := range map[string]struct {
		client *streamClient
		lease  string
		code   protocol.ErrorCode
	}{
		"viewer with the lease": {viewer, *grant.Lease, protocol.NotController},
		"controller stale":      {controller, strings.Repeat("A", 43), protocol.StaleLease},
	} {
		commit.Lease = test.lease
		for kind, payload := range map[protocol.MessageType]any{
			protocol.ChangesCommit: commit, protocol.ChangesPush: protocol.ChangesPushPayload{Lease: test.lease},
		} {
			request := test.client.action(t, kind, payload)
			failure, _ := protocol.DecodePayload[protocol.ErrorPayload](test.client.await(t, protocol.Error, request))
			if failure.Code != test.code {
				t.Errorf("%s %s = %#v", name, kind, failure)
			}
		}
	}
	commit.Lease = *grant.Lease
	request := controller.action(t, protocol.ChangesCommit, commit)
	result, _ := protocol.DecodePayload[protocol.ChangesCommittedPayload](controller.await(t, protocol.ChangesCommitted, request))
	if result.State != protocol.Committed || result.Commit == nil {
		t.Fatalf("commit = %#v", result)
	}
	request = controller.action(t, protocol.ChangesPush, protocol.ChangesPushPayload{Lease: *grant.Lease})
	pushed, _ := protocol.DecodePayload[protocol.ChangesPushedPayload](controller.await(t, protocol.ChangesPushed, request))
	if pushed.State != protocol.NoRemote {
		t.Fatalf("push = %#v", pushed)
	}
}

func TestSessionStreamRunsOneWriteThatOutlivesTheViewer(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	previous := commitChanges
	commitChanges = func(ctx context.Context, _, _ string, _ []string) protocol.ChangesCommittedPayload {
		close(started)
		<-release
		finished <- ctx.Err()
		return protocol.ChangesCommittedPayload{State: protocol.Nothing}
	}
	previousPush := pushChanges
	pushChanges = func(context.Context, string) protocol.ChangesPushedPayload {
		return protocol.ChangesPushedPayload{State: protocol.NoRemote}
	}
	t.Cleanup(func() { commitChanges, pushChanges = previous, previousPush })
	fixture, _, _ := newStreamFixture(t)
	first := startStreamClient(t, fixture.server, protocol.CLI)
	first.attach(t)
	grant := first.acquire(t)
	first.action(t, protocol.ChangesCommit, protocol.ChangesCommitPayload{Lease: *grant.Lease, Message: "m", Paths: []string{"a"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("commit never started")
	}
	request := first.action(t, protocol.ChangesPush, protocol.ChangesPushPayload{Lease: *grant.Lease})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](first.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("second write = %#v", failure)
	}
	// The viewer leaves; the commit is not cancelled.
	_ = first.conn.Close()
	<-first.done
	second := startStreamClient(t, fixture.server, protocol.CLI)
	second.attach(t)
	grant = second.acquire(t)
	request = second.action(t, protocol.ChangesPush, protocol.ChangesPushPayload{Lease: *grant.Lease})
	failure, _ = protocol.DecodePayload[protocol.ErrorPayload](second.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("write from another viewer during the first = %#v", failure)
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("detach cancelled the commit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("commit never finished")
	}
	// The slot frees once the first write ends.
	deadline := time.Now().Add(3 * time.Second)
	for {
		request = second.action(t, protocol.ChangesPush, protocol.ChangesPushPayload{Lease: *grant.Lease})
		message := second.await(t, "", request)
		if message.Type == protocol.ChangesPushed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("push after the commit = %s", message.Payload)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

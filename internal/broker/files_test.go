package broker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestSessionStreamBrowsesFilesWithoutControl(t *testing.T) {
	fixture, session, _ := newStreamFixture(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	session.directory = dir
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t) // A viewer: no control.acquire.
	request := client.action(t, protocol.FilesList, protocol.FilesListPayload{Path: ""})
	message := client.await(t, protocol.FilesListed, request)
	listed, _ := protocol.DecodePayload[protocol.FilesListedPayload](message)
	if *message.SessionID != rpcSessionID || listed.State != protocol.FilesListStateReady || listed.Root == nil ||
		*listed.Root != filepath.ToSlash(dir) || len(listed.Entries) != 1 || listed.Entries[0].Name != "src" {
		t.Fatalf("listed = %#v", listed)
	}

	// A bad path is refused with invalid_request, and the stream stays open.
	for _, bad := range []string{"../etc", "/etc/passwd"} {
		request = client.action(t, protocol.FilesRead, protocol.FilesReadPayload{Path: bad})
		failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
		if failure.Code != protocol.InvalidRequest {
			t.Fatalf("read %q = %#v", bad, failure)
		}
	}

	request = client.action(t, protocol.FilesRead, protocol.FilesReadPayload{Path: "./src//main.go"})
	content, _ := protocol.DecodePayload[protocol.FilesContentPayload](client.await(t, protocol.FilesContent, request))
	if content.State != protocol.FilesReadStateReady || content.Path != "src/main.go" || content.Text == nil || *content.Text != "package main\n" {
		t.Fatalf("content = %#v", content)
	}
	request = client.action(t, protocol.FilesSearch, protocol.FilesSearchPayload{Query: "MAIN"})
	found, _ := protocol.DecodePayload[protocol.FilesFoundPayload](client.await(t, protocol.FilesFound, request))
	if found.State != protocol.FilesSearchStateReady || len(found.Paths) != 1 || found.Paths[0] != "src/main.go" {
		t.Fatalf("found = %#v", found)
	}
}

func TestSessionStreamLimitsFilesAndCancelsOnDetach(t *testing.T) {
	started, cancelled := make(chan string, 1), make(chan struct{})
	previous := searchFiles
	searchFiles = func(ctx context.Context, folder, query string) protocol.FilesFoundPayload {
		started <- folder
		<-ctx.Done()
		close(cancelled)
		return protocol.FilesFoundPayload{State: protocol.FilesSearchStateUnavailable, Query: query, Paths: []string{}}
	}
	t.Cleanup(func() { searchFiles = previous })
	fixture, session, _ := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	client.action(t, protocol.FilesSearch, protocol.FilesSearchPayload{Query: "x"})
	select {
	case folder := <-started:
		if folder != session.directory {
			t.Fatalf("files folder = %q, want %q", folder, session.directory)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("files request never started")
	}
	// Any second files request waits its turn; changes has its own slot.
	request := client.action(t, protocol.FilesList, protocol.FilesListPayload{Path: ""})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("second files request = %#v", failure)
	}
	request = client.action(t, protocol.ChangesList, protocol.ChangesListPayload{})
	client.await(t, protocol.ChangesSummary, request)
	_ = client.conn.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("detaching did not cancel the files request")
	}
}

package broker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestSessionStreamListsTasksOfTheProjectFolder(t *testing.T) {
	fixture, session, process := newStreamFixture(t)
	session.directory = t.TempDir()
	if err := os.WriteFile(filepath.Join(session.directory, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The live folder moves elsewhere; tasks still come from the project folder.
	live := t.TempDir()
	if err := os.WriteFile(filepath.Join(live, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	process.Emit([]byte("\x1b]7;file://host/" + strings.TrimPrefix(filepath.ToSlash(live), "/") + "\x07"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd != "" })

	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	request := client.action(t, protocol.TasksList, protocol.TasksListPayload{})
	message := client.await(t, protocol.TasksListed, request)
	listed, _ := protocol.DecodePayload[protocol.TasksListedPayload](message)
	if *message.SessionID != rpcSessionID || *message.Generation != rpcSessionGen || listed.State != protocol.TasksStateReady ||
		listed.Folder == nil || *listed.Folder != session.directory || len(listed.Tasks) != 1 ||
		listed.Tasks[0] != (protocol.ProjectTask{Name: "dev", Command: "npm run dev", Source: protocol.PackageJSON, Kind: protocol.Dev}) {
		t.Fatalf("tasks = %#v", listed)
	}
}

func TestSessionStreamLimitsTasksAndCancelsOnDetach(t *testing.T) {
	started, cancelled := make(chan string, 1), make(chan struct{})
	previous := listProjectTasks
	listProjectTasks = func(ctx context.Context, dir string) protocol.TasksListedPayload {
		started <- dir
		<-ctx.Done()
		close(cancelled)
		return protocol.TasksListedPayload{State: protocol.TasksStateUnavailable, Tasks: []protocol.ProjectTask{}}
	}
	t.Cleanup(func() { listProjectTasks = previous })
	fixture, session, _ := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	client.action(t, protocol.TasksList, protocol.TasksListPayload{})
	select {
	case dir := <-started:
		if dir != session.directory {
			t.Fatalf("tasks folder = %q, want project folder %q", dir, session.directory)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tasks request never started")
	}
	request := client.action(t, protocol.TasksList, protocol.TasksListPayload{})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("second tasks request = %#v", failure)
	}
	_ = client.conn.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("detaching did not cancel the tasks request")
	}
}

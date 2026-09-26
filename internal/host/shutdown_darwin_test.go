//go:build darwin

package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	shutdownProjectID = "c1111111-1111-4111-8111-111111111111"
	shutdownProfileID = "c2222222-2222-4222-8222-222222222222"
)

// admin.shutdown must end every session through the normal termination path
// (state exited, not lost), leave no shell or descendant process, and stop the
// broker cleanly.
func TestAdminShutdownEndsEverySessionTreeAndStopsBroker(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pterm-host-shutdown-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir, runtimeDir := filepath.Join(root, "data"), filepath.Join(root, "runtime")
	children := filepath.Join(root, "children")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ready := make(chan BrokerReady, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunBroker(context.Background(), BrokerConfig{
			DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "shutdown-test", Profile: DevelopmentProfile,
			Ready: func(state BrokerReady) { ready <- state },
		})
	}()
	var state BrokerReady
	select {
	case state = <-ready:
	case err := <-done:
		t.Fatalf("broker stopped before ready: %v", err)
	case <-ctx.Done():
		t.Fatal("broker did not become ready")
	}
	now := time.Now().UTC()
	if err := errors.Join(
		state.Store.PutProject(ctx, store.Project{
			ID: shutdownProjectID, DisplayName: "shutdown", CanonicalPath: root, CreatedAt: now, UpdatedAt: now,
		}),
		// Each shell starts a descendant in its process group and records it.
		state.Store.PutShellProfile(ctx, store.ShellProfile{
			ID: shutdownProfileID, DisplayName: "sh", Platform: store.PlatformDarwin, ExecutablePath: "/bin/sh",
			Arguments: []string{"-c", "sleep 600 & echo $! >> '" + children + "'; wait"}, CreatedAt: now, UpdatedAt: now,
		}),
	); err != nil {
		t.Fatal(err)
	}

	management, err := localclient.DialManagement(ctx, runtimeDir, "shutdown-test")
	if err != nil {
		t.Fatal(err)
	}
	defer management.Close()
	var pids []int
	var sessions []string
	for _, operationID := range []string{"c3333333-3333-4333-8333-333333333333", "c4444444-4444-4444-8444-444444444444"} {
		created, err := management.Create(ctx, operationID, shutdownProjectID, shutdownProfileID)
		if err != nil {
			t.Fatal(err)
		}
		session, err := state.Registry.Get(created.Session.ID)
		if err != nil {
			t.Fatal(err)
		}
		status, err := session.Status(ctx)
		if err != nil || status.PID <= 0 {
			t.Fatalf("session status = %#v, %v", status, err)
		}
		pids = append(pids, status.PID)
		sessions = append(sessions, created.Session.ID)
	}
	for {
		data, _ := os.ReadFile(children)
		if lines := strings.Fields(string(data)); len(lines) == 2 {
			for _, line := range lines {
				pid, err := strconv.Atoi(line)
				if err != nil {
					t.Fatal(err)
				}
				pids = append(pids, pid)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("session descendants did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}

	admin, err := localclient.DialAdmin(ctx, runtimeDir, "shutdown-test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := admin.Shutdown(ctx)
	_ = admin.Close()
	if err != nil || result.TerminatedSessions != 2 {
		t.Fatalf("shutdown = %#v, %v", result, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("broker shutdown: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("broker did not stop after admin.shutdown")
	}
	for _, pid := range pids {
		for syscall.Kill(pid, 0) == nil {
			select {
			case <-ctx.Done():
				t.Fatalf("process %d outlived shutdown", pid)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	if _, err := localclient.DialManagement(ctx, runtimeDir, "shutdown-test"); err == nil {
		t.Fatal("broker endpoint still answers after shutdown")
	}
	persistence, err := store.Open(ctx, filepath.Join(dataDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	for _, id := range sessions {
		metadata, err := persistence.GetSession(ctx, id)
		if err != nil || metadata.State != store.SessionExited {
			t.Fatalf("session %s after shutdown = %s, %v", id, metadata.State, err)
		}
	}
}

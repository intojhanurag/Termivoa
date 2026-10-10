package broker

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func admissionRequest() protocol.BrokerRPCSessionCreateRequestPayload {
	return protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: operationID("admission-create"), ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		Worktree: &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "patient-otter", Base: "main"},
	}
}

func TestWorktreeAdmissionDoesNotWaitBehindAcceptedCreate(t *testing.T) {
	fixture := newManagementFixture(t)
	fixture.server.worktreeCreates.Lock()
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.server.createSession(context.Background(), admissionRequest())
		finished <- err
	}()
	select {
	case err := <-finished:
		if failure := classifyManagementError(err); failure.code != protocol.LimitExceeded {
			t.Errorf("busy create = %v (%+v)", err, failure)
		}
	case <-time.After(250 * time.Millisecond):
		t.Error("busy create queued behind accepted work")
		fixture.server.worktreeCreates.Unlock()
		<-finished
		return
	}
	fixture.server.worktreeCreates.Unlock()
	if fixture.starts.Load() != 0 {
		t.Fatal("refused request started a session")
	}
	// A newly accepted request that fails validation must release admission,
	// too; it must not leave all later creates permanently busy.
	_, err := fixture.server.createSession(context.Background(), admissionRequest())
	if !errors.Is(err, errWorktreeSource) {
		t.Fatalf("create after busy slot release = %v", err)
	}
	if !fixture.server.worktreeCreates.TryLock() {
		t.Fatal("failed accepted request retained admission")
	}
	fixture.server.worktreeCreates.Unlock()
}

func TestWorktreeAdmissionRejectsCanceledRequestBeforeAcceptance(t *testing.T) {
	fixture := newManagementFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.server.createSession(ctx, admissionRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create = %v", err)
	}
	if fixture.starts.Load() != 0 {
		t.Fatal("canceled request started a session")
	}
	if !fixture.server.worktreeCreates.TryLock() {
		t.Fatal("canceled request retained admission")
	}
	fixture.server.worktreeCreates.Unlock()
}

// A fully written create followed by peer close previously left a handler
// behind the mutex. Hold the active slot, close twenty callers, and require
// every registered handler to leave while that slot remains held.
func TestWorktreeAdmissionClosedCallersAndShutdownDoNotQueue(t *testing.T) {
	fixture := newManagementFixture(t)
	fixture.server.worktreeCreates.Lock()
	defer func() { fixture.server.worktreeCreates.Unlock(); fixture.server.connectionsWG.Wait() }()
	const callers = 20
	finished := make(chan error, callers)
	for i := 0; i < callers; i++ {
		client, connection := net.Pipe()
		if !fixture.server.registerConnection(connection) {
			t.Fatal("connection refused")
		}
		go func() {
			defer fixture.server.unregisterConnection(connection)
			finished <- fixture.server.serveConnection(context.Background(), connection)
		}()
		if _, err := ipc.ClientHandshake(context.Background(), client, protocol.CLI, "admission-test"); err != nil {
			t.Fatal(err)
		}
		writer, _ := ipc.NewFrameWriter(client, protocol.MaxInboundMessageBytes)
		if err := writer.WriteFrame(managementRequest(t, operationID("admission-request"), protocol.SessionCreateRequest, nil, nil, admissionRequest())); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
	}
	fixture.server.stopConnections()
	for i := 0; i < callers; i++ {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Errorf("closed caller %d remained queued during shutdown", i)
			return
		}
	}
	fixture.server.connectionsWG.Wait()
	fixture.server.mu.Lock()
	count := len(fixture.server.connections)
	fixture.server.mu.Unlock()
	if count != 0 || fixture.starts.Load() != 0 {
		t.Fatalf("remaining connections=%d starts=%d", count, fixture.starts.Load())
	}
	t.Logf("closed callers=%d; remaining registered connections=%d; started sessions=%d", callers, count, fixture.starts.Load())
}

// A real hook marks the acceptance boundary. Disconnecting afterwards must
// preserve the first job, while its duplicate gets a correlated refusal and
// never stores a competing receipt. Shutdown waits only for that one job.
func TestWorktreeAdmissionAcceptedJobSurvivesDisconnectAndReplaysOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX git hook")
	}
	fixture, sessions, mu := worktreeFixture(t)
	service, err := auth.NewService(auth.ServiceConfig{Store: fixture.persistence})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.config.Auth = service
	paired := pairDevice(t, service, "Fixture phone")
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "-q", "--allow-empty", "-m", "fixture")
	if _, err := fixture.server.createSession(context.Background(), protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: operationID("admission-source"), ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	sessions[sourceSessionID].directory = repo
	mu.Unlock()
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n: > \"$0.started\"\nwhile [ ! -f \"$0.release\" ]; do sleep 0.01; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Unix sockets need a short path on macOS.
	runtimeDir, err := os.MkdirTemp("/tmp", "pterm-admit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{RuntimeDir: runtimeDir, BrokerGeneration: registryBrokerGeneration})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- fixture.server.Serve(ctx, listener) }()
	client, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: runtimeDir, ClientBuild: "admission-test"})
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		_ = os.WriteFile(hook+".release", nil, 0600)
		_ = client.Close()
		cancel()
		if !stopped {
			select {
			case <-served:
			case <-time.After(5 * time.Second):
				t.Error("broker did not stop")
			}
		}
	})
	request := admissionRequest()
	callCtx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	first := make(chan error, 1)
	go func() {
		_, err := client.CreateSession(callCtx, paired.Credential, protocol.HTTPSessionCreateRequest{
			OperationID: request.OperationID, ProjectID: request.ProjectID, ShellProfileID: request.ShellProfileID, Worktree: request.Worktree,
		})
		first <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(hook + ".started"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted hook did not start")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, gatewayclient.ErrDeliveryUncertain) {
			t.Fatalf("accepted disconnect = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller did not return on cancel")
	}
	// The duplicate shares the operation ID but receives a definite refusal,
	// not a second accepted job, a successful replay, or delivery uncertainty.
	duplicateCtx, stopDuplicate := context.WithTimeout(context.Background(), time.Second)
	_, err = client.CreateSession(duplicateCtx, paired.Credential, protocol.HTTPSessionCreateRequest{
		OperationID: request.OperationID, ProjectID: request.ProjectID, ShellProfileID: request.ShellProfileID, Worktree: request.Worktree,
	})
	stopDuplicate()
	var failure *gatewayapi.Error
	if !errors.As(err, &failure) || failure.Code != protocol.LimitExceeded || errors.Is(err, gatewayclient.ErrDeliveryUncertain) {
		t.Fatalf("duplicate refusal = %v", err)
	}
	invalidFirst := byte('A')
	if paired.Credential[0] == invalidFirst {
		invalidFirst = 'B'
	}
	_, err = client.CreateSession(context.Background(), string(invalidFirst)+paired.Credential[1:], protocol.HTTPSessionCreateRequest{
		OperationID: request.OperationID, ProjectID: request.ProjectID, ShellProfileID: request.ShellProfileID, Worktree: request.Worktree,
	})
	if !errors.As(err, &failure) || failure.Code != protocol.Unauthorized {
		t.Fatalf("busy admission bypassed authorization: %v", err)
	}
	if _, found, err := fixture.registry.ReplayCreate(context.Background(), request.OperationID, request.ProjectID, request.ShellProfileID, request.Worktree); err != nil || found {
		t.Fatalf("refused duplicate wrote a receipt: found=%v err=%v", found, err)
	}
	cancel()
	select {
	case <-served:
		stopped = true
		t.Fatal("shutdown abandoned accepted work")
	case <-time.After(30 * time.Millisecond):
	}
	if err := os.WriteFile(hook+".release", nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		stopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after accepted job")
	}
	replay, err := fixture.server.createSession(context.Background(), request)
	if err != nil || replay.Session.ID != rpcSessionID {
		t.Fatalf("accepted receipt replay = %+v, %v", replay, err)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("source plus accepted sessions=%d", count)
	}
	listed := gitIn(t, repo, "worktree", "list", "--porcelain")
	if strings.Count(listed, "worktree ") != 2 {
		t.Fatalf("duplicate created extra worktree: %s", listed)
	}
}

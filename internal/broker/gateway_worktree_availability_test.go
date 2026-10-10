package broker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

// A real git post-checkout hook holds worktree creation while independent
// authenticated list and Live-open calls cross the same gateway client.
// Everything runs in fixture-owned temp folders, with synthetic processes.
func TestSlowWorktreeHookDoesNotBlockGatewayReadsOrLiveOpen(t *testing.T) {
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
		OperationID: operationID("slow-hook-source"), ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
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
	runtimeDir, err := os.MkdirTemp("/tmp", "pterm-hol-")
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
	client, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: runtimeDir, ClientBuild: "fixture-gateway"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(hook+".release", nil, 0600)
		_ = client.Close()
		cancel()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("fixture broker did not stop")
		}
	})
	created := make(chan error, 1)
	go func() {
		_, err := client.CreateSession(context.Background(), paired.Credential, protocol.HTTPSessionCreateRequest{
			OperationID: operationID("slow-hook-worktree"), ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
			Worktree: &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "patient-otter", Base: "main"},
		})
		created <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(hook + ".started"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("post-checkout hook did not start")
		}
		time.Sleep(time.Millisecond)
	}
	for _, action := range []string{"list", "authenticate", "live-open"} {
		readCtx, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
		started := time.Now()
		switch action {
		case "list":
			_, err = client.ListSessions(readCtx, paired.Credential)
		case "authenticate":
			_, err = client.Authenticate(readCtx, paired.Credential)
		case "live-open":
			stream, openErr := client.OpenLive(readCtx, paired.Credential)
			err = openErr
			if stream != nil {
				_ = stream.Close()
			}
		}
		stop()
		if err != nil {
			t.Fatalf("%s waited behind a running git hook: %v", action, err)
		}
		t.Logf("%s completed in %s while the git hook remained blocked", action, time.Since(started))
	}
	// An invalid credential must still be refused on an independent exchange.
	invalidFirst := byte('A')
	if paired.Credential[0] == invalidFirst {
		invalidFirst = 'B'
	}
	if _, err := client.Authenticate(context.Background(), string(invalidFirst)+paired.Credential[1:]); err == nil {
		t.Fatal("a slow worktree allowed an invalid credential")
	}
	if err := os.WriteFile(hook+".release", nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-created:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worktree did not complete after hook release")
	}
	if record, err := fixture.persistence.GetSession(context.Background(), rpcSessionID); err != nil || record.State != store.SessionRunning {
		t.Fatalf("created session = %+v, %v", record, err)
	}
}

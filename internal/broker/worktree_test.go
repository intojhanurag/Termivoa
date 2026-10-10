package broker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/gitchanges"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	sourceSessionID  = "86666666-6666-4666-8666-666666666661"
	sourceSessionGen = "86666666-6666-4666-8666-666666666662"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// worktreeFixture starts real sessions (fake processes): the source session
// gets sourceSessionID, the next one rpcSessionID, so stream clients reach it.
func worktreeFixture(t *testing.T) (*managementFixture, map[string]*Session, *sync.Mutex) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	fixture := newManagementFixture(t)
	fixture.server.config.WorktreeRoot = filepath.Join(t.TempDir(), "worktrees")
	ids := []string{sourceSessionID, sourceSessionGen, rpcSessionID, rpcSessionGen}
	sessions, mu := map[string]*Session{}, &sync.Mutex{}
	fixture.registry.config.newID = func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(ids) == 0 {
			return uuid.New().String(), nil
		}
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		config.InputQueueCapacity, config.PTYOutputQueue = DefaultInputQueueCapacity, DefaultPTYOutputQueue
		ctx, cancel := context.WithCancel(context.Background())
		core, err := terminal.New(ctx, terminal.Config{Columns: config.Process.Columns, Rows: config.Process.Rows})
		if err != nil {
			cancel()
			return nil, err
		}
		session, err := startSessionRuntime(ctx, cancel, parent.Done(), config, &streamProcess{viewerProcess: newViewerProcess()}, core)
		if err == nil {
			mu.Lock()
			sessions[config.ID] = session
			mu.Unlock()
		}
		return session, err
	}
	return fixture, sessions, mu
}

func TestWorktreeCreateStartsTheSessionInTheWorktree(t *testing.T) {
	fixture, sessions, mu := worktreeFixture(t)
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first")

	if _, err := fixture.server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "87777777-7777-4777-8777-777777777771", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	source := sessions[sourceSessionID]
	mu.Unlock()
	source.directory = repo // The project folder; ChangesFolder falls back to it.

	request := protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "87777777-7777-4777-8777-777777777772", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		Worktree: &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "brave-otter", Base: "main"},
	}
	result, err := fixture.server.createSession(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.server.config.WorktreeRoot, "proj", "brave-otter")
	mu.Lock()
	created := sessions[result.Session.ID]
	mu.Unlock()
	if result.Session.ID != rpcSessionID || created.ProjectFolder() != path ||
		result.Session.Title != "brave-otter" || result.Session.Worktree == nil ||
		*result.Session.Worktree != (protocol.SessionWorktree{Name: "brave-otter", Repo: "proj"}) {
		t.Fatalf("created %+v in %q", result.Session, created.ProjectFolder())
	}
	if got := gitIn(t, path, "symbolic-ref", "--short", "HEAD"); got != "termivoa/brave-otter" {
		t.Fatalf("worktree branch = %q", got)
	}

	// A replay returns the same session and makes no second worktree.
	replay, err := fixture.server.createSession(ctx, request)
	if err != nil || replay.Session.ID != result.Session.ID || replay.Session.Worktree == nil {
		t.Fatalf("replay = %+v, %v", replay.Session, err)
	}
	// A replay while another create holds the slot still returns the
	// receipt, not limit_exceeded (review of #244).
	fixture.server.worktreeCreates.Lock()
	busyReplay, err := fixture.server.createSession(ctx, request)
	fixture.server.worktreeCreates.Unlock()
	if err != nil || busyReplay.Session.ID != result.Session.ID {
		t.Fatalf("replay during another create = %+v, %v", busyReplay.Session, err)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("sessions after replay = %d", len(sessions))
	}
	listed, err := fixture.server.listSessions(ctx, protocol.BrokerRPCSessionListRequestPayload{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range listed.Sessions {
		if (record.Worktree != nil) != (record.ID == rpcSessionID) {
			t.Errorf("listed %s worktree = %+v", record.ID, record.Worktree)
		}
	}

	for name, test := range map[string]struct {
		worktree protocol.WorktreeCreate
		folder   string
		code     protocol.ErrorCode
	}{
		"same name":      {protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "brave-otter", Base: "main"}, repo, protocol.WorktreeExists},
		"bad base":       {protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "calm-fox", Base: "nope"}, repo, protocol.InvalidRequest},
		"not a repo":     {protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "calm-fox", Base: "main"}, t.TempDir(), protocol.NotARepository},
		"missing source": {protocol.WorktreeCreate{SourceSessionID: sourceSessionGen, Name: "calm-fox", Base: "main"}, repo, protocol.SessionMissing},
	} {
		source.directory = test.folder
		operationID := uuid.New().String()
		_, err := fixture.server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: operationID, ProjectID: rpcProjectID, ShellProfileID: rpcProfileID, Worktree: &test.worktree,
		})
		if got := classifyManagementError(err).code; err == nil || got != test.code {
			t.Errorf("%s: %v (%s), want %s", name, err, got, test.code)
		}
		if message := classifyManagementError(err).message; name == "missing source" && !strings.Contains(message, "the worktree is made from") {
			t.Errorf("missing source message = %q", message)
		}
	}
	// The same operation ID with another worktree is a different request.
	conflict := request
	conflict.Worktree = &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "other-name", Base: "main"}
	if _, err := fixture.server.createSession(ctx, conflict); classifyManagementError(err).code != protocol.OperationConflict {
		t.Errorf("reused operation ID with another worktree: %v", err)
	}
	// A session that fails to start rolls its new worktree and branch back.
	source.directory = repo
	start := fixture.registry.config.start
	fixture.registry.config.start = func(context.Context, SessionConfig) (registrySession, error) {
		return nil, errors.New("no shell")
	}
	if _, err := fixture.server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "87777777-7777-4777-8777-777777777773", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		Worktree: &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "calm-fox", Base: "main"},
	}); err == nil {
		t.Fatal("create without a shell succeeded")
	}
	fixture.registry.config.start = start
	if _, err := os.Stat(filepath.Join(fixture.server.config.WorktreeRoot, "proj", "calm-fox")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused or failed create left a folder: %v", err)
	}
	if out := gitIn(t, repo, "branch", "--list", "termivoa/calm-fox"); out != "" {
		t.Errorf("a failed create left its branch: %q", out)
	}

	// worktree.remove: only the controller; dirty without force; the
	// session ends after a removal; the branch stays.
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	viewerRequest := client.action(t, protocol.WorktreeRemove, protocol.WorktreeRemovePayload{Lease: strings.Repeat("A", 43)})
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, viewerRequest)); failure.Code != protocol.NotController {
		t.Fatalf("remove without control = %+v", failure)
	}
	grant := client.acquire(t)
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeRequest := client.action(t, protocol.WorktreeRemove, protocol.WorktreeRemovePayload{Lease: *grant.Lease})
	if removed, _ := protocol.DecodePayload[protocol.WorktreeRemovedPayload](client.await(t, protocol.WorktreeRemoved, removeRequest)); removed.State != protocol.Dirty {
		t.Fatalf("dirty remove = %+v", removed)
	}
	removeRequest = client.action(t, protocol.WorktreeRemove, protocol.WorktreeRemovePayload{Lease: *grant.Lease, Force: true})
	if removed, _ := protocol.DecodePayload[protocol.WorktreeRemovedPayload](client.await(t, protocol.WorktreeRemoved, removeRequest)); removed.State != protocol.Removed {
		t.Fatalf("forced remove = %+v", removed)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree folder still exists: %v", err)
	}
	gitIn(t, repo, "rev-parse", "--verify", "termivoa/brave-otter")
	deadline := time.Now().Add(3 * time.Second)
	for {
		metadata, err := fixture.persistence.GetSession(ctx, rpcSessionID)
		if err == nil && (metadata.State == "exited" || metadata.State == "lost") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session was not terminated: %+v %v", metadata, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorktreeRemoveRefusesOtherSessions(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	grant := client.acquire(t)
	request := client.action(t, protocol.WorktreeRemove, protocol.WorktreeRemovePayload{Lease: *grant.Lease, Force: true})
	if failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request)); failure.Code != protocol.InvalidRequest {
		t.Fatalf("remove in a project session = %+v", failure)
	}
	request = client.action(t, protocol.BranchesList, protocol.BranchesListPayload{})
	listed, _ := protocol.DecodePayload[protocol.BranchesListedPayload](client.await(t, protocol.BranchesListed, request))
	if listed.State == protocol.ChangesStateReady {
		t.Fatalf("branches of a folder outside git = %+v", listed)
	}
}

// On Windows the session ends before git removes its folder.
func TestWorktreeRemoveStopsTheSessionFirstWhenAsked(t *testing.T) {
	fixture, sessions, mu := worktreeFixture(t)
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first")
	if _, err := fixture.server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "87777777-7777-4777-8777-777777777781", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	sessions[sourceSessionID].directory = repo
	mu.Unlock()
	if _, err := fixture.server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "87777777-7777-4777-8777-777777777782", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		Worktree: &protocol.WorktreeCreate{SourceSessionID: sourceSessionID, Name: "win-order", Base: "main"},
	}); err != nil {
		t.Fatal(err)
	}
	previousOrder, previousRemove := stopSessionFirst, removeWorktree
	stateAtRemove := make(chan string, 1)
	stopSessionFirst = true
	removeWorktree = func(ctx context.Context, path string, force bool, before func()) protocol.WorktreeRemovedPayload {
		return previousRemove(ctx, path, force, func() {
			before()
			metadata, _ := fixture.persistence.GetSession(ctx, rpcSessionID)
			stateAtRemove <- string(metadata.State)
		})
	}
	t.Cleanup(func() { stopSessionFirst, removeWorktree = previousOrder, previousRemove })

	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	grant := client.acquire(t)
	client.action(t, protocol.WorktreeRemove, protocol.WorktreeRemovePayload{Lease: *grant.Lease, Force: true})
	select {
	case state := <-stateAtRemove:
		if state == "running" {
			t.Fatal("git removed the folder while the session was still running")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the remove never reached git")
	}
	path := filepath.Join(fixture.server.config.WorktreeRoot, "proj", "win-order")
	deadline := time.Now().Add(5 * time.Second)
	for _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist); _, err = os.Stat(path) {
		if time.Now().After(deadline) {
			t.Fatalf("worktree folder still exists: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorktreeAddFailureMessage(t *testing.T) {
	failure := classifyManagementError(errors.Join(&gitchanges.AddError{Output: "Preparing worktree\nhusky - post-checkout\n\tfailed\x1b[0m"}, errors.New("rollback")))
	if failure.code != protocol.WorktreeFailed || failure.message != "git worktree add failed: Preparing worktree husky - post-checkout failed [0m" {
		t.Fatalf("failure = %+v", failure)
	}
	long := classifyManagementError(&gitchanges.AddError{Output: strings.Repeat("é", 400) + " END"}).message
	if len(long) > 512 || !strings.HasSuffix(long, " END") || protocol.ValidateDisplayText(long, 512) != nil {
		t.Fatalf("long message = %d bytes %q", len(long), long[len(long)-10:])
	}
}

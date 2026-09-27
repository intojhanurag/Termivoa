package broker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	registryBrokerGeneration       = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	registryProjectID              = "11111111-1111-4111-8111-111111111111"
	registryProfileID              = "22222222-2222-4222-8222-222222222222"
	legacyUnavailableReceiptMarker = `{"unavailable":"legacy-v2"}`
)

func TestRegistryCreatesOpaqueGenerationsAndListsSessions(t *testing.T) {
	var configs []SessionConfig
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			configs = append(configs, config)
			return newFakeManagedSession(config.ID, config.Generation, SessionRunning), nil
		},
	})
	defer registry.Close()

	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("create-one"), Process: ptyhost.Spec{Executable: "shell-one"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("create-two"), Process: ptyhost.Spec{Executable: "shell-two"}})
	if err != nil {
		t.Fatal(err)
	}
	firstStatus, _ := first.Session.Status(context.Background())
	secondStatus, _ := second.Session.Status(context.Background())
	if firstStatus.ID == secondStatus.ID || firstStatus.Generation == secondStatus.Generation {
		t.Fatalf("identities were reused: first=%#v second=%#v", firstStatus, secondStatus)
	}
	for _, value := range []string{firstStatus.ID, firstStatus.Generation, secondStatus.ID, secondStatus.Generation} {
		if !uuidPattern.MatchString(value) {
			t.Fatalf("identity %q is not an RFC 4122 version 4 UUID", value)
		}
	}
	if configs[0].Process.Executable != "shell-one" || configs[1].Process.Executable != "shell-two" {
		t.Fatalf("process arguments changed: %#v", configs)
	}
	listed, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != firstStatus.ID || listed[1].ID != secondStatus.ID {
		t.Fatalf("listed statuses = %#v", listed)
	}
	lookedUp, err := registry.Get(secondStatus.ID)
	if err != nil || lookedUp != second.Session {
		t.Fatalf("lookup = %#v, %v", lookedUp, err)
	}
	if _, err := registry.Get("missing"); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("missing lookup error = %v", err)
	}
}

func TestRegistryEnforcesConcurrentLiveLimit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	registry := newFakeRegistry(t, RegistryConfig{
		MaxLiveSessions: 1,
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			close(started)
			<-release
			return newFakeManagedSession(config.ID, config.Generation, SessionRunning), nil
		},
	})
	defer registry.Close()

	created := make(chan error, 1)
	go func() {
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("first")})
		created <- err
	}()
	<-started
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("second")}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("second create error = %v", err)
	}
	close(release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
}

func TestRegistryEndedSessionReleasesCapacity(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{MaxLiveSessions: 1})
	defer registry.Close()

	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("first")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("blocked")}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("live limit error = %v", err)
	}
	first.Session.(*fakeManagedSession).setState(SessionExited)
	firstStatus, _ := first.Session.Status(context.Background())
	deadline := time.Now().Add(time.Second)
	for {
		_, getErr := registry.Get(firstStatus.ID)
		if errors.Is(getErr, ErrSessionRetired) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ended session was not retired: %v", getErr)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("after-exit")}); err != nil {
		t.Fatalf("create after exit: %v", err)
	}
}

func TestRegistryUsesBrokerLifetimeAndClosesEverySession(t *testing.T) {
	brokerContext, cancelBroker := context.WithCancel(context.Background())
	defer cancelBroker()
	requestContext, cancelRequest := context.WithCancel(context.Background())
	var receivedContext context.Context
	var sessions []*fakeManagedSession
	registry, err := NewRegistry(brokerContext, RegistryConfig{
		start: func(ctx context.Context, config SessionConfig) (registrySession, error) {
			receivedContext = ctx
			session := newFakeManagedSession(config.ID, config.Generation, SessionRunning)
			sessions = append(sessions, session)
			return session, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(requestContext, CreateSessionRequest{OperationID: operationID("request-owned")}); err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	select {
	case <-receivedContext.Done():
		t.Fatal("request cancellation reached the managed session lifetime")
	default:
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("second")}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	for index, session := range sessions {
		if !session.closed.Load() {
			t.Fatalf("session %d was not closed", index)
		}
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("after-close")}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("create after close error = %v", err)
	}
	if _, err := registry.List(context.Background()); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("list after close error = %v", err)
	}
}

func TestRegistryClosesSessionThatFinishesStartingDuringShutdown(t *testing.T) {
	started := make(chan struct{})
	session := newFakeManagedSession("late", "late-generation", SessionRunning)
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(ctx context.Context, _ SessionConfig) (registrySession, error) {
			close(started)
			<-ctx.Done()
			return session, nil
		},
	})
	created := make(chan error, 1)
	go func() {
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("late")})
		created <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- registry.Close() }()
	if err := <-created; !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("racing create error = %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if !session.closed.Load() {
		t.Fatal("late-started session was not closed")
	}
}

func TestRegistryCloseWaitsForCreateAdmittedBeforeStart(t *testing.T) {
	lookupFinished := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	registry := newFakeRegistry(t, RegistryConfig{
		now: func() time.Time {
			close(lookupFinished)
			<-release
			return time.Now().UTC()
		},
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = registry.Close()
	})
	request := CreateSessionRequest{OperationID: operationID("create-before-start-close")}
	created := make(chan error, 1)
	go func() {
		_, err := registry.Create(context.Background(), request)
		created <- err
	}()
	<-lookupFinished

	closed := make(chan error, 1)
	go func() { closed <- registry.Close() }()
	waitForRegistryClosed(t, registry)
	select {
	case err := <-closed:
		t.Fatalf("Close returned before admitted create finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-created; !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("create racing close error = %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("registry close: %v", err)
	}
	if _, err := registry.Create(context.Background(), request); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("replayed create after close = %v", err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("new-create-after-close"),
	}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("new create after close = %v", err)
	}
}

func TestRegistryCloseWaitsForAdmittedDurableCreateReplayAndDuplicate(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "create-replay-close.sqlite"))
	t.Cleanup(func() { _ = persistence.Close() })
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	first := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000017",
		"40000000-0000-4000-8000-000000000017",
	})
	request := CreateSessionRequest{
		OperationID: operationID("durable-create-close"), Title: "durable replay",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	}
	original, err := first.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("seed durable create receipt: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close receipt-seeding registry: %v", err)
	}

	second := newPersistentFakeRegistry(t, persistence, &starts, nil)
	lookupFinished := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	second.config.afterCreateLookup = func() {
		close(lookupFinished)
		<-release
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = second.Close()
	})
	type createOutcome struct {
		result CreateSessionResult
		err    error
	}
	ownerResult := make(chan createOutcome, 1)
	go func() {
		result, err := second.Create(context.Background(), request)
		ownerResult <- createOutcome{result: result, err: err}
	}()
	<-lookupFinished

	duplicateContext := &doneObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	duplicateResult := make(chan createOutcome, 1)
	go func() {
		result, err := second.Create(duplicateContext, request)
		duplicateResult <- createOutcome{result: result, err: err}
	}()
	select {
	case <-duplicateContext.observed:
	case <-time.After(time.Second):
		t.Fatal("duplicate create did not reach operation wait")
	}

	closed := make(chan error, 1)
	go func() { closed <- second.Close() }()
	waitForRegistryClosed(t, second)
	select {
	case err := <-closed:
		t.Fatalf("Close returned before durable create callers finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-closed; err != nil {
		t.Fatalf("close replay registry: %v", err)
	}
	// Store ownership may end immediately after Registry.Close because every
	// admitted owner and duplicate has left Create by this point.
	if err := persistence.Close(); err != nil {
		t.Fatalf("close store after registry: %v", err)
	}
	for label, outcome := range map[string]<-chan createOutcome{
		"owner": ownerResult, "duplicate": duplicateResult,
	} {
		result := <-outcome
		if result.err != nil {
			t.Fatalf("%s durable replay: %v", label, result.err)
		}
		if result.result.ID != original.ID || result.result.Generation != original.Generation || result.result.Session != nil {
			t.Fatalf("%s durable replay = %+v, want identity %+v", label, result.result, original)
		}
	}
	if starts.Load() != 1 {
		t.Fatalf("durable replay started %d runtimes, want 1", starts.Load())
	}
	if _, err := second.Create(context.Background(), request); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("durable replay after close = %v", err)
	}
	newRequest := request
	newRequest.OperationID = operationID("new-durable-create-after-close")
	if _, err := second.Create(context.Background(), newRequest); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("new durable create after close = %v", err)
	}
}

func TestRegistryRecoversReservationAfterStartFailure(t *testing.T) {
	var attempts atomic.Int32
	registry := newFakeRegistry(t, RegistryConfig{
		MaxLiveSessions: 1,
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			if attempts.Add(1) == 1 {
				return nil, errors.New("synthetic start failure")
			}
			return newFakeManagedSession(config.ID, config.Generation, SessionRunning), nil
		},
	})
	defer registry.Close()
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("first-fails")}); err == nil {
		t.Fatal("expected first start to fail")
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("second-works")}); err != nil {
		t.Fatalf("reservation leaked after failure: %v", err)
	}
}

func TestRegistryRetriesSessionIDCollision(t *testing.T) {
	values := []string{"same-id", "generation-one", "same-id", "second-id", "generation-two"}
	registry := newFakeRegistry(t, RegistryConfig{
		newID: func() (string, error) {
			value := values[0]
			values = values[1:]
			return value, nil
		},
	})
	defer registry.Close()
	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("first")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("second")})
	if err != nil {
		t.Fatal(err)
	}
	firstStatus, _ := first.Session.Status(context.Background())
	secondStatus, _ := second.Session.Status(context.Background())
	if firstStatus.ID != "same-id" || secondStatus.ID != "second-id" {
		t.Fatalf("collision allocation = %q then %q", firstStatus.ID, secondStatus.ID)
	}
}

func TestRegistryReservesCollidingIDsAcrossConcurrentAllocations(t *testing.T) {
	var calls atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	registry := newFakeRegistry(t, RegistryConfig{
		newID: func() (string, error) {
			call := calls.Add(1)
			if call <= 2 {
				arrived <- struct{}{}
				<-release
				return "colliding-id", nil
			}
			return "replacement-id", nil
		},
	})
	defer registry.Close()
	ids := make(chan string, 2)
	for range 2 {
		go func() {
			id, err := registry.reserveSessionID()
			if err != nil {
				ids <- "error:" + err.Error()
				return
			}
			ids <- id
		}()
	}
	<-arrived
	<-arrived
	close(release)
	first, second := <-ids, <-ids
	if first == second || strings.HasPrefix(first, "error:") || strings.HasPrefix(second, "error:") {
		t.Fatalf("concurrent allocations = %q and %q", first, second)
	}
	registry.mu.Lock()
	if len(registry.reserved) != 2 {
		t.Fatalf("reserved IDs = %#v", registry.reserved)
	}
	registry.reserved = make(map[string]struct{})
	registry.mu.Unlock()
}

func TestRegistryDeduplicatesConcurrentCreateOperation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var starts atomic.Int32
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			if starts.Add(1) == 1 {
				close(started)
			}
			<-release
			return newFakeManagedSession(config.ID, config.Generation, SessionRunning), nil
		},
	})
	defer registry.Close()
	results := make(chan CreateSessionResult, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			session, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("same-operation")})
			results <- session
			errorsFound <- err
		}()
	}
	<-started
	close(release)
	first, second := <-results, <-results
	if err := <-errorsFound; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsFound; err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || first.ID != second.ID || first.Session != second.Session {
		t.Fatalf("dedupe starts=%d, results=%#v and %#v", starts.Load(), first, second)
	}
}

func TestRegistryDeduplicatesRetiredCreateWithoutRetainingRuntime(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{})
	defer registry.Close()
	request := CreateSessionRequest{OperationID: operationID("retired-retry"), Process: ptyhost.Spec{Executable: "shell"}}
	first, err := registry.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Session.(*fakeManagedSession).setState(SessionExited)
	waitForRetiredRegistrySession(t, registry, first.ID)
	retried, err := registry.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != first.ID || retried.Generation != first.Generation || retried.Session != nil {
		t.Fatalf("retired retry = %#v, original = %#v", retried, first)
	}
	registry.mu.Lock()
	retainedRuntime := registry.operations[request.OperationID].result.Session
	registry.mu.Unlock()
	if retainedRuntime != nil {
		t.Fatal("operation history retained a closed runtime")
	}
}

func TestRegistryRejectsOperationIDWithDifferentRequest(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{})
	defer registry.Close()
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("conflict"), ProjectID: "first-project",
		ShellProfileID: "shell", Process: ptyhost.Spec{Executable: "first-shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("conflict"), ProjectID: "different-project",
		ShellProfileID: "shell", Process: ptyhost.Spec{Executable: "first-shell"},
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("conflicting operation error = %v", err)
	}
}

func TestRegistryDurablyReplaysCreateWithoutStartingAnotherRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-create.sqlite")
	persistence := openRegistryStore(t, path)
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	ids := []string{
		"30000000-0000-4000-8000-000000000001",
		"40000000-0000-4000-8000-000000000001",
	}
	registry := newPersistentFakeRegistry(t, persistence, &starts, ids)
	request := CreateSessionRequest{
		OperationID: operationID("durable-create"), Title: "durable session",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	}
	created, err := registry.Create(context.Background(), request)
	if err != nil || created.Session == nil {
		t.Fatalf("create = %+v, err=%v", created, err)
	}
	if starts.Load() != 1 {
		t.Fatalf("starts = %d, want 1", starts.Load())
	}
	stored, err := persistence.GetSession(context.Background(), created.ID)
	if err != nil || stored.Title != request.Title || stored.State != store.SessionRunning {
		t.Fatalf("stored session = %+v, err=%v", stored, err)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("close first registry: %v", err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened := openRegistryStore(t, path)
	defer reopened.Close()
	if _, err := reopened.BeginBrokerGeneration(context.Background(), registryBrokerGeneration, time.Now().UTC()); err != nil {
		t.Fatalf("resume broker generation: %v", err)
	}
	second := newPersistentFakeRegistry(t, reopened, &starts, []string{
		"30000000-0000-4000-8000-000000000002",
		"40000000-0000-4000-8000-000000000002",
	})
	defer second.Close()
	replayed, err := second.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if replayed.ID != created.ID || replayed.Generation != created.Generation || replayed.Session != nil ||
		replayed.Snapshot != created.Snapshot {
		t.Fatalf("replayed create = %+v, original = %+v", replayed, created)
	}
	if starts.Load() != 1 {
		t.Fatalf("durable replay started %d runtimes", starts.Load())
	}
	conflict := request
	conflict.ProjectID = "10000000-0000-4000-8000-000000000099"
	if _, err := second.Create(context.Background(), conflict); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("durable create conflict = %v", err)
	}
}

func TestRegistryDoesNotRerunCreateWithUnavailableLegacyResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-legacy-create.sqlite")
	persistence := openRegistryStore(t, path)
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	first := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000091",
		"40000000-0000-4000-8000-000000000091",
	})
	request := CreateSessionRequest{
		OperationID: operationID("legacy-unavailable-create"), Title: "original",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	}
	if _, err := first.Create(context.Background(), request); err != nil {
		t.Fatalf("create original session: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first registry: %v", err)
	}
	markMutationResultUnavailable(t, path, request.OperationID)

	second := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000092",
		"40000000-0000-4000-8000-000000000092",
	})
	defer second.Close()
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := second.Create(context.Background(), request); !errors.Is(err, ErrOperationResultUnavailable) {
			t.Fatalf("unavailable replay attempt %d error = %v", attempt, err)
		}
	}
	if starts.Load() != 1 {
		t.Fatalf("unavailable replay starts = %d, want 1", starts.Load())
	}
	conflict := request
	conflict.ProjectID = "10000000-0000-4000-8000-000000000099"
	if _, err := second.Create(context.Background(), conflict); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("unavailable receipt conflict error = %v", err)
	}
}

func TestRegistryDurablyDeduplicatesRenameAndConcurrentTerminate(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "registry-mutations.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000003",
		"40000000-0000-4000-8000-000000000003",
	})
	defer registry.Close()
	created, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("mutation-create"), Title: "before",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rename := RenameSessionRequest{
		OperationID: operationID("rename"), SessionID: created.ID,
		Generation: created.Generation, Title: "after",
	}
	firstRename, err := registry.Rename(context.Background(), rename)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	replayedRename, err := registry.Rename(context.Background(), rename)
	if err != nil || replayedRename != firstRename {
		t.Fatalf("rename replay = %+v, err=%v; want %+v", replayedRename, err, firstRename)
	}
	conflictingRename := rename
	conflictingRename.Title = "conflict"
	if _, err := registry.Rename(context.Background(), conflictingRename); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("rename conflict = %v", err)
	}
	metadata, err := persistence.GetSession(context.Background(), created.ID)
	if err != nil || metadata.Title != "after" {
		t.Fatalf("renamed metadata = %+v, err=%v", metadata, err)
	}

	terminate := TerminateSessionRequest{
		OperationID: operationID("terminate"), SessionID: created.ID,
		Generation: created.Generation,
	}
	const callers = 16
	results := make(chan TerminateSessionResult, callers)
	errorsFound := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			ready.Done()
			<-start
			result, err := registry.Terminate(context.Background(), terminate)
			results <- result
			errorsFound <- err
		}()
	}
	ready.Wait()
	close(start)
	var expected TerminateSessionResult
	for index := range callers {
		result := <-results
		if err := <-errorsFound; err != nil {
			t.Fatalf("terminate caller %d: %v", index, err)
		}
		if index == 0 {
			expected = result
		} else if result != expected {
			t.Fatalf("terminate result %d = %+v, want %+v", index, result, expected)
		}
	}
	managed := created.Session.(*fakeManagedSession)
	if managed.terminateCalls.Load() != 1 {
		t.Fatalf("runtime terminate calls = %d, want 1", managed.terminateCalls.Load())
	}
	metadata, err = persistence.GetSession(context.Background(), created.ID)
	if err != nil || metadata.State != store.SessionExiting {
		t.Fatalf("terminating metadata = %+v, err=%v", metadata, err)
	}
}

func TestRegistryCloseWaitsForInFlightRenameAndRejectsNewMutation(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "rename-close.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000010",
		"40000000-0000-4000-8000-000000000010",
	})
	created := createPersistentTestSession(t, registry, "rename-close-create")

	nowStarted := make(chan struct{})
	nowRelease := make(chan struct{})
	var blockOnce sync.Once
	registry.config.now = func() time.Time {
		blockOnce.Do(func() {
			close(nowStarted)
			<-nowRelease
		})
		return time.Now().UTC()
	}
	renameDone := make(chan error, 1)
	go func() {
		_, err := registry.Rename(context.Background(), RenameSessionRequest{
			OperationID: operationID("rename-close"), SessionID: created.ID,
			Generation: created.Generation, Title: "renamed before close",
		})
		renameDone <- err
	}()
	<-nowStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- registry.Close() }()
	waitForRegistryClosed(t, registry)
	if _, err := registry.Rename(context.Background(), RenameSessionRequest{
		OperationID: operationID("rename-after-close"), SessionID: created.ID,
		Generation: created.Generation, Title: "must reject",
	}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("rename after close gate error = %v", err)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight rename: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(nowRelease)
	if err := <-renameDone; err != nil {
		t.Fatalf("in-flight rename: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("close after rename: %v", err)
	}
}

func TestRegistryCloseWaitsForInFlightTerminateAndClosedGatePrecedesReplay(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "terminate-close.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000011",
		"40000000-0000-4000-8000-000000000011",
	})
	created := createPersistentTestSession(t, registry, "terminate-close-create")
	managed := created.Session.(*fakeManagedSession)
	managed.terminateStarted = make(chan struct{})
	managed.terminateGate = make(chan struct{})
	request := TerminateSessionRequest{
		OperationID: operationID("terminate-close"), SessionID: created.ID,
		Generation: created.Generation,
	}
	terminateDone := make(chan error, 1)
	go func() {
		_, err := registry.Terminate(context.Background(), request)
		terminateDone <- err
	}()
	<-managed.terminateStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- registry.Close() }()
	waitForRegistryClosed(t, registry)
	if _, err := registry.Terminate(context.Background(), request); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("durable replay after close gate error = %v", err)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight terminate: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(managed.terminateGate)
	if err := <-terminateDone; err != nil {
		t.Fatalf("in-flight terminate: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("close after terminate: %v", err)
	}
}

func TestRegistryAcceptedTerminateReportsRuntimeFailureOnClose(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "terminate-error.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000012",
		"40000000-0000-4000-8000-000000000012",
	})
	created := createPersistentTestSession(t, registry, "terminate-error-create")
	managed := created.Session.(*fakeManagedSession)
	runtimeErr := errors.New("synthetic runtime termination failure")
	managed.terminateErr = runtimeErr
	request := TerminateSessionRequest{
		OperationID: operationID("terminate-error"), SessionID: created.ID,
		Generation: created.Generation,
	}
	first, err := registry.Terminate(context.Background(), request)
	if err != nil {
		t.Fatalf("accepted termination returned runtime failure: %v", err)
	}
	replayed, err := registry.Terminate(context.Background(), request)
	if err != nil || replayed != first {
		t.Fatalf("accepted termination replay = %+v, err=%v; want %+v", replayed, err, first)
	}
	if managed.terminateCalls.Load() != 1 {
		t.Fatalf("runtime termination calls = %d, want 1", managed.terminateCalls.Load())
	}
	if err := registry.Close(); !errors.Is(err, runtimeErr) {
		t.Fatalf("close diagnostics error = %v, want runtime failure", err)
	}
}

func TestRegistryPersistsFinalStateWhenReceiptTimestampIsLaterThanActualEnd(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "terminate-clock.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000013",
		"40000000-0000-4000-8000-000000000013",
	})
	created := createPersistentTestSession(t, registry, "terminate-clock-create")
	managed := created.Session.(*fakeManagedSession)
	managed.mu.Lock()
	startedAt := managed.status.StartedAt
	managed.mu.Unlock()
	receiptAt := startedAt.Add(10 * time.Second).Truncate(time.Millisecond)
	actualEnd := startedAt.Add(2 * time.Second).Truncate(time.Millisecond)
	registry.config.now = func() time.Time { return receiptAt }
	if _, err := registry.Terminate(context.Background(), TerminateSessionRequest{
		OperationID: operationID("terminate-clock"), SessionID: created.ID,
		Generation: created.Generation,
	}); err != nil {
		t.Fatalf("accept termination: %v", err)
	}
	exitCode := 0
	managed.finish(SessionExited, actualEnd, &exitCode)
	waitForRetiredRegistrySession(t, registry, created.ID)
	metadata, err := persistence.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get persisted final state: %v", err)
	}
	if metadata.State != store.SessionExited || !metadata.UpdatedAt.Equal(receiptAt) ||
		metadata.EndedAt == nil || !metadata.EndedAt.Equal(actualEnd) {
		t.Fatalf("persisted final timeline = %+v; want updated=%v ended=%v", metadata, receiptAt, actualEnd)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("close registry: %v", err)
	}
}

func TestRegistryTerminationSurvivesClockRollback(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "terminate-clock-rollback.sqlite"))
	defer persistence.Close()
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000016",
		"40000000-0000-4000-8000-000000000016",
	})
	created := createPersistentTestSession(t, registry, "terminate-clock-rollback-create")
	managed := created.Session.(*fakeManagedSession)
	metadataBefore, err := persistence.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get session before clock rollback: %v", err)
	}
	registry.config.now = func() time.Time { return metadataBefore.UpdatedAt.Add(-time.Hour) }
	result, err := registry.Terminate(context.Background(), TerminateSessionRequest{
		OperationID: operationID("terminate-clock-rollback"), SessionID: created.ID,
		Generation: created.Generation,
	})
	if err != nil || result.State != SessionExiting || managed.terminateCalls.Load() != 1 {
		t.Fatalf("rollback termination = %+v, calls=%d, err=%v", result, managed.terminateCalls.Load(), err)
	}
	metadataAfter, err := persistence.GetSession(context.Background(), created.ID)
	if err != nil || metadataAfter.State != store.SessionExiting || !metadataAfter.UpdatedAt.Equal(metadataBefore.UpdatedAt) {
		t.Fatalf("clamped termination metadata = %+v, err=%v; want updated=%v", metadataAfter, err, metadataBefore.UpdatedAt)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("close registry: %v", err)
	}
}

func TestRegistryPersistsAndListsFinalStateBeforeViewerRetirement(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "viewer-retirement.sqlite"))
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000014",
		"40000000-0000-4000-8000-000000000014",
		"30000000-0000-4000-8000-000000000015",
		"40000000-0000-4000-8000-000000000015",
	})
	registry.config.MaxLiveSessions = 1
	created := createPersistentTestSession(t, registry, "viewer-retirement-create")
	managed := created.Session.(*fakeManagedSession)
	managed.holdRetirement()
	t.Cleanup(func() {
		managed.releaseRetirement()
		_ = registry.Close()
		_ = persistence.Close()
	})

	managed.mu.Lock()
	actualEnd := managed.status.StartedAt.Add(2 * time.Second).Truncate(time.Millisecond)
	managed.mu.Unlock()
	exitCode := 7
	managed.finish(SessionExited, actualEnd, &exitCode)

	metadata := waitForPersistedSessionState(t, persistence, created.ID, store.SessionExited)
	if metadata.EndedAt == nil || !metadata.EndedAt.Equal(actualEnd) || metadata.ExitCode == nil || *metadata.ExitCode != int32(exitCode) {
		t.Fatalf("persisted final metadata = %+v; want ended=%v exit=%d", metadata, actualEnd, exitCode)
	}
	final := waitForRegistryFinalStatus(t, registry, created.ID)
	if final.State != SessionExited || !final.EndedAt.Equal(actualEnd) || final.ExitCode == nil || *final.ExitCode != exitCode {
		t.Fatalf("registry final status = %+v", final)
	}
	listed, err := registry.List(context.Background())
	if err != nil {
		t.Fatalf("list while viewer retirement is pending: %v", err)
	}
	if len(listed) != 1 || listed[0].State != SessionExited || !listed[0].EndedAt.Equal(actualEnd) {
		t.Fatalf("listed final status = %+v", listed)
	}
	if managed.closed.Load() {
		t.Fatal("runtime closed before final viewer retirement barrier")
	}
	if current, err := registry.Get(created.ID); err != nil || current != managed {
		t.Fatalf("session retired before viewer barrier: session=%T err=%v", current, err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("viewer-retirement-capacity"), Title: "must wait",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("capacity released before viewer retirement: %v", err)
	}

	managed.releaseRetirement()
	waitForRetiredRegistrySession(t, registry, created.ID)
	if !managed.closed.Load() {
		t.Fatal("runtime was not closed after final viewer barrier")
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("viewer-retirement-after"), Title: "after retirement",
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	}); err != nil {
		t.Fatalf("capacity was not released after viewer retirement: %v", err)
	}
}

func TestRegistryViewerBarrierCleanupErrorRemainsObservableOnClose(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{})
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("viewer-cleanup-error")})
	if err != nil {
		t.Fatal(err)
	}
	managed := created.Session.(*fakeManagedSession)
	managed.holdRetirement()
	cleanupErr := errors.New("viewer barrier cleanup failure")
	managed.closeErr = cleanupErr
	t.Cleanup(func() {
		managed.releaseRetirement()
		managed.doneOnce.Do(func() { close(managed.done) })
		_ = registry.Close()
	})

	managed.finish(SessionLost, time.Now().UTC(), nil)
	waitForRegistryFinalStatus(t, registry, created.ID)
	if managed.closed.Load() {
		t.Fatal("cleanup started before viewer retirement barrier")
	}
	managed.releaseRetirement()
	waitForFakeSessionClose(t, managed)
	managed.doneOnce.Do(func() { close(managed.done) })
	waitForRetiredRegistrySession(t, registry, created.ID)
	if err := registry.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("registry close error after retirement = %v, want cleanup error", err)
	}
}

func TestRegistryReceiptTerminatePersistsFinalStateBeforeViewerRetirement(t *testing.T) {
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "terminate-viewer-retirement.sqlite"))
	seedRegistryStore(t, persistence)
	var starts atomic.Int32
	registry := newPersistentFakeRegistry(t, persistence, &starts, []string{
		"30000000-0000-4000-8000-000000000016",
		"40000000-0000-4000-8000-000000000016",
	})
	created := createPersistentTestSession(t, registry, "terminate-viewer-create")
	managed := created.Session.(*fakeManagedSession)
	managed.holdRetirement()
	t.Cleanup(func() {
		managed.releaseRetirement()
		_ = registry.Close()
		_ = persistence.Close()
	})

	request := TerminateSessionRequest{
		OperationID: operationID("terminate-viewer"), SessionID: created.ID,
		Generation: created.Generation,
	}
	accepted, err := registry.Terminate(context.Background(), request)
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if accepted.State != SessionExiting || managed.terminateCalls.Load() != 1 {
		t.Fatalf("terminate acceptance = %+v, runtime calls=%d", accepted, managed.terminateCalls.Load())
	}
	managed.mu.Lock()
	actualEnd := managed.status.StartedAt.Add(3 * time.Second).Truncate(time.Millisecond)
	managed.mu.Unlock()
	exitCode := 0
	managed.finish(SessionExited, actualEnd, &exitCode)
	metadata := waitForPersistedSessionState(t, persistence, created.ID, store.SessionExited)
	if metadata.EndedAt == nil || !metadata.EndedAt.Equal(actualEnd) {
		t.Fatalf("persisted termination final state = %+v; want ended=%v", metadata, actualEnd)
	}
	waitForRegistryFinalStatus(t, registry, created.ID)
	replayed, err := registry.Terminate(context.Background(), request)
	if err != nil || replayed != accepted {
		t.Fatalf("termination replay = %+v, err=%v; want %+v", replayed, err, accepted)
	}
	if managed.terminateCalls.Load() != 1 {
		t.Fatalf("runtime terminate calls after replay = %d, want 1", managed.terminateCalls.Load())
	}
	if managed.closed.Load() {
		t.Fatal("terminated runtime closed before final viewer retirement barrier")
	}
	listed, err := registry.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].State != SessionExited {
		t.Fatalf("list after termination final state = %+v, err=%v", listed, err)
	}
	managed.releaseRetirement()
	waitForRetiredRegistrySession(t, registry, created.ID)
	if !managed.closed.Load() {
		t.Fatal("terminated runtime was not closed after final viewer barrier")
	}
}

func TestRegistryCloseReleasesPendingViewerRetirementBarrier(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{})
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("close-viewer-barrier")})
	if err != nil {
		t.Fatal(err)
	}
	managed := created.Session.(*fakeManagedSession)
	managed.holdRetirement()
	managed.finish(SessionExited, time.Now().UTC(), nil)
	waitForRegistryFinalStatus(t, registry, created.ID)
	select {
	case <-managed.RetirementReady():
		t.Fatal("viewer retirement barrier was not held")
	default:
	}

	closed := make(chan error, 1)
	go func() { closed <- registry.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("registry close: %v", err)
		}
	case <-time.After(time.Second):
		managed.releaseRetirement()
		t.Fatal("registry close deadlocked on pending viewer retirement")
	}
	if !managed.closed.Load() {
		t.Fatal("registry close did not close runtime")
	}
	select {
	case <-managed.RetirementReady():
	default:
		t.Fatal("runtime close did not release viewer retirement barrier")
	}
	select {
	case <-managed.Done():
	default:
		t.Fatal("runtime cleanup did not complete")
	}
	registry.mu.Lock()
	_, stillActive := registry.sessions[created.ID]
	_, retired := registry.retired[created.ID]
	registry.mu.Unlock()
	if stillActive || !retired {
		t.Fatalf("registry close retirement state: active=%t retired=%t", stillActive, retired)
	}
}

func TestRegistryReleasesCapacityAfterDelayedCleanupCompletes(t *testing.T) {
	cleanupErr := errors.New("temporary cleanup failure")
	var first *fakeManagedSession
	registry := newFakeRegistry(t, RegistryConfig{
		MaxLiveSessions: 1,
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			session := newFakeManagedSession(config.ID, config.Generation, SessionRunning)
			if first == nil {
				first = session
				session.closeErr = cleanupErr
			}
			return session, nil
		},
	})
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("first")})
	if err != nil {
		t.Fatal(err)
	}
	first.setState(SessionLost)
	deadline := time.Now().Add(time.Second)
	for !first.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("still-counted")}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("create before definitive cleanup = %v", err)
	}
	first.doneOnce.Do(func() { close(first.done) })
	waitForRetiredRegistrySession(t, registry, created.ID)
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("after-cleanup")}); err != nil {
		t.Fatalf("create after definitive cleanup: %v", err)
	}
	if err := registry.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("registry close did not preserve cleanup failure: %v", err)
	}
}

func TestRegistryShutdownReportsLateStartCleanupFailure(t *testing.T) {
	started := make(chan struct{})
	cleanupErr := errors.New("synthetic cleanup failure")
	session := newFakeManagedSession("late", "late-generation", SessionRunning)
	session.closeErr = cleanupErr
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(ctx context.Context, _ SessionConfig) (registrySession, error) {
			close(started)
			<-ctx.Done()
			return session, nil
		},
	})
	created := make(chan error, 1)
	go func() {
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("late-error")})
		created <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- registry.Close() }()
	if err := <-created; !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("racing create error = %v", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("registry closed before definitive cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	session.doneOnce.Do(func() { close(session.done) })
	if err := <-closed; !errors.Is(err, cleanupErr) {
		t.Fatalf("registry close error = %v", err)
	}
}

func TestRegistryConcurrentCloseWaitsForSameResult(t *testing.T) {
	gate := make(chan struct{})
	cleanupErr := errors.New("blocked cleanup failure")
	var managed *fakeManagedSession
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			session := newFakeManagedSession(config.ID, config.Generation, SessionRunning)
			session.closeGate = gate
			session.closeErr = cleanupErr
			managed = session
			return session, nil
		},
	})
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("close-test")}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- registry.Close() }()
	go func() { results <- registry.Close() }()
	select {
	case err := <-results:
		t.Fatalf("Close returned before child cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(gate)
	managed.doneOnce.Do(func() { close(managed.done) })
	for range 2 {
		if err := <-results; !errors.Is(err, cleanupErr) {
			t.Fatalf("concurrent Close error = %v", err)
		}
	}
}

func TestRegistryCloseRunningRealSessionSucceeds(t *testing.T) {
	fixture := buildSessionFixture(t)
	registry, err := NewRegistry(context.Background(), RegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("shutdown-running"),
		Process: ptyhost.Spec{
			Executable: fixture,
			Env:        []string{"PTERM_PTY_FIXTURE_MODE=sleep"},
			Columns:    80,
			Rows:       24,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("normal running-session shutdown: %v", err)
	}
}

func TestRegistryListSurvivesConcurrentRetirement(t *testing.T) {
	var managed *fakeManagedSession
	registry := newFakeRegistry(t, RegistryConfig{
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			managed = newFakeManagedSession(config.ID, config.Generation, SessionRunning)
			return managed, nil
		},
	})
	defer registry.Close()
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID("list-race")})
	if err != nil {
		t.Fatal(err)
	}
	managed.statusStarted = make(chan struct{})
	managed.statusRelease = make(chan struct{})
	managed.statusErr = ErrSessionClosed
	listed := make(chan []SessionStatus, 1)
	listErr := make(chan error, 1)
	go func() {
		statuses, err := registry.List(context.Background())
		listed <- statuses
		listErr <- err
	}()
	<-managed.statusStarted
	managed.setState(SessionExited)
	waitForRetiredRegistrySession(t, registry, created.ID)
	close(managed.statusRelease)
	if err := <-listErr; err != nil {
		t.Fatal(err)
	}
	statuses := <-listed
	if len(statuses) != 1 || statuses[0].ID != created.ID || statuses[0].State != SessionExited {
		t.Fatalf("list during retirement = %#v", statuses)
	}
}

func TestRegistryBoundsRetiredSessionRecords(t *testing.T) {
	registry := newFakeRegistry(t, RegistryConfig{MaxLiveSessions: 1, RetainedSessionRecords: 1})
	defer registry.Close()
	for index, operation := range []string{"first", "second"} {
		session, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operationID(operation)})
		if err != nil {
			t.Fatal(err)
		}
		status, _ := session.Session.Status(context.Background())
		session.Session.(*fakeManagedSession).setState(SessionExited)
		deadline := time.Now().Add(time.Second)
		for {
			_, getErr := registry.Get(status.ID)
			if errors.Is(getErr, ErrSessionRetired) || (index == 1 && errors.Is(getErr, ErrSessionMissing)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("session %d was not retired: %v", index, getErr)
			}
			time.Sleep(time.Millisecond)
		}
	}
	statuses, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("retained statuses = %d, want 1", len(statuses))
	}
}

func TestRegistryRetiresRealExitedSession(t *testing.T) {
	fixture := buildSessionFixture(t)
	registry, err := NewRegistry(context.Background(), RegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID("real-exit"),
		Process: ptyhost.Spec{
			Executable: fixture,
			Env:        []string{"PTERM_PTY_FIXTURE_MODE=query"},
			Columns:    80,
			Rows:       24,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := session.Session.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		_, getErr := registry.Get(status.ID)
		if errors.Is(getErr, ErrSessionRetired) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real session was not retired: %v", getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	statuses, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != SessionExited || statuses[0].ExitCode == nil || *statuses[0].ExitCode != 0 {
		t.Fatalf("retired real status = %#v", statuses)
	}
}

func TestRegistryConfigurationValidation(t *testing.T) {
	if _, err := NewRegistry(nil, RegistryConfig{}); err == nil {
		t.Fatal("nil context was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRegistry(cancelled, RegistryConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	if _, err := NewRegistry(context.Background(), RegistryConfig{MaxLiveSessions: MaxLiveSessions + 1}); err == nil {
		t.Fatal("oversized live limit was accepted")
	}
	registry := newFakeRegistry(t, RegistryConfig{})
	defer registry.Close()
	cancelledList, cancelList := context.WithCancel(context.Background())
	cancelList()
	if _, err := registry.List(cancelledList); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error = %v", err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{}); !errors.Is(err, ErrOperationID) {
		t.Fatalf("empty operation ID error = %v", err)
	}
	for _, invalid := range []string{
		"named-operation",
		"90000000-0000-4000-8000-00000000000A",
		"90000000000040008000000000000000",
		"90000000-0000-0000-8000-000000000001",
	} {
		if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: invalid}); !errors.Is(err, ErrOperationID) {
			t.Fatalf("invalid operation ID %q error = %v", invalid, err)
		}
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func operationID(label string) string {
	value := sha256.Sum256([]byte(label))
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func newFakeRegistry(t *testing.T, config RegistryConfig) *Registry {
	t.Helper()
	if config.start == nil {
		config.start = func(_ context.Context, sessionConfig SessionConfig) (registrySession, error) {
			return newFakeManagedSession(sessionConfig.ID, sessionConfig.Generation, SessionRunning), nil
		}
	}
	registry, err := NewRegistry(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func openRegistryStore(t *testing.T, path string) *store.Store {
	t.Helper()
	persistence, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open persistence store: %v", err)
	}
	return persistence
}

func markMutationResultUnavailable(t *testing.T, path, operationID string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open receipt database: %v", err)
	}
	defer database.Close()
	result, err := database.ExecContext(context.Background(), `UPDATE mutation_receipts
		SET result_session_json = ? WHERE operation_id = ?`, legacyUnavailableReceiptMarker, operationID)
	if err != nil {
		t.Fatalf("mark mutation result unavailable: %v", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		t.Fatalf("marked mutation results = %d, err=%v; want 1", updated, err)
	}
}

func seedRegistryStore(t *testing.T, persistence *store.Store) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	if _, err := persistence.BeginBrokerGeneration(context.Background(), registryBrokerGeneration, now); err != nil {
		t.Fatalf("begin broker generation: %v", err)
	}
	if err := persistence.PutProject(context.Background(), store.Project{
		ID: registryProjectID, DisplayName: "project", CanonicalPath: "/tmp/project",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("put project: %v", err)
	}
	if err := persistence.PutShellProfile(context.Background(), store.ShellProfile{
		ID: registryProfileID, DisplayName: "shell", Platform: store.PlatformDarwin,
		ExecutablePath: "/bin/zsh", Arguments: []string{"-l"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("put shell profile: %v", err)
	}
}

func newPersistentFakeRegistry(
	t *testing.T,
	persistence *store.Store,
	starts *atomic.Int32,
	ids []string,
) *Registry {
	t.Helper()
	var idMu sync.Mutex
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		Store: persistence, BrokerGeneration: registryBrokerGeneration,
		newID: func() (string, error) {
			idMu.Lock()
			defer idMu.Unlock()
			if len(ids) == 0 {
				return "", errors.New("test ID sequence exhausted")
			}
			id := ids[0]
			ids = ids[1:]
			return id, nil
		},
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			starts.Add(1)
			return newFakeManagedSession(config.ID, config.Generation, SessionRunning), nil
		},
	})
	if err != nil {
		t.Fatalf("new persistent registry: %v", err)
	}
	return registry
}

func createPersistentTestSession(t *testing.T, registry *Registry, label string) CreateSessionResult {
	t.Helper()
	created, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: operationID(label), Title: label,
		ProjectID: registryProjectID, ShellProfileID: registryProfileID,
		Process: ptyhost.Spec{Executable: "/bin/zsh", Columns: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("create persistent session: %v", err)
	}
	return created
}

func waitForRegistryClosed(t *testing.T, registry *Registry) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		closed := registry.closed
		registry.mu.Unlock()
		if closed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("registry did not enter closed state")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForRetiredRegistrySession(t *testing.T, registry *Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := registry.Get(id)
		if errors.Is(err, ErrSessionRetired) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s was not retired: %v", id, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForPersistedSessionState(
	t *testing.T,
	persistence *store.Store,
	id string,
	state store.SessionState,
) store.SessionMetadata {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		metadata, err := persistence.GetSession(context.Background(), id)
		if err == nil && metadata.State == state {
			return metadata
		}
		if time.Now().After(deadline) {
			t.Fatalf("persisted session %s did not reach %s: metadata=%+v err=%v", id, state, metadata, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForRegistryFinalStatus(t *testing.T, registry *Registry, id string) SessionStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		entry := registry.sessions[id]
		var final *SessionStatus
		if entry != nil && entry.final != nil {
			cloned := cloneSessionStatus(*entry.final)
			final = &cloned
		}
		registry.mu.Unlock()
		if final != nil {
			return *final
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry session %s did not publish final status", id)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForFakeSessionClose(t *testing.T, session *fakeManagedSession) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !session.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("fake session close did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

type doneObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *doneObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

type fakeManagedSession struct {
	mu                  sync.Mutex
	status              SessionStatus
	closed              atomic.Bool
	ended               chan struct{}
	endedOnce           sync.Once
	retirementReady     chan struct{}
	retirementOnce      sync.Once
	automaticRetirement bool
	done                chan struct{}
	doneOnce            sync.Once
	closeGate           <-chan struct{}
	closeErr            error
	statusStarted       chan struct{}
	statusRelease       chan struct{}
	statusOnce          sync.Once
	statusErr           error
	terminateCalls      atomic.Int32
	terminateErr        error
	terminateGate       chan struct{}
	terminateStarted    chan struct{}
	terminateOnce       sync.Once
}

func newFakeManagedSession(id, generation string, state SessionState) *fakeManagedSession {
	session := &fakeManagedSession{
		status: SessionStatus{
			ID: id, Generation: generation, State: state,
			Columns: 80, Rows: 24, StartedAt: time.Now().UTC(),
		},
		ended:               make(chan struct{}),
		retirementReady:     make(chan struct{}),
		automaticRetirement: true,
		done:                make(chan struct{}),
	}
	if state == SessionExited || state == SessionLost {
		session.endedOnce.Do(func() { close(session.ended) })
		session.releaseRetirement()
	}
	return session
}

func (session *fakeManagedSession) setState(state SessionState) {
	session.mu.Lock()
	session.status.State = state
	automaticRetirement := session.automaticRetirement
	session.mu.Unlock()
	if state == SessionExited || state == SessionLost {
		session.endedOnce.Do(func() { close(session.ended) })
		if automaticRetirement {
			session.releaseRetirement()
		}
	}
}

func (session *fakeManagedSession) finish(state SessionState, endedAt time.Time, exitCode *int) {
	session.mu.Lock()
	session.status.State = state
	session.status.EndedAt = endedAt
	if exitCode == nil {
		session.status.ExitCode = nil
	} else {
		clonedExitCode := *exitCode
		session.status.ExitCode = &clonedExitCode
	}
	automaticRetirement := session.automaticRetirement
	session.mu.Unlock()
	session.endedOnce.Do(func() { close(session.ended) })
	if automaticRetirement {
		session.releaseRetirement()
	}
}

func (session *fakeManagedSession) holdRetirement() {
	session.mu.Lock()
	session.automaticRetirement = false
	session.mu.Unlock()
}

func (session *fakeManagedSession) releaseRetirement() {
	session.retirementOnce.Do(func() { close(session.retirementReady) })
}

func (session *fakeManagedSession) Ended() <-chan struct{} { return session.ended }
func (session *fakeManagedSession) RetirementReady() <-chan struct{} {
	return session.retirementReady
}
func (session *fakeManagedSession) Done() <-chan struct{} { return session.done }
func (session *fakeManagedSession) FinalStatus() (SessionStatus, bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status.State != SessionExited && session.status.State != SessionLost {
		return SessionStatus{}, false
	}
	return cloneSessionStatus(session.status), true
}

func (session *fakeManagedSession) AcquireControl(context.Context, *ViewerSubscription) (ControlGrant, error) {
	return ControlGrant{}, nil
}
func (session *fakeManagedSession) AcquireControlForRequest(context.Context, *ViewerSubscription, string) (ControlGrant, error) {
	return ControlGrant{}, errors.New("fake correlated grant is not implemented")
}
func (session *fakeManagedSession) AttachViewer(context.Context, ViewerConfig) (*ViewerSubscription, error) {
	return nil, errors.New("fake viewer transport is not implemented")
}
func (session *fakeManagedSession) Heartbeat(context.Context, MutationIdentity) (ControlState, error) {
	return ControlState{}, nil
}
func (session *fakeManagedSession) ReleaseControl(context.Context, MutationIdentity) error {
	return nil
}
func (session *fakeManagedSession) SubmitInput(context.Context, MutationIdentity, uint64, InputAction) (InputAcceptance, error) {
	return InputAcceptance{}, nil
}
func (session *fakeManagedSession) Resize(context.Context, MutationIdentity, int, int) error {
	return nil
}
func (session *fakeManagedSession) Snapshot(context.Context) (terminal.Frame, error) {
	return terminal.Frame{}, nil
}
func (session *fakeManagedSession) ReadHistory(context.Context, string, terminal.HistoryReadRequest) (terminal.HistoryReadResult, error) {
	return terminal.HistoryReadResult{}, errors.New("fake history transport is not implemented")
}
func (session *fakeManagedSession) Status(context.Context) (SessionStatus, error) {
	if session.statusStarted != nil {
		session.statusOnce.Do(func() { close(session.statusStarted) })
		<-session.statusRelease
	}
	if session.statusErr != nil {
		return SessionStatus{}, session.statusErr
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return cloneSessionStatus(session.status), nil
}
func (session *fakeManagedSession) Presence(context.Context, bool) (SessionPresence, error) {
	return SessionPresence{}, nil
}
func (session *fakeManagedSession) Terminate(context.Context) error {
	session.terminateCalls.Add(1)
	if session.terminateStarted != nil {
		session.terminateOnce.Do(func() { close(session.terminateStarted) })
	}
	if session.terminateGate != nil {
		<-session.terminateGate
	}
	return session.terminateErr
}
func (session *fakeManagedSession) RevokeDevice(context.Context, string) (int, error) {
	return 0, nil
}
func (session *fakeManagedSession) PreviewTarget(context.Context, uint16) (string, error) {
	return "", ErrPreviewPort
}
func (session *fakeManagedSession) Close() error {
	session.closed.Store(true)
	session.mu.Lock()
	state := session.status.State
	session.mu.Unlock()
	if state != SessionExited && state != SessionLost {
		session.setState(SessionLost)
	}
	// Real Session.Close starts shutdown, which detaches pending viewers and
	// consequently releases RetirementReady. Mirror that contract so registry
	// shutdown tests cannot manufacture a viewer-barrier deadlock.
	session.releaseRetirement()
	if session.closeGate != nil {
		<-session.closeGate
	}
	if session.closeErr == nil {
		session.doneOnce.Do(func() { close(session.done) })
	}
	return session.closeErr
}

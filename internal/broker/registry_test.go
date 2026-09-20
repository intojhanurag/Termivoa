package broker

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
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

	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "create-one", Process: ptyhost.Spec{Executable: "shell-one"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "create-two", Process: ptyhost.Spec{Executable: "shell-two"}})
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
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "first"})
		created <- err
	}()
	<-started
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "second"}); !errors.Is(err, ErrSessionLimit) {
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

	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "blocked"}); !errors.Is(err, ErrSessionLimit) {
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
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "after-exit"}); err != nil {
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
	if _, err := registry.Create(requestContext, CreateSessionRequest{OperationID: "request-owned"}); err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	select {
	case <-receivedContext.Done():
		t.Fatal("request cancellation reached the managed session lifetime")
	default:
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "second"}); err != nil {
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
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "after-close"}); !errors.Is(err, ErrRegistryClosed) {
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
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "late"})
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
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "first-fails"}); err == nil {
		t.Fatal("expected first start to fail")
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "second-works"}); err != nil {
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
	first, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "second"})
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
			session, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "same-operation"})
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
	request := CreateSessionRequest{OperationID: "retired-retry", Process: ptyhost.Spec{Executable: "shell"}}
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
		OperationID: "conflict", Process: ptyhost.Spec{Executable: "first-shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{
		OperationID: "conflict", Process: ptyhost.Spec{Executable: "different-shell"},
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("conflicting operation error = %v", err)
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
	defer registry.Close()
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	first.setState(SessionLost)
	deadline := time.Now().Add(time.Second)
	for !first.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "still-counted"}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("create before definitive cleanup = %v", err)
	}
	first.doneOnce.Do(func() { close(first.done) })
	waitForRetiredRegistrySession(t, registry, created.ID)
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "after-cleanup"}); err != nil {
		t.Fatalf("create after definitive cleanup: %v", err)
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
		_, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "late-error"})
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
	if _, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "close-test"}); err != nil {
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
		OperationID: "shutdown-running",
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
	created, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: "list-race"})
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
		session, err := registry.Create(context.Background(), CreateSessionRequest{OperationID: operation})
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
		OperationID: "real-exit",
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
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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

type fakeManagedSession struct {
	mu            sync.Mutex
	status        SessionStatus
	closed        atomic.Bool
	ended         chan struct{}
	endedOnce     sync.Once
	done          chan struct{}
	doneOnce      sync.Once
	closeGate     <-chan struct{}
	closeErr      error
	statusStarted chan struct{}
	statusRelease chan struct{}
	statusOnce    sync.Once
	statusErr     error
}

func newFakeManagedSession(id, generation string, state SessionState) *fakeManagedSession {
	session := &fakeManagedSession{
		status: SessionStatus{ID: id, Generation: generation, State: state, StartedAt: time.Now()},
		ended:  make(chan struct{}),
		done:   make(chan struct{}),
	}
	if state == SessionExited || state == SessionLost {
		session.endedOnce.Do(func() { close(session.ended) })
	}
	return session
}

func (session *fakeManagedSession) setState(state SessionState) {
	session.mu.Lock()
	session.status.State = state
	session.mu.Unlock()
	if state == SessionExited || state == SessionLost {
		session.endedOnce.Do(func() { close(session.ended) })
	}
}

func (session *fakeManagedSession) Ended() <-chan struct{} { return session.ended }
func (session *fakeManagedSession) Done() <-chan struct{}  { return session.done }
func (session *fakeManagedSession) FinalStatus() (SessionStatus, bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status.State != SessionExited && session.status.State != SessionLost {
		return SessionStatus{}, false
	}
	return cloneSessionStatus(session.status), true
}

func (session *fakeManagedSession) AcquireControl(context.Context, string, string, string) (ControlGrant, error) {
	return ControlGrant{}, nil
}
func (session *fakeManagedSession) Heartbeat(context.Context, MutationIdentity) (ControlState, error) {
	return ControlState{}, nil
}
func (session *fakeManagedSession) ReleaseControl(context.Context, MutationIdentity) error {
	return nil
}
func (session *fakeManagedSession) Disconnect(context.Context, string) error { return nil }
func (session *fakeManagedSession) SubmitInput(context.Context, MutationIdentity, uint64, InputAction) (InputAcceptance, error) {
	return InputAcceptance{}, nil
}
func (session *fakeManagedSession) Resize(context.Context, MutationIdentity, int, int) error {
	return nil
}
func (session *fakeManagedSession) Snapshot(context.Context) (terminal.Frame, error) {
	return terminal.Frame{}, nil
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
func (session *fakeManagedSession) Terminate(context.Context) error { return nil }
func (session *fakeManagedSession) RevokeDevice(context.Context, string) (int, error) {
	return 0, nil
}
func (session *fakeManagedSession) Close() error {
	session.closed.Store(true)
	session.mu.Lock()
	state := session.status.State
	session.mu.Unlock()
	if state != SessionExited && state != SessionLost {
		session.setState(SessionLost)
	}
	if session.closeGate != nil {
		<-session.closeGate
	}
	if session.closeErr == nil {
		session.doneOnce.Do(func() { close(session.done) })
	}
	return session.closeErr
}

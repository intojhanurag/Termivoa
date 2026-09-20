package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	DefaultMaxLiveSessions        = 8
	MaxLiveSessions               = 64
	DefaultRetainedSessionRecords = 256
	MaxRetainedSessionRecords     = 4096
	DefaultCreateOperationHistory = 1024
	MaxCreateOperationHistory     = 16384
)

var (
	ErrRegistryClosed    = errors.New("broker: session registry is closed")
	ErrSessionLimit      = errors.New("broker: live session limit reached")
	ErrSessionMissing    = errors.New("broker: session does not exist")
	ErrOperationID       = errors.New("broker: invalid operation ID")
	ErrOperationConflict = errors.New("broker: operation ID was already used for a different request")
	ErrOperationLimit    = errors.New("broker: durable operation receipt capacity reached")
	ErrSessionRetired    = errors.New("broker: session process is no longer live")
	ErrPersistenceNeeded = errors.New("broker: durable session mutation store is required")
)

// ManagedSession is the capability exposed to local IPC and the gateway. It
// intentionally omits Close so a viewer cannot end the broker-owned lifetime.
type ManagedSession interface {
	AttachViewer(context.Context, ViewerConfig) (*ViewerSubscription, error)
	AcquireControl(context.Context, *ViewerSubscription) (ControlGrant, error)
	Heartbeat(context.Context, MutationIdentity) (ControlState, error)
	ReleaseControl(context.Context, MutationIdentity) error
	SubmitInput(context.Context, MutationIdentity, uint64, InputAction) (InputAcceptance, error)
	Resize(context.Context, MutationIdentity, int, int) error
	Snapshot(context.Context) (terminal.Frame, error)
	Status(context.Context) (SessionStatus, error)
	Terminate(context.Context) error
	RevokeDevice(context.Context, string) (int, error)
}

type registrySession interface {
	ManagedSession
	Ended() <-chan struct{}
	RetirementReady() <-chan struct{}
	Done() <-chan struct{}
	FinalStatus() (SessionStatus, bool)
	Close() error
}

type RegistryConfig struct {
	MaxLiveSessions        int
	RetainedSessionRecords int
	CreateOperationHistory int
	InputQueueCapacity     int
	PTYOutputQueue         int

	start func(context.Context, SessionConfig) (registrySession, error)
	newID func() (string, error)
	now   func() time.Time

	// afterCreateLookup is a deterministic test seam for the boundary between
	// a durable receipt lookup and returning its result to an admitted caller.
	afterCreateLookup func()

	Store            *store.Store
	BrokerGeneration string
}

// CreateSessionRequest is built only after the broker resolves trusted local
// project and shell-profile IDs. Network clients must never populate Process.
type CreateSessionRequest struct {
	OperationID    string
	Title          string
	ProjectID      string
	ShellProfileID string
	Process        ptyhost.Spec
}

// CreateSessionResult is safe to retain for operation deduplication. Session
// is present only while the live runtime is still registry-owned.
type CreateSessionResult struct {
	ID         string
	Generation string
	Session    ManagedSession
}

type RenameSessionRequest struct {
	OperationID string
	SessionID   string
	Generation  string
	Title       string
}

type RenameSessionResult struct {
	ID          string
	Generation  string
	Title       string
	CommittedAt time.Time
}

type TerminateSessionRequest struct {
	OperationID string
	SessionID   string
	Generation  string
}

type TerminateSessionResult struct {
	ID          string
	Generation  string
	State       SessionState
	CommittedAt time.Time
}

type registryEntry struct {
	session registrySession
	final   *SessionStatus
}

type createOperation struct {
	done        chan struct{}
	fingerprint [sha256.Size]byte
	result      CreateSessionResult
	err         error
}

// Registry owns all managed sessions for one broker generation.
type Registry struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu             sync.Mutex
	config         RegistryConfig
	sessions       map[string]*registryEntry
	reserved       map[string]struct{}
	retired        map[string]SessionStatus
	retiredOrder   []string
	operations     map[string]*createOperation
	operationOrder []string
	pending        int
	closed         bool
	creates        sync.WaitGroup
	starts         sync.WaitGroup
	mutations      sync.WaitGroup
	cleanups       sync.WaitGroup
	closeDone      chan struct{}
	closeErr       error
	lifecycleErrs  []error
}

func NewRegistry(ctx context.Context, config RegistryConfig) (*Registry, error) {
	if ctx == nil {
		return nil, errors.New("broker: registry context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("broker: registry context: %w", err)
	}
	if config.MaxLiveSessions == 0 {
		config.MaxLiveSessions = DefaultMaxLiveSessions
	}
	if config.RetainedSessionRecords == 0 {
		config.RetainedSessionRecords = DefaultRetainedSessionRecords
	}
	if config.CreateOperationHistory == 0 {
		config.CreateOperationHistory = DefaultCreateOperationHistory
	}
	if config.MaxLiveSessions < 1 || config.MaxLiveSessions > MaxLiveSessions {
		return nil, fmt.Errorf("broker: live session limit must be between 1 and %d", MaxLiveSessions)
	}
	if config.RetainedSessionRecords < config.MaxLiveSessions || config.RetainedSessionRecords > MaxRetainedSessionRecords {
		return nil, fmt.Errorf("broker: retained session records must be between %d and %d", config.MaxLiveSessions, MaxRetainedSessionRecords)
	}
	if config.CreateOperationHistory < 1 || config.CreateOperationHistory > MaxCreateOperationHistory {
		return nil, fmt.Errorf("broker: create operation history must be between 1 and %d", MaxCreateOperationHistory)
	}
	if config.start == nil {
		config.start = func(ctx context.Context, config SessionConfig) (registrySession, error) {
			return StartSession(ctx, config)
		}
	}
	if config.newID == nil {
		config.newID = randomUUID
	}
	if config.now == nil {
		config.now = time.Now
	}
	if (config.Store == nil) != (config.BrokerGeneration == "") {
		return nil, errors.New("broker: persistence store and broker generation must be configured together")
	}
	if config.Store != nil && !validOperationUUID(config.BrokerGeneration) {
		return nil, errors.New("broker: broker generation must be a canonical UUID")
	}
	registryContext, cancel := context.WithCancel(ctx)
	return &Registry{
		ctx: registryContext, cancel: cancel, config: config,
		sessions: make(map[string]*registryEntry), reserved: make(map[string]struct{}),
		retired: make(map[string]SessionStatus), operations: make(map[string]*createOperation),
		closeDone: make(chan struct{}),
	}, nil
}

// Create deduplicates the operation before reserving capacity. Once accepted,
// request cancellation cannot become process-lifetime cancellation.
func (registry *Registry) Create(ctx context.Context, request CreateSessionRequest) (CreateSessionResult, error) {
	if ctx == nil {
		return CreateSessionResult{}, errors.New("broker: create context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return CreateSessionResult{}, err
	}
	if err := validateOperationID(request.OperationID); err != nil {
		return CreateSessionResult{}, err
	}
	fingerprint, err := createRequestFingerprint(request)
	if err != nil {
		return CreateSessionResult{}, err
	}
	operation, owner, err := registry.beginOperation(request.OperationID, fingerprint)
	if err != nil {
		return CreateSessionResult{}, err
	}
	defer registry.creates.Done()
	if !owner {
		select {
		case <-operation.done:
			return registry.operationResult(operation), operation.err
		case <-ctx.Done():
			return CreateSessionResult{}, ctx.Err()
		}
	}
	result, createErr := registry.createOnce(request)
	registry.finishOperation(request.OperationID, operation, result, createErr)
	return result, createErr
}

func (registry *Registry) createOnce(request CreateSessionRequest) (CreateSessionResult, error) {
	if registry.config.Store != nil {
		fingerprint, err := createRequestFingerprint(request)
		if err != nil {
			return CreateSessionResult{}, err
		}
		persisted, found, err := registry.config.Store.LookupMutation(
			registry.ctx, request.OperationID, store.OperationSessionCreate, fingerprint,
		)
		if err != nil {
			return CreateSessionResult{}, mapStoreOperationError(err)
		}
		if registry.config.afterCreateLookup != nil {
			registry.config.afterCreateLookup()
		}
		if found {
			return registry.persistedCreateResult(persisted), nil
		}
	}
	createdAt := registry.config.now().UTC()
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return CreateSessionResult{}, ErrRegistryClosed
	}
	if len(registry.sessions)+registry.pending >= registry.config.MaxLiveSessions {
		registry.mu.Unlock()
		return CreateSessionResult{}, fmt.Errorf("%w: maximum %d", ErrSessionLimit, registry.config.MaxLiveSessions)
	}
	registry.pending++
	registry.starts.Add(1)
	registry.mu.Unlock()
	defer registry.starts.Done()

	id, err := registry.reserveSessionID()
	if err != nil {
		registry.releasePending("")
		return CreateSessionResult{}, err
	}
	generation, err := registry.config.newID()
	if err != nil {
		registry.releasePending(id)
		return CreateSessionResult{}, fmt.Errorf("broker: create session generation: %w", err)
	}
	session, err := registry.config.start(registry.ctx, SessionConfig{
		ID: id, Generation: generation, Process: request.Process,
		InputQueueCapacity: registry.config.InputQueueCapacity,
		PTYOutputQueue:     registry.config.PTYOutputQueue,
	})
	if err != nil {
		registry.releasePending(id)
		return CreateSessionResult{}, err
	}
	if registry.config.Store != nil {
		status, statusErr := session.Status(context.Background())
		if statusErr != nil {
			registry.releasePending(id)
			return CreateSessionResult{}, errors.Join(statusErr, session.Close())
		}
		startedAt := status.StartedAt.UTC()
		metadata := store.SessionMetadata{
			ID: id, Generation: generation, BrokerGeneration: registry.config.BrokerGeneration,
			Title: request.Title, ProjectID: request.ProjectID, ShellProfileID: request.ShellProfileID,
			State: store.SessionRunning, Columns: status.Columns, Rows: status.Rows,
			CreatedAt: createdAt, UpdatedAt: startedAt, StartedAt: &startedAt,
		}
		fingerprint, fingerprintErr := createRequestFingerprint(request)
		if fingerprintErr != nil {
			registry.releasePending(id)
			return CreateSessionResult{}, errors.Join(fingerprintErr, session.Close())
		}
		persisted, replayed, persistErr := registry.config.Store.CreateSessionWithReceipt(
			registry.ctx, request.OperationID, fingerprint, metadata,
		)
		if persistErr != nil {
			registry.releasePending(id)
			return CreateSessionResult{}, errors.Join(mapStoreOperationError(persistErr), session.Close())
		}
		if replayed {
			registry.releasePending(id)
			if closeErr := session.Close(); closeErr != nil {
				return CreateSessionResult{}, closeErr
			}
			return registry.persistedCreateResult(persisted), nil
		}
	}

	entry := &registryEntry{session: session}
	registry.mu.Lock()
	registry.pending--
	delete(registry.reserved, id)
	registry.sessions[id] = entry
	closed := registry.closed
	registry.cleanups.Add(1)
	registry.mu.Unlock()
	go registry.retireWhenEnded(id, entry)
	if closed {
		return CreateSessionResult{ID: id, Generation: generation}, ErrRegistryClosed
	}
	return CreateSessionResult{ID: id, Generation: generation, Session: session}, nil
}

// Rename durably deduplicates a metadata change. The live runtime is not
// touched because title is broker-owned metadata, not terminal state.
func (registry *Registry) Rename(ctx context.Context, request RenameSessionRequest) (RenameSessionResult, error) {
	if ctx == nil {
		return RenameSessionResult{}, errors.New("broker: rename context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return RenameSessionResult{}, err
	}
	if registry.config.Store == nil {
		return RenameSessionResult{}, ErrPersistenceNeeded
	}
	if err := registry.beginMutation(); err != nil {
		return RenameSessionResult{}, err
	}
	defer registry.mutations.Done()
	if err := validateOperationID(request.OperationID); err != nil {
		return RenameSessionResult{}, err
	}
	fingerprint, err := renameRequestFingerprint(request)
	if err != nil {
		return RenameSessionResult{}, err
	}
	result, _, err := registry.config.Store.RenameSessionWithReceipt(
		ctx, request.OperationID, fingerprint, request.SessionID, request.Generation,
		request.Title, registry.config.now().UTC(),
	)
	if err != nil {
		return RenameSessionResult{}, mapStoreOperationError(err)
	}
	return RenameSessionResult{
		ID: result.SessionID, Generation: result.Generation,
		Title: result.Title, CommittedAt: result.CommittedAt,
	}, nil
}

// Terminate persists acceptance before invoking the process-tree side effect.
// A retry returns the stored acknowledgement and never invokes Terminate twice.
func (registry *Registry) Terminate(ctx context.Context, request TerminateSessionRequest) (TerminateSessionResult, error) {
	if ctx == nil {
		return TerminateSessionResult{}, errors.New("broker: terminate context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return TerminateSessionResult{}, err
	}
	if registry.config.Store == nil {
		return TerminateSessionResult{}, ErrPersistenceNeeded
	}
	if err := registry.beginMutation(); err != nil {
		return TerminateSessionResult{}, err
	}
	defer registry.mutations.Done()
	if err := validateOperationID(request.OperationID); err != nil {
		return TerminateSessionResult{}, err
	}
	fingerprint, err := terminateRequestFingerprint(request)
	if err != nil {
		return TerminateSessionResult{}, err
	}
	persisted, found, err := registry.config.Store.LookupMutation(
		ctx, request.OperationID, store.OperationSessionTerminate, fingerprint,
	)
	if err != nil {
		return TerminateSessionResult{}, mapStoreOperationError(err)
	}
	if found {
		return terminateResult(persisted), nil
	}
	registry.mu.Lock()
	entry, exists := registry.sessions[request.SessionID]
	registry.mu.Unlock()
	if !exists {
		return TerminateSessionResult{}, ErrSessionMissing
	}
	status, err := entry.session.Status(ctx)
	if err != nil {
		return TerminateSessionResult{}, err
	}
	if status.Generation != request.Generation {
		return TerminateSessionResult{}, ErrGenerationChanged
	}
	result, replayed, err := registry.config.Store.BeginSessionTerminationWithReceipt(
		ctx, request.OperationID, fingerprint, request.SessionID, request.Generation,
		registry.config.now().UTC(),
	)
	if err != nil {
		return TerminateSessionResult{}, mapStoreOperationError(err)
	}
	if !replayed {
		// Receipt acceptance is the stable client result. Runtime failure changes
		// the session to lost and is persisted by retireWhenEnded.
		terminateContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		terminateErr := entry.session.Terminate(terminateContext)
		cancel()
		if terminateErr != nil {
			registry.recordLifecycleError(fmt.Errorf("session %s terminate accepted operation %s: %w",
				request.SessionID, request.OperationID, terminateErr))
		}
	}
	return terminateResult(result), nil
}

func (registry *Registry) Get(id string) (ManagedSession, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return nil, ErrRegistryClosed
	}
	if entry, ok := registry.sessions[id]; ok {
		return entry.session, nil
	}
	if _, ok := registry.retired[id]; ok {
		return nil, ErrSessionRetired
	}
	return nil, ErrSessionMissing
}

func (registry *Registry) List(ctx context.Context) ([]SessionStatus, error) {
	if ctx == nil {
		return nil, errors.New("broker: list context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil, ErrRegistryClosed
	}
	entries := make([]*registryEntry, 0, len(registry.sessions))
	for _, entry := range registry.sessions {
		entries = append(entries, entry)
	}
	statuses := make([]SessionStatus, 0, len(entries)+len(registry.retired))
	for _, status := range registry.retired {
		statuses = append(statuses, cloneSessionStatus(status))
	}
	registry.mu.Unlock()

	for _, entry := range entries {
		registry.mu.Lock()
		final := entry.final
		registry.mu.Unlock()
		if final != nil {
			statuses = append(statuses, cloneSessionStatus(*final))
			continue
		}
		status, err := entry.session.Status(ctx)
		if err != nil {
			registry.mu.Lock()
			final = entry.final
			registry.mu.Unlock()
			if final == nil {
				return nil, err
			}
			statuses = append(statuses, cloneSessionStatus(*final))
			continue
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(left, right int) bool {
		if statuses[left].StartedAt.Equal(statuses[right].StartedAt) {
			return statuses[left].ID < statuses[right].ID
		}
		return statuses[left].StartedAt.Before(statuses[right].StartedAt)
	})
	return statuses, nil
}

// Close waits for admitted mutations, create callers, starts, and lifecycle
// cleanup. Every caller observes the same completion and error instead of
// returning while shutdown is still running.
func (registry *Registry) Close() error {
	registry.mu.Lock()
	if registry.closed {
		done := registry.closeDone
		registry.mu.Unlock()
		<-done
		registry.mu.Lock()
		err := registry.closeErr
		registry.mu.Unlock()
		return err
	}
	registry.closed = true
	registry.mu.Unlock()

	registry.mutations.Wait()
	registry.cancel()
	registry.creates.Wait()
	registry.starts.Wait()
	registry.mu.Lock()
	sessions := make([]registrySession, 0, len(registry.sessions))
	for _, entry := range registry.sessions {
		sessions = append(sessions, entry.session)
	}
	registry.mu.Unlock()

	directErrors := make(chan error, len(sessions))
	var wait sync.WaitGroup
	for _, session := range sessions {
		session := session
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := session.Close(); err != nil {
				directErrors <- err
			}
		}()
	}
	wait.Wait()
	close(directErrors)
	registry.cleanups.Wait()

	var closeErrors []error
	for err := range directErrors {
		closeErrors = append(closeErrors, err)
	}
	registry.mu.Lock()
	closeErrors = append(closeErrors, registry.lifecycleErrs...)
	registry.closeErr = errors.Join(closeErrors...)
	close(registry.closeDone)
	err := registry.closeErr
	registry.mu.Unlock()
	return err
}

func (registry *Registry) retireWhenEnded(id string, entry *registryEntry) {
	defer registry.cleanups.Done()
	<-entry.session.Ended()
	status, hasStatus := entry.session.FinalStatus()
	if hasStatus {
		registry.persistFinalStatus(status)
		registry.mu.Lock()
		entry.final = &status
		registry.mu.Unlock()
	}
	<-entry.session.RetirementReady()
	closeErr := entry.session.Close()
	if closeErr != nil {
		<-entry.session.Done()
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !hasStatus {
		registry.lifecycleErrs = append(registry.lifecycleErrs, fmt.Errorf("session %s final status unavailable", id))
	}
	if closeErr != nil {
		registry.lifecycleErrs = append(registry.lifecycleErrs, fmt.Errorf("session %s cleanup: %w", id, closeErr))
	}
	if current, ok := registry.sessions[id]; !ok || current != entry {
		return
	}
	delete(registry.sessions, id)
	if hasStatus {
		registry.retired[id] = cloneSessionStatus(status)
		registry.retiredOrder = append(registry.retiredOrder, id)
		for len(registry.retiredOrder) > registry.config.RetainedSessionRecords {
			oldest := registry.retiredOrder[0]
			registry.retiredOrder = registry.retiredOrder[1:]
			delete(registry.retired, oldest)
		}
	}
}

func (registry *Registry) beginMutation() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return ErrRegistryClosed
	}
	registry.mutations.Add(1)
	return nil
}

func (registry *Registry) recordLifecycleError(err error) {
	if err == nil {
		return
	}
	registry.mu.Lock()
	registry.lifecycleErrs = append(registry.lifecycleErrs, err)
	registry.mu.Unlock()
}

func (registry *Registry) beginOperation(id string, fingerprint [sha256.Size]byte) (*createOperation, bool, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return nil, false, ErrRegistryClosed
	}
	if operation, ok := registry.operations[id]; ok {
		if operation.fingerprint != fingerprint {
			return nil, false, ErrOperationConflict
		}
		registry.creates.Add(1)
		return operation, false, nil
	}
	operation := &createOperation{done: make(chan struct{}), fingerprint: fingerprint}
	registry.operations[id] = operation
	registry.creates.Add(1)
	return operation, true, nil
}

func (registry *Registry) finishOperation(id string, operation *createOperation, result CreateSessionResult, err error) {
	registry.mu.Lock()
	operation.result = CreateSessionResult{ID: result.ID, Generation: result.Generation}
	operation.err = err
	close(operation.done)
	registry.operationOrder = append(registry.operationOrder, id)
	for len(registry.operationOrder) > registry.config.CreateOperationHistory {
		oldest := registry.operationOrder[0]
		registry.operationOrder = registry.operationOrder[1:]
		delete(registry.operations, oldest)
	}
	registry.mu.Unlock()
}

func (registry *Registry) operationResult(operation *createOperation) CreateSessionResult {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := operation.result
	if entry, ok := registry.sessions[result.ID]; ok {
		result.Session = entry.session
	}
	return result
}

func createRequestFingerprint(request CreateSessionRequest) ([sha256.Size]byte, error) {
	process := request.Process
	process.Args = append([]string{}, process.Args...)
	process.Env = append([]string{}, process.Env...)
	payload, err := json.Marshal(struct {
		Title          string   `json:"title"`
		ProjectID      string   `json:"projectId"`
		ShellProfileID string   `json:"shellProfileId"`
		Executable     string   `json:"executable"`
		Arguments      []string `json:"arguments"`
		Directory      string   `json:"directory"`
		Environment    []string `json:"environment"`
		Columns        int      `json:"columns"`
		Rows           int      `json:"rows"`
	}{
		Title: request.Title, ProjectID: request.ProjectID,
		ShellProfileID: request.ShellProfileID,
		Executable:     process.Executable, Arguments: process.Args,
		Directory: process.Directory, Environment: process.Env,
		Columns: process.Columns, Rows: process.Rows,
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("broker: fingerprint create request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func renameRequestFingerprint(request RenameSessionRequest) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		SessionID  string `json:"sessionId"`
		Generation string `json:"generation"`
		Title      string `json:"title"`
	}{SessionID: request.SessionID, Generation: request.Generation, Title: request.Title})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("broker: fingerprint rename request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func terminateRequestFingerprint(request TerminateSessionRequest) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		SessionID  string `json:"sessionId"`
		Generation string `json:"generation"`
	}{SessionID: request.SessionID, Generation: request.Generation})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("broker: fingerprint terminate request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func (registry *Registry) reserveSessionID() (string, error) {
	for attempts := 0; attempts < 4; attempts++ {
		id, err := registry.config.newID()
		if err != nil {
			return "", fmt.Errorf("broker: create session ID: %w", err)
		}
		registry.mu.Lock()
		_, active := registry.sessions[id]
		_, retired := registry.retired[id]
		_, reserved := registry.reserved[id]
		if !active && !retired && !reserved {
			registry.reserved[id] = struct{}{}
		}
		registry.mu.Unlock()
		if !active && !retired && !reserved {
			return id, nil
		}
	}
	return "", errors.New("broker: could not allocate a unique session ID")
}

func (registry *Registry) releasePending(id string) {
	registry.mu.Lock()
	if id != "" {
		delete(registry.reserved, id)
	}
	registry.pending--
	registry.mu.Unlock()
}

func validateOperationID(id string) error {
	if strings.TrimSpace(id) != id || !validOperationUUID(id) {
		return ErrOperationID
	}
	return nil
}

func validOperationUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' ||
		value[14] < '1' || value[14] > '8' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func (registry *Registry) persistedCreateResult(result store.MutationResult) CreateSessionResult {
	created := CreateSessionResult{ID: result.SessionID, Generation: result.Generation}
	registry.mu.Lock()
	if entry, ok := registry.sessions[result.SessionID]; ok {
		created.Session = entry.session
	}
	registry.mu.Unlock()
	return created
}

func terminateResult(result store.MutationResult) TerminateSessionResult {
	return TerminateSessionResult{
		ID: result.SessionID, Generation: result.Generation,
		State: SessionState(result.State), CommittedAt: result.CommittedAt,
	}
}

func mapStoreOperationError(err error) error {
	switch {
	case errors.Is(err, store.ErrOperationConflict):
		return fmt.Errorf("%w: %v", ErrOperationConflict, err)
	case errors.Is(err, store.ErrOperationReceiptLimit):
		return fmt.Errorf("%w: %v", ErrOperationLimit, err)
	case errors.Is(err, store.ErrSessionMissing):
		return fmt.Errorf("%w: %v", ErrSessionMissing, err)
	case errors.Is(err, store.ErrGenerationChanged):
		return fmt.Errorf("%w: %v", ErrGenerationChanged, err)
	default:
		return err
	}
}

func (registry *Registry) persistFinalStatus(status SessionStatus) {
	if registry.config.Store == nil {
		return
	}
	state := store.SessionLost
	var exitCode *int32
	if status.State == SessionExited {
		state = store.SessionExited
		if status.ExitCode != nil {
			value := int32(*status.ExitCode)
			if int(value) == *status.ExitCode {
				exitCode = &value
			}
		}
	}
	when := status.EndedAt
	if when.IsZero() {
		when = registry.config.now().UTC()
	}
	if err := registry.config.Store.TransitionSession(
		context.Background(), status.ID, status.Generation, state, exitCode, when,
	); err != nil {
		registry.mu.Lock()
		registry.lifecycleErrs = append(registry.lifecycleErrs, fmt.Errorf("session %s persist final status: %w", status.ID, err))
		registry.mu.Unlock()
	}
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

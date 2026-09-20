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

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	DefaultMaxLiveSessions        = 8
	MaxLiveSessions               = 64
	DefaultRetainedSessionRecords = 256
	MaxRetainedSessionRecords     = 4096
	DefaultCreateOperationHistory = 1024
	MaxCreateOperationHistory     = 16384
	MaxOperationIDBytes           = 128
)

var (
	ErrRegistryClosed    = errors.New("broker: session registry is closed")
	ErrSessionLimit      = errors.New("broker: live session limit reached")
	ErrSessionMissing    = errors.New("broker: session does not exist")
	ErrOperationID       = errors.New("broker: invalid operation ID")
	ErrOperationConflict = errors.New("broker: operation ID was already used for a different request")
	ErrSessionRetired    = errors.New("broker: session process is no longer live")
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
}

// CreateSessionRequest is built only after the broker resolves trusted local
// project and shell-profile IDs. Network clients must never populate Process.
type CreateSessionRequest struct {
	OperationID string
	Process     ptyhost.Spec
}

// CreateSessionResult is safe to retain for operation deduplication. Session
// is present only while the live runtime is still registry-owned.
type CreateSessionResult struct {
	ID         string
	Generation string
	Session    ManagedSession
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
	starts         sync.WaitGroup
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

// Close waits for starts and lifecycle cleanup. Every caller observes the same
// completion and error instead of returning while shutdown is still running.
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
	registry.cancel()
	registry.mu.Unlock()

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
		registry.mu.Lock()
		entry.final = &status
		registry.mu.Unlock()
	}
	<-entry.session.RetirementReady()
	closeErr := entry.session.Close()
	if closeErr != nil {
		<-entry.session.Done()
		closeErr = nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !hasStatus {
		registry.lifecycleErrs = append(registry.lifecycleErrs, fmt.Errorf("session %s final status unavailable", id))
	}
	if closeErr != nil {
		registry.lifecycleErrs = append(registry.lifecycleErrs, fmt.Errorf("session %s cleanup: %w", id, closeErr))
		return
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
		return operation, false, nil
	}
	operation := &createOperation{done: make(chan struct{}), fingerprint: fingerprint}
	registry.operations[id] = operation
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
	payload, err := json.Marshal(request.Process)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("broker: fingerprint create request: %w", err)
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
	if id == "" || strings.TrimSpace(id) != id || len(id) > MaxOperationIDBytes {
		return ErrOperationID
	}
	for _, character := range id {
		if character < 0x21 || character > 0x7e {
			return ErrOperationID
		}
	}
	return nil
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

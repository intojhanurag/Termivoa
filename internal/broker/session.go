package broker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	DefaultPTYOutputQueue = 128
	MaxPTYOutputQueue     = 1024
	defaultRequestQueue   = 128
	shutdownTimeout       = 3 * time.Second
	processExitGrace      = time.Second
)

var (
	ErrSessionClosed     = errors.New("broker: session is closed")
	ErrSessionNotRunning = errors.New("broker: session is not running")
	ErrSessionShutdown   = errors.New("broker: session shutdown did not complete")
)

// SessionState is the explicit lifecycle of one managed process generation.
type SessionState string

const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionExiting  SessionState = "exiting"
	SessionExited   SessionState = "exited"
	SessionLost     SessionState = "lost"
)

// SessionConfig describes a broker-owned terminal process. ID and Generation
// are opaque UUIDs assigned by the future registry. The supplied context must
// be the broker/session lifetime, never a request or viewer context.
type SessionConfig struct {
	ID                 string
	Generation         string
	Process            ptyhost.Spec
	InputQueueCapacity int
	PTYOutputQueue     int
}

// SessionStatus is an immutable lifecycle snapshot. A nil ExitCode means the
// child has not produced a normal process result or the session was lost.
type SessionStatus struct {
	ID         string
	Generation string
	State      SessionState
	PID        int
	Columns    int
	Rows       int
	StartedAt  time.Time
	EndedAt    time.Time
	ExitCode   *int
}

// MutationIdentity binds a mutating request to the exact controller lease.
type MutationIdentity struct {
	Generation   string
	ConnectionID string
	DeviceID     string
	Lease        string
}

type terminalRuntime interface {
	WriteOutput(context.Context, []byte) error
	Resize(context.Context, int, int) error
	Snapshot(context.Context) (terminal.Frame, error)
	SendText(context.Context, string) error
	Paste(context.Context, string) error
	SendKey(context.Context, uv.KeyEvent) error
	SendMouse(context.Context, uv.MouseEvent) error
	Output() <-chan []byte
	Close() error
}

type processRuntime interface {
	PID() int
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(int, int) error
	Wait(context.Context) error
	Terminate(context.Context) error
	Close() error
}

// Session owns a live PTY and authoritative emulator independently of every
// gateway, HTTP request, WebSocket, and viewer.
type Session struct {
	id         string
	generation string
	process    processRuntime
	terminal   terminalRuntime
	controller *Controller
	inbox      *InputInbox

	ctx                 context.Context
	cancel              context.CancelFunc
	parent              <-chan struct{}
	shutdown            chan struct{}
	shutdownAttemptDone chan struct{}
	requests            chan sessionRequest
	pty                 chan ptyEvent
	waited              chan error
	wrote               chan error
	done                chan struct{}

	closeOnce          sync.Once
	shutdownReportOnce sync.Once
	pumpWG             sync.WaitGroup
	shutdownErr        error
}

type sessionRequestKind uint8

const (
	requestAcquire sessionRequestKind = iota
	requestHeartbeat
	requestRelease
	requestDisconnect
	requestSubmit
	requestResize
	requestSnapshot
	requestStatus
	requestTerminate
	requestRevokeDevice
)

type sessionRequest struct {
	kind       sessionRequestKind
	identity   MutationIdentity
	connection string
	device     string
	action     InputAction
	sequence   uint64
	columns    int
	rows       int
	reply      chan sessionResponse
}

type sessionResponse struct {
	grant      ControlGrant
	control    ControlState
	acceptance InputAcceptance
	frame      terminal.Frame
	status     SessionStatus
	dropped    int
	err        error
}

type ptyEvent struct {
	data []byte
	err  error
}

// StartSession allocates the authoritative terminal, starts the PTY child, and
// launches viewer-independent pumps before returning.
func StartSession(ctx context.Context, config SessionConfig) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("broker: session context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("broker: session context: %w", err)
	}
	if config.ID == "" || config.Generation == "" {
		return nil, errors.New("broker: session ID and generation must not be empty")
	}
	if config.InputQueueCapacity == 0 {
		config.InputQueueCapacity = DefaultInputQueueCapacity
	}
	if config.InputQueueCapacity < 1 || config.InputQueueCapacity > MaxInputQueueCapacity {
		return nil, fmt.Errorf("broker: input queue must be between 1 and %d", MaxInputQueueCapacity)
	}
	if config.PTYOutputQueue == 0 {
		config.PTYOutputQueue = DefaultPTYOutputQueue
	}
	if config.PTYOutputQueue < 1 || config.PTYOutputQueue > MaxPTYOutputQueue {
		return nil, fmt.Errorf("broker: PTY output queue must be between 1 and %d", MaxPTYOutputQueue)
	}

	// The runtime context is canceled only after the actor has terminated and
	// reaped the process, drained ordered PTY output, and closed its handles.
	// Parent/request cancellation is observed by the actor as a shutdown signal
	// rather than being wired directly into xpty or the terminal core.
	sessionContext, cancel := context.WithCancel(context.Background())
	core, err := terminal.New(sessionContext, terminal.Config{
		Columns: config.Process.Columns,
		Rows:    config.Process.Rows,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("broker: create terminal core: %w", err)
	}
	process, err := ptyhost.Start(sessionContext, config.Process)
	if err != nil {
		cancel()
		_ = core.Close()
		return nil, fmt.Errorf("broker: start PTY process: %w", err)
	}
	return startSessionRuntime(sessionContext, cancel, ctx.Done(), config, process, core)
}

func startSessionRuntime(
	ctx context.Context,
	cancel context.CancelFunc,
	parent <-chan struct{},
	config SessionConfig,
	process processRuntime,
	core terminalRuntime,
) (*Session, error) {
	controller, err := NewController(config.Generation)
	if err != nil {
		cancel()
		_ = process.Close()
		_ = core.Close()
		return nil, err
	}
	inbox, err := NewInputInbox(controller, config.InputQueueCapacity)
	if err != nil {
		cancel()
		_ = process.Close()
		_ = core.Close()
		return nil, err
	}
	session := &Session{
		id:                  config.ID,
		generation:          config.Generation,
		process:             process,
		terminal:            core,
		controller:          controller,
		inbox:               inbox,
		ctx:                 ctx,
		cancel:              cancel,
		parent:              parent,
		shutdown:            make(chan struct{}),
		shutdownAttemptDone: make(chan struct{}),
		requests:            make(chan sessionRequest, defaultRequestQueue),
		pty:                 make(chan ptyEvent, config.PTYOutputQueue),
		waited:              make(chan error, 1),
		wrote:               make(chan error, 1),
		done:                make(chan struct{}),
	}

	session.pumpWG.Add(3)
	go func() {
		defer session.pumpWG.Done()
		session.readPTY()
	}()
	go func() {
		defer session.pumpWG.Done()
		session.writePTY()
	}()
	go func() {
		defer session.pumpWG.Done()
		session.waited <- process.Wait(context.Background())
	}()
	go session.run(config.Process.Columns, config.Process.Rows)
	return session, nil
}

// AcquireControl explicitly replaces the current controller. Pending input
// from the replaced connection is removed before the new lease is returned.
func (session *Session) AcquireControl(ctx context.Context, generation, connectionID, deviceID string) (ControlGrant, error) {
	response, err := session.call(ctx, sessionRequest{
		kind: requestAcquire, identity: MutationIdentity{Generation: generation},
		connection: connectionID, device: deviceID,
	})
	return response.grant, err
}

func (session *Session) Heartbeat(ctx context.Context, identity MutationIdentity) (ControlState, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestHeartbeat, identity: identity})
	return response.control, err
}

func (session *Session) ReleaseControl(ctx context.Context, identity MutationIdentity) error {
	_, err := session.call(ctx, sessionRequest{kind: requestRelease, identity: identity})
	return err
}

// Disconnect detaches one connection. It never ends the managed process.
func (session *Session) Disconnect(ctx context.Context, connectionID string) error {
	_, err := session.call(ctx, sessionRequest{kind: requestDisconnect, connection: connectionID})
	return err
}

func (session *Session) SubmitInput(ctx context.Context, identity MutationIdentity, sequence uint64, action InputAction) (InputAcceptance, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestSubmit, identity: identity, sequence: sequence, action: action})
	return response.acceptance, err
}

func (session *Session) Resize(ctx context.Context, identity MutationIdentity, columns, rows int) error {
	_, err := session.call(ctx, sessionRequest{kind: requestResize, identity: identity, columns: columns, rows: rows})
	return err
}

func (session *Session) Snapshot(ctx context.Context) (terminal.Frame, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestSnapshot})
	return response.frame, err
}

func (session *Session) Status(ctx context.Context) (SessionStatus, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestStatus})
	return response.status, err
}

// Terminate explicitly ends the process tree while keeping the final terminal
// state available until the broker closes the Session.
func (session *Session) Terminate(ctx context.Context) error {
	_, err := session.call(ctx, sessionRequest{kind: requestTerminate})
	return err
}

// RevokeDevice releases its controller lease and drops accepted input that has
// not yet entered the authoritative terminal actor.
func (session *Session) RevokeDevice(ctx context.Context, deviceID string) (int, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestRevokeDevice, device: deviceID})
	return response.dropped, err
}

// Close ends the broker-owned session lifetime. Detaching a viewer must never
// call this method.
func (session *Session) Close() error {
	session.closeOnce.Do(func() { close(session.shutdown) })
	select {
	case <-session.done:
		return session.shutdownErr
	case <-session.shutdownAttemptDone:
		return session.shutdownErr
	}
}

func (session *Session) call(ctx context.Context, request sessionRequest) (sessionResponse, error) {
	if ctx == nil {
		return sessionResponse{}, errors.New("broker: session operation context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return sessionResponse{}, err
	}
	select {
	case <-session.shutdown:
		return sessionResponse{}, ErrSessionClosed
	default:
	}
	request.reply = make(chan sessionResponse, 1)
	select {
	case session.requests <- request:
	case <-session.done:
		return sessionResponse{}, ErrSessionClosed
	case <-ctx.Done():
		return sessionResponse{}, ctx.Err()
	}
	// Enqueue is the broker acceptance boundary. Once crossed, wait for the
	// actor's authoritative answer so callers never retry an uncertain input.
	select {
	case response := <-request.reply:
		return response, response.err
	case <-session.done:
		return sessionResponse{}, ErrSessionClosed
	}
}

func (session *Session) readPTY() {
	buffer := make([]byte, 32*1024)
	for {
		count, err := session.process.Read(buffer)
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			select {
			case session.pty <- ptyEvent{data: chunk}:
			case <-session.ctx.Done():
				return
			}
		}
		if err != nil {
			select {
			case session.pty <- ptyEvent{err: err}:
			case <-session.ctx.Done():
			}
			return
		}
	}
}

func (session *Session) writePTY() {
	for output := range session.terminal.Output() {
		if _, err := session.process.Write(output); err != nil {
			session.wrote <- err
			return
		}
	}
	session.wrote <- nil
}

func (session *Session) run(columns, rows int) {
	status := SessionStatus{
		ID: session.id, Generation: session.generation, State: SessionRunning,
		PID: session.process.PID(), Columns: columns, Rows: rows, StartedAt: time.Now(),
	}
	parentDone := session.parent
	shutdownDone := session.shutdown
	var waitErr error
	var waitSeen, readDone, shuttingDown, processClosed bool
	var counterpartTimer *time.Timer
	var counterpartDeadline <-chan time.Time
	var shutdownTimer *time.Timer
	var shutdownDeadline <-chan time.Time
	beginShutdown := func() {
		if shuttingDown {
			return
		}
		shuttingDown = true
		session.closeOnce.Do(func() { close(session.shutdown) })
		parentDone = nil
		shutdownDone = nil
		if status.State == SessionRunning {
			status.State = SessionLost
			status.EndedAt = time.Now()
			session.clearControl()
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		err := session.process.Terminate(shutdownContext)
		cancel()
		if err != nil {
			session.reportShutdownError(fmt.Errorf("%w: terminate process: %v", ErrSessionShutdown, err))
			return
		}
		shutdownTimer = time.NewTimer(2 * processExitGrace)
		shutdownDeadline = shutdownTimer.C
	}
	defer func() {
		if counterpartTimer != nil {
			counterpartTimer.Stop()
		}
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
		session.inbox.Close()
		if !waitSeen {
			shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			_ = session.process.Terminate(shutdownContext)
			cancel()
		}
		if !processClosed {
			_ = session.process.Close()
		}
		_ = session.terminal.Close()
		session.cancel()
		session.pumpWG.Wait()
		close(session.done)
	}()

	for {
		if waitSeen && readDone {
			if status.State != SessionLost && status.State != SessionExited {
				status.State = SessionExited
				status.EndedAt = time.Now()
				code := processExitCode(waitErr)
				status.ExitCode = &code
				session.clearControl()
			}
			if shuttingDown {
				return
			}
		}

		// Lifecycle signals have priority over requests. Parent cancellation is
		// the broker shutdown boundary and must not be starved by a client that
		// continuously replenishes the bounded request queue.
		if !shuttingDown {
			select {
			case <-parentDone:
				beginShutdown()
				continue
			case <-shutdownDone:
				beginShutdown()
				continue
			default:
			}
		}

		// Prefer control requests so a sustained child-output stream cannot
		// starve snapshots, resize, termination, or lease management.
		if !shuttingDown {
			select {
			case request := <-session.requests:
				session.handleRequest(&status, request)
				continue
			default:
			}
		}

		select {
		case <-parentDone:
			beginShutdown()
		case <-shutdownDone:
			beginShutdown()
		case request := <-session.requests:
			if shuttingDown {
				request.reply <- sessionResponse{err: ErrSessionClosed}
			} else {
				session.handleRequest(&status, request)
			}
		case event := <-session.pty:
			if len(event.data) > 0 {
				if err := session.terminal.WriteOutput(session.ctx, event.data); err != nil {
					session.markLost(&status)
				}
			}
			if event.err != nil {
				readDone = true
				if waitSeen && counterpartTimer != nil {
					counterpartTimer.Stop()
					counterpartTimer = nil
					counterpartDeadline = nil
				} else if !waitSeen && counterpartDeadline == nil {
					counterpartTimer = time.NewTimer(processExitGrace)
					counterpartDeadline = counterpartTimer.C
				}
			}
		case inputReady := <-session.inbox.Ready():
			_ = inputReady
			input, ok, err := session.inbox.TryNext()
			if err != nil && !errors.Is(err, ErrInputQueueClosed) {
				session.markLost(&status)
			} else if ok {
				control := session.controller.State()
				if control.ConnectionID != input.ConnectionID || control.DeviceID != input.DeviceID || control.Epoch != input.LeaseEpoch {
					threshold := control.Epoch
					if threshold <= input.LeaseEpoch {
						threshold = input.LeaseEpoch + 1
					}
					session.inbox.DropBeforeLeaseEpoch(threshold)
				} else if err := session.executeInput(input.Action); err != nil {
					session.markLost(&status)
				}
			}
		case waitResult := <-session.waited:
			waitErr = waitResult
			waitSeen = true
			if status.State == SessionRunning {
				status.State = SessionExiting
			}
			if counterpartTimer != nil {
				counterpartTimer.Stop()
				counterpartTimer = nil
				counterpartDeadline = nil
			}
			if !readDone {
				counterpartTimer = time.NewTimer(processExitGrace)
				counterpartDeadline = counterpartTimer.C
			}
		case writeErr := <-session.wrote:
			if writeErr != nil && !waitSeen && status.State == SessionRunning {
				session.markLost(&status)
			}
		case <-counterpartDeadline:
			counterpartDeadline = nil
			counterpartTimer = nil
			if readDone && !waitSeen {
				session.markLost(&status)
			} else if waitSeen && !readDone && !processClosed {
				// Wait has completed, so closing the PTY now follows the
				// Process contract and wakes a platform read that did not
				// naturally report EOF after the child exited.
				_ = session.process.Close()
				processClosed = true
			}
		case <-shutdownDeadline:
			shutdownDeadline = nil
			shutdownTimer = nil
			if !(waitSeen && readDone) {
				session.reportShutdownError(ErrSessionShutdown)
			}
		}
	}
}

func (session *Session) handleRequest(status *SessionStatus, request sessionRequest) {
	response := sessionResponse{}
	switch request.kind {
	case requestAcquire:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		response.grant, response.err = session.controller.Acquire(
			request.identity.Generation, request.connection, request.device,
		)
		if response.err == nil {
			session.inbox.DropBeforeLeaseEpoch(response.grant.Epoch)
		}
	case requestHeartbeat:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		response.control, response.err = session.controller.Heartbeat(
			request.identity.Generation, request.identity.ConnectionID,
			request.identity.DeviceID, request.identity.Lease,
		)
	case requestRelease:
		response.err = session.controller.Release(
			request.identity.Generation, request.identity.ConnectionID,
			request.identity.DeviceID, request.identity.Lease,
		)
		if response.err == nil {
			session.inbox.DropConnection(request.identity.ConnectionID)
		}
	case requestDisconnect:
		session.controller.Disconnect(request.connection)
		session.inbox.DropConnection(request.connection)
	case requestSubmit:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		response.acceptance, response.err = session.inbox.Submit(
			request.identity.Generation, request.identity.ConnectionID,
			request.identity.DeviceID, request.identity.Lease,
			request.sequence, request.action,
		)
	case requestResize:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		if _, err := session.controller.Authorize(
			request.identity.Generation, request.identity.ConnectionID,
			request.identity.DeviceID, request.identity.Lease,
		); err != nil {
			response.err = err
			break
		}
		if err := session.process.Resize(request.columns, request.rows); err != nil {
			response.err = err
			break
		}
		response.err = session.terminal.Resize(session.ctx, request.columns, request.rows)
		if response.err == nil {
			status.Columns, status.Rows = request.columns, request.rows
		}
	case requestSnapshot:
		response.frame, response.err = session.terminal.Snapshot(session.ctx)
	case requestStatus:
		response.status = cloneSessionStatus(*status)
	case requestTerminate:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		status.State = SessionExiting
		terminateContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		response.err = session.process.Terminate(terminateContext)
		cancel()
		if response.err != nil {
			status.State = SessionLost
			status.EndedAt = time.Now()
			session.clearControl()
		}
	case requestRevokeDevice:
		response.dropped = session.inbox.RevokeDevice(request.device)
	default:
		response.err = errors.New("broker: unknown session request")
	}
	request.reply <- response
}

func (session *Session) executeInput(action InputAction) error {
	switch action.Kind {
	case InputKindText:
		return session.terminal.SendText(session.ctx, action.Text)
	case InputKindPaste:
		return session.terminal.Paste(session.ctx, action.Text)
	case InputKindKey:
		key, err := terminalKey(action)
		if err != nil {
			return err
		}
		return session.terminal.SendKey(session.ctx, uv.KeyPressEvent(key))
	case InputKindMouse:
		mouse, err := terminalMouse(action)
		if err != nil {
			return err
		}
		return session.terminal.SendMouse(session.ctx, mouse)
	default:
		return errors.New("broker: unsupported input action")
	}
}

func (session *Session) markLost(status *SessionStatus) {
	if status.State == SessionExited || status.State == SessionLost {
		return
	}
	status.State = SessionLost
	status.EndedAt = time.Now()
	session.clearControl()
	terminateContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	_ = session.process.Terminate(terminateContext)
	cancel()
}

func (session *Session) clearControl() {
	control := session.controller.State()
	if control.ConnectionID != "" {
		session.controller.Disconnect(control.ConnectionID)
	}
	if control.Epoch < ^uint64(0) {
		session.inbox.DropBeforeLeaseEpoch(control.Epoch + 1)
	}
}

func (session *Session) reportShutdownError(err error) {
	if err == nil {
		return
	}
	session.shutdownReportOnce.Do(func() {
		session.shutdownErr = err
		close(session.shutdownAttemptDone)
	})
}

func cloneSessionStatus(status SessionStatus) SessionStatus {
	if status.ExitCode != nil {
		code := *status.ExitCode
		status.ExitCode = &code
	}
	return status
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

package broker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/agentstate"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

const (
	DefaultPTYOutputQueue = 128
	MaxPTYOutputQueue     = 1024
	defaultRequestQueue   = 128
	shutdownTimeout       = 3 * time.Second
	processExitGrace      = time.Second
	// agentScanInterval bounds agent-state work: after output, the screen is
	// scanned at most this often, never per output chunk.
	agentScanInterval = 500 * time.Millisecond
)

var (
	ErrSessionClosed     = errors.New("broker: session is closed")
	ErrSessionNotRunning = errors.New("broker: session is not running")
	ErrSessionShutdown   = errors.New("broker: session shutdown did not complete")
	// ErrQuestionChanged rejects an answer whose question is no longer the
	// one on screen, so a phone never approves a prompt it did not show.
	ErrQuestionChanged = errors.New("broker: agent question changed")
	errAnswerOption    = errors.New("broker: answer option does not exist")
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
	// Notify receives agent transitions worth a push notification. It is
	// called on the session actor and must not block. Nil disables them.
	Notify func(webpush.Event)
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
	// Access re-authorizes a browser principal inside the actor. Nil means a
	// local desktop connection inside the current-user boundary.
	Access AccessCheck
}

// AccessCheck reports whether a browser credential may still act. It must be
// cheap and non-blocking because the session actor calls it before encoding.
type AccessCheck interface {
	Check() error
}

// ErrAccessRevoked rejects an action whose browser access was revoked or expired.
var ErrAccessRevoked = errors.New("broker: browser access was revoked or expired")

func checkAccess(access AccessCheck) error {
	if access != nil && access.Check() != nil {
		return ErrAccessRevoked
	}
	return nil
}

type terminalRuntime interface {
	WriteOutput(context.Context, []byte) error
	Resize(context.Context, int, int) error
	Snapshot(context.Context) (terminal.Frame, error)
	ScreenSnapshot(context.Context) (terminal.Frame, error)
	ReadHistory(context.Context, terminal.HistoryReadRequest) (terminal.HistoryReadResult, error)
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
	viewerEvents        chan viewerDetachEvent
	pty                 chan ptyEvent
	waited              chan error
	wrote               chan error
	done                chan struct{}
	ended               chan struct{}
	retirementReady     chan struct{}

	closeOnce          sync.Once
	endedOnce          sync.Once
	retirementOnce     sync.Once
	shutdownReportOnce sync.Once
	finalMu            sync.Mutex
	finalStatus        SessionStatus
	hasFinalStatus     bool
	pumpWG             sync.WaitGroup
	shutdownErr        error
	viewers            map[string]*viewerActorState
	published          *publishedFrame
	retiring           bool
	retirementRevision uint64
	publishedControl   ControlState
	publishedStatus    SessionStatus
	lastOutput         time.Time           // Actor-owned: when child output was last applied.
	agent              *agentstate.Tracker // Actor-owned agent-state detector.
	publishedAgent     agentstate.Status
	// agentDropped is set when an accepted answer was dropped because its
	// question changed; the actor then rescans and republishes session.state.
	agentDropped   bool
	agentRepublish bool
	notify         func(webpush.Event)
	notifiedID     string // Last question a waiting notification was sent for.
	// Dev-server preview ports (devpreview.go). previews and
	// previewRepublish are actor-owned; the scanner is shared with gateway checks.
	previewScanner   *previewScanner
	previewResults   chan previewScanResult
	previews         []previewPort
	previewRepublish bool
	// Live working folder (cwd.go). cwd and cwdRepublish are actor-owned.
	cwdRead      func(context.Context) (string, error)
	cwdResults   chan string
	cwd          cwdTracker
	cwdRepublish bool
	directory    string              // Project folder the process started in; immutable.
	termination  *sessionTermination // Actor-owned; one bounded attempt per session.
}

type sessionTermination struct {
	done     chan struct{}
	err      error // Published by closing done; read only after completion.
	finished bool  // Actor-owned completion handling.
	reply    chan sessionResponse
}

type sessionRequestKind uint8

const (
	requestAcquire sessionRequestKind = iota
	requestHeartbeat
	requestRelease
	requestSubmit
	requestResize
	requestSnapshot
	requestHistoryRead
	requestStatus
	requestTerminate
	requestRevokeDevice
	requestAttachViewer
	requestReserveViewerDelivery
	requestCompleteViewerDelivery
	requestViewerSnapshotApplied
	requestViewerResync
	requestRestartViewerSnapshot
	requestLatestViewerFrame
	requestCompleteViewerNotice
	requestPrepareViewerSnapshot
	requestPresence
)

type sessionRequest struct {
	kind          sessionRequestKind
	identity      MutationIdentity
	connection    string
	device        string
	action        InputAction
	sequence      uint64
	columns       int
	rows          int
	viewer        *ViewerSubscription
	deliveryKind  viewerDeliveryKind
	deliveryEpoch uint64
	snapshotID    string
	revision      uint64
	deliveryErr   error
	clippedRows   []bool
	history       terminal.HistoryReadRequest
	requestID     string
	preview       bool
	notice        *viewerNotice
	reply         chan sessionResponse
}

type sessionResponse struct {
	grant      ControlGrant
	control    ControlState
	acceptance InputAcceptance
	frame      terminal.Frame
	status     SessionStatus
	dropped    int
	delivery   viewerDeliveryPlan
	published  *publishedFrame
	history    terminal.HistoryReadResult
	presence   SessionPresence
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
		viewerEvents:        make(chan viewerDetachEvent, MaxViewersPerSession),
		pty:                 make(chan ptyEvent, config.PTYOutputQueue),
		waited:              make(chan error, 1),
		wrote:               make(chan error, 1),
		done:                make(chan struct{}),
		ended:               make(chan struct{}),
		retirementReady:     make(chan struct{}),
		viewers:             make(map[string]*viewerActorState),
		agent:               agentstate.NewTracker(config.Generation),
		notify:              config.Notify,
		previewScanner:      newPreviewScanner(process),
		previewResults:      make(chan previewScanResult, 1),
		cwdRead:             readOSDirectory(process),
		cwdResults:          make(chan string, 1),
		directory:           config.Process.Directory,
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

// Ended closes when the process generation reaches exited or lost. It is a
// broker-internal lifecycle signal; viewers must use Status for state.
func (session *Session) Ended() <-chan struct{} {
	return session.ended
}

// Done closes only after process reaping, PTY/terminal closure, and pump joins.
// Unlike Ended, it is proof that the runtime no longer owns live resources.
func (session *Session) Done() <-chan struct{} {
	return session.done
}

// RetirementReady closes after Ended and after every attached viewer has
// either acknowledged the final published frame and drained lifecycle/control
// notices or detached. The registry waits for this barrier before closing the
// session runtime.
func (session *Session) RetirementReady() <-chan struct{} {
	return session.retirementReady
}

// FinalStatus returns the immutable terminal lifecycle state after Ended has
// closed. It does not enqueue work on an actor that may already be shutting down.
func (session *Session) FinalStatus() (SessionStatus, bool) {
	session.finalMu.Lock()
	defer session.finalMu.Unlock()
	if !session.hasFinalStatus {
		return SessionStatus{}, false
	}
	return cloneSessionStatus(session.finalStatus), true
}

// AcquireControl explicitly replaces the current controller with an attached
// viewer incarnation. Pending input from the replaced connection is removed
// before the new lease is returned. It is the in-process primitive and does not
// check snapshot readiness; client requests use AcquireControlForRequest.
func (session *Session) AcquireControl(ctx context.Context, viewer *ViewerSubscription) (ControlGrant, error) {
	if viewer == nil {
		return ControlGrant{}, ErrViewerMissing
	}
	response, err := session.call(ctx, sessionRequest{
		kind: requestAcquire, connection: viewer.connectionID,
		device: viewer.deviceID, viewer: viewer,
	})
	return response.grant, err
}

// AcquireControlForRequest serializes the holder's secret grant with every
// observer notification in the session actor. The caller must not send another
// successful grant reply: it is already queued on the viewer sink using requestID.
// The actor rejects it with ErrViewerSnapshotPending until the viewer has
// applied its current snapshot.
func (session *Session) AcquireControlForRequest(ctx context.Context, viewer *ViewerSubscription, requestID string) (ControlGrant, error) {
	if viewer == nil {
		return ControlGrant{}, ErrViewerMissing
	}
	if !validOperationUUID(requestID) {
		return ControlGrant{}, errors.New("broker: control request must have a canonical UUID")
	}
	response, err := session.call(ctx, sessionRequest{
		kind: requestAcquire, connection: viewer.connectionID,
		device: viewer.deviceID, viewer: viewer, requestID: requestID,
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

// ReadHistory returns an actor-copied immutable normal-history page. The
// generation and history token are checked in the session actor before the
// result leaves broker ownership for protocol encoding or transmission.
func (session *Session) ReadHistory(
	ctx context.Context,
	generation string,
	request terminal.HistoryReadRequest,
) (terminal.HistoryReadResult, error) {
	response, err := session.call(ctx, sessionRequest{
		kind: requestHistoryRead, identity: MutationIdentity{Generation: generation}, history: request,
	})
	return response.history, err
}

func (session *Session) Status(ctx context.Context) (SessionStatus, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestStatus})
	return response.status, err
}

// SessionPresence is a point-in-time, non-secret view of who is attached.
// ControllerDevice is broker-internal: callers compare it with the requesting
// device and must never send it to another device. LastOutput is zero until
// child output is applied. Preview is set only when requested and is empty
// for a blank screen. Agent has an empty State until the first screen scan.
type SessionPresence struct {
	ControllerDevice string
	Viewers          int
	LastOutput       time.Time
	Preview          string
	Agent            agentstate.Status
	Previews         []protocol.PreviewPort
	Cwd              string
}

// Presence reads presence in one actor call. With preview, the actor also
// copies the current screen; the text is extracted after the actor replies.
func (session *Session) Presence(ctx context.Context, preview bool) (SessionPresence, error) {
	response, err := session.call(ctx, sessionRequest{kind: requestPresence, preview: preview})
	if err != nil {
		return SessionPresence{}, err
	}
	if preview {
		response.presence.Preview = screenPreview(response.frame)
	}
	return response.presence, nil
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
		// Cleanup can finish immediately after the actor publishes its reply.
		// Preserve that authoritative result rather than racing it with Done.
		select {
		case response := <-request.reply:
			return response, response.err
		default:
			return sessionResponse{}, ErrSessionClosed
		}
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
	var shutdownTerminationHandled bool
	var publishTimer *time.Timer
	var publishDeadline <-chan time.Time
	var viewerAckTimer *time.Timer
	var viewerAckDeadline <-chan time.Time
	var viewerAckAt time.Time
	var controllerTimer *time.Timer
	var controllerDeadline <-chan time.Time
	var controllerAt time.Time
	agentTimer := time.NewTimer(agentScanInterval)
	agentDeadline, agentAt := agentTimer.C, status.StartedAt.Add(agentScanInterval)
	// armAgent keeps the earliest pending scan, so a burst of output costs one
	// scan per agentScanInterval at most.
	armAgent := func(at time.Time) {
		if at.IsZero() || agentDeadline != nil && !at.Before(agentAt) {
			return
		}
		if !agentTimer.Stop() {
			select {
			case <-agentTimer.C:
			default:
			}
		}
		agentTimer.Reset(max(time.Until(at), 0))
		agentDeadline, agentAt = agentTimer.C, at
	}
	// Preview scans follow the agent pattern: one pending timer, earliest
	// wins, and at most one OS scan in flight off the actor.
	var previewTimer *time.Timer
	var previewDeadline <-chan time.Time
	var previewAt, previewLastScan time.Time
	var previewScanning bool
	armPreview := func(at time.Time) {
		if session.previewScanner == nil || previewDeadline != nil && !at.Before(previewAt) {
			return
		}
		if previewTimer != nil {
			previewTimer.Stop()
		}
		previewTimer = time.NewTimer(max(time.Until(at), 0))
		previewDeadline, previewAt = previewTimer.C, at
	}
	// Working-folder checks follow the same pattern.
	cwdTimer := time.NewTimer(cwdCheckDelay)
	cwdDeadline, cwdAt := cwdTimer.C, status.StartedAt.Add(cwdCheckDelay)
	var cwdLastCheck time.Time
	var cwdMissed bool // A check came due while one was running.
	armCwd := func(activity time.Time) {
		at := later(activity.Add(cwdCheckDelay), cwdLastCheck.Add(cwdCheckEvery))
		if cwdDeadline != nil && !at.Before(cwdAt) {
			return
		}
		cwdTimer.Stop()
		cwdTimer = time.NewTimer(max(time.Until(at), 0))
		cwdDeadline, cwdAt = cwdTimer.C, at
	}
	refreshControllerTimer := func() {
		// Use the same state capture that published observer notices. A second
		// State call could expire the lease between publication and timer setup,
		// leaving idle observers with neither a notice nor another wake-up.
		state := session.publishedControl
		if state.ConnectionID == "" {
			if controllerTimer != nil {
				controllerTimer.Stop()
			}
			controllerDeadline = nil
			controllerAt = time.Time{}
			return
		}
		if state.ExpiresAt.Equal(controllerAt) {
			return
		}
		if controllerTimer != nil {
			controllerTimer.Stop()
		}
		controllerTimer = time.NewTimer(max(time.Until(state.ExpiresAt), 0))
		controllerDeadline = controllerTimer.C
		controllerAt = state.ExpiresAt
	}
	refreshViewerAckTimer := func() {
		next, ok := session.nextViewerAckDeadline()
		if !ok {
			if viewerAckTimer != nil {
				viewerAckTimer.Stop()
			}
			viewerAckDeadline = nil
			viewerAckAt = time.Time{}
			return
		}
		if next.Equal(viewerAckAt) {
			return
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		if viewerAckTimer == nil {
			viewerAckTimer = time.NewTimer(delay)
		} else {
			if !viewerAckTimer.Stop() {
				select {
				case <-viewerAckTimer.C:
				default:
				}
			}
			viewerAckTimer.Reset(delay)
		}
		viewerAckDeadline = viewerAckTimer.C
		viewerAckAt = next
	}
	schedulePublish := func() {
		if len(session.viewers) == 0 || publishDeadline != nil {
			return
		}
		publishTimer = time.NewTimer(framePublishInterval)
		publishDeadline = publishTimer.C
	}
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
			session.startRetirement(status)
			session.markEnded(status)
			session.clearControl()
		}
		for connectionID := range session.viewers {
			session.detachViewer(connectionID, ErrSessionClosed)
		}
		session.startTermination()
	}
	defer func() {
		if counterpartTimer != nil {
			counterpartTimer.Stop()
		}
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
		if publishTimer != nil {
			publishTimer.Stop()
		}
		if viewerAckTimer != nil {
			viewerAckTimer.Stop()
		}
		if controllerTimer != nil {
			controllerTimer.Stop()
		}
		agentTimer.Stop()
		cwdTimer.Stop()
		if previewTimer != nil {
			previewTimer.Stop()
		}
		for connectionID := range session.viewers {
			session.detachViewer(connectionID, ErrSessionClosed)
		}
		session.checkRetirementReady()
		session.inbox.Close()
		// The loop exits only after Wait, ordered PTY EOF, and the one owned
		// termination worker. A process wait must never block that loop's drain.
		if session.termination != nil {
			<-session.termination.done
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
		session.publishViewerEvents(status)
		refreshControllerTimer()
		refreshViewerAckTimer()
		if shuttingDown && session.termination.finished && !shutdownTerminationHandled {
			shutdownTerminationHandled = true
			if err := session.termination.err; err != nil {
				session.reportShutdownError(fmt.Errorf("%w: terminate process: %w", ErrSessionShutdown, err))
			} else {
				shutdownTimer = time.NewTimer(2 * processExitGrace)
				shutdownDeadline = shutdownTimer.C
			}
		}
		if waitSeen && readDone {
			if status.State != SessionLost && status.State != SessionExited {
				if publishTimer != nil {
					publishTimer.Stop()
					publishTimer = nil
					publishDeadline = nil
				}
				if len(session.viewers) > 0 {
					if err := session.capturePublished(); err != nil {
						session.markLost(&status)
						continue
					}
				}
				status.State = SessionExited
				status.EndedAt = time.Now()
				code := processExitCode(waitErr)
				status.ExitCode = &code
				session.startRetirement(status)
				session.markEnded(status)
				session.clearControl()
			}
			if shuttingDown && session.termination.finished {
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

		// Viewer lifecycle events bypass the public request queue. A saturated
		// client queue cannot suppress acknowledgement expiry or detach cleanup.
		select {
		case <-session.terminationDone():
			session.completeTermination(&status)
			continue
		case <-controllerDeadline:
			controllerDeadline = nil
			controllerAt = time.Time{}
			session.controller.Expire()
			continue
		case <-viewerAckDeadline:
			viewerAckDeadline = nil
			viewerAckAt = time.Time{}
			session.expireViewerAckDeadlines(time.Now())
			continue
		case event := <-session.viewerEvents:
			session.handleViewerDetach(event)
			continue
		default:
		}

		// Prefer control requests so a sustained child-output stream cannot
		// starve snapshots, resize, termination, or lease management.
		// Once exiting, service requests in the balanced select below: a busy
		// observer must not starve output draining needed for process teardown.
		if !shuttingDown && status.State == SessionRunning {
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
		case <-session.terminationDone():
			session.completeTermination(&status)
		case request := <-session.requests:
			if shuttingDown {
				request.reply <- sessionResponse{err: ErrSessionClosed}
			} else {
				session.handleRequest(&status, request)
			}
		case <-viewerAckDeadline:
			viewerAckDeadline = nil
			viewerAckAt = time.Time{}
			session.expireViewerAckDeadlines(time.Now())
		case <-controllerDeadline:
			controllerDeadline = nil
			controllerAt = time.Time{}
			session.controller.Expire()
		case event := <-session.viewerEvents:
			session.handleViewerDetach(event)
		case event := <-session.pty:
			if len(event.data) > 0 {
				if err := session.terminal.WriteOutput(session.ctx, event.data); err != nil {
					session.markLost(&status)
				} else {
					session.lastOutput = time.Now()
					schedulePublish()
					armAgent(session.lastOutput.Add(agentScanInterval))
					armPreview(later(session.lastOutput.Add(previewScanDelay), previewLastScan.Add(previewScanEvery)))
					armCwd(session.lastOutput)
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
				if checkAccess(input.access) != nil {
					// Revocation or expiry after admission: never encode it, and
					// drop the rest of that connection's accepted input.
					session.inbox.DropConnection(input.ConnectionID)
				} else if control.ConnectionID != input.ConnectionID || control.DeviceID != input.DeviceID || control.Epoch != input.LeaseEpoch {
					threshold := control.Epoch
					if threshold <= input.LeaseEpoch {
						threshold = input.LeaseEpoch + 1
					}
					session.inbox.DropBeforeLeaseEpoch(threshold)
				} else if err := session.executeInput(input.Action); err != nil {
					session.markLost(&status)
				} else {
					armCwd(time.Now())
				}
				if session.agentDropped && status.State == SessionRunning {
					// The phone saw input.accepted for an answer that was not
					// written. Send the current state now so it refreshes.
					session.agentDropped, session.agentRepublish = false, true
					armAgent(session.observeAgent(status.StartedAt))
				}
			}
		case waitResult := <-session.waited:
			waitErr = waitResult
			waitSeen = true
			if status.State == SessionRunning {
				session.markExiting(&status)
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
				if session.termination != nil && !session.termination.finished {
					// Wait can complete before the owned terminator's cleanup.
					// Do not close its PTY while that worker can still access it.
					counterpartTimer = time.NewTimer(processExitGrace)
					counterpartDeadline = counterpartTimer.C
					continue
				}
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
		case <-publishDeadline:
			publishDeadline = nil
			publishTimer = nil
			if err := session.capturePublished(); err != nil {
				session.markLost(&status)
			}
		case <-agentDeadline:
			agentDeadline = nil
			if status.State == SessionRunning && !shuttingDown {
				armAgent(session.observeAgent(status.StartedAt))
			}
		case <-previewDeadline:
			previewDeadline = nil
			if status.State == SessionRunning && !shuttingDown && !previewScanning {
				previewScanning = true
				go func() {
					ports, err := session.previewScanner.scan(session.ctx, 0)
					session.previewResults <- previewScanResult{ports: ports, err: err}
				}()
			}
		case <-cwdDeadline:
			cwdDeadline = nil
			cwdMissed = session.cwd.checking
			if status.State == SessionRunning && !shuttingDown && !session.cwd.checking {
				session.cwd.checking = true
				go func() {
					ctx, cancel := context.WithTimeout(session.ctx, cwdCheckTimeout)
					defer cancel()
					path, _ := session.cwdRead(ctx)
					session.cwdResults <- path
				}()
			}
		case path := <-session.cwdResults:
			session.cwd.checking, cwdLastCheck = false, time.Now()
			if status.State == SessionRunning && !shuttingDown {
				var report terminal.DirectoryReport
				if reporter, ok := session.terminal.(directoryReporter); ok {
					report = reporter.DirectoryReport()
				}
				session.cwdRepublish = session.cwd.next(path, report) || session.cwdRepublish
				if cwdMissed {
					cwdMissed = false
					armCwd(cwdLastCheck)
				}
			}
		case result := <-session.previewResults:
			previewScanning, previewLastScan = false, time.Now()
			if status.State != SessionRunning || shuttingDown {
				break
			}
			if result.err == nil {
				var changed bool
				session.previews, changed = mergePreviews(session.previews, result.ports, previewLastScan)
				session.previewRepublish = session.previewRepublish || changed
			}
			switch {
			case time.Since(session.lastOutput) < previewActiveFor:
				armPreview(previewLastScan.Add(previewScanEvery))
			case len(session.previews) > 0:
				armPreview(previewLastScan.Add(previewIdleEvery))
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
		viewer, err := session.viewerForRequest(request.connection, request.viewer)
		if err != nil {
			response.err = err
			break
		}
		if response.err = checkAccess(viewer.subscription.access); response.err != nil {
			break
		}
		if request.requestID != "" && !viewer.snapshotApplied() {
			response.err = ErrViewerSnapshotPending
			break
		}
		response.grant, response.err = session.controller.Acquire(
			session.generation, viewer.subscription.connectionID, viewer.subscription.deviceID,
		)
		if response.err == nil {
			session.inbox.DropBeforeLeaseEpoch(response.grant.Epoch)
			response.err = session.publishControlGrant(response.grant, request.requestID)
		}
	case requestHeartbeat:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		if response.err = checkAccess(request.identity.Access); response.err != nil {
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
	case requestSubmit:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		if response.err = checkAccess(request.identity.Access); response.err != nil {
			break
		}
		if request.action.Kind == InputKindAnswer {
			// Authorize first so a non-controller learns nothing about the
			// screen; neither check consumes the input sequence.
			if _, response.err = session.controller.Authorize(
				request.identity.Generation, request.identity.ConnectionID,
				request.identity.DeviceID, request.identity.Lease,
			); response.err != nil {
				break
			}
			if response.err = session.checkAnswer(request.action); response.err != nil {
				break
			}
		}
		response.acceptance, response.err = session.inbox.submit(request.identity, request.sequence, request.action)
	case requestResize:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		if response.err = checkAccess(request.identity.Access); response.err != nil {
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
			response.err = session.capturePublished()
		}
	case requestSnapshot:
		response.frame, response.err = session.terminal.Snapshot(session.ctx)
	case requestHistoryRead:
		if request.identity.Generation != session.generation {
			response.err = ErrGenerationChanged
			break
		}
		response.history, response.err = session.terminal.ReadHistory(session.ctx, request.history)
	case requestStatus:
		response.status = cloneSessionStatus(*status)
	case requestPresence:
		response.presence = SessionPresence{
			ControllerDevice: session.controller.State().DeviceID, Viewers: len(session.viewers),
			LastOutput: session.lastOutput, Agent: session.agent.Status(),
		}
		if status.State == SessionRunning {
			response.presence.Previews = previewPayload(session.previews)
			response.presence.Cwd = session.cwd.current
		}
		if request.preview {
			response.frame, response.err = session.terminal.ScreenSnapshot(session.ctx)
		}
	case requestTerminate:
		if status.State != SessionRunning {
			response.err = ErrSessionNotRunning
			break
		}
		session.markExiting(status)
		attempt := session.startTermination()
		attempt.reply = request.reply
		return // The admitted caller receives the real bounded attempt result.
	case requestRevokeDevice:
		response.dropped = session.inbox.RevokeDevice(request.device)
		for connectionID, viewer := range session.viewers {
			if viewer.subscription.deviceID == request.device {
				session.detachViewer(connectionID, nil)
			}
		}
	case requestAttachViewer:
		response.err = session.attachViewer(request, *status)
	case requestCompleteViewerNotice:
		response.err = session.completeViewerNotice(request)
	case requestPrepareViewerSnapshot:
		response.err = session.prepareViewerSnapshot(request)
	case requestReserveViewerDelivery:
		response.delivery, response.err = session.reserveViewerDelivery(request.connection, request.viewer)
	case requestCompleteViewerDelivery:
		response.err = session.completeViewerDelivery(request)
	case requestViewerSnapshotApplied:
		response.err = session.applyViewerSnapshot(
			request.connection, request.viewer, request.snapshotID, request.revision,
		)
	case requestViewerResync:
		response.err = session.resyncViewer(request.connection, request.viewer)
	case requestRestartViewerSnapshot:
		response.err = session.restartViewerSnapshot(request.connection, request.viewer, request.deliveryEpoch)
	case requestLatestViewerFrame:
		response.published, response.err = session.latestViewerFrame(
			request.connection, request.viewer, request.deliveryEpoch,
		)
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
	case InputKindAnswer:
		return session.executeAnswer(action)
	default:
		return errors.New("broker: unsupported input action")
	}
}

// The exit boundary revokes input that was accepted but has not been encoded.
// Bytes already handed to the single PTY writer remain in flight.
func (session *Session) markExiting(status *SessionStatus) {
	status.State = SessionExiting
	session.clearControl()
}

func (session *Session) markLost(status *SessionStatus) {
	if status.State == SessionExited || status.State == SessionLost {
		return
	}
	status.State = SessionLost
	status.EndedAt = time.Now()
	session.startRetirement(*status)
	session.markEnded(*status)
	session.clearControl()
	session.startTermination()
}

// startTermination is called only by the actor. Darwin may wait for PTY output
// to drain during process teardown, so never wait for the process on the actor
// that is the sole canonical output consumer. Close, explicit termination, and
// failure cleanup reuse this one attempt and its original result.
func (session *Session) startTermination() *sessionTermination {
	if session.termination != nil {
		return session.termination
	}
	attempt := &sessionTermination{done: make(chan struct{})}
	session.termination = attempt
	go func() {
		defer close(attempt.done)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		attempt.err = session.process.Terminate(ctx)
	}()
	return attempt
}

func (session *Session) terminationDone() <-chan struct{} {
	if session.termination == nil || session.termination.finished {
		return nil
	}
	return session.termination.done
}

func (session *Session) completeTermination(status *SessionStatus) {
	attempt := session.termination
	attempt.finished = true
	if attempt.err != nil {
		session.markLost(status)
	}
	if attempt.reply != nil {
		attempt.reply <- sessionResponse{err: attempt.err}
		attempt.reply = nil
	}
}

func (session *Session) markEnded(status SessionStatus) {
	session.endedOnce.Do(func() {
		session.finalMu.Lock()
		session.finalStatus = cloneSessionStatus(status)
		session.hasFinalStatus = true
		session.finalMu.Unlock()
		close(session.ended)
	})
}

func (session *Session) startRetirement(status SessionStatus) {
	if session.retiring {
		return
	}
	session.retiring = true
	session.publishSessionState(status)
	if session.published != nil {
		session.retirementRevision = session.published.frame.Revision
	}
	for _, viewer := range session.viewers {
		signalViewer(viewer.subscription)
	}
	session.checkRetirementReady()
}

func (session *Session) checkRetirementReady() {
	if !session.retiring {
		return
	}
	for _, viewer := range session.viewers {
		if len(viewer.notices) > 0 || viewer.noticeInFlight != nil || viewer.inFlight != nil || viewer.awaiting != nil || viewer.base == nil ||
			viewer.base.frame.Revision < session.retirementRevision {
			return
		}
	}
	session.retirementOnce.Do(func() { close(session.retirementReady) })
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

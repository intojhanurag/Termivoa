package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	MaxViewersPerSession = 16
	MaxViewerQueueBytes  = 2 * 1024 * 1024
	// DefaultViewerTimeout bounds one delivery to a viewer connection. The
	// gateway stops reading while its phone is behind, which makes this worker
	// coalesce to the newest screen; the gateway disconnects a phone that makes
	// no progress for 15 s, so this must stay above that.
	DefaultViewerTimeout      = 20 * time.Second
	DefaultSnapshotAckTimeout = 5 * time.Second
	framePublishInterval      = time.Second / 60
	MaxViewerQueuedNotices    = 64
)

var (
	ErrViewerExists           = errors.New("broker: viewer connection already exists")
	ErrViewerLimit            = errors.New("broker: viewer limit reached")
	ErrViewerMissing          = errors.New("broker: viewer connection does not exist")
	ErrViewerSlow             = errors.New("broker: viewer could not consume bounded output")
	ErrViewerSnapshotMismatch = errors.New("broker: viewer snapshot acknowledgement does not match")
	ErrViewerSnapshotTimeout  = errors.New("broker: viewer snapshot acknowledgement timed out")
	ErrViewerSnapshotPending  = errors.New("broker: viewer has not applied its current snapshot")
	ErrViewerReadOnly         = errors.New("broker: thumbnail viewer is read-only")
	errViewerDeliveryStale    = errors.New("broker: viewer delivery was superseded")
	errViewerSnapshotRestart  = errors.New("broker: snapshot catch-up window exceeded")
)

// ViewerSink is the broker-to-connection boundary. Send must not mutate
// message. It may return after a bounded connection writer has copied the
// message, but it must invoke drained exactly once after every retained copy
// has left that writer's queue. An error return means the sink retained no
// copy. It must honor context cancellation. The broker retains the message's
// byte reservation until drained runs.
type ViewerSink interface {
	Send(context.Context, []byte, func()) error
}

type ViewerConfig struct {
	ConnectionID       string
	DeviceID           string
	Generation         string
	Sink               ViewerSink
	SendTimeout        time.Duration
	SnapshotAckTimeout time.Duration
	// Access re-authorizes a browser viewer before control acquisition. Nil
	// means a local desktop connection.
	Access AccessCheck
	// MaxUpdatesPerSecond makes a read-only thumbnail viewer: screen frames are
	// coalesced to at most this rate and the viewer can never take control.
	// Zero means a normal full-rate viewer.
	MaxUpdatesPerSecond int
	// budget is an optional connection-wide reservation boundary shared with
	// dispatcher responses. A matching sink must not reserve these bytes twice.
	budget viewerByteBudget
}

type viewerByteBudget interface {
	reserve(int) error
	release(int)
}

// ViewerSubscription owns one observer connection. Closing it releases only
// this viewer and any controller lease held by its connection; it never ends
// the managed process.
type ViewerSubscription struct {
	session      *Session
	connectionID string
	deviceID     string
	sink         ViewerSink
	access       AccessCheck
	timeout      time.Duration
	ackTimeout   time.Duration
	// frameInterval is the minimum spacing of screen frame deliveries; nonzero
	// only for a read-only thumbnail viewer.
	frameInterval time.Duration
	budget        viewerByteBudget
	notify        chan struct{}
	done          chan struct{}
	detached      chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc

	mu                 sync.Mutex
	deliveryCancel     context.CancelCauseFunc
	deliveryEpochFloor uint64
	err                error
	budgetUsed         int
	budgetPeak         int
	closeOnce          sync.Once
	detachOnce         sync.Once
}

type publishedFrame struct {
	frame           terminal.Frame
	historyEpoch    uint64
	historyRevision uint64
}

type viewerActorState struct {
	subscription         *ViewerSubscription
	base                 *publishedFrame
	inFlight             *publishedFrame
	inFlightEpoch        uint64
	awaiting             *viewerSnapshot
	sendingSnapshot      *viewerSnapshot
	earlySnapshotApplied bool
	ackDeadline          time.Time
	clippedRows          []bool
	forceSnapshot        bool
	epoch                uint64
	notices              []*viewerNotice
	noticeInFlight       *viewerNotice
	// nextFrameAt and frameTimer rate-limit a thumbnail viewer. The one lazily
	// created timer wakes the worker when the next slot opens, so a skipped
	// frame is always delivered later; detach stops it.
	nextFrameAt time.Time
	frameTimer  *time.Timer
}

type viewerSnapshot struct {
	id          string
	target      *publishedFrame
	clippedRows []bool
}

type viewerDeliveryKind uint8

const (
	viewerDeliveryNone viewerDeliveryKind = iota
	viewerDeliverySnapshot
	viewerDeliveryPatch
	viewerDeliveryNotice
)

type viewerDeliveryPlan struct {
	kind            viewerDeliveryKind
	epoch           uint64
	base            *publishedFrame
	target          *publishedFrame
	baseClippedRows []bool
	notice          *viewerNotice
}

type viewerDetachEvent struct {
	subscription *ViewerSubscription
}

// AttachViewer atomically registers an observer and captures its initial
// canonical snapshot inside the session actor. Network transmission runs in a
// separate worker and cannot block PTY parsing or canonical-state mutation.
func (session *Session) AttachViewer(ctx context.Context, config ViewerConfig) (*ViewerSubscription, error) {
	if !validOperationUUID(config.ConnectionID) || !validOperationUUID(config.DeviceID) || !validOperationUUID(config.Generation) {
		return nil, errors.New("broker: viewer identity must contain canonical UUIDs")
	}
	if config.Sink == nil {
		return nil, errors.New("broker: viewer sink must not be nil")
	}
	if config.SendTimeout == 0 {
		config.SendTimeout = DefaultViewerTimeout
	}
	if config.SendTimeout < time.Millisecond || config.SendTimeout > time.Minute {
		return nil, errors.New("broker: viewer send timeout must be between 1ms and 1m")
	}
	if config.SnapshotAckTimeout == 0 {
		config.SnapshotAckTimeout = DefaultSnapshotAckTimeout
	}
	if config.SnapshotAckTimeout < time.Millisecond || config.SnapshotAckTimeout > time.Minute {
		return nil, errors.New("broker: viewer snapshot acknowledgement timeout must be between 1ms and 1m")
	}
	if config.MaxUpdatesPerSecond < 0 || config.MaxUpdatesPerSecond > protocol.MaxThumbnailUpdatesPerSecond {
		return nil, fmt.Errorf("broker: thumbnail rate must be between 1 and %d updates per second", protocol.MaxThumbnailUpdatesPerSecond)
	}
	var frameInterval time.Duration
	if config.MaxUpdatesPerSecond > 0 {
		frameInterval = time.Second / time.Duration(config.MaxUpdatesPerSecond)
	}
	viewerContext, cancel := context.WithCancel(session.ctx)
	subscription := &ViewerSubscription{
		session: session, connectionID: config.ConnectionID, deviceID: config.DeviceID,
		sink: config.Sink, access: config.Access, timeout: config.SendTimeout, ackTimeout: config.SnapshotAckTimeout,
		frameInterval: frameInterval, budget: config.budget,
		notify: make(chan struct{}, 1), detached: make(chan struct{}),
		done: make(chan struct{}), ctx: viewerContext, cancel: cancel,
	}
	response, err := session.call(ctx, sessionRequest{
		kind: requestAttachViewer, identity: MutationIdentity{Generation: config.Generation},
		connection: config.ConnectionID, device: config.DeviceID, viewer: subscription,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	_ = response
	go subscription.run()
	return subscription, nil
}

func (subscription *ViewerSubscription) Done() <-chan struct{} { return subscription.done }

func (subscription *ViewerSubscription) Err() error {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.err
}

// SnapshotApplied unlocks live patches only after the client has atomically
// validated and painted the complete snapshot transaction.
func (subscription *ViewerSubscription) SnapshotApplied(ctx context.Context, snapshotID string, revision uint64) error {
	_, err := subscription.session.call(ctx, sessionRequest{
		kind: requestViewerSnapshotApplied, connection: subscription.connectionID,
		viewer: subscription, snapshotID: snapshotID, revision: revision,
	})
	return err
}

// Resync invalidates an in-progress delivery and schedules a fresh snapshot.
func (subscription *ViewerSubscription) Resync(ctx context.Context) error {
	_, err := subscription.session.call(ctx, sessionRequest{
		kind: requestViewerResync, connection: subscription.connectionID, viewer: subscription,
	})
	return err
}

func (subscription *ViewerSubscription) Close() error {
	subscription.stop(nil)
	select {
	case <-subscription.done:
		return subscription.Err()
	case <-subscription.session.done:
		return subscription.Err()
	}
}

func (subscription *ViewerSubscription) run() {
	defer close(subscription.done)
	defer func() {
		subscription.session.queueViewerDetach(subscription)
		select {
		case <-subscription.detached:
		case <-subscription.session.done:
		}
	}()

	supersededSnapshots := 0
	for {
		select {
		case <-subscription.ctx.Done():
			return
		case <-subscription.session.done:
			return
		case <-subscription.notify:
		}

		response, err := subscription.session.call(subscription.ctx, sessionRequest{
			kind: requestReserveViewerDelivery, connection: subscription.connectionID, viewer: subscription,
		})
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSessionClosed) {
				subscription.stop(err)
			}
			return
		}
		plan := response.delivery
		if plan.kind == viewerDeliveryNone {
			continue
		}
		if plan.kind == viewerDeliveryNotice {
			if err := subscription.deliverNotice(plan.notice); err != nil {
				subscription.stop(err)
				return
			}
			continue
		}

		timedContext, cancelTimeout := context.WithTimeout(subscription.ctx, subscription.timeout)
		deliveryContext, cancel := context.WithCancelCause(timedContext)
		if !subscription.setDeliveryCancel(plan.epoch, cancel) {
			cancelTimeout()
			continue
		}
		kind, snapshotID, clippedRows, deliveryErr := subscription.stream(deliveryContext, plan)
		cancel(nil)
		cancelTimeout()
		subscription.clearDeliveryCancel()

		if errors.Is(deliveryErr, errViewerSnapshotRestart) {
			supersededSnapshots++
			_, restartErr := subscription.session.call(subscription.ctx, sessionRequest{
				kind: requestRestartViewerSnapshot, connection: subscription.connectionID,
				viewer: subscription, deliveryEpoch: plan.epoch,
			})
			if restartErr != nil && !errors.Is(restartErr, errViewerDeliveryStale) {
				subscription.stop(restartErr)
				return
			}
			if supersededSnapshots > 1 {
				subscription.stop(fmt.Errorf("%w: snapshot repeatedly superseded", ErrViewerSlow))
				return
			}
			continue
		}

		response, completeErr := subscription.session.call(subscription.ctx, sessionRequest{
			kind: requestCompleteViewerDelivery, connection: subscription.connectionID,
			viewer: subscription, deliveryEpoch: plan.epoch, deliveryKind: kind, snapshotID: snapshotID,
			clippedRows: clippedRows, deliveryErr: deliveryErr,
		})
		_ = response
		if errors.Is(completeErr, errViewerDeliveryStale) {
			continue
		}
		if deliveryErr != nil {
			if errors.Is(deliveryErr, context.DeadlineExceeded) || errors.Is(deliveryErr, ErrViewerSlow) {
				deliveryErr = fmt.Errorf("%w: %v", ErrViewerSlow, deliveryErr)
			}
			subscription.stop(deliveryErr)
			return
		}
		if completeErr != nil {
			subscription.stop(completeErr)
			return
		}
		supersededSnapshots = 0
	}
}

func (subscription *ViewerSubscription) stream(ctx context.Context, plan viewerDeliveryPlan) (viewerDeliveryKind, string, []bool, error) {
	if plan.kind == viewerDeliveryPatch {
		patch, err := terminal.Diff(plan.base.frame, plan.target.frame)
		if err == nil {
			clippedRows := slices.Clone(plan.baseClippedRows)
			if len(clippedRows) != plan.target.frame.Rows {
				return subscription.streamSnapshot(ctx, plan)
			}
			payload, payloadErr := protocol.PatchPayload(
				patch, plan.base.frame.HistoryEpoch != patch.HistoryEpoch,
			)
			if payloadErr != nil {
				return viewerDeliveryPatch, "", nil, payloadErr
			}
			for index, changed := range payload.Rows {
				rowIndex := int(changed.Index)
				if rowIndex < 0 || rowIndex >= len(clippedRows) {
					return viewerDeliveryPatch, "", nil, errors.New("broker: patch row is outside terminal bounds")
				}
				clippedRows[rowIndex] = protocolRowContentClipped(changed.Row) ||
					terminalRowContentClipped(patch.ChangedRows[index].Row)
			}
			payload.Screen.ContentClipped = payload.Screen.ContentClipped || slices.Contains(clippedRows, true)
			message, messageErr := subscription.hostMessage(protocol.ScreenPatch, payload)
			if messageErr == nil && len(message) <= protocol.MaxOutboundMessageBytes {
				return viewerDeliveryPatch, "", clippedRows, subscription.send(ctx, message)
			}
			if messageErr != nil && !errors.Is(messageErr, errOutboundMessageTooLarge) {
				return viewerDeliveryPatch, "", nil, messageErr
			}
		} else if !errors.Is(err, terminal.ErrFullSnapshotNeeded) {
			return viewerDeliveryPatch, "", nil, err
		}
	}
	return subscription.streamSnapshot(ctx, plan)
}

func (subscription *ViewerSubscription) streamSnapshot(ctx context.Context, plan viewerDeliveryPlan) (viewerDeliveryKind, string, []bool, error) {
	snapshotID, err := randomUUID()
	if err != nil {
		return viewerDeliverySnapshot, "", nil, err
	}
	metadata, rows, err := protocol.Snapshot(
		plan.target.frame, plan.target.historyEpoch, plan.target.historyRevision,
	)
	if err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	clippedRows := frameContentClippedRows(plan.target.frame)
	for index, row := range rows {
		clippedRows[index] = clippedRows[index] || protocolRowContentClipped(row)
	}
	revision := strconv.FormatUint(plan.target.frame.Revision, 10)
	begin, err := subscription.hostMessage(protocol.SnapshotBegin, protocol.SnapshotBeginPayload{
		SnapshotID: snapshotID, Revision: revision, Screen: metadata,
	})
	if err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	if err := subscription.send(ctx, begin); err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	for start := 0; start < len(rows); {
		// Measure each row once, not every growing prefix. Re-encoding and
		// validating rows[start:end+1] for every row makes snapshots quadratic
		// in the screen height and can exhaust the delivery deadline locally.
		// 1 KiB reserves more than the bounded envelope/UUID/counter overhead.
		end, rowBytes := start, 0
		for end < len(rows) {
			encodedRow, encodeErr := json.Marshal(rows[end])
			if encodeErr != nil {
				return viewerDeliverySnapshot, snapshotID, nil, encodeErr
			}
			if rowBytes+len(encodedRow)+1 > protocol.MaxOutboundMessageBytes-1024 {
				break
			}
			rowBytes += len(encodedRow) + 1
			end++
		}
		if end == start {
			return viewerDeliverySnapshot, snapshotID, nil, errOutboundMessageTooLarge
		}
		message, err := subscription.snapshotRowsMessage(snapshotID, revision, start, rows[start:end])
		if err != nil {
			return viewerDeliverySnapshot, snapshotID, nil, err
		}
		if err := subscription.send(ctx, message); err != nil {
			return viewerDeliverySnapshot, snapshotID, nil, err
		}
		start = end
	}

	latestResponse, err := subscription.session.call(ctx, sessionRequest{
		kind: requestLatestViewerFrame, connection: subscription.connectionID,
		viewer: subscription, deliveryEpoch: plan.epoch,
	})
	if err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	if snapshotCatchupExceedsLimit(plan.target, latestResponse.published, subscription) {
		return viewerDeliverySnapshot, snapshotID, nil, errViewerSnapshotRestart
	}
	end, err := subscription.hostMessage(protocol.SnapshotEnd, protocol.SnapshotEndPayload{
		SnapshotID: snapshotID, Revision: revision, RowCount: int64(plan.target.frame.Rows),
	})
	if err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	if _, err := subscription.session.call(ctx, sessionRequest{
		kind: requestPrepareViewerSnapshot, connection: subscription.connectionID,
		viewer: subscription, deliveryEpoch: plan.epoch, snapshotID: snapshotID,
	}); err != nil {
		return viewerDeliverySnapshot, snapshotID, nil, err
	}
	return viewerDeliverySnapshot, snapshotID, clippedRows, subscription.send(ctx, end)
}

func (subscription *ViewerSubscription) snapshotRowsMessage(
	snapshotID, revision string,
	start int,
	rows []protocol.TerminalRow,
) ([]byte, error) {
	return subscription.hostMessage(protocol.SnapshotRows, protocol.SnapshotRowsPayload{
		SnapshotID: snapshotID, Revision: revision, StartRow: int64(start), Rows: rows,
	})
}

func snapshotCatchupExceedsLimit(base, latest *publishedFrame, subscription *ViewerSubscription) bool {
	if latest == nil || latest.frame.Revision == base.frame.Revision {
		return false
	}
	patch, err := terminal.Diff(base.frame, latest.frame)
	if err != nil {
		return true
	}
	payload, err := protocol.PatchPayload(
		patch, base.frame.HistoryEpoch != patch.HistoryEpoch,
	)
	if err != nil {
		return true
	}
	message, err := subscription.hostMessage(protocol.ScreenPatch, payload)
	return err != nil || len(message) > protocol.MaxOutboundMessageBytes
}

var errOutboundMessageTooLarge = errors.New("broker: outbound viewer message exceeds protocol limit")

func (subscription *ViewerSubscription) hostMessage(messageType protocol.MessageType, payload any) ([]byte, error) {
	return subscription.hostMessageForRequest(messageType, payload, "")
}

func (subscription *ViewerSubscription) hostMessageForRequest(messageType protocol.MessageType, payload any, requestID string) ([]byte, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("broker: encode %s payload: %w", messageType, err)
	}
	if requestID == "" {
		requestID, err = randomUUID()
		if err != nil {
			return nil, err
		}
	}
	sessionID, generation := subscription.session.id, subscription.session.generation
	message, err := json.Marshal(protocol.Envelope{
		Version: protocol.Version, Type: messageType, RequestID: requestID,
		SessionID: &sessionID, Generation: &generation, Payload: payloadJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: encode %s envelope: %w", messageType, err)
	}
	if len(message) > protocol.MaxOutboundMessageBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", errOutboundMessageTooLarge, messageType, len(message))
	}
	if _, err := protocol.DecodeHost(message); err != nil {
		return nil, fmt.Errorf("broker: validate %s envelope: %w", messageType, err)
	}
	return message, nil
}

func (subscription *ViewerSubscription) send(ctx context.Context, message []byte) error {
	if len(message) == 0 || len(message) > protocol.MaxOutboundMessageBytes {
		return errOutboundMessageTooLarge
	}
	if err := subscription.reserveBudget(len(message)); err != nil {
		return err
	}
	return subscription.sendReserved(ctx, message)
}

func (subscription *ViewerSubscription) sendReserved(ctx context.Context, message []byte) error {
	drained := make(chan struct{})
	var once sync.Once
	release := func() {
		once.Do(func() {
			subscription.releaseBudget(len(message))
			close(drained)
		})
	}
	if err := subscription.sink.Send(ctx, message, release); err != nil {
		release()
		return err
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func frameContentClippedRows(frame terminal.Frame) []bool {
	rows := make([]bool, len(frame.Screen))
	for index, row := range frame.Screen {
		rows[index] = terminalRowContentClipped(row)
	}
	return rows
}

func terminalRowContentClipped(row terminal.Row) bool {
	for _, cell := range row.Cells {
		if cell.ContentClipped {
			return true
		}
	}
	return false
}

func protocolRowContentClipped(row protocol.TerminalRow) bool {
	for _, run := range row.Runs {
		for _, cell := range run.Cells {
			if cell.ContentClipped {
				return true
			}
		}
	}
	return false
}

func (subscription *ViewerSubscription) reserveBudget(size int) error {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if subscription.budgetUsed+size > MaxViewerQueueBytes {
		return ErrViewerSlow
	}
	if subscription.budget != nil {
		if err := subscription.budget.reserve(size + 4); err != nil {
			return err
		}
	}
	subscription.budgetUsed += size
	if subscription.budgetUsed > subscription.budgetPeak {
		subscription.budgetPeak = subscription.budgetUsed
	}
	return nil
}

func (subscription *ViewerSubscription) releaseBudget(size int) {
	subscription.mu.Lock()
	subscription.budgetUsed -= size
	if subscription.budget != nil {
		subscription.budget.release(size + 4)
	}
	subscription.mu.Unlock()
}

func (subscription *ViewerSubscription) setDeliveryCancel(epoch uint64, cancel context.CancelCauseFunc) bool {
	subscription.mu.Lock()
	if epoch < subscription.deliveryEpochFloor {
		subscription.mu.Unlock()
		cancel(errViewerDeliveryStale)
		return false
	}
	subscription.deliveryCancel = cancel
	subscription.mu.Unlock()
	return true
}

func (subscription *ViewerSubscription) clearDeliveryCancel() {
	subscription.mu.Lock()
	// Context cancellation functions are not comparable. The worker is the
	// only setter, so clearing the current value is sufficient.
	subscription.deliveryCancel = nil
	subscription.mu.Unlock()
}

func (subscription *ViewerSubscription) invalidateDeliveriesBefore(epoch uint64) {
	subscription.mu.Lock()
	// Reservation precedes worker registration. Remember the actor's reset
	// even if the old delivery has not installed a cancel function yet.
	subscription.deliveryEpochFloor = epoch
	cancel := subscription.deliveryCancel
	subscription.deliveryCancel = nil
	subscription.mu.Unlock()
	if cancel != nil {
		// The stale cause lets a frame already on the wire finish intact.
		cancel(errViewerDeliveryStale)
	}
}

func (subscription *ViewerSubscription) stop(err error) {
	subscription.closeOnce.Do(func() {
		subscription.mu.Lock()
		subscription.err = err
		cancelDelivery := subscription.deliveryCancel
		subscription.mu.Unlock()
		if cancelDelivery != nil {
			cancelDelivery(nil)
		}
		subscription.cancel()
	})
}

func (session *Session) queueViewerDetach(subscription *ViewerSubscription) {
	select {
	case session.viewerEvents <- viewerDetachEvent{subscription: subscription}:
	case <-session.done:
	}
}

func (session *Session) handleViewerDetach(event viewerDetachEvent) {
	subscription := event.subscription
	if subscription == nil {
		return
	}
	viewer, ok := session.viewers[subscription.connectionID]
	if ok && viewer.subscription == subscription {
		session.detachViewer(subscription.connectionID, nil)
		return
	}
	subscription.detachOnce.Do(func() { close(subscription.detached) })
}

func signalViewer(subscription *ViewerSubscription) {
	select {
	case subscription.notify <- struct{}{}:
	default:
	}
}

func (session *Session) capturePublished() error {
	frame, err := session.terminal.ScreenSnapshot(session.ctx)
	if err != nil {
		return err
	}
	session.published = &publishedFrame{
		frame: frame, historyEpoch: frame.HistoryEpoch,
		historyRevision: frame.HistoryRevision,
	}
	for _, viewer := range session.viewers {
		signalViewer(viewer.subscription)
	}
	return nil
}

func (session *Session) attachViewer(request sessionRequest, status SessionStatus) error {
	if request.identity.Generation != session.generation {
		return ErrGenerationChanged
	}
	if session.retiring {
		return ErrSessionNotRunning
	}
	if _, exists := session.viewers[request.connection]; exists {
		return ErrViewerExists
	}
	if len(session.viewers) >= MaxViewersPerSession {
		return fmt.Errorf("%w: maximum %d", ErrViewerLimit, MaxViewersPerSession)
	}
	if request.viewer == nil || request.viewer.connectionID != request.connection || request.viewer.deviceID != request.device {
		return errors.New("broker: invalid viewer registration")
	}
	if err := session.capturePublished(); err != nil {
		return err
	}
	state := &viewerActorState{subscription: request.viewer, forceSnapshot: true}
	session.viewers[request.connection] = state
	if err := session.queueControlNotice(state, session.controller.State(), nil, ""); err != nil {
		session.detachViewer(request.connection, err)
		return err
	}
	if err := session.queueStateNotice(state, status); err != nil {
		session.detachViewer(request.connection, err)
		return err
	}
	signalViewer(request.viewer)
	return nil
}

func (session *Session) reserveViewerDelivery(connectionID string, subscription *ViewerSubscription) (viewerDeliveryPlan, error) {
	viewer, err := session.viewerForRequest(connectionID, subscription)
	if err != nil {
		return viewerDeliveryPlan{}, err
	}
	if viewer.noticeInFlight != nil {
		return viewerDeliveryPlan{}, nil
	}
	if len(viewer.notices) > 0 {
		notice := viewer.notices[0]
		viewer.notices[0] = nil
		viewer.notices = viewer.notices[1:]
		viewer.noticeInFlight = notice
		return viewerDeliveryPlan{kind: viewerDeliveryNotice, notice: notice}, nil
	}
	if viewer.inFlight != nil || viewer.awaiting != nil || session.published == nil {
		return viewerDeliveryPlan{}, nil
	}
	if viewer.base != nil && viewer.base.frame.Revision == session.published.frame.Revision && !viewer.forceSnapshot {
		return viewerDeliveryPlan{}, nil
	}
	if interval := viewer.subscription.frameInterval; interval > 0 {
		// Thumbnail rate limit. Skipping is revision-correct: the next patch is
		// built from this viewer's base to the newest published frame.
		now := time.Now()
		if wait := viewer.nextFrameAt.Sub(now); wait > 0 {
			if viewer.frameTimer == nil {
				subscription := viewer.subscription
				viewer.frameTimer = time.AfterFunc(wait, func() { signalViewer(subscription) })
			} else {
				viewer.frameTimer.Reset(wait)
			}
			return viewerDeliveryPlan{}, nil
		}
		viewer.nextFrameAt = now.Add(interval)
	}
	kind := viewerDeliveryPatch
	if viewer.forceSnapshot || viewer.base == nil ||
		viewer.base.frame.Columns != session.published.frame.Columns ||
		viewer.base.frame.Rows != session.published.frame.Rows {
		kind = viewerDeliverySnapshot
	}
	viewer.epoch++
	viewer.inFlightEpoch = viewer.epoch
	viewer.inFlight = session.published
	return viewerDeliveryPlan{
		kind: kind, epoch: viewer.inFlightEpoch,
		base: viewer.base, target: viewer.inFlight,
		baseClippedRows: slices.Clone(viewer.clippedRows),
	}, nil
}

func (session *Session) completeViewerDelivery(request sessionRequest) error {
	viewer, err := session.viewerForRequest(request.connection, request.viewer)
	if err != nil {
		return err
	}
	if viewer.inFlight == nil || viewer.inFlightEpoch != request.deliveryEpoch {
		return errViewerDeliveryStale
	}
	target := viewer.inFlight
	viewer.inFlight = nil
	earlyApplied := viewer.earlySnapshotApplied
	viewer.sendingSnapshot = nil
	viewer.earlySnapshotApplied = false
	if request.deliveryErr != nil {
		return nil
	}
	switch request.deliveryKind {
	case viewerDeliverySnapshot:
		if request.snapshotID == "" {
			return errors.New("broker: completed snapshot has no identity")
		}
		if earlyApplied {
			viewer.base = target
			viewer.clippedRows = slices.Clone(request.clippedRows)
		} else {
			viewer.awaiting = &viewerSnapshot{
				id: request.snapshotID, target: target,
				clippedRows: slices.Clone(request.clippedRows),
			}
			viewer.ackDeadline = time.Now().Add(viewer.subscription.ackTimeout)
		}
		viewer.forceSnapshot = false
	case viewerDeliveryPatch:
		if viewer.base == nil || viewer.base.frame.Revision >= target.frame.Revision {
			return errors.New("broker: completed patch has an invalid base")
		}
		viewer.base = target
		viewer.clippedRows = slices.Clone(request.clippedRows)
		viewer.forceSnapshot = false
	default:
		return errors.New("broker: completed viewer delivery has invalid kind")
	}
	if viewer.awaiting == nil && session.published != nil && session.published.frame.Revision != target.frame.Revision {
		signalViewer(viewer.subscription)
	}
	session.checkRetirementReady()
	return nil
}

func (session *Session) applyViewerSnapshot(
	connectionID string,
	subscription *ViewerSubscription,
	snapshotID string,
	revision uint64,
) error {
	viewer, err := session.viewerForRequest(connectionID, subscription)
	if err != nil {
		return err
	}
	if viewer.sendingSnapshot != nil && viewer.sendingSnapshot.id == snapshotID && viewer.sendingSnapshot.target.frame.Revision == revision {
		// The sink may have delivered snapshot.end before its drained callback
		// allows the worker to commit delivery. Keep this acknowledgement pending;
		// it cannot unlock patches until completeViewerDelivery confirms the end.
		viewer.earlySnapshotApplied = true
		return nil
	}
	if viewer.awaiting == nil || viewer.awaiting.id != snapshotID || viewer.awaiting.target.frame.Revision != revision {
		session.resetViewer(viewer)
		return ErrViewerSnapshotMismatch
	}
	clearViewerAckDeadline(viewer)
	viewer.base = viewer.awaiting.target
	viewer.clippedRows = slices.Clone(viewer.awaiting.clippedRows)
	viewer.awaiting = nil
	viewer.sendingSnapshot = nil
	viewer.earlySnapshotApplied = false
	if session.published != nil && session.published.frame.Revision != viewer.base.frame.Revision {
		signalViewer(viewer.subscription)
	}
	session.checkRetirementReady()
	return nil
}

// snapshotApplied reports whether the client applied the newest snapshot the
// broker sent or scheduled for it. An early snapshot.applied for the snapshot
// being sent counts; the worker commits it when that delivery completes.
func (viewer *viewerActorState) snapshotApplied() bool {
	if viewer.sendingSnapshot != nil {
		return viewer.earlySnapshotApplied
	}
	return viewer.base != nil && viewer.awaiting == nil && !viewer.forceSnapshot
}

func (session *Session) resyncViewer(connectionID string, subscription *ViewerSubscription) error {
	viewer, err := session.viewerForRequest(connectionID, subscription)
	if err != nil {
		return err
	}
	session.resetViewer(viewer)
	return nil
}

func (session *Session) resetViewer(viewer *viewerActorState) {
	// Invalidate/cancel old deliveries before waking the replacement. A public
	// caller can resume after the replacement has registered its cancel function;
	// it must never perform unscoped cancellation after the actor reply.
	viewer.epoch++
	viewer.subscription.invalidateDeliveriesBefore(viewer.epoch)
	clearViewerAckDeadline(viewer)
	viewer.inFlight = nil
	viewer.awaiting = nil
	viewer.sendingSnapshot = nil
	viewer.earlySnapshotApplied = false
	viewer.base = nil
	viewer.clippedRows = nil
	viewer.forceSnapshot = true
	signalViewer(viewer.subscription)
}

func (session *Session) restartViewerSnapshot(
	connectionID string,
	subscription *ViewerSubscription,
	epoch uint64,
) error {
	viewer, err := session.viewerForRequest(connectionID, subscription)
	if err != nil {
		return err
	}
	if viewer.inFlight == nil || viewer.inFlightEpoch != epoch {
		return errViewerDeliveryStale
	}
	session.resetViewer(viewer)
	return nil
}

func (session *Session) prepareViewerSnapshot(request sessionRequest) error {
	viewer, err := session.viewerForRequest(request.connection, request.viewer)
	if err != nil {
		return err
	}
	if viewer.inFlight == nil || viewer.inFlightEpoch != request.deliveryEpoch {
		return errViewerDeliveryStale
	}
	viewer.sendingSnapshot = &viewerSnapshot{id: request.snapshotID, target: viewer.inFlight}
	viewer.earlySnapshotApplied = false
	return nil
}

func (session *Session) latestViewerFrame(
	connectionID string,
	subscription *ViewerSubscription,
	epoch uint64,
) (*publishedFrame, error) {
	viewer, err := session.viewerForRequest(connectionID, subscription)
	if err != nil {
		return nil, err
	}
	if viewer.inFlight == nil || viewer.inFlightEpoch != epoch {
		return nil, errViewerDeliveryStale
	}
	return session.published, nil
}

func (session *Session) viewerForRequest(
	connectionID string,
	subscription *ViewerSubscription,
) (*viewerActorState, error) {
	viewer, ok := session.viewers[connectionID]
	if !ok || subscription == nil || viewer.subscription != subscription {
		return nil, ErrViewerMissing
	}
	return viewer, nil
}

func (session *Session) detachViewer(connectionID string, err error) {
	viewer, ok := session.viewers[connectionID]
	if !ok {
		return
	}
	delete(session.viewers, connectionID)
	session.controller.Disconnect(connectionID)
	session.inbox.DropConnection(connectionID)
	for _, notice := range viewer.notices {
		viewer.subscription.releaseBudget(len(notice.message))
	}
	viewer.notices = nil
	clearViewerAckDeadline(viewer)
	if viewer.frameTimer != nil {
		viewer.frameTimer.Stop()
	}
	viewer.subscription.stop(err)
	viewer.subscription.detachOnce.Do(func() { close(viewer.subscription.detached) })
	session.checkRetirementReady()
}

func (session *Session) nextViewerAckDeadline() (time.Time, bool) {
	var next time.Time
	for _, viewer := range session.viewers {
		if viewer.awaiting == nil || viewer.ackDeadline.IsZero() {
			continue
		}
		if next.IsZero() || viewer.ackDeadline.Before(next) {
			next = viewer.ackDeadline
		}
	}
	return next, !next.IsZero()
}

func (session *Session) expireViewerAckDeadlines(now time.Time) {
	for connectionID, viewer := range session.viewers {
		if viewer.awaiting == nil || viewer.ackDeadline.IsZero() || viewer.ackDeadline.After(now) {
			continue
		}
		err := fmt.Errorf("%w: %w", ErrViewerSlow, ErrViewerSnapshotTimeout)
		session.detachViewer(connectionID, err)
	}
}

func clearViewerAckDeadline(viewer *viewerActorState) {
	viewer.ackDeadline = time.Time{}
}

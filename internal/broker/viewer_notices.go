package broker

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// viewerNotice is immutable once queued by the actor. Its bytes are reserved
// against the same per-viewer budget as snapshots and patches until drained.
type viewerNotice struct {
	message []byte
}

func (subscription *ViewerSubscription) deliverNotice(notice *viewerNotice) error {
	ctx, cancel := context.WithTimeout(subscription.ctx, subscription.timeout)
	err := subscription.sendReserved(ctx, notice.message)
	cancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%w: notice delivery deadline", ErrViewerSlow)
		}
		return err
	}
	_, completeErr := subscription.session.call(subscription.ctx, sessionRequest{
		kind: requestCompleteViewerNotice, connection: subscription.connectionID,
		viewer: subscription, notice: notice,
	})
	return completeErr
}

func (session *Session) queueViewerNotice(viewer *viewerActorState, messageType protocol.MessageType, payload any, requestID string) error {
	if len(viewer.notices) >= MaxViewerQueuedNotices {
		return fmt.Errorf("%w: too many pending session notices", ErrViewerSlow)
	}
	message, err := viewer.subscription.hostMessageForRequest(messageType, payload, requestID)
	if err != nil {
		return err
	}
	if err := viewer.subscription.reserveBudget(len(message)); err != nil {
		return err
	}
	viewer.notices = append(viewer.notices, &viewerNotice{message: message})
	signalViewer(viewer.subscription)
	return nil
}

func (session *Session) completeViewerNotice(request sessionRequest) error {
	viewer, err := session.viewerForRequest(request.connection, request.viewer)
	if err != nil {
		return err
	}
	if request.notice == nil || viewer.noticeInFlight != request.notice {
		return errViewerDeliveryStale
	}
	viewer.noticeInFlight = nil
	// A single notification may represent several queued notices plus a pending
	// frame. Wake the worker again after each serialized notice has drained.
	signalViewer(viewer.subscription)
	session.checkRetirementReady()
	return nil
}

func (session *Session) queueControlNotice(viewer *viewerActorState, state ControlState, grant *ControlGrant, requestID string) error {
	payload := protocol.ControlChangedPayload{LeaseEpoch: strconv.FormatUint(state.Epoch, 10)}
	if state.ConnectionID != "" {
		connectionID := state.ConnectionID
		payload.ControllerConnectionID = &connectionID
	}
	if grant != nil {
		lease := grant.Lease
		payload.Lease = &lease
	}
	return session.queueViewerNotice(viewer, protocol.ControlChanged, payload, requestID)
}

func (session *Session) queueStateNotice(viewer *viewerActorState, state SessionStatus) error {
	payload := protocol.SessionStatePayload{State: protocol.State(state.State)}
	if state.ExitCode != nil {
		code := int64(*state.ExitCode)
		payload.ExitCode = &code
	}
	if state.State == SessionRunning {
		payload.Agent = agentPayload(session.publishedAgent)
		payload.Previews = previewPayload(session.previews)
		payload.Cwd = cwdPayload(session.cwd.current)
	}
	return session.queueViewerNotice(viewer, protocol.SessionState, payload, "")
}

func (session *Session) publishControlGrant(grant ControlGrant, requestID string) error {
	state := session.controller.State()
	if state.ConnectionID != grant.ConnectionID || state.Epoch != grant.Epoch {
		return ErrNotController
	}
	session.publishedControl = state
	var holderErr error
	for connectionID, viewer := range session.viewers {
		var directedGrant *ControlGrant
		var directedRequestID string
		if requestID != "" && connectionID == grant.ConnectionID {
			directedGrant, directedRequestID = &grant, requestID
		}
		if err := session.queueControlNotice(viewer, state, directedGrant, directedRequestID); err != nil {
			session.detachViewer(connectionID, err)
			if connectionID == grant.ConnectionID {
				holderErr = err
			}
		}
	}
	return holderErr
}

func (session *Session) publishViewerEvents(status SessionStatus) {
	state := session.controller.State()
	previous := session.publishedControl
	session.publishedControl = state
	if previous.ConnectionID != state.ConnectionID || previous.DeviceID != state.DeviceID || previous.Epoch != state.Epoch {
		if previous.ConnectionID != "" {
			session.inbox.DropConnection(previous.ConnectionID)
		}
		for connectionID, viewer := range session.viewers {
			if err := session.queueControlNotice(viewer, state, nil, ""); err != nil {
				session.detachViewer(connectionID, err)
			}
		}
	}
	session.publishSessionState(status)
}

func (session *Session) publishSessionState(status SessionStatus) {
	previous, agent := session.publishedStatus, session.agent.Status()
	if !session.agentRepublish && !session.previewRepublish && !session.cwdRepublish && previous.State == status.State && sameExitCode(previous.ExitCode, status.ExitCode) &&
		(status.State != SessionRunning || agent.State == session.publishedAgent.State &&
			agent.QuestionID == session.publishedAgent.QuestionID) {
		return
	}
	session.publishedStatus = cloneSessionStatus(status)
	session.publishedAgent, session.agentRepublish, session.previewRepublish, session.cwdRepublish = agent, false, false, false
	for connectionID, viewer := range session.viewers {
		if err := session.queueStateNotice(viewer, status); err != nil {
			session.detachViewer(connectionID, err)
		}
	}
}

func sameExitCode(first, second *int) bool {
	return first == nil && second == nil || first != nil && second != nil && *first == *second
}

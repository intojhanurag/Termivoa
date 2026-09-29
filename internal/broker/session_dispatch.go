package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// sessionConnection is one live observer, never a PTY owner. The broker
// generates its connection identity; neither connection ID nor device ID is
// taken from untrusted message fields. A local desktop gets a random device ID.
// A browser stream is bound to a broker-validated credential and uses its
// paired device ID; access is rechecked before every action.
type sessionConnection struct {
	server       *ManagementServer
	writer       *sessionWriter
	access       *auth.Binding
	connectionID string
	deviceID     string
	sessionID    string
	generation   string
	session      ManagedSession
	viewer       *ViewerSubscription
	changesBusy  atomic.Bool // A changes request is running (changes.go).
	tasksBusy    atomic.Bool // A tasks request is running (tasks.go).
}

// errStreamRevoked ends a browser stream after access.revoked was sent.
var errStreamRevoked = errors.New("broker: browser stream access ended")

func (server *ManagementServer) serveSessionConnection(
	ctx context.Context, conn net.Conn, reader *ipc.FrameReader,
	peer protocol.IPCHelloClass, hello protocol.Envelope, binding *auth.Binding,
) error {
	if binding != nil {
		// Registered first so it runs last: the viewer is closed (lease
		// released, queued input dropped) and the connection closed before a
		// waiting revocation is told the stream is gone.
		defer binding.Release()
	}
	helloPayload, err := protocol.DecodePayload[protocol.HelloPayload](hello)
	if err != nil {
		return err
	}
	stream := &sessionConnection{server: server, writer: newSessionWriter(conn), access: binding}
	desktop := binding == nil && peer.ClientKind == protocol.CLI && helloPayload.ClientKind == protocol.Desktop
	browser := binding != nil && peer.ClientKind == protocol.Gateway && helloPayload.ClientKind == protocol.Browser
	if !desktop && !browser {
		return stream.failure(ctx, hello, protocol.Unauthorized,
			"live access requires a local CLI desktop or a credential-bound gateway browser")
	}
	stream.connectionID, err = randomUUID()
	if err != nil {
		return err
	}
	if browser {
		stream.deviceID = binding.Principal().DeviceID
		// Revocation, logout or expiry interrupts a peer blocked in ReadFrame;
		// the dispatch goroutine then performs the ordered cleanup itself.
		stopWatch := make(chan struct{})
		defer close(stopWatch)
		go func() {
			select {
			case <-binding.Revoked():
				_ = conn.SetReadDeadline(time.Now())
			case <-stopWatch:
			}
		}()
	} else if stream.deviceID, err = randomUUID(); err != nil {
		return err
	}
	if err := stream.send(ctx, hello, protocol.Welcome, protocol.WelcomePayload{
		BrokerGeneration: server.config.BrokerGeneration, HostBuild: server.config.HostBuild,
		NegotiatedVersion: protocol.Version, CapabilityProfile: protocol.PhoneTerminalV1,
		Limits: protocol.ProtocolLimits{
			InboundMessageBytes: protocol.MaxInboundMessageBytes, OutboundMessageBytes: protocol.MaxOutboundMessageBytes,
			OutgoingQueueBytes: MaxViewerQueueBytes, MaxTextBytes: protocol.MaxTextBytes, MaxPasteBytes: protocol.MaxPasteBytes,
			MinColumns: protocol.MinColumns, MaxColumns: protocol.MaxColumns, MinRows: protocol.MinRows, MaxRows: protocol.MaxRows,
			MaxHistoryPageRows: protocol.MaxHistoryPageRows,
		},
	}); err != nil {
		return err
	}
	defer func() {
		if stream.viewer != nil {
			_ = stream.viewer.Close()
		}
	}()
	for {
		data, err := reader.ReadFrame()
		if err != nil {
			if stream.revoked() {
				return stream.endAccess(ctx, conn)
			}
			return err
		}
		request, err := protocol.DecodeClient(data)
		if err != nil {
			return errors.Join(err, stream.rejectMalformed(ctx))
		}
		if stream.revoked() {
			return stream.endAccess(ctx, conn)
		}
		if request.Type == protocol.Hello {
			return stream.failure(ctx, request, protocol.ProtocolMismatch, "hello is only valid at connection start")
		}
		if err := stream.dispatch(ctx, request); err != nil {
			if errors.Is(err, errStreamRevoked) {
				return stream.endAccess(ctx, conn)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (stream *sessionConnection) revoked() bool {
	return stream.access != nil && stream.access.Check() != nil
}

// endAccess sends access.revoked as the stream's last message, then closes the
// viewer (releasing its lease and queued input) and the connection. Delivery of
// the notice is best effort; the closure is not.
func (stream *sessionConnection) endAccess(ctx context.Context, conn net.Conn) error {
	message := "browser access was revoked"
	if errors.Is(stream.access.Check(), auth.ErrExpired) {
		message = "browser access expired; pair this device again"
	}
	var sendErr error
	requestID, err := randomUUID()
	if err == nil {
		payload, _ := json.Marshal(protocol.AccessRevokedPayload{Message: message})
		var notice []byte
		notice, err = json.Marshal(protocol.Envelope{
			Version: protocol.Version, Type: protocol.AccessRevoked, RequestID: requestID, Payload: payload,
		})
		if err == nil {
			_ = conn.SetReadDeadline(time.Time{})
			sendErr = stream.writer.Send(ctx, notice, func() {})
		}
	}
	if stream.viewer != nil {
		_ = stream.viewer.Close()
		stream.viewer = nil
	}
	_ = conn.Close()
	_ = sendErr // The peer may already be gone; revocation does not depend on it.
	return err
}

func (stream *sessionConnection) dispatch(ctx context.Context, request protocol.Envelope) error {
	if stream.revoked() {
		return errStreamRevoked
	}
	if request.Type == protocol.SessionAttach {
		return stream.attach(ctx, request)
	}
	if stream.viewer == nil || *request.SessionID != stream.sessionID {
		return stream.failure(ctx, request, protocol.SessionMissing, "attach to the selected session first")
	}
	if *request.Generation != stream.generation {
		return stream.failure(ctx, request, protocol.GenerationChanged, "session generation changed")
	}
	identity := func(lease string) MutationIdentity {
		identity := MutationIdentity{Generation: stream.generation, ConnectionID: stream.connectionID, DeviceID: stream.deviceID, Lease: lease}
		if stream.access != nil {
			identity.Access = stream.access
		}
		return identity
	}
	var err error
	switch request.Type {
	case protocol.SnapshotApplied:
		payload, _ := protocol.DecodePayload[protocol.SnapshotAppliedPayload](request)
		revision, _ := strconv.ParseUint(payload.Revision, 10, 64)
		err = stream.viewer.SnapshotApplied(ctx, payload.SnapshotID, revision)
	case protocol.ScreenResync:
		err = stream.viewer.Resync(ctx)
	case protocol.ControlAcquire:
		// The actor rejects a viewer whose current snapshot is unapplied and
		// queues the holder-only grant in the same ordered stream as
		// takeover/release notices. Sending it here could overtake a newer lease.
		_, err = stream.session.AcquireControlForRequest(ctx, stream.viewer, request.RequestID)
	case protocol.ControlRelease:
		payload, _ := protocol.DecodePayload[protocol.LeasePayload](request)
		err = stream.session.ReleaseControl(ctx, identity(payload.Lease))
	case protocol.Heartbeat:
		payload, _ := protocol.DecodePayload[protocol.HeartbeatPayload](request)
		if payload.Lease != nil {
			_, err = stream.session.Heartbeat(ctx, identity(*payload.Lease))
		}
	case protocol.TerminalResize:
		payload, _ := protocol.DecodePayload[protocol.TerminalResizePayload](request)
		err = stream.session.Resize(ctx, identity(payload.Lease), int(payload.Columns), int(payload.Rows))
	case protocol.InputKey, protocol.InputText, protocol.InputPaste, protocol.InputMouse, protocol.InputAnswer:
		lease, sequence, action := sessionInput(request)
		counter, _ := strconv.ParseUint(sequence, 10, 64)
		_, err = stream.session.SubmitInput(ctx, identity(lease), counter, action)
		if err == nil {
			return stream.send(ctx, request, protocol.InputAccepted, protocol.InputAcceptedPayload{InputSequence: sequence})
		}
		failure := classifySessionError(err)
		return stream.send(ctx, request, protocol.InputRejected, protocol.InputRejectedPayload{
			Code: failure.code, Message: failure.message, InputSequence: &sequence,
		})
	case protocol.HistoryRead:
		payload, _ := protocol.DecodePayload[protocol.HistoryReadPayload](request)
		epoch, _ := strconv.ParseUint(payload.HistoryEpoch, 10, 64)
		revision, _ := strconv.ParseUint(payload.HistoryRevision, 10, 64)
		var result terminal.HistoryReadResult
		result, err = stream.session.ReadHistory(ctx, stream.generation, terminal.HistoryReadRequest{
			Epoch: epoch, Revision: revision, Offset: int(payload.Offset), Limit: int(payload.Limit),
		})
		if err == nil {
			if result.Changed {
				changed, convertErr := protocol.HistoryChangedFrom(result)
				if convertErr != nil {
					err = convertErr
					break
				}
				return stream.send(ctx, request, protocol.MessageTypeHistoryChanged, changed)
			}
			page, convertErr := protocol.HistoryPageFrom(result)
			if convertErr != nil {
				err = convertErr
				break
			}
			return stream.send(ctx, request, protocol.HistoryPage, page)
		}
	case protocol.ChangesList, protocol.ChangesFile:
		return stream.readChanges(ctx, request)
	case protocol.ChangesCommit, protocol.ChangesPush:
		return stream.writeChanges(ctx, request, identity)
	case protocol.TasksList:
		return stream.listTasks(ctx, request)
	case protocol.SessionDetach:
		return io.EOF // outer connection close releases the viewer, never the PTY
	default:
		return stream.failure(ctx, request, protocol.ProtocolMismatch, "message is not valid in this session stream")
	}
	if err != nil {
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	return nil
}

func (stream *sessionConnection) attach(ctx context.Context, request protocol.Envelope) error {
	if stream.viewer != nil {
		return stream.failure(ctx, request, protocol.InvalidRequest, "detach before attaching another session")
	}
	session, err := stream.server.config.Registry.Get(*request.SessionID)
	if err == nil {
		config := ViewerConfig{
			ConnectionID: stream.connectionID, DeviceID: stream.deviceID, Generation: *request.Generation,
			Sink: reservedViewerSink{writer: stream.writer}, budget: stream.writer,
		}
		if stream.access != nil {
			config.Access = stream.access
		}
		stream.viewer, err = session.AttachViewer(ctx, config)
	}
	if err != nil {
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	if stream.revoked() {
		// A revocation that raced this attachment could not see the new
		// viewer; recheck after registration so it cannot outlive access.
		return errStreamRevoked
	}
	stream.session, stream.sessionID, stream.generation = session, *request.SessionID, *request.Generation
	// Retirement, a stalled viewer, or revocation must also interrupt a peer
	// blocked in ReadFrame. Wait for the stop signal, not Done: a superseded
	// frame may still hold the writer until its deadline. This goroutine ends
	// with the viewer on disconnect.
	viewer := stream.viewer
	go func() { <-viewer.ctx.Done(); _ = stream.writer.conn.Close() }()
	return nil
}

func sessionInput(request protocol.Envelope) (lease, sequence string, action InputAction) {
	switch request.Type {
	case protocol.InputText:
		p, _ := protocol.DecodePayload[protocol.InputTextPayload](request)
		return p.Lease, p.InputSequence, InputAction{Kind: InputKindText, Text: p.Text}
	case protocol.InputPaste:
		p, _ := protocol.DecodePayload[protocol.InputPastePayload](request)
		return p.Lease, p.InputSequence, InputAction{Kind: InputKindPaste, Text: p.Text}
	case protocol.InputKey:
		p, _ := protocol.DecodePayload[protocol.InputKeyPayload](request)
		return p.Lease, p.InputSequence, InputAction{Kind: InputKindKey, Key: p.Key, Character: p.Character, Modifiers: p.Modifiers}
	case protocol.InputMouse:
		p, _ := protocol.DecodePayload[protocol.InputMousePayload](request)
		return p.Lease, p.InputSequence, InputAction{Kind: InputKindMouse, MouseKind: p.Kind, Button: p.Button, Column: p.Column, Row: p.Row, Modifiers: p.Modifiers}
	case protocol.InputAnswer:
		p, _ := protocol.DecodePayload[protocol.InputAnswerPayload](request)
		return p.Lease, p.InputSequence, InputAction{Kind: InputKindAnswer, QuestionID: p.QuestionID, OptionIndex: int(p.OptionIndex)}
	}
	panic("broker: input conversion called for non-input message")
}

func classifySessionError(err error) managementFailure {
	switch {
	case errors.Is(err, ErrAccessRevoked):
		return managementFailure{protocol.ErrorCodeRevoked, "browser access was revoked or expired"}
	case errors.Is(err, ErrNotController):
		return managementFailure{protocol.NotController, "take control before sending input"}
	case errors.Is(err, ErrStaleLease):
		return managementFailure{protocol.StaleLease, "controller lease is no longer valid"}
	case errors.Is(err, ErrPreviewPort):
		return managementFailure{protocol.PreviewMissing, "no dev server of this session listens on that port"}
	case errors.Is(err, ErrQuestionChanged):
		return managementFailure{protocol.QuestionChanged, "the question on screen changed; check it and answer again"}
	case errors.Is(err, errAnswerOption):
		return managementFailure{protocol.InvalidRequest, "that answer is not offered"}
	case errors.Is(err, ErrInputGap):
		return managementFailure{protocol.InputGap, "input sequence has a gap; check the terminal before sending again"}
	case errors.Is(err, ErrViewerSnapshotPending):
		return managementFailure{protocol.ResyncRequired, "apply the current snapshot before taking control"}
	case errors.Is(err, ErrViewerSnapshotMismatch):
		return managementFailure{protocol.ResyncRequired, "snapshot acknowledgement does not match; request a new snapshot"}
	case errors.Is(err, ErrViewerMissing):
		return managementFailure{protocol.SessionMissing, "viewer is no longer attached"}
	case errors.Is(err, ErrInputQueueFull), errors.Is(err, ErrViewerLimit):
		return managementFailure{protocol.LimitExceeded, "session resource limit reached"}
	case errors.Is(err, ErrSessionClosed), errors.Is(err, ErrSessionRetired), errors.Is(err, ErrSessionNotRunning):
		return managementFailure{protocol.SessionLost, "session process is no longer running"}
	default:
		if failure, ok := classifyUploadError(err); ok {
			return failure
		}
		return classifyManagementError(err)
	}
}

func (stream *sessionConnection) failure(ctx context.Context, request protocol.Envelope, code protocol.ErrorCode, message string) error {
	if request.Type == protocol.InputKey || request.Type == protocol.InputText || request.Type == protocol.InputPaste ||
		request.Type == protocol.InputMouse || request.Type == protocol.InputAnswer {
		_, sequence, _ := sessionInput(request)
		return stream.send(ctx, request, protocol.InputRejected, protocol.InputRejectedPayload{Code: code, Message: message, InputSequence: &sequence})
	}
	return stream.send(ctx, request, protocol.Error, protocol.ErrorPayload{Code: code, Message: message})
}

// rejectMalformed tells the client why the stream is closing before the
// connection ends. The request ID is fresh because the original is untrusted.
func (stream *sessionConnection) rejectMalformed(ctx context.Context) error {
	requestID, err := randomUUID()
	if err != nil {
		return err
	}
	return stream.send(ctx, protocol.Envelope{RequestID: requestID}, protocol.Error,
		protocol.ErrorPayload{Code: protocol.InvalidRequest, Message: "message does not match protocol v1; reconnecting is required"})
}

func (stream *sessionConnection) send(ctx context.Context, request protocol.Envelope, kind protocol.MessageType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	message, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: kind, RequestID: request.RequestID,
		SessionID: request.SessionID, Generation: request.Generation, Payload: data})
	if err != nil {
		return err
	}
	if _, err := protocol.DecodeHost(message); err != nil {
		return err
	}
	return stream.writer.Send(ctx, message, func() {})
}

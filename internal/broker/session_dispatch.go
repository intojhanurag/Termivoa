package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// sessionConnection is one local desktop observer, never a PTY owner. The
// broker generates its identity; neither connection ID nor device ID is taken
// from untrusted message fields. Browser access will require the distinct
// broker-authorized pairing boundary, not a gateway-supplied desktop identity.
type sessionConnection struct {
	server          *ManagementServer
	writer          *sessionWriter
	connectionID    string
	deviceID        string
	sessionID       string
	generation      string
	session         ManagedSession
	viewer          *ViewerSubscription
	snapshotApplied bool
}

func (server *ManagementServer) serveSessionConnection(
	ctx context.Context, conn net.Conn, reader *ipc.FrameReader,
	peer protocol.IPCHelloClass, hello protocol.Envelope,
) error {
	helloPayload, err := protocol.DecodePayload[protocol.HelloPayload](hello)
	if err != nil {
		return err
	}
	stream := &sessionConnection{server: server, writer: newSessionWriter(conn)}
	if peer.ClientKind != protocol.CLI || helloPayload.ClientKind != protocol.Desktop {
		return stream.failure(ctx, hello, protocol.Unauthorized, "local desktop access requires a restricted CLI connection")
	}
	stream.connectionID, err = randomUUID()
	if err != nil {
		return err
	}
	stream.deviceID, err = randomUUID()
	if err != nil {
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
			return err
		}
		request, err := protocol.DecodeClient(data)
		if err != nil {
			return err
		}
		if request.Type == protocol.Hello {
			return stream.failure(ctx, request, protocol.ProtocolMismatch, "hello is only valid at connection start")
		}
		if err := stream.dispatch(ctx, request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (stream *sessionConnection) dispatch(ctx context.Context, request protocol.Envelope) error {
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
		return MutationIdentity{Generation: stream.generation, ConnectionID: stream.connectionID, DeviceID: stream.deviceID, Lease: lease}
	}
	var err error
	switch request.Type {
	case protocol.SnapshotApplied:
		payload, _ := protocol.DecodePayload[protocol.SnapshotAppliedPayload](request)
		revision, _ := strconv.ParseUint(payload.Revision, 10, 64)
		err = stream.viewer.SnapshotApplied(ctx, payload.SnapshotID, revision)
		stream.snapshotApplied = err == nil
	case protocol.ScreenResync:
		stream.snapshotApplied = false
		err = stream.viewer.Resync(ctx)
	case protocol.ControlAcquire:
		if !stream.snapshotApplied {
			return stream.failure(ctx, request, protocol.ResyncRequired, "apply the current snapshot before taking control")
		}
		// The actor queues the holder-only grant in the same ordered stream as
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
	case protocol.InputKey, protocol.InputText, protocol.InputPaste, protocol.InputMouse:
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
		stream.viewer, err = session.AttachViewer(ctx, ViewerConfig{
			ConnectionID: stream.connectionID, DeviceID: stream.deviceID, Generation: *request.Generation,
			Sink: reservedViewerSink{writer: stream.writer}, budget: stream.writer,
		})
	}
	if err != nil {
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	stream.session, stream.sessionID, stream.generation = session, *request.SessionID, *request.Generation
	// Retirement, a stalled viewer, or revocation must also interrupt a peer
	// blocked in ReadFrame. This goroutine ends with the viewer on disconnect.
	go func() { <-stream.viewer.Done(); _ = stream.writer.conn.Close() }()
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
	}
	panic("broker: input conversion called for non-input message")
}

func classifySessionError(err error) managementFailure {
	switch {
	case errors.Is(err, ErrNotController):
		return managementFailure{protocol.NotController, "take control before sending input"}
	case errors.Is(err, ErrStaleLease):
		return managementFailure{protocol.StaleLease, "controller lease is no longer valid"}
	case errors.Is(err, ErrInputGap):
		return managementFailure{protocol.InputGap, "input sequence has a gap; check the terminal before sending again"}
	case errors.Is(err, ErrViewerSnapshotMismatch):
		return managementFailure{protocol.ResyncRequired, "snapshot acknowledgement does not match; request a new snapshot"}
	case errors.Is(err, ErrViewerMissing):
		return managementFailure{protocol.SessionMissing, "viewer is no longer attached"}
	case errors.Is(err, ErrInputQueueFull), errors.Is(err, ErrViewerLimit):
		return managementFailure{protocol.LimitExceeded, "session resource limit reached"}
	case errors.Is(err, ErrSessionClosed), errors.Is(err, ErrSessionRetired), errors.Is(err, ErrSessionNotRunning):
		return managementFailure{protocol.SessionLost, "session process is no longer running"}
	default:
		return classifyManagementError(err)
	}
}

func (stream *sessionConnection) failure(ctx context.Context, request protocol.Envelope, code protocol.ErrorCode, message string) error {
	if request.Type == protocol.InputKey || request.Type == protocol.InputText || request.Type == protocol.InputPaste || request.Type == protocol.InputMouse {
		_, sequence, _ := sessionInput(request)
		return stream.send(ctx, request, protocol.InputRejected, protocol.InputRejectedPayload{Code: code, Message: message, InputSequence: &sequence})
	}
	return stream.send(ctx, request, protocol.Error, protocol.ErrorPayload{Code: code, Message: message})
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

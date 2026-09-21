package liveclient

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func (client *Client) readLoop() {
	defer client.wg.Done()
	for {
		data, err := client.reader.ReadFrame()
		if err != nil {
			// The broker retires after final frames have reached the transport.
			// Preserve queued complete patches on orderly process exit; ordinary
			// connection loss still disables sending and cancels paint immediately.
			client.mu.Lock()
			ended := client.state.Lifecycle == protocol.Exited && (client.pending == nil || client.pending.completed)
			detaching := client.detaching
			client.mu.Unlock()
			if detaching && errors.Is(err, io.EOF) {
				client.stop(nil)
				return
			}
			if ended && errors.Is(err, io.EOF) {
				client.readFinished <- err
				return
			}
			client.stop(err)
			return
		}
		message, err := protocol.DecodeHost(data)
		if err == nil {
			err = client.receive(message, len(data))
		}
		if err != nil {
			client.stop(err)
			return
		}
	}
}

func (client *Client) receive(message protocol.Envelope, size int) error {
	if message.SessionID != nil && *message.SessionID != client.config.SessionID {
		return fmt.Errorf("%w: wrong session", ErrProtocol)
	}
	if message.Generation != nil && *message.Generation != client.config.Generation {
		return ErrGenerationChanged
	}
	switch message.Type {
	case protocol.SnapshotBegin, protocol.SnapshotRows, protocol.SnapshotEnd, protocol.ScreenPatch:
		client.mu.Lock()
		if message.Type == protocol.SnapshotBegin {
			client.frameSerial++
			client.state.Ready = false
			client.changedLocked()
		}
		if client.queuedBytes+size > maxFrameQueueBytes {
			client.mu.Unlock()
			return ErrSlowDisplay
		}
		client.queuedBytes += size
		serial := client.frameSerial
		client.mu.Unlock()
		select {
		case client.frameQueue <- frameMessage{envelope: message, size: size, serial: serial}:
			return nil
		default:
			client.mu.Lock()
			client.queuedBytes -= size
			client.mu.Unlock()
			return ErrSlowDisplay
		}
	case protocol.ControlChanged:
		return client.controlChanged(message)
	case protocol.InputAccepted, protocol.InputRejected:
		return client.resolveInput(message)
	case protocol.SessionState:
		payload, _ := protocol.DecodePayload[protocol.SessionStatePayload](message)
		client.mu.Lock()
		client.state.Lifecycle, client.state.ExitCode = payload.State, payload.ExitCode
		if payload.State != protocol.Running {
			client.lease, client.state.Controller = "", false
		}
		client.changedLocked()
		client.mu.Unlock()
		return nil
	case protocol.AccessRevoked:
		payload, _ := protocol.DecodePayload[protocol.AccessRevokedPayload](message)
		return remoteError(protocol.Revoked, payload.Message)
	case protocol.Error:
		return client.hostError(message)
	default:
		return fmt.Errorf("%w: unexpected %s", ErrProtocol, message.Type)
	}
}

func (client *Client) controlChanged(message protocol.Envelope) error {
	payload, _ := protocol.DecodePayload[protocol.ControlChangedPayload](message)
	epoch, _ := strconv.ParseUint(payload.LeaseEpoch, 10, 64)
	client.mu.Lock()
	defer client.mu.Unlock()
	pending := client.pending
	if payload.Lease != nil && (pending == nil || pending.kind != protocol.ControlAcquire || pending.id != message.RequestID) {
		return fmt.Errorf("%w: unsolicited controller grant", ErrProtocol)
	}
	if payload.Lease != nil && epoch <= client.leaseEpoch {
		return fmt.Errorf("%w: obsolete controller grant", ErrProtocol)
	}
	if epoch < client.leaseEpoch {
		return nil
	} // never restore an obsolete holder
	connectionID, label := "", ""
	if payload.ControllerConnectionID != nil {
		connectionID = *payload.ControllerConnectionID
	}
	if payload.ControllerLabel != nil {
		label = displayText(*payload.ControllerLabel)
	}
	if epoch != client.leaseEpoch || connectionID != client.state.ControllerConnectionID || connectionID == "" {
		client.lease, client.state.Controller = "", false
	}
	client.leaseEpoch = epoch
	client.state.LeaseEpoch, client.state.ControllerConnectionID, client.state.ControllerLabel = payload.LeaseEpoch, connectionID, label
	if payload.Lease != nil {
		if pending.completed || epoch <= pending.epoch || connectionID == "" {
			return fmt.Errorf("%w: controller grant does not advance lease", ErrProtocol)
		}
		client.lease, client.state.Controller, client.sequence = *payload.Lease, true, 0
		pending.completed = true
		select {
		case pending.result <- nil:
		default:
			return fmt.Errorf("%w: repeated control grant", ErrProtocol)
		}
	}
	client.changedLocked()
	return nil
}

func (client *Client) hostError(message protocol.Envelope) error {
	payload, _ := protocol.DecodePayload[protocol.ErrorPayload](message)
	err := remoteError(payload.Code, payload.Message)
	if payload.Code == protocol.GenerationChanged {
		return errors.Join(ErrGenerationChanged, err)
	}
	if payload.Code == protocol.Revoked || payload.Code == protocol.SessionLost || payload.Code == protocol.SessionMissing || payload.Code == protocol.Unauthorized {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if payload.Code == protocol.ResyncRequired {
		client.state.Ready = false
	}
	if pending := client.pending; pending != nil && pending.id == message.RequestID {
		if pending.sequence != "" || pending.completed {
			return fmt.Errorf("%w: invalid operation error", ErrProtocol)
		}
		client.remoteErrorLocked(err, pending.lease, pending.epoch)
		pending.completed = true
		select {
		case pending.result <- err:
		default:
			return fmt.Errorf("%w: duplicate error", ErrProtocol)
		}
		return nil
	}
	for _, sent := range client.recent {
		if sent.id == message.RequestID {
			client.remoteErrorLocked(err, sent.lease, sent.epoch)
			return nil
		}
	}
	return fmt.Errorf("%w: uncorrelated error", ErrProtocol)
}

// paintLoop is the only FrameClient caller. The reader can process takeover
// and revocation even while Output is blocked; frame retention remains bounded.
func (client *Client) paintLoop() {
	defer client.wg.Done()
	defer func() {
		for {
			select {
			case frame := <-client.frameQueue:
				client.mu.Lock()
				client.queuedBytes -= frame.size
				client.mu.Unlock()
			default:
				return
			}
		}
	}()
	assembling, waitingSnapshot := false, true
	var snapshotSerial uint64
	var snapshotScreen protocol.ScreenMetadata
	var readErr error
	for {
		if readErr != nil && len(client.frameQueue) == 0 {
			client.stop(nil)
			return
		}
		select {
		case <-client.ctx.Done():
			return
		case readErr = <-client.readFinished:
			continue
		case frame := <-client.frameQueue:
			if client.ctx.Err() != nil {
				client.mu.Lock()
				client.queuedBytes -= frame.size
				client.mu.Unlock()
				return
			}
			message := frame.envelope
			var err error
			switch message.Type {
			case protocol.SnapshotBegin:
				payload, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](message)
				err = client.frames.BeginSnapshot(client.config.Generation, payload)
				if err == nil {
					snapshotScreen = payload.Screen
				}
				assembling, waitingSnapshot = err == nil, false
				snapshotSerial = frame.serial
			case protocol.SnapshotRows:
				if !waitingSnapshot {
					payload, _ := protocol.DecodePayload[protocol.SnapshotRowsPayload](message)
					err = client.frames.AddSnapshotRows(client.config.Generation, payload)
				}
			case protocol.SnapshotEnd:
				if !waitingSnapshot {
					payload, _ := protocol.DecodePayload[protocol.SnapshotEndPayload](message)
					if client.config.ValidateScreen != nil {
						err = client.config.ValidateScreen(snapshotScreen)
					}
					if err == nil {
						err = client.frames.EndSnapshot(client.config.Generation, payload)
					}
					if err == nil {
						err = client.oneWay(client.ctx, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: payload.SnapshotID, Revision: payload.Revision}, "", 0)
						if err == nil {
							client.mu.Lock()
							if client.state.Connected && snapshotSerial == client.frameSerial {
								client.state.Ready = true
								client.changedLocked()
							}
							client.mu.Unlock()
						}
					}
					assembling = false
				}
			case protocol.ScreenPatch:
				if !waitingSnapshot {
					if assembling {
						err = desktop.ErrResyncRequired
					} else {
						payload, _ := protocol.DecodePayload[protocol.ScreenPatchPayload](message)
						if client.config.ValidateScreen != nil {
							err = client.config.ValidateScreen(payload.Screen)
						}
						if err == nil {
							err = client.frames.ApplyPatch(client.config.Generation, payload)
						}
					}
				}
			}
			client.mu.Lock()
			client.queuedBytes -= frame.size
			client.mu.Unlock()
			if errors.Is(err, desktop.ErrResyncRequired) {
				assembling, waitingSnapshot = false, true
				client.mu.Lock()
				client.state.Ready = false
				client.changedLocked()
				client.mu.Unlock()
				err = client.oneWay(client.ctx, protocol.ScreenResync, protocol.ScreenResyncPayload{Reason: protocol.BaseMismatch}, "", 0)
			}
			if err != nil {
				client.stop(err)
				return
			}
		}
	}
}

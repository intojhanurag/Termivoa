package liveclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var _ desktop.Transport = (*Client)(nil)

func (client *Client) beginUser(ctx context.Context) error {
	if ctx == nil {
		return errors.New("liveclient: nil action context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case client.userGate <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

func (client *Client) readyIdentity(controller bool) (string, uint64, uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.state.Connected {
		return "", 0, 0, ErrClosed
	}
	if !client.state.Ready {
		return "", 0, 0, ErrNotReady
	}
	if controller && client.lease == "" {
		return "", 0, 0, ErrNotController
	}
	return client.lease, client.leaseEpoch, client.sequence, nil
}

// AcquireControl is always explicit. A successful grant must correlate to
// this request; public controller notices never silently grant control.
func (client *Client) AcquireControl(ctx context.Context) error {
	if err := client.beginUser(ctx); err != nil {
		return err
	}
	defer func() { <-client.userGate }()
	_, epoch, _, err := client.readyIdentity(false)
	if err != nil {
		return err
	}
	message, err := encode(protocol.ControlAcquire, client.config.SessionID, client.config.Generation, protocol.EmptyPayload{})
	if err != nil {
		return err
	}
	return client.request(ctx, message, "", epoch, "")
}

// Send waits for broker acceptance, never retries, and rejects overlapping
// user calls instead of retaining queued keystrokes for a later prompt.
func (client *Client) Send(ctx context.Context, action desktop.InputAction) error {
	return client.send(ctx, action, 0)
}

// SendAdmitted refuses a decoded action from an earlier local readiness or
// lease epoch. Zero is never an admissible captured epoch.
func (client *Client) SendAdmitted(ctx context.Context, action desktop.InputAction, inputEpoch uint64) error {
	if inputEpoch == 0 {
		return ErrNotReady
	}
	return client.send(ctx, action, inputEpoch)
}

func (client *Client) send(ctx context.Context, action desktop.InputAction, inputEpoch uint64) error {
	if err := client.beginUser(ctx); err != nil {
		return err
	}
	defer func() { <-client.userGate }()
	lease, epoch, sequence, err := client.readyIdentity(true)
	if err != nil {
		return err
	}
	if sequence == math.MaxUint64 {
		return errors.New("liveclient: input sequence exhausted; explicitly acquire a new lease")
	}
	counter := strconv.FormatUint(sequence+1, 10)
	kind, payload, err := inputPayload(action, lease, counter)
	if err != nil {
		return err
	}
	message, err := encode(kind, client.config.SessionID, client.config.Generation, payload)
	if err != nil {
		return err
	}
	return client.requestAdmitted(ctx, message, counter, epoch, lease, inputEpoch)
}

func inputPayload(action desktop.InputAction, lease, sequence string) (protocol.MessageType, any, error) {
	switch value := action.(type) {
	case desktop.TextAction:
		return protocol.InputText, protocol.InputTextPayload{Lease: lease, InputSequence: sequence, Text: value.Text}, nil
	case desktop.PasteAction:
		return protocol.InputPaste, protocol.InputPastePayload{Lease: lease, InputSequence: sequence, Text: value.Text}, nil
	case desktop.KeyAction:
		var character *string
		if value.Character != "" {
			character = &value.Character
		}
		return protocol.InputKey, protocol.InputKeyPayload{Lease: lease, InputSequence: sequence, Key: value.Key, Character: character, Modifiers: value.Modifiers}, nil
	case desktop.MouseAction:
		return protocol.InputMouse, protocol.InputMousePayload{Lease: lease, InputSequence: sequence, Kind: value.Kind, Button: value.Button, Column: value.Column, Row: value.Row, Modifiers: value.Modifiers}, nil
	default:
		return "", nil, errors.New("liveclient: unsupported input action")
	}
}

func (client *Client) request(ctx context.Context, message protocol.Envelope, sequence string, epoch uint64, lease string) error {
	return client.requestAdmitted(ctx, message, sequence, epoch, lease, 0)
}

func (client *Client) requestAdmitted(ctx context.Context, message protocol.Envelope, sequence string, epoch uint64, lease string, inputEpoch uint64) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	pending := &pendingRequest{id: message.RequestID, kind: message.Type, sequence: sequence, epoch: epoch, lease: lease, inputEpoch: inputEpoch, result: make(chan error, 1)}
	client.mu.Lock()
	if !client.state.Connected {
		client.mu.Unlock()
		return client.closedError()
	}
	client.pending = pending
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		if client.pending == pending {
			client.pending = nil
		}
		client.mu.Unlock()
	}()
	uncertain := func(err error) error {
		select {
		case <-client.done:
			// The connection already failed; a concurrent write error such as
			// a closed pipe must not hide the recorded root cause.
			err = client.closedError()
		default:
		}
		client.mu.Lock()
		attempted := pending.attempted
		client.mu.Unlock()
		if sequence != "" && attempted && !errors.Is(err, ErrDeliveryUncertain) {
			err = errors.Join(ErrDeliveryUncertain, err)
		}
		client.stop(err)
		return err
	}
	if err := client.write(ctx, message); err != nil {
		// A local epoch rejection occurs before writing/acceptance and does
		// not break the connection or create delivery uncertainty.
		if err == ErrNotReady && inputEpoch != 0 {
			return err
		}
		return uncertain(err)
	}
	select {
	case err := <-pending.result:
		return err
	case <-ctx.Done():
		select {
		case err := <-pending.result:
			return err
		default:
		}
		return uncertain(ctx.Err())
	case <-client.done:
		select {
		case err := <-pending.result:
			return err
		default:
		}
		return uncertain(client.closedError())
	}
}

// ReleaseControl clears local sending immediately. Success means the release
// was written; the public control.changed notice confirms broker state.
func (client *Client) ReleaseControl(ctx context.Context) error {
	if err := client.beginUser(ctx); err != nil {
		return err
	}
	defer func() { <-client.userGate }()
	client.mu.Lock()
	lease, epoch, connected := client.lease, client.leaseEpoch, client.state.Connected
	client.lease, client.state.Controller = "", false
	client.changedLocked()
	client.mu.Unlock()
	if !connected {
		return client.closedError()
	}
	if lease == "" {
		return ErrNotController
	}
	return client.oneWay(ctx, protocol.ControlRelease, protocol.LeasePayload{Lease: lease}, lease, epoch)
}

// Resize sends only while this connection holds control. A successful write
// is followed by the broker's full canonical snapshot, not an invented frame.
func (client *Client) Resize(ctx context.Context, columns, rows int) error {
	if err := client.beginUser(ctx); err != nil {
		return err
	}
	defer func() { <-client.userGate }()
	if _, _, _, err := client.readyIdentity(true); err != nil {
		return err
	}
	return client.resizeViewport(ctx, columns, rows)
}

// Detach closes only this connection. It is safe even if an input operation
// is still waiting; that caller receives delivery uncertainty.
func (client *Client) Detach(ctx context.Context) error {
	if ctx == nil {
		return errors.New("liveclient: nil detach context")
	}
	client.mu.Lock()
	client.detaching = true
	client.lease, client.state.Controller = "", false
	client.changedLocked()
	client.mu.Unlock()
	message, err := encode(protocol.SessionDetach, client.config.SessionID, client.config.Generation, protocol.EmptyPayload{})
	if err == nil {
		err = client.write(ctx, message)
	}
	closeErr := client.Close()
	return errors.Join(err, closeErr)
}

func (client *Client) oneWay(ctx context.Context, kind protocol.MessageType, payload any, lease string, epoch uint64) error {
	message, err := encode(kind, client.config.SessionID, client.config.Generation, payload)
	if err != nil {
		return err
	}
	client.remember(message, lease, epoch)
	return client.write(ctx, message)
}

func (client *Client) remember(message protocol.Envelope, lease string, epoch uint64) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.recent) == 16 {
		copy(client.recent, client.recent[1:])
		client.recent = client.recent[:15]
	}
	client.recent = append(client.recent, sentRequest{id: message.RequestID, kind: message.Type, epoch: epoch, lease: lease})
}

func (client *Client) heartbeatLoop() {
	defer client.wg.Done()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-client.ctx.Done():
			return
		case <-ticker.C:
		}
		if err := client.heartbeat(); err != nil {
			client.stop(err)
			return
		}
	}
}

func (client *Client) heartbeat() error {
	client.mu.Lock()
	lease, epoch := client.lease, client.leaseEpoch
	client.mu.Unlock()
	if lease == "" {
		return nil
	}
	return client.oneWay(client.ctx, protocol.Heartbeat, protocol.HeartbeatPayload{Lease: &lease}, lease, epoch)
}

func (client *Client) resolveInput(message protocol.Envelope) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	pending := client.pending
	if pending == nil || pending.sequence == "" || pending.id != message.RequestID {
		return fmt.Errorf("%w: unsolicited input acknowledgement", ErrProtocol)
	}
	if pending.completed {
		return fmt.Errorf("%w: duplicate acknowledgement", ErrProtocol)
	}
	var err error
	if message.Type == protocol.InputAccepted {
		payload, _ := protocol.DecodePayload[protocol.InputAcceptedPayload](message)
		if payload.InputSequence != pending.sequence {
			return fmt.Errorf("%w: input sequence mismatch", ErrProtocol)
		}
		if client.lease == pending.lease && client.leaseEpoch == pending.epoch {
			client.sequence, _ = strconv.ParseUint(pending.sequence, 10, 64)
		}
	} else {
		payload, _ := protocol.DecodePayload[protocol.InputRejectedPayload](message)
		if payload.InputSequence == nil || *payload.InputSequence != pending.sequence {
			return fmt.Errorf("%w: rejected input sequence mismatch", ErrProtocol)
		}
		err = remoteError(payload.Code, payload.Message)
		client.remoteErrorLocked(err.(*RemoteError), pending.lease, pending.epoch)
	}
	pending.completed = true
	select {
	case pending.result <- err:
	default:
		return fmt.Errorf("%w: duplicate acknowledgement", ErrProtocol)
	}
	return nil
}

func (client *Client) remoteErrorLocked(err *RemoteError, lease string, epoch uint64) {
	client.state.LastError = err
	if client.lease == lease && client.leaseEpoch == epoch &&
		(err.Code == protocol.NotController || err.Code == protocol.StaleLease || err.Code == protocol.InputGap) {
		client.lease, client.state.Controller = "", false
	}
	client.changedLocked()
}

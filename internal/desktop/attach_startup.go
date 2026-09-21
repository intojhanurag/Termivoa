package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
)

type startupResult struct {
	transport AttachmentTransport
	err       error
}

// connectDuringStartup has no event queue. Setup keystrokes go only to a
// discard transport. A distinct live decoder prevents an incomplete startup
// paste/escape or quote chord from crossing the input-acceptance boundary.
func (attachment *Attachment) connectDuringStartup(ctx context.Context) (transport AttachmentTransport, detached bool, returnErr error) {
	if attachment.StartupEvents == nil {
		transport, returnErr = attachment.Connect(ctx)
		return
	}
	connectionContext, cancelConnection := context.WithCancel(ctx)
	startupContext, cancelStartup := context.WithCancel(ctx)
	events := make(chan uv.Event)
	stopped := make(chan struct{})
	var streamErr error
	go func() {
		defer close(stopped)
		streamErr = attachment.StartupEvents.StreamEvents(startupContext, events)
	}()
	connected := make(chan startupResult, 1)
	go func() {
		value, err := attachment.Connect(connectionContext)
		connected <- startupResult{value, err}
	}()
	defer func() {
		cancelStartup()
		if detached || returnErr != nil {
			cancelConnection()
		}
		// Continue draining unconditional decoder sends while joining BOTH
		// setup workers. Restoration must not race a final read or screen paint.
		for stopped != nil || connected != nil {
			select {
			case _, ok := <-events:
				if !ok {
					events = nil
				}
			case <-stopped:
				stopped = nil
			case result := <-connected:
				connected = nil
				transport = result.transport
				if !onlyStartupCancellation(result.err) {
					returnErr = errors.Join(returnErr, result.err)
				}
			}
		}
		if streamErr != nil && !onlyStartupCancellation(streamErr) {
			returnErr = errors.Join(returnErr, fmt.Errorf("read startup input: %w", streamErr))
		}
		if !detached && returnErr == nil {
			if err := ctx.Err(); err != nil {
				returnErr = err
			} else if transport == nil {
				returnErr = errors.New("desktop connector returned no transport")
			} else if err := attachment.BeforeInput(ctx); err != nil {
				returnErr = fmt.Errorf("desktop input handoff: %w", err)
			}
		}
		if detached || returnErr != nil {
			cancelConnection()
			if transport != nil {
				if err := transport.Close(); !onlyStartupCancellation(err) {
					returnErr = errors.Join(returnErr, err)
				}
				transport = nil
			}
		} else {
			transport = &startupTransport{AttachmentTransport: transport, cancel: cancelConnection}
		}
	}()
	controller := InputController{}
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-stopped:
			stopped = nil
			return nil, false, io.EOF
		case event, ok := <-events:
			if !ok {
				events = nil
				return nil, false, io.EOF
			}
			detached, err := controller.Handle(ctx, event, startupDiscard{})
			if detached || err != nil {
				return nil, detached, err
			}
		case result := <-connected:
			connected = nil
			return result.transport, false, result.err
		}
	}
}

type startupDiscard struct{}

func (startupDiscard) Send(context.Context, InputAction) error { return nil }

type startupTransport struct {
	AttachmentTransport
	cancel context.CancelFunc
}

func (transport *startupTransport) Close() error {
	transport.cancel()
	return transport.AttachmentTransport.Close()
}

// Only expected cancellation is quiet. Joined descriptor/transport cleanup
// errors must survive even when they also wrap context.Canceled.
func onlyStartupCancellation(err error) bool {
	if err == nil || err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !onlyStartupCancellation(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyStartupCancellation(wrapped.Unwrap())
	}
	return false
}

package liveclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const viewportResizeTimeout = 5 * time.Second

// ViewportResizeRequired is a precise, local display request, not a generic
// protocol resync error. It is recoverable only after an applied snapshot and
// while this connection already holds control. Output may return it only before
// writing any bytes. Observers and initial attachments still fail closed.
type ViewportResizeRequired struct {
	Columns, Rows int
	Cause         error
}

func (err *ViewportResizeRequired) Error() string {
	if err.Cause != nil {
		return err.Cause.Error()
	}
	return fmt.Sprintf("desktop viewport requires %dx%d; enlarge the window and re-attach", err.Columns, err.Rows)
}

type viewportResize struct {
	columns, rows int
	epoch         uint64
	deadline      time.Time
	attempts      int
}

func viewportRequest(err error) *ViewportResizeRequired {
	for err != nil {
		if request, ok := err.(*ViewportResizeRequired); ok {
			return request
		}
		if multiple, ok := err.(interface{ Unwrap() []error }); ok {
			causes := multiple.Unwrap()
			// FrameClient preserves its resync sentinel and the renderer error.
			// No arbitrary joined cleanup failure may be mistaken for resizing.
			if len(causes) != 2 || causes[0] != desktop.ErrResyncRequired {
				return nil
			}
			err = causes[1]
			continue
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil
		}
		err = wrapped.Unwrap()
	}
	return nil
}

func (client *Client) resizeViewport(ctx context.Context, columns, rows int) error {
	if columns < protocol.MinColumns || columns > protocol.MaxColumns || rows < protocol.MinRows || rows > protocol.MaxRows {
		return errors.New("desktop viewport is outside supported dimensions; enlarge the window and re-attach")
	}
	select {
	case client.resizeGate <- struct{}{}:
		defer func() { <-client.resizeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-client.ctx.Done():
		return client.closedError()
	}
	client.mu.Lock()
	if !client.state.Connected || !client.snapshotApplied {
		client.mu.Unlock()
		return ErrNotReady
	}
	if client.lease == "" {
		client.mu.Unlock()
		return ErrNotController
	}
	lease, epoch := client.lease, client.leaseEpoch
	transition := &viewportResize{columns: columns, rows: rows, epoch: epoch, deadline: time.Now().Add(viewportResizeTimeout), attempts: 1}
	if previous := client.viewport; previous != nil {
		if previous.columns == columns && previous.rows == rows && previous.epoch == epoch {
			client.mu.Unlock()
			return nil
		}
		transition.deadline, transition.attempts = previous.deadline, previous.attempts+1
		if transition.attempts > 8 || !time.Now().Before(transition.deadline) {
			client.mu.Unlock()
			return errors.New("desktop viewport kept changing during resize; settle the window size and re-attach")
		}
	}
	client.viewport = transition
	client.state.Ready, client.state.Resizing = false, true
	client.changedLocked()
	client.mu.Unlock()
	select {
	case client.paintWake <- struct{}{}:
	default:
	}
	ctx, cancel := context.WithDeadline(ctx, transition.deadline)
	defer cancel()
	if err := client.oneWay(ctx, protocol.TerminalResize, protocol.TerminalResizePayload{Lease: lease, Columns: int64(columns), Rows: int64(rows)}, lease, epoch); err != nil {
		return err
	}
	// An old snapshot may be awaiting our acknowledgement. We cannot acknowledge
	// a screen that did not fit, so abandon that transfer exactly once per target.
	return client.oneWay(ctx, protocol.ScreenResync, protocol.ScreenResyncPayload{Reason: protocol.InvalidState}, "", 0)
}

func (client *Client) pendingViewport() *viewportResize {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.viewport // Immutable; replacement, not mutation, changes target.
}

func (client *Client) viewportSnapshotMatches(screen protocol.ScreenMetadata, transition *viewportResize) bool {
	if transition == nil {
		return true
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	// After takeover, a fitting authoritative full snapshot is safe to observe;
	// this connection never resizes again without its original lease.
	return client.leaseEpoch != transition.epoch || client.lease == "" ||
		(screen.Columns == int64(transition.columns) && screen.Rows == int64(transition.rows))
}

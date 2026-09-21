package broker

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// sessionWriter retains no asynchronous payload copies. Its byte budget covers
// callers waiting for the single socket writer as well as the active frame.
// It implements the same four-byte length framing as ipc.FrameWriter, but
// writes header and payload separately to avoid a second maximum-size copy.
type sessionWriter struct {
	conn net.Conn
	gate chan struct{}
	mu   sync.Mutex
	used int
}

func newSessionWriter(conn net.Conn) *sessionWriter {
	return &sessionWriter{conn: conn, gate: make(chan struct{}, 1)}
}

func (writer *sessionWriter) Send(ctx context.Context, message []byte, drained func()) error {
	if drained == nil {
		return errors.New("broker: missing writer drain callback")
	}
	if len(message) == 0 || len(message) > protocol.MaxOutboundMessageBytes {
		return errOutboundMessageTooLarge
	}
	size := len(message) + 4
	if err := writer.reserve(size); err != nil {
		_ = writer.conn.Close()
		return err
	}
	defer writer.release(size)
	return writer.sendReserved(ctx, message, drained)
}

func (writer *sessionWriter) reserve(size int) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.used+size > MaxViewerQueueBytes {
		return ErrViewerSlow
	}
	writer.used += size
	return nil
}

func (writer *sessionWriter) release(size int) {
	writer.mu.Lock()
	writer.used -= size
	writer.mu.Unlock()
}

// reservedViewerSink is paired with ViewerConfig.budget. Actor-queued notices
// and frame deliveries already hold their connection reservation before Send.
type reservedViewerSink struct{ writer *sessionWriter }

func (sink reservedViewerSink) Send(ctx context.Context, message []byte, drained func()) error {
	return sink.writer.sendReserved(ctx, message, drained)
}

func (writer *sessionWriter) sendReserved(ctx context.Context, message []byte, drained func()) error {
	if drained == nil || len(message) == 0 || len(message) > protocol.MaxOutboundMessageBytes {
		return errors.New("broker: invalid reserved viewer message")
	}
	select {
	case writer.gate <- struct{}{}:
		defer func() { <-writer.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(DefaultViewerTimeout)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	if err := writer.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	// A cancellation during a partial frame poisons the stream. Close it rather
	// than appending another message to an incomplete JSON frame.
	stop := context.AfterFunc(ctx, func() { _ = writer.conn.Close() })
	defer stop()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(message)))
	buffers := net.Buffers{header[:], message}
	if _, err := buffers.WriteTo(writer.conn); err != nil {
		_ = writer.conn.Close()
		return err
	}
	if err := writer.conn.SetWriteDeadline(time.Time{}); err != nil {
		_ = writer.conn.Close()
		return err
	}
	if !stop() && ctx.Err() != nil {
		return ctx.Err()
	}
	drained()
	return nil
}

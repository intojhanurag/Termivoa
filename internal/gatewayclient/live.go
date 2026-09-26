package gatewayclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// liveStream relays canonical envelope JSON on a dedicated broker connection.
// Send may be called concurrently. Receive supports one caller at a time. Any
// transport or validation failure, including a canceled Send or Receive, closes
// the stream: a partial frame cannot be resumed safely.
type liveStream struct {
	conn   net.Conn
	reader *ipc.FrameReader
	writer *ipc.FrameWriter
	sendMu sync.Mutex

	mu        sync.Mutex
	failure   error // terminal; set once
	closeOnce sync.Once
	closeErr  error
}

func (stream *liveStream) Send(ctx context.Context, envelope []byte) error {
	if ctx == nil {
		return errors.New("gatewayclient: context must not be nil")
	}
	if err := stream.err(); err != nil {
		return err
	}
	if _, err := protocol.DecodeClient(envelope); err != nil {
		return fmt.Errorf("gatewayclient: invalid live client message: %w", err)
	}
	stream.sendMu.Lock()
	defer stream.sendMu.Unlock()
	if err := stream.err(); err != nil {
		return err
	}
	clearDeadline, err := setContextDeadline(ctx, stream.conn.SetWriteDeadline)
	if err != nil {
		return err
	}
	err = stream.writer.WriteFrame(envelope)
	clearDeadline()
	if err != nil {
		return stream.fail(fmt.Errorf("gatewayclient: live send failed: %w", failure(ctx, err, false)))
	}
	return nil
}

// Receive returns one validated host envelope. After relaying access.revoked
// it closes the stream, and later calls return ErrAccessRevoked.
func (stream *liveStream) Receive(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("gatewayclient: context must not be nil")
	}
	if err := stream.err(); err != nil {
		return nil, err
	}
	clearDeadline, err := setContextDeadline(ctx, stream.conn.SetReadDeadline)
	if err != nil {
		return nil, err
	}
	data, err := stream.reader.ReadFrame()
	clearDeadline()
	if err != nil {
		return nil, stream.fail(fmt.Errorf("gatewayclient: live receive failed: %w", failure(ctx, err, false)))
	}
	envelope, err := protocol.DecodeHost(data)
	if err != nil {
		return nil, stream.fail(fmt.Errorf("gatewayclient: invalid live host message: %w", err))
	}
	if envelope.Type == protocol.AccessRevoked {
		_ = stream.fail(ErrAccessRevoked)
	}
	return data, nil
}

// Close is idempotent.
func (stream *liveStream) Close() error {
	_ = stream.fail(net.ErrClosed)
	return stream.closeErr
}

func (stream *liveStream) err() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.failure
}

// fail records the first terminal error, closes the connection, and returns
// the recorded error.
func (stream *liveStream) fail(err error) error {
	stream.mu.Lock()
	if stream.failure == nil {
		stream.failure = err
	}
	err = stream.failure
	stream.mu.Unlock()
	stream.closeOnce.Do(func() { stream.closeErr = stream.conn.Close() })
	return err
}

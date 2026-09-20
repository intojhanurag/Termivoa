package ipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	ErrEmptyFrame     = errors.New("ipc: empty frame")
	ErrFrameTooLarge  = errors.New("ipc: frame exceeds configured limit")
	ErrReaderPoisoned = errors.New("ipc: frame reader is unusable after a framing failure")
	ErrWriterPoisoned = errors.New("ipc: frame writer is unusable after a write failure")
)

// FrameReader reads four-byte big-endian length-prefixed JSON frames.
type FrameReader struct {
	mu          sync.Mutex
	reader      io.Reader
	max         uint32
	terminalErr error
}

func NewFrameReader(reader io.Reader, maxBytes int) (*FrameReader, error) {
	max, err := checkedFrameLimit(maxBytes)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("ipc: frame reader must not be nil")
	}
	return &FrameReader{reader: reader, max: max}, nil
}

func (reader *FrameReader) ReadFrame() ([]byte, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.terminalErr != nil {
		return nil, reader.terminalErr
	}
	var header [4]byte
	if _, err := io.ReadFull(reader.reader, header[:]); err != nil {
		return nil, reader.poison(fmt.Errorf("ipc: read frame header: %w", err))
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 {
		return nil, reader.poison(ErrEmptyFrame)
	}
	if length > reader.max {
		return nil, reader.poison(fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, length, reader.max))
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader.reader, payload); err != nil {
		return nil, reader.poison(fmt.Errorf("ipc: read frame body: %w", err))
	}
	return payload, nil
}

func (reader *FrameReader) poison(cause error) error {
	reader.terminalErr = errors.Join(ErrReaderPoisoned, cause)
	return reader.terminalErr
}

// FrameWriter serializes complete frames so concurrent callers cannot
// interleave a header or body with another message.
type FrameWriter struct {
	mu          sync.Mutex
	writer      io.Writer
	max         uint32
	terminalErr error
}

func NewFrameWriter(writer io.Writer, maxBytes int) (*FrameWriter, error) {
	max, err := checkedFrameLimit(maxBytes)
	if err != nil {
		return nil, err
	}
	if writer == nil {
		return nil, errors.New("ipc: frame writer must not be nil")
	}
	return &FrameWriter{writer: writer, max: max}, nil
}

func (writer *FrameWriter) WriteFrame(payload []byte) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.terminalErr != nil {
		return writer.terminalErr
	}
	if len(payload) == 0 {
		return ErrEmptyFrame
	}
	if uint64(len(payload)) > uint64(writer.max) {
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(payload), writer.max)
	}
	// Allocate and copy only after acquiring the serialization boundary. This
	// keeps concurrent callers from each retaining a maximum-size frame while
	// waiting and prevents caller mutation during the underlying writes.
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	for len(frame) > 0 {
		written, err := writer.writer.Write(frame)
		if err != nil {
			return writer.poison(fmt.Errorf("ipc: write frame: %w", err))
		}
		if written <= 0 || written > len(frame) {
			return writer.poison(io.ErrShortWrite)
		}
		frame = frame[written:]
	}
	return nil
}

func (writer *FrameWriter) poison(cause error) error {
	writer.terminalErr = errors.Join(ErrWriterPoisoned, cause)
	return writer.terminalErr
}

func checkedFrameLimit(maxBytes int) (uint32, error) {
	if maxBytes <= 0 || maxBytes > protocol.MaxOutboundMessageBytes {
		return 0, fmt.Errorf("ipc: frame limit must be between 1 and %d", protocol.MaxOutboundMessageBytes)
	}
	return uint32(maxBytes), nil
}

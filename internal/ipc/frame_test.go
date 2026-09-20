package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestFrameReaderHandlesSplitAndConsecutiveFrames(t *testing.T) {
	var stream bytes.Buffer
	for _, payload := range [][]byte{[]byte(`{"one":1}`), []byte(`{"two":2}`)} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
		stream.Write(header[:])
		stream.Write(payload)
	}
	reader, err := NewFrameReader(iotest.OneByteReader(&stream), protocol.MaxInboundMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	for index, expected := range []string{`{"one":1}`, `{"two":2}`} {
		frame, err := reader.ReadFrame()
		if err != nil || string(frame) != expected {
			t.Fatalf("frame %d = %q, %v", index, frame, err)
		}
	}
}

func TestFrameReaderRejectsLengthBeforeBodyAllocation(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], protocol.MaxInboundMessageBytes+1)
	reader, _ := NewFrameReader(bytes.NewReader(header[:]), protocol.MaxInboundMessageBytes)
	if _, err := reader.ReadFrame(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame error = %v", err)
	}
	binary.BigEndian.PutUint32(header[:], 0)
	reader, _ = NewFrameReader(bytes.NewReader(header[:]), protocol.MaxInboundMessageBytes)
	if _, err := reader.ReadFrame(); !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("empty frame error = %v", err)
	}
}

func TestFrameReaderReportsTruncatedHeaderAndBody(t *testing.T) {
	reader, _ := NewFrameReader(bytes.NewReader([]byte{0, 0}), 16)
	if _, err := reader.ReadFrame(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header error = %v", err)
	}
	reader, _ = NewFrameReader(bytes.NewReader([]byte{0, 0, 0, 2, 'x'}), 16)
	if _, err := reader.ReadFrame(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated body error = %v", err)
	}
}

func TestFrameReaderRemainsPoisonedAfterOversizedHeader(t *testing.T) {
	var stream bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 17)
	stream.Write(header[:])
	stream.Write(bytes.Repeat([]byte{'x'}, 17))
	binary.BigEndian.PutUint32(header[:], 1)
	stream.Write(header[:])
	stream.WriteByte('y')
	reader, _ := NewFrameReader(&stream, 16)
	if _, err := reader.ReadFrame(); !errors.Is(err, ErrFrameTooLarge) || !errors.Is(err, ErrReaderPoisoned) {
		t.Fatalf("first oversized read error = %v", err)
	}
	remaining := stream.Len()
	if _, err := reader.ReadFrame(); !errors.Is(err, ErrReaderPoisoned) {
		t.Fatalf("second read error = %v", err)
	}
	if stream.Len() != remaining {
		t.Fatal("poisoned reader consumed more bytes")
	}
}

func TestFrameWriterHandlesShortWrites(t *testing.T) {
	output := &oneByteWriter{}
	writer, _ := NewFrameWriter(output, 16)
	if err := writer.WriteFrame([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	reader, _ := NewFrameReader(bytes.NewReader(output.Bytes()), 16)
	frame, err := reader.ReadFrame()
	if err != nil || string(frame) != "abc" {
		t.Fatalf("frame = %q, %v", frame, err)
	}
}

func TestFrameWriterSerializesConcurrentFrames(t *testing.T) {
	output := &oneByteWriter{}
	writer, _ := NewFrameWriter(output, 64)
	payloads := [][]byte{[]byte(`{"id":1}`), []byte(`{"id":2}`), []byte(`{"id":3}`), []byte(`{"id":4}`)}
	var wait sync.WaitGroup
	for _, payload := range payloads {
		payload := payload
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := writer.WriteFrame(payload); err != nil {
				t.Errorf("write frame: %v", err)
			}
		}()
	}
	wait.Wait()

	reader, _ := NewFrameReader(bytes.NewReader(output.Bytes()), 64)
	seen := make(map[string]bool)
	for range payloads {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		seen[string(frame)] = true
	}
	for _, payload := range payloads {
		if !seen[string(payload)] {
			t.Fatalf("missing payload %q in %#v", payload, seen)
		}
	}
}

func TestFrameWriterPoisonsStreamAfterPartialFailure(t *testing.T) {
	sentinel := errors.New("write failed")
	output := &blockingPartialErrorWriter{
		entered: make(chan struct{}), release: make(chan struct{}), failure: sentinel,
	}
	writer, _ := NewFrameWriter(output, 64)
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { firstResult <- writer.WriteFrame([]byte(`{"id":1}`)) }()
	<-output.entered
	go func() { secondResult <- writer.WriteFrame([]byte(`{"id":2}`)) }()
	close(output.release)
	for index, result := range []<-chan error{firstResult, secondResult} {
		err := <-result
		if !errors.Is(err, ErrWriterPoisoned) {
			t.Fatalf("write %d error = %v", index, err)
		}
	}
	if output.calls != 1 || output.buffer.Len() != 2 {
		t.Fatalf("underlying writes = %d, bytes = %d", output.calls, output.buffer.Len())
	}
	if err := writer.WriteFrame(nil); !errors.Is(err, ErrWriterPoisoned) {
		t.Fatalf("future invalid write did not preserve terminal error: %v", err)
	}
}

func TestFrameConstructionRejectsUnsafeLimitsAndPayloads(t *testing.T) {
	if _, err := NewFrameReader(bytes.NewReader(nil), protocol.MaxOutboundMessageBytes+1); err == nil {
		t.Fatal("oversized reader limit was accepted")
	}
	if _, err := NewFrameWriter(io.Discard, 0); err == nil {
		t.Fatal("zero writer limit was accepted")
	}
	writer, _ := NewFrameWriter(io.Discard, 2)
	if err := writer.WriteFrame(nil); !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("empty write error = %v", err)
	}
	if err := writer.WriteFrame([]byte("abc")); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized write error = %v", err)
	}
}

type oneByteWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

type blockingPartialErrorWriter struct {
	entered chan struct{}
	release chan struct{}
	failure error
	buffer  bytes.Buffer
	calls   int
}

func (writer *blockingPartialErrorWriter) Write(payload []byte) (int, error) {
	writer.calls++
	close(writer.entered)
	<-writer.release
	written, _ := writer.buffer.Write(payload[:2])
	return written, writer.failure
}

func (writer *oneByteWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(payload) == 0 {
		return 0, nil
	}
	return writer.buffer.Write(payload[:1])
}

func (writer *oneByteWriter) Bytes() []byte {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return bytes.Clone(writer.buffer.Bytes())
}

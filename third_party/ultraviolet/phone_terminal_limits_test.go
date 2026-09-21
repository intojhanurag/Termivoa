package uv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muesli/cancelreader"
)

// Zero-valued limits deliberately retain upstream behavior. This fixture also
// records why Phone Terminal must configure limits before decoding user input.
func TestPhoneTerminalReproduceUnboundedDefaults(t *testing.T) {
	const productLimit = 64 * 1024
	scanner := newEventScanner()
	scanner.scanEvents([]byte("\x1b[200~"), false)
	chunk := bytes.Repeat([]byte("x"), 4096)
	for range 17 {
		scanner.scanEvents(chunk, false)
	}
	if len(scanner.paste) <= productLimit {
		t.Fatalf("upstream default did not reproduce oversized paste: %d bytes", len(scanner.paste))
	}

	scanner = newEventScanner()
	pending := []byte("\x1b]52;c;")
	for range 17 {
		pending = append(pending, chunk...)
		consumed, events := scanner.scanEvents(pending, false)
		if consumed != 0 || len(events) != 0 {
			t.Fatalf("incomplete OSC did not retain pending input: consumed=%d events=%d", consumed, len(events))
		}
	}
	if len(pending) <= productLimit {
		t.Fatalf("upstream default did not reproduce oversized pending sequence: %d bytes", len(pending))
	}
}

type limitedContextInput struct {
	input          []byte
	chunk          int
	waitAfterInput bool
	active         atomic.Int64
	waiting        chan struct{}
}

func (input *limitedContextInput) ReadContext(ctx context.Context, p []byte) (int, error) {
	input.active.Add(1)
	defer input.active.Add(-1)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(input.input) == 0 {
		if input.waitAfterInput {
			if input.waiting != nil {
				close(input.waiting)
			}
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return 0, io.EOF
	}
	n := min(len(p), len(input.input))
	if input.chunk > 0 {
		n = min(n, input.chunk)
	}
	copy(p, input.input[:n])
	input.input = input.input[n:]
	return n, nil
}

func runLimitedInput(t *testing.T, data []byte, chunk int) ([]Event, error, *TerminalReader) {
	t.Helper()
	source := &limitedContextInput{input: data, chunk: chunk}
	reader := NewContextTerminalReader(source, "xterm-256color")
	reader.MaxPasteBytes = 64 * 1024
	reader.MaxPendingBytes = 64 * 1024
	reader.EscTimeout = time.Hour // Deterministic incomplete-sequence limit.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events := make(chan Event)
	done := make(chan error, 1)
	go func() {
		done <- reader.StreamEvents(ctx, events)
		close(events)
	}()
	var received []Event
	for event := range events {
		received = append(received, event)
	}
	err := <-done
	if ctx.Err() != nil {
		t.Fatalf("decoder did not finish: %v", ctx.Err())
	}
	if source.active.Load() != 0 {
		t.Fatal("reader returned before stopping its input read")
	}
	return received, err, reader
}

func TestPhoneTerminalPasteLimitBeforeAccumulation(t *testing.T) {
	for _, chunk := range []int{1, 4096} {
		data := append([]byte("\x1b[200~"), bytes.Repeat([]byte("x"), 64*1024+1)...)
		data = append(data, []byte("\x1b[201~echo must-not-run\r")...)
		events, err, reader := runLimitedInput(t, data, chunk)
		if !errors.Is(err, ErrPasteLimit) {
			t.Fatalf("chunk=%d error=%v", chunk, err)
		}
		for _, event := range events {
			if _, harmless := event.(PasteStartEvent); !harmless {
				t.Fatalf("oversized paste produced input: %T", event)
			}
		}
		if len(reader.eventScanner.paste) != 0 || cap(reader.eventScanner.paste) > reader.MaxPasteBytes {
			t.Fatal("rejected paste retained excess content")
		}
		// The same decoder cannot reinterpret the rejected paste tail as keys.
		if err := reader.StreamEvents(context.Background(), make(chan Event)); !errors.Is(err, ErrPasteLimit) {
			t.Fatalf("failed decoder restarted: %v", err)
		}
	}
}

func TestPhoneTerminalAllowsExactUTF8PasteLimit(t *testing.T) {
	content := append(bytes.Repeat([]byte("界"), (64*1024)/3), 'x')
	data := append([]byte("\x1b[200~"), content...)
	data = append(data, []byte("\x1b[201~")...)
	events, err, _ := runLimitedInput(t, data, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var pastes int
	for _, event := range events {
		if paste, ok := event.(PasteEvent); ok {
			pastes++
			if paste.Content != string(content) {
				t.Fatalf("paste bytes changed: got=%d want=%d", len(paste.Content), len(content))
			}
		}
	}
	if pastes != 1 {
		t.Fatalf("paste events=%d", pastes)
	}
}

func TestPhoneTerminalPendingSequenceLimitBeforeBufferGrowth(t *testing.T) {
	data := append([]byte("\x1b]52;c;"), bytes.Repeat([]byte("x"), 64*1024+1)...)
	events, err, reader := runLimitedInput(t, data, 4096)
	if !errors.Is(err, ErrPendingInputLimit) {
		t.Fatalf("pending-sequence error=%v", err)
	}
	if len(events) != 0 {
		t.Fatalf("incomplete oversized sequence emitted %d events", len(events))
	}
	if err := reader.StreamEvents(context.Background(), make(chan Event)); !errors.Is(err, ErrPendingInputLimit) {
		t.Fatalf("failed decoder restarted: %v", err)
	}
}

func TestPhoneTerminalLimitStopsReadPumpWithoutClosingSource(t *testing.T) {
	source := &limitedContextInput{
		input:          append([]byte("\x1b[200~"), bytes.Repeat([]byte("x"), 1000)...),
		waitAfterInput: true,
		waiting:        make(chan struct{}),
	}
	reader := NewContextTerminalReader(source, "xterm-256color")
	reader.MaxPasteBytes = 100
	reader.MaxPendingBytes = 8192
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reader.SetLogger(limitLoggerFunc(func(format string, _ ...interface{}) {
		if format == "input: %q" {
			select {
			case <-source.waiting:
			case <-ctx.Done():
			}
		}
	}))
	if err := reader.StreamEvents(ctx, make(chan Event, 2)); !errors.Is(err, ErrPasteLimit) {
		t.Fatalf("limit error=%v", err)
	}
	if source.active.Load() != 0 {
		t.Fatal("read pump survived decoder error")
	}
	if ctx.Err() != nil {
		t.Fatal("reader needed caller cancellation to stop")
	}
}

type limitLoggerFunc func(string, ...interface{})

func (logger limitLoggerFunc) Printf(format string, args ...interface{}) { logger(format, args...) }

func TestPhoneTerminalCanceledEventDeliveryJoinsReadPump(t *testing.T) {
	source := &limitedContextInput{input: []byte("x"), waitAfterInput: true}
	reader := NewContextTerminalReader(source, "xterm-256color")
	reader.MaxPasteBytes = 65536
	reader.MaxPendingBytes = 65536
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := reader.StreamEvents(ctx, make(chan Event)) // Deliberately no consumer.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if source.active.Load() != 0 {
		t.Fatal("read pump survived canceled delivery")
	}
}

func TestPhoneTerminalBoundedModeRejectsUncancellableReader(t *testing.T) {
	reader := NewTerminalReader(bytes.NewReader(nil), "xterm-256color")
	reader.MaxPasteBytes = 65536
	if err := reader.StreamEvents(context.Background(), make(chan Event)); err == nil {
		t.Fatal("bounded mode accepted an arbitrary blocking io.Reader")
	}
}

type contextInputFunc func(context.Context, []byte) (int, error)

func (read contextInputFunc) ReadContext(ctx context.Context, data []byte) (int, error) {
	return read(ctx, data)
}

func TestPhoneTerminalReadPumpPreservesDataReturnedWithEOF(t *testing.T) {
	reader := NewContextTerminalReader(contextInputFunc(func(_ context.Context, data []byte) (int, error) {
		return copy(data, "\x1b[200~yes\x1b[201~"), io.EOF
	}), "xterm-256color")
	reader.MaxPasteBytes, reader.MaxPendingBytes = 65536, 65536
	events := make(chan Event, 4)
	if err := reader.StreamEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	close(events)
	var pastes int
	for event := range events {
		if paste, ok := event.(PasteEvent); ok {
			pastes++
			if paste.Content != "yes" {
				t.Fatalf("final paste=%q", paste.Content)
			}
		}
	}
	if pastes != 1 {
		t.Fatalf("final read lost its paste: events=%d", pastes)
	}
}

func TestPhoneTerminalBoundedReadEndPreservesJoinedFailures(t *testing.T) {
	failure := errors.New("synthetic input cleanup failure")
	for _, sentinel := range []error{io.EOF, cancelreader.ErrCanceled} {
		for _, test := range []struct {
			name    string
			err     error
			failure bool
		}{
			{name: "bare", err: sentinel},
			{name: "wrapped", err: fmt.Errorf("read ended: %w", sentinel)},
			{name: "joined", err: errors.Join(sentinel, failure), failure: true},
			{name: "wrapped-joined", err: fmt.Errorf("read ended: %w", errors.Join(sentinel, failure)), failure: true},
		} {
			t.Run(sentinel.Error()+"/"+test.name, func(t *testing.T) {
				reader := NewContextTerminalReader(contextInputFunc(func(context.Context, []byte) (int, error) { return 0, test.err }), "xterm-256color")
				reader.MaxPasteBytes, reader.MaxPendingBytes = 65536, 65536
				err := reader.StreamEvents(context.Background(), make(chan Event))
				if test.failure {
					if !errors.Is(err, failure) {
						t.Fatalf("lost read failure: %v", err)
					}
				} else if err != nil {
					t.Fatalf("clean read end: %v", err)
				}
			})
		}
	}
}

func TestPhoneTerminalCancellationPreservesDataReadFailure(t *testing.T) {
	failure := errors.New("synthetic input cleanup failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := NewContextTerminalReader(contextInputFunc(func(_ context.Context, data []byte) (int, error) {
		cancel()
		return copy(data, "x"), failure
	}), "xterm-256color")
	reader.MaxPasteBytes, reader.MaxPendingBytes = 65536, 65536
	if err := reader.StreamEvents(ctx, make(chan Event)); !errors.Is(err, failure) {
		t.Fatalf("canceled data delivery lost read failure: %v", err)
	}
}

func TestPhoneTerminalCanceledFlushPreservesReadFailure(t *testing.T) {
	failure := errors.New("synthetic input cleanup failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads int
	reader := NewContextTerminalReader(contextInputFunc(func(_ context.Context, data []byte) (int, error) {
		reads++
		if reads == 1 {
			return copy(data, "\x1b"), nil
		}
		return 0, failure
	}), "xterm-256color")
	reader.MaxPasteBytes, reader.MaxPendingBytes = 65536, 65536
	reader.EscTimeout = time.Hour
	var scans int
	reader.eventScanner.setLogger(limitLoggerFunc(func(format string, _ ...interface{}) {
		if format == "processing buf %d %q" {
			scans++
			if scans == 2 {
				// The first scan retains ESC. Cancel precisely when the error
				// branch flushes it, before its event can be delivered.
				cancel()
			}
		}
	}))
	if err := reader.StreamEvents(ctx, make(chan Event)); !errors.Is(err, failure) {
		t.Fatalf("canceled final event hid read failure: %v", err)
	}
	if scans != 2 {
		t.Fatalf("did not exercise final flush: scans=%d", scans)
	}
}

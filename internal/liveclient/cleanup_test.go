package liveclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

type cleanupOutput struct {
	started chan struct{}
	stopped chan struct{}
	failure error
}

func (output *cleanupOutput) WriteContext(ctx context.Context, _ []byte) (int, error) {
	close(output.started)
	defer close(output.stopped)
	<-ctx.Done()
	return 0, errors.Join(ctx.Err(), output.failure)
}

func TestCloseRetainsLateOutputCleanupFailure(t *testing.T) {
	for _, parentCancellation := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_cancellation_%t", parentCancellation), func(t *testing.T) {
			failure := errors.New("synthetic descriptor flags restoration failure")
			output := &cleanupOutput{started: make(chan struct{}), stopped: make(chan struct{}), failure: failure}
			client, host, cancel := newClientFixture(t, output)
			screen, rows := snapshot(t, "blocked")
			host.snapshot(t, "1", screen, rows)
			select {
			case <-output.started:
			case <-time.After(time.Second):
				t.Fatal("paint did not begin")
			}
			if parentCancellation {
				cancel()
			}
			if err := client.Close(); !errors.Is(err, failure) {
				t.Fatalf("Close discarded output cleanup failure: %v", err)
			}
			select {
			case <-output.stopped:
			default:
				t.Fatal("Close returned before output cleanup finished")
			}
			if err := client.Err(); !errors.Is(err, failure) {
				t.Fatalf("connection error lost cleanup failure: %v", err)
			}
			if err := client.Close(); !errors.Is(err, failure) {
				t.Fatalf("repeated Close lost cleanup failure: %v", err)
			}
		})
	}
}

func TestCloseIgnoresPureOutputCancellationAfterShutdown(t *testing.T) {
	output := &cleanupOutput{started: make(chan struct{}), stopped: make(chan struct{})}
	client, host, _ := newClientFixture(t, output)
	screen, rows := snapshot(t, "blocked")
	host.snapshot(t, "1", screen, rows)
	select {
	case <-output.started:
	case <-time.After(time.Second):
		t.Fatal("paint did not begin")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("ordinary Close reported output cancellation: %v", err)
	}
}

func TestOnlyContextCancellation(t *testing.T) {
	failure := errors.New("synthetic cleanup failure")
	for _, testCase := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"wrapped", fmt.Errorf("write: %w", context.Canceled), true},
		{"joined_cancellation", errors.Join(context.Canceled, context.DeadlineExceeded), true},
		{"joined_failure", errors.Join(context.Canceled, failure), false},
		{"nested_failure", fmt.Errorf("write: %w", errors.Join(context.Canceled, failure)), false},
		{"failure", failure, false},
		{"closed_output", io.ErrClosedPipe, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := onlyContextCancellation(testCase.err); got != testCase.want {
				t.Fatalf("onlyContextCancellation(%v) = %t", testCase.err, got)
			}
		})
	}
}

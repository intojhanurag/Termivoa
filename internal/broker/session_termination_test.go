package broker

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// A terminating PTY may not finish exiting until the master drains its output.
// The one-slot broker queue makes this dependency deterministic without an OS,
// timing-dependent output flood, or a larger production shutdown deadline.
type drainingTerminationProcess struct {
	*instrumentedProcess
	chunks                  chan []byte
	parsed                  <-chan struct{}
	started                 chan struct{}
	finish                  chan struct{}
	finished                chan struct{}
	once                    sync.Once
	calls                   atomic.Int32
	failure                 error
	closedBeforeTermination atomic.Bool
}

func (process *drainingTerminationProcess) Read(data []byte) (int, error) {
	select {
	case chunk := <-process.chunks:
		return copy(data, chunk), nil
	case <-process.exited:
		return 0, io.EOF
	}
}

func (process *drainingTerminationProcess) Terminate(ctx context.Context) error {
	if process.calls.Add(1) != 1 {
		return errors.New("duplicate termination attempt")
	}
	defer close(process.finished)
	close(process.started)
	for _, value := range []string{"one ", "two ", "three ", "four"} {
		select {
		case process.chunks <- []byte(value):
		case <-ctx.Done():
			return ctx.Err()
		case <-process.exited:
			return errors.New("fixture released before output drained")
		}
	}
	select {
	case <-process.parsed:
	case <-ctx.Done():
		return ctx.Err()
	case <-process.exited:
		return errors.New("fixture released before canonical parsing")
	}
	process.release() // Wait/EOF may finish before the terminator itself returns.
	select {
	case <-process.finish:
		return process.failure
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (process *drainingTerminationProcess) allowFinish() {
	process.once.Do(func() { close(process.finish) })
}

func (process *drainingTerminationProcess) Close() error {
	select {
	case <-process.finished:
	default:
		process.closedBeforeTermination.Store(true)
	}
	return process.instrumentedProcess.Close()
}

type drainingTerminationCore struct {
	*stubTerminal
	parsed                  chan struct{}
	text                    strings.Builder // Only the actor mutates it; parsed publishes the result.
	terminationFinished     <-chan struct{}
	closedBeforeTermination atomic.Bool
}

func (core *drainingTerminationCore) WriteOutput(_ context.Context, data []byte) error {
	if string(data) == "lose" {
		return errors.New("synthetic terminal parsing failure")
	}
	core.text.Write(data)
	if core.text.String() == "one two three four" {
		close(core.parsed)
	}
	return nil
}

func (core *drainingTerminationCore) Close() error {
	select {
	case <-core.terminationFinished:
	default:
		core.closedBeforeTermination.Store(true)
	}
	return core.stubTerminal.Close()
}

func TestSessionTerminationKeepsCanonicalOutputConsumerRunning(t *testing.T) {
	for _, mode := range []string{"close", "terminate", "lost"} {
		t.Run(mode, func(t *testing.T) {
			core := &drainingTerminationCore{stubTerminal: newStubTerminal(), parsed: make(chan struct{})}
			process := &drainingTerminationProcess{instrumentedProcess: newInstrumentedProcess(nil),
				chunks: make(chan []byte), parsed: core.parsed, started: make(chan struct{}),
				finish: make(chan struct{}), finished: make(chan struct{})}
			core.terminationFinished = process.finished
			runtimeContext, cancel := context.WithCancel(context.Background())
			session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
				ID: testSessionID, Generation: testGeneration, Process: ptyhost.Spec{Columns: 80, Rows: 24},
				InputQueueCapacity: DefaultInputQueueCapacity, PTYOutputQueue: 1,
			}, process, core)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				process.allowFinish()
				process.release()
				_ = session.Close()
				select {
				case <-session.Done():
				case <-time.After(time.Second):
					t.Error("synthetic runtime did not join")
				}
			}()
			result := make(chan error, 1)
			requestContext, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			switch mode {
			case "close":
				go func() { result <- session.Close() }()
			case "terminate":
				go func() { result <- session.Terminate(requestContext) }()
			case "lost":
				process.chunks <- []byte("lose")
				go func() { <-process.started; result <- session.Close() }()
			}
			select {
			case <-core.parsed:
			case <-time.After(time.Second):
				t.Fatal("termination blocked the only canonical output consumer")
			}
			cancelRequest() // Accepted termination must still finish once, not retry.
			if core.text.String() != "one two three four" {
				t.Fatal("shutdown discarded or reordered raw output")
			}
			select {
			case err := <-result:
				t.Fatalf("returned before termination worker joined: %v", err)
			default:
			}
			if process.closed.Load() || core.closed.Load() {
				t.Fatal("closed resources before termination worker joined")
			}
			if err := session.Terminate(context.Background()); !errors.Is(err, ErrSessionNotRunning) && !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("repeated termination falsely succeeded: %v", err)
			}
			secondClose := make(chan error, 1)
			go func() { secondClose <- session.Close() }()
			select {
			case err := <-secondClose:
				t.Fatalf("repeated Close returned before attempt joined: %v", err)
			default:
			}
			process.allowFinish()
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("termination result did not finish")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-secondClose; err != nil {
				t.Fatal(err)
			}
			if process.calls.Load() != 1 || !process.waited.Load() || process.closeBeforeWait.Load() || !core.closed.Load() {
				t.Fatalf("lifecycle: calls=%d waited=%t closeBeforeWait=%t coreClosed=%t", process.calls.Load(), process.waited.Load(), process.closeBeforeWait.Load(), core.closed.Load())
			}
			if process.closedBeforeTermination.Load() || core.closedBeforeTermination.Load() {
				t.Fatal("cleanup raced the owned termination worker")
			}
		})
	}
}

func TestSessionLateTerminationErrorPreservesNaturalExitAndOriginalCause(t *testing.T) {
	core := &drainingTerminationCore{stubTerminal: newStubTerminal(), parsed: make(chan struct{})}
	failure := errors.New("synthetic post-wait termination cleanup failed")
	process := &drainingTerminationProcess{instrumentedProcess: newInstrumentedProcess(nil),
		chunks: make(chan []byte), parsed: core.parsed, started: make(chan struct{}),
		finish: make(chan struct{}), finished: make(chan struct{}), failure: failure}
	core.terminationFinished = process.finished
	runtimeContext, cancel := context.WithCancel(context.Background())
	session, err := startSessionRuntime(runtimeContext, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration, Process: ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity, PTYOutputQueue: 1,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { process.allowFinish(); process.release(); _ = session.Close() }()
	result := make(chan error, 1)
	go func() { result <- session.Terminate(context.Background()) }()
	select {
	case <-session.Ended():
	case <-time.After(time.Second):
		t.Fatal("natural Wait and ordered EOF did not finish while termination was pending")
	}
	before, ok := session.FinalStatus()
	if !ok || before.State != SessionExited || before.ExitCode == nil || *before.ExitCode != 0 {
		t.Fatalf("natural process exit was lost: %+v", before)
	}
	process.allowFinish()
	if err := <-result; !errors.Is(err, failure) {
		t.Fatalf("termination cleanup failure was hidden: %v", err)
	}
	if err := session.Close(); !errors.Is(err, failure) || !errors.Is(err, ErrSessionShutdown) {
		t.Fatalf("Close lost the original termination failure: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("completed process did not finish cleanup")
	}
	after, _ := session.FinalStatus()
	if after.State != before.State || !after.EndedAt.Equal(before.EndedAt) || process.calls.Load() != 1 {
		t.Fatalf("late error rewrote natural process identity/state: before=%+v after=%+v calls=%d", before, after, process.calls.Load())
	}
}

func TestSessionClosedCallPreservesAuthoritativeTerminationReply(t *testing.T) {
	failure := errors.New("synthetic authoritative termination failure")
	for range 128 {
		session := &Session{requests: make(chan sessionRequest), done: make(chan struct{}), shutdown: make(chan struct{})}
		go func() {
			request := <-session.requests
			request.reply <- sessionResponse{err: failure}
			close(session.done)
		}()
		_, err := session.call(context.Background(), sessionRequest{kind: requestTerminate})
		<-session.done // Join the owned synthetic actor on every iteration.
		if !errors.Is(err, failure) {
			t.Fatalf("closed actor replaced its authoritative reply: %v", err)
		}
	}
}

func TestSessionTerminationBoundaryRevokesQueuedInput(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "natural-wait", true: "explicit-terminate"}[explicit], func(t *testing.T) {
			controller, err := NewController(testGeneration)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := controller.Acquire(testGeneration, "connection", "device")
			if err != nil {
				t.Fatal(err)
			}
			inbox, err := NewInputInbox(controller, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer inbox.Close()
			for sequence := uint64(1); sequence <= 2; sequence++ {
				if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, sequence, InputAction{Kind: InputKindText, Text: "queued"}); err != nil {
					t.Fatal(err)
				}
			}
			session := &Session{controller: controller, inbox: inbox, process: newInstrumentedProcess(nil)}
			status := SessionStatus{State: SessionRunning}
			if explicit {
				request := sessionRequest{kind: requestTerminate, reply: make(chan sessionResponse, 1)}
				session.handleRequest(&status, request)
				defer func() { <-session.termination.done }() // Join the owned worker even on failure.
			} else {
				// The natural Wait branch uses this same actor-owned transition.
				session.markExiting(&status)
			}
			if status.State != SessionExiting || controller.State().ConnectionID != "" {
				t.Fatal("termination boundary retained the controller lease")
			}
			if _, ok, err := inbox.TryNext(); err != nil || ok {
				t.Fatalf("termination retained already-accepted, not-yet-encoded input: queued=%t err=%v", ok, err)
			}
		})
	}
}

func TestSessionTerminationDrainsDespiteContinuouslyQueuedStatus(t *testing.T) {
	core := &drainingTerminationCore{stubTerminal: newBlockingStubTerminal(), parsed: make(chan struct{})}
	process := &drainingTerminationProcess{instrumentedProcess: newInstrumentedProcess(nil),
		chunks: make(chan []byte), parsed: core.parsed, started: make(chan struct{}),
		finish: make(chan struct{}), finished: make(chan struct{})}
	core.terminationFinished = process.finished
	ctx, cancel := context.WithCancel(context.Background())
	session, err := startSessionRuntime(ctx, cancel, nil, SessionConfig{
		ID: testSessionID, Generation: testGeneration, Process: ptyhost.Spec{Columns: 80, Rows: 24},
		InputQueueCapacity: DefaultInputQueueCapacity, PTYOutputQueue: 1,
	}, process, core)
	if err != nil {
		t.Fatal(err)
	}
	var snapshotRelease sync.Once
	stop, pumpDone := make(chan struct{}), make(chan struct{})
	responses := make(chan sessionResponse)
	defer func() {
		close(stop)
		snapshotRelease.Do(func() { close(core.snapshotRelease) })
		process.allowFinish()
		process.release()
		closed := make(chan error, 1)
		go func() { closed <- session.Close() }()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case <-responses: // Join a response already being handed to the fixture.
			case err := <-closed:
				if err != nil {
					t.Error(err)
				}
				<-pumpDone
				return
			case <-deadline.C:
				t.Error("status pump or session did not join")
				return
			}
		}
	}()
	// Hold the actor at a real snapshot boundary while constructing a bounded
	// request queue with termination first and Status requests behind it.
	session.requests <- sessionRequest{kind: requestSnapshot, reply: make(chan sessionResponse, 1)}
	<-core.snapshotStarted
	terminationReply := make(chan sessionResponse, 1)
	session.requests <- sessionRequest{kind: requestTerminate, reply: terminationReply}
	for range cap(session.requests) - 1 {
		session.requests <- sessionRequest{kind: requestStatus, reply: responses}
	}
	go func() {
		defer close(pumpDone)
		for {
			// Refill before acknowledging a status response, keeping the bounded
			// queue continuously non-empty without unbounded goroutines/storage.
			select {
			case session.requests <- sessionRequest{kind: requestStatus, reply: responses}:
			case <-stop:
				return
			}
			select {
			case <-responses:
			case <-stop:
				return
			}
		}
	}()
	snapshotRelease.Do(func() { close(core.snapshotRelease) })
	select {
	case <-core.parsed:
	case <-time.After(time.Second):
		t.Fatal("queued status traffic starved the termination output drain")
	}
	process.allowFinish()
	select {
	case reply := <-terminationReply:
		if reply.err != nil {
			t.Fatal(reply.err)
		}
	case <-time.After(time.Second):
		t.Fatal("status traffic starved termination completion")
	}
}

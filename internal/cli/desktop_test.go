package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/liveclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type desktopFakeConnection struct {
	mu         sync.Mutex
	state      liveclient.State
	done       chan struct{}
	changed    chan struct{}
	sent       int
	resizes    [][2]int
	sendErr    error
	closeErr   error
	sendStart  chan struct{}
	sendResume chan struct{}
	closeOnce  sync.Once
	stateRead  chan struct{}
}

func desktopFake() *desktopFakeConnection {
	return &desktopFakeConnection{state: liveclient.State{Connected: true, Ready: true, LeaseEpoch: "1", InputEpoch: 1}, done: make(chan struct{}), changed: make(chan struct{}, 1)}
}
func (client *desktopFakeConnection) State() liveclient.State {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.stateRead != nil {
		select {
		case client.stateRead <- struct{}{}:
		default:
		}
	}
	return client.state
}
func (client *desktopFakeConnection) setState(state liveclient.State) {
	client.mu.Lock()
	if state.Ready != client.state.Ready || state.Controller != client.state.Controller || state.Connected != client.state.Connected || state.LeaseEpoch != client.state.LeaseEpoch {
		state.InputEpoch = client.state.InputEpoch + 1
	}
	client.state = state
	client.mu.Unlock()
}

func (client *desktopFakeConnection) SendAdmitted(ctx context.Context, action desktop.InputAction, epoch uint64) error {
	state := client.State()
	if epoch == 0 || epoch != state.InputEpoch || !state.Ready || !state.Controller {
		return liveclient.ErrNotReady
	}
	return client.Send(ctx, action)
}
func (client *desktopFakeConnection) Send(ctx context.Context, _ desktop.InputAction) error {
	client.mu.Lock()
	client.sent++
	client.mu.Unlock()
	if client.sendStart != nil {
		close(client.sendStart)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.sendResume:
		}
	}
	return client.sendErr
}
func (client *desktopFakeConnection) Resize(_ context.Context, columns, rows int) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.resizes = append(client.resizes, [2]int{columns, rows})
	return nil
}
func (client *desktopFakeConnection) Done() <-chan struct{}    { return client.done }
func (client *desktopFakeConnection) Changed() <-chan struct{} { return client.changed }
func (client *desktopFakeConnection) Err() error               { return nil }
func (client *desktopFakeConnection) Close() error {
	client.closeOnce.Do(func() { close(client.done) })
	return client.closeErr
}

type desktopBuffer struct{ bytes.Buffer }

func (output *desktopBuffer) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return output.Write(data)
}

func testDesktopDisplay(columns, rows int) (*desktopDisplay, *desktopBuffer) {
	output := &desktopBuffer{}
	return &desktopDisplay{writer: output, screen: &protocol.ScreenMetadata{Columns: 20, Rows: 5}, size: func() (int, int, error) { return columns, rows, nil }}, output
}

func TestDesktopObserverTypingIsDiscardedAndDetachStaysLocal(t *testing.T) {
	client := desktopFake()
	display, _ := testDesktopDisplay(80, 24)
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	controller := desktop.InputController{}
	for _, event := range []uv.Event{uv.KeyPressEvent{Code: 'x', Text: "x"}, uv.PasteEvent{Content: "ignored"}, uv.KeyPressEvent{Code: uv.KeyEnter}} {
		detach, err := controller.Handle(context.Background(), event, transport)
		if err != nil || detach {
			t.Fatalf("observer input stopped attachment: detach=%v err=%v", detach, err)
		}
	}
	detach, err := controller.Handle(context.Background(), uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}, transport)
	if err != nil || !detach || client.sent != 0 {
		t.Fatalf("detach=%v sends=%d err=%v", detach, client.sent, err)
	}
	state := client.State()
	state.Controller = true
	client.setState(state)
	if err := transport.Send(context.Background(), desktop.TextAction{Text: "new"}); err != nil || client.sent != 1 {
		t.Fatalf("fresh controller action: sends=%d err=%v", client.sent, err)
	}
}

func TestDesktopControllerRejectsConcurrentInputWithoutQueue(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	client.sendStart, client.sendResume = make(chan struct{}), make(chan struct{})
	display, _ := testDesktopDisplay(80, 24)
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	result := make(chan error, 1)
	go func() { result <- transport.Send(context.Background(), desktop.TextAction{Text: "one"}) }()
	<-client.sendStart
	if err := transport.Send(context.Background(), desktop.TextAction{Text: "two"}); !errors.Is(err, liveclient.ErrBusy) {
		t.Fatalf("overlapping input = %v", err)
	}
	close(client.sendResume)
	if err := <-result; err != nil || client.sent != 1 {
		t.Fatalf("sends=%d err=%v", client.sent, err)
	}
}

func TestDesktopTakeoverDiscardsActionWaitingForResize(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	client.stateRead = make(chan struct{}, 1)
	display, _ := testDesktopDisplay(80, 24)
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	transport.gate <- struct{}{} // A bounded resize write is already in flight.
	result := make(chan error, 1)
	go func() { result <- transport.Send(context.Background(), desktop.TextAction{Text: "stale"}) }()
	select {
	case <-client.stateRead:
	case <-time.After(time.Second):
		t.Fatal("input did not observe its initial lease")
	}
	state := client.State()
	state.LeaseEpoch = "2" // Even a newer local lease must not inherit old input.
	client.setState(state)
	<-transport.gate
	if err := <-result; err != nil || client.sent != 0 {
		t.Fatalf("stale action was delivered: sends=%d err=%v", client.sent, err)
	}
}

func TestDesktopControlErrorsDoNotHideDeliveryUncertainty(t *testing.T) {
	for _, test := range []struct {
		err     error
		ignored bool
	}{
		{liveclient.ErrNotController, true}, {liveclient.ErrNotReady, true},
		{&liveclient.RemoteError{Code: protocol.StaleLease}, true},
		{errors.Join(liveclient.ErrDeliveryUncertain, liveclient.ErrNotController), false},
		{errors.New("write failed"), false},
	} {
		if got := inactiveDesktopControl(test.err); got != test.ignored {
			t.Fatalf("inactive(%v)=%v", test.err, got)
		}
	}
}

func TestDesktopCancelledInputCannotWaitForLaterResize(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	client.stateRead = make(chan struct{}, 1)
	display, _ := testDesktopDisplay(80, 24)
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	transport.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- transport.Send(ctx, desktop.TextAction{Text: "cancelled"}) }()
	<-client.stateRead
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled action=%v", err)
	}
	<-transport.gate
	if client.sent != 0 {
		t.Fatal("cancelled action survived resize")
	}
}

func TestDesktopResizeOnlyControllerAndCoalesces(t *testing.T) {
	client := desktopFake()
	display, _ := testDesktopDisplay(500, 300)
	display.screen = &protocol.ScreenMetadata{Columns: 80, Rows: 24}
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	if err := transport.resize(context.Background()); err != nil || len(client.resizes) != 0 {
		t.Fatalf("observer resized: %v %v", client.resizes, err)
	}
	client.state.Controller = true
	for range 2 {
		if err := transport.resize(context.Background()); err != nil {
			t.Fatal(err)
		}
		display.screen = &protocol.ScreenMetadata{Columns: 400, Rows: 200}
	}
	if len(client.resizes) != 1 || client.resizes[0] != [2]int{400, 200} {
		t.Fatalf("controller sizes = %v", client.resizes)
	}
	for _, size := range [][2]int{{80, 24}, {20, 5}} {
		display.size = func() (int, int, error) { return size[0], size[1], nil }
		if err := transport.resize(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if client.resizes[1] != [2]int{80, 23} || client.resizes[2] != [2]int{20, 5} {
		t.Fatalf("footer/minimum sizes = %v", client.resizes)
	}
}

func TestDesktopViewportDoesNotOverwriteExactFit(t *testing.T) {
	display, output := testDesktopDisplay(80, 24)
	screen := protocol.ScreenMetadata{Columns: 80, Rows: 24, Cursor: protocol.Cursor{Row: 3, Column: 5}}
	if err := display.validate(screen); err != nil {
		t.Fatal(err)
	}
	if _, err := display.WriteContext(context.Background(), []byte("synthetic drawing")); err != nil || output.String() != "synthetic drawing" {
		t.Fatalf("exact-fit drawing=%q err=%v", output.String(), err)
	}
	display.size = func() (int, int, error) { return 80, 25, nil }
	output.Reset()
	if err := display.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "\x1b[25;1H") || !strings.Contains(output.String(), "Read-only") || !strings.HasSuffix(output.String(), "\x1b[4;6H") {
		t.Fatalf("footer overwrote grid or cursor: %q", output.String())
	}
	display.size = func() (int, int, error) { return 79, 25, nil }
	output.Reset()
	if _, err := display.WriteContext(context.Background(), []byte("MUST NOT DRAW")); err == nil || !strings.Contains(err.Error(), "enlarge") || output.Len() != 0 {
		t.Fatalf("oversized screen drawn: %q %v", output.String(), err)
	}
}

func TestDesktopClosePreservesJoinedCleanupErrors(t *testing.T) {
	failure := errors.New("native cleanup failure")
	for _, closeErr := range []error{context.Canceled, errors.Join(context.Canceled, failure)} {
		client := desktopFake()
		client.closeErr = closeErr
		display, _ := testDesktopDisplay(80, 24)
		transport := newDesktopTransport(context.Background(), client, display)
		transport.start()
		err := transport.Close()
		if closeErr == context.Canceled && err != nil || closeErr != context.Canceled && !errors.Is(err, failure) {
			t.Fatalf("Close(%v)=%v", closeErr, err)
		}
	}
}

func TestRestorationWriterUsesFreshBoundedContext(t *testing.T) {
	output := &desktopBuffer{}
	writer := restorationWriter{writer: output}
	if n, err := writer.Write([]byte("restore")); err != nil || n != 7 {
		t.Fatalf("restoration write=%d,%v", n, err)
	}
}

type desktopCleanupOutput struct {
	started chan struct{}
	failure error
}

func (output desktopCleanupOutput) WriteContext(ctx context.Context, _ []byte) (int, error) {
	close(output.started)
	<-ctx.Done()
	return 0, errors.Join(ctx.Err(), output.failure)
}

func TestDesktopCloseJoinsStatusWriterAndPreservesLateCleanup(t *testing.T) {
	failure := errors.New("status native cleanup failed")
	output := desktopCleanupOutput{started: make(chan struct{}), failure: failure}
	client := desktopFake()
	display, _ := testDesktopDisplay(80, 25)
	display.writer = output
	if err := display.validate(protocol.ScreenMetadata{Columns: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	transport := newDesktopTransport(context.Background(), client, display)
	transport.start()
	client.changed <- struct{}{}
	select {
	case <-output.started:
	case <-time.After(time.Second):
		t.Fatal("status writer did not begin")
	}
	if err := transport.Close(); !errors.Is(err, failure) {
		t.Fatalf("status cleanup was lost: %v", err)
	}
}

func TestDesktopShrinkShowsSyncingWithoutQueueingInput(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	display, output := testDesktopDisplay(80, 25)
	display.setClient(client)
	if err := display.validate(protocol.ScreenMetadata{Columns: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	display.size = func() (int, int, error) { return 20, 5, nil }
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	if err := transport.Send(context.Background(), desktop.TextAction{Text: "must not queue"}); err != nil {
		t.Fatal(err)
	}
	if client.sent != 0 || !strings.Contains(output.String(), "Syncing") {
		t.Fatalf("shrink input/status = %d / %q", client.sent, output.String())
	}
	state := client.State()
	state.Controller, state.Ready, state.Resizing = false, false, true
	client.setState(state)
	if err := display.status(context.Background()); err != nil {
		t.Fatalf("takeover prematurely stopped pending snapshot: %v", err)
	}
	state.Controller, state.Ready, state.Resizing = true, true, false
	client.setState(state)
	if err := display.validate(protocol.ScreenMetadata{Columns: 20, Rows: 5}); err != nil {
		t.Fatal(err)
	}
	if err := transport.Send(context.Background(), desktop.TextAction{Text: "fresh"}); err != nil || client.sent != 1 {
		t.Fatalf("fresh input = %d %v", client.sent, err)
	}
}

func TestDesktopRecoveredSizeDoesNotTriggerDuplicateResize(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	display, _ := testDesktopDisplay(60, 18)
	if err := display.validate(protocol.ScreenMetadata{Columns: 60, Rows: 17}); err != nil {
		t.Fatal(err)
	}
	transport := newDesktopTransport(context.Background(), client, display)
	defer transport.Close()
	if err := transport.resize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.resizes) != 0 {
		t.Fatalf("duplicate resize=%v", client.resizes)
	}
	// Grow back before the next shrink-sized poll would have refreshed cache.
	display.size = func() (int, int, error) { return 80, 24, nil }
	if err := transport.resize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.resizes) != 1 || client.resizes[0] != [2]int{80, 23} {
		t.Fatalf("stale cache suppressed growth: %v", client.resizes)
	}
}

type desktopReadyBarrier struct{ entered, ready chan struct{} }

func (barrier desktopReadyBarrier) WaitReady(ctx context.Context) error {
	close(barrier.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-barrier.ready:
		return nil
	}
}

func TestDesktopStartupKeepsSyncingUntilLiveDecoderActivation(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	display, output := testDesktopDisplay(80, 25)
	display.setClient(client)
	display.controlPending = true
	barrier := desktopReadyBarrier{entered: make(chan struct{}), ready: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- finishDesktopStartup(context.Background(), barrier, display)
	}()
	<-barrier.entered
	if err := display.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Control |") || !strings.Contains(output.String(), "Syncing") {
		t.Fatalf("premature control: %q", output.String())
	}
	close(barrier.ready)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Control |") {
		t.Fatal("ready input advertised before live decoder activation")
	}
	admitted := false
	if err := display.activate(context.Background(), func() { admitted = true }); err != nil {
		t.Fatal(err)
	}
	if !admitted || !strings.Contains(output.String(), "Control |") {
		t.Fatal("live input activation not advertised")
	}
}

func TestDesktopStartupCanceledBeforeReadyDoesNotAdvertiseControl(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	display, output := testDesktopDisplay(80, 25)
	display.setClient(client)
	display.controlPending = true
	barrier := desktopReadyBarrier{entered: make(chan struct{}), ready: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := finishDesktopStartup(ctx, barrier, display)
	if !errors.Is(err, context.Canceled) || !display.controlPending || strings.Contains(output.String(), "Control |") {
		t.Fatalf("failed startup advertised control: %v %q", err, output.String())
	}
}

func TestDesktopRejectedInputShowsBriefFooterNotice(t *testing.T) {
	client := desktopFake()
	client.state.Controller = true
	display, output := testDesktopDisplay(80, 24)
	display.setClient(client)
	rejected := &desktop.InvalidInputError{Kind: "paste", Err: errors.New("control byte")}
	if err := display.rejectInput(context.Background(), rejected); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Input not sent: invalid paste") {
		t.Fatalf("rejection footer = %q", output.String())
	}
	output.Reset()
	if err := display.expireNotice(context.Background()); err != nil || output.Len() != 0 {
		t.Fatalf("fresh notice expired early: %q %v", output.String(), err)
	}
	display.noticeUntil = time.Now().Add(-time.Second)
	if err := display.expireNotice(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); strings.Contains(got, "Input not sent") || !strings.Contains(got, "Control |") {
		t.Fatalf("expired notice footer = %q", got)
	}
}

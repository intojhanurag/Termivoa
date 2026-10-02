package liveclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	testBroker     = "11111111-1111-4111-8111-111111111111"
	testSession    = "22222222-2222-4222-8222-222222222222"
	testGeneration = "33333333-3333-4333-8333-333333333333"
	testHolder     = "44444444-4444-4444-8444-444444444444"
	testOther      = "55555555-5555-4555-8555-555555555555"
	testLease      = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type bufferOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (output *bufferOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.Buffer.Write(data)
}
func (output *bufferOutput) text() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.Buffer.String()
}

type fakeHost struct {
	conn     net.Conn
	reader   *ipc.FrameReader
	writer   *ipc.FrameWriter
	requests chan protocol.Envelope
	done     chan struct{}
	attach   protocol.Envelope
}

func newClientFixture(t *testing.T, output Output, configure ...func(*Config)) (*Client, *fakeHost, context.CancelFunc) {
	t.Helper()
	server, peer := net.Pipe()
	host := &fakeHost{conn: server, requests: make(chan protocol.Envelope, 32), done: make(chan struct{})}
	host.reader, _ = ipc.NewFrameReader(server, protocol.MaxInboundMessageBytes)
	host.writer, _ = ipc.NewFrameWriter(server, protocol.MaxOutboundMessageBytes)
	go func() {
		defer close(host.done)
		if _, err := ipc.ServerHandshake(context.Background(), server, testBroker, "fixture"); err != nil {
			return
		}
		data, err := host.reader.ReadFrame()
		if err != nil {
			return
		}
		hello, err := protocol.DecodeClient(data)
		if err != nil || hello.Type != protocol.Hello {
			return
		}
		if err := host.sendMessage(protocol.Welcome, hello.RequestID, "", welcomePayload()); err != nil {
			return
		}
		for {
			data, err := host.reader.ReadFrame()
			if err != nil {
				return
			}
			request, err := protocol.DecodeClient(data)
			if err != nil {
				return
			}
			select {
			case host.requests <- request:
			case <-time.After(time.Second):
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	welcome, err := ipc.ClientHandshake(ctx, peer, protocol.CLI, "test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	config := Config{SessionID: testSession, Generation: testGeneration, ClientBuild: "test", Output: output}
	for _, update := range configure {
		update(&config)
	}
	client, err := start(ctx, peer, welcome, config)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = client.Close(); _ = server.Close(); <-host.done })
	host.attach = host.await(t, protocol.SessionAttach)
	host.send(t, protocol.SessionState, "", protocol.SessionStatePayload{State: protocol.Running})
	return client, host, cancel
}

func welcomePayload() protocol.WelcomePayload {
	return protocol.WelcomePayload{BrokerGeneration: testBroker, HostBuild: "fixture", NegotiatedVersion: 1, CapabilityProfile: protocol.PhoneTerminalV1,
		Limits: protocol.ProtocolLimits{InboundMessageBytes: protocol.MaxInboundMessageBytes, OutboundMessageBytes: protocol.MaxOutboundMessageBytes,
			OutgoingQueueBytes: maxFrameQueueBytes, MaxTextBytes: protocol.MaxTextBytes, MaxPasteBytes: protocol.MaxPasteBytes,
			MinColumns: protocol.MinColumns, MaxColumns: protocol.MaxColumns, MinRows: protocol.MinRows, MaxRows: protocol.MaxRows, MaxHistoryPageRows: protocol.MaxHistoryPageRows}}
}

func (host *fakeHost) sendMessage(kind protocol.MessageType, requestID, generation string, payload any) error {
	if requestID == "" {
		requestID = uuid.New().String()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	envelope := protocol.Envelope{Version: 1, Type: kind, RequestID: requestID, Payload: data}
	if generation != "" {
		id := testSession
		envelope.SessionID, envelope.Generation = &id, &generation
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := protocol.DecodeHost(encoded); err != nil {
		return err
	}
	_ = host.conn.SetWriteDeadline(time.Now().Add(time.Second))
	return host.writer.WriteFrame(encoded)
}

func (host *fakeHost) send(t *testing.T, kind protocol.MessageType, requestID string, payload any) {
	t.Helper()
	if err := host.sendMessage(kind, requestID, testGeneration, payload); err != nil {
		t.Fatal(err)
	}
}

func (host *fakeHost) await(t *testing.T, kind protocol.MessageType) protocol.Envelope {
	t.Helper()
	select {
	case request := <-host.requests:
		if request.Type != kind {
			t.Fatalf("request = %s, want %s", request.Type, kind)
		}
		return request
	case <-time.After(time.Second):
		t.Fatalf("waiting for %s", kind)
	}
	return protocol.Envelope{}
}

func snapshot(t *testing.T, text string) (protocol.ScreenMetadata, []protocol.TerminalRow) {
	t.Helper()
	core, err := terminal.New(context.Background(), terminal.Config{Columns: 20, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := core.WriteOutput(context.Background(), []byte(text)); err != nil {
		t.Fatal(err)
	}
	frame, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	screen, rows, err := protocol.Snapshot(frame, frame.HistoryEpoch, frame.HistoryRevision)
	if err != nil {
		t.Fatal(err)
	}
	return screen, rows
}

func (host *fakeHost) snapshot(t *testing.T, revision string, screen protocol.ScreenMetadata, rows []protocol.TerminalRow) string {
	t.Helper()
	id := uuid.New().String()
	host.send(t, protocol.SnapshotBegin, "", protocol.SnapshotBeginPayload{SnapshotID: id, Revision: revision, Screen: screen})
	host.send(t, protocol.SnapshotRows, "", protocol.SnapshotRowsPayload{SnapshotID: id, Revision: revision, Rows: rows})
	host.send(t, protocol.SnapshotEnd, "", protocol.SnapshotEndPayload{SnapshotID: id, Revision: revision, RowCount: screen.Rows})
	return id
}

func readyClient(t *testing.T) (*Client, *fakeHost, *bufferOutput) {
	t.Helper()
	output := &bufferOutput{}
	client, host, _ := newClientFixture(t, output)
	screen, rows := snapshot(t, "ready")
	host.snapshot(t, "9007199254740993", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	return client, host, output
}

func acquire(t *testing.T, client *Client, host *fakeHost, epoch string) {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- client.AcquireControl(context.Background()) }()
	request := host.await(t, protocol.ControlAcquire)
	conn, lease := testHolder, testLease
	host.send(t, protocol.ControlChanged, request.RequestID, protocol.ControlChangedPayload{ControllerConnectionID: &conn, Lease: &lease, LeaseEpoch: epoch})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func waitState(t *testing.T, client *Client, predicate func(State) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for !predicate(client.State()) {
		select {
		case <-client.Changed():
		case <-ctx.Done():
			t.Fatalf("state not reached: %#v", client.State())
		}
	}
}

func TestSnapshotPaintThenAckObserverAndResync(t *testing.T) {
	output := &bufferOutput{}
	client, host, _ := newClientFixture(t, output)
	if err := client.AcquireControl(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("early acquire = %v", err)
	}
	screen, rows := snapshot(t, "hello")
	id := uuid.New().String()
	host.send(t, protocol.SnapshotBegin, "", protocol.SnapshotBeginPayload{SnapshotID: id, Revision: "9007199254740993", Screen: screen})
	host.send(t, protocol.SnapshotRows, "", protocol.SnapshotRowsPayload{SnapshotID: id, Revision: "9007199254740993", Rows: rows})
	if output.text() != "" {
		t.Fatal("partial snapshot painted")
	}
	host.send(t, protocol.SnapshotEnd, "", protocol.SnapshotEndPayload{SnapshotID: id, Revision: "9007199254740993", RowCount: 5})
	ack, _ := protocol.DecodePayload[protocol.SnapshotAppliedPayload](host.await(t, protocol.SnapshotApplied))
	if ack.SnapshotID != id || ack.Revision != "9007199254740993" || output.text() == "" {
		t.Fatal("snapshot ack preceded paint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if client.State().Controller {
		t.Fatal("attach automatically acquired control")
	}
	if err := client.Resize(ctx, 80, 24); !errors.Is(err, ErrNotController) {
		t.Fatalf("observer resize = %v", err)
	}
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "0", Revision: "1", Screen: screen, Rows: []protocol.ChangedRow{}})
	host.await(t, protocol.ScreenResync)
	if client.State().Ready {
		t.Fatal("bad patch retained readiness")
	}
	host.snapshot(t, "9007199254740995", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestInputAcceptanceSequenceRejectionAndNoQueuedCalls(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	result := make(chan error, 1)
	go func() { result <- client.Send(context.Background(), desktop.TextAction{Text: "one"}) }()
	first := host.await(t, protocol.InputText)
	payload, _ := protocol.DecodePayload[protocol.InputTextPayload](first)
	if payload.InputSequence != "1" || payload.Lease != testLease {
		t.Fatalf("first input = %#v", payload)
	}
	if err := client.Send(context.Background(), desktop.TextAction{Text: "queued"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent input = %v", err)
	}
	host.send(t, protocol.InputRejected, first.RequestID, protocol.InputRejectedPayload{Code: protocol.LimitExceeded, Message: "limited", InputSequence: &payload.InputSequence})
	var remote *RemoteError
	if err := <-result; !errors.As(err, &remote) || remote.Code != protocol.LimitExceeded {
		t.Fatalf("rejection = %v", err)
	}
	go func() { result <- client.Send(context.Background(), desktop.TextAction{Text: "explicit retry"}) }()
	second := host.await(t, protocol.InputText)
	payload, _ = protocol.DecodePayload[protocol.InputTextPayload](second)
	if payload.InputSequence != "1" {
		t.Fatal("rejected action consumed sequence")
	}
	host.send(t, protocol.InputAccepted, second.RequestID, protocol.InputAcceptedPayload{InputSequence: "1"})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	go func() { result <- client.Send(context.Background(), desktop.KeyAction{Key: protocol.Enter}) }()
	third := host.await(t, protocol.InputKey)
	key, _ := protocol.DecodePayload[protocol.InputKeyPayload](third)
	if key.InputSequence != "2" || key.Key != protocol.Enter {
		t.Fatalf("next input = %#v", key)
	}
	host.send(t, protocol.InputAccepted, third.RequestID, protocol.InputAcceptedPayload{InputSequence: "2"})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverBeforeInputAckDoesNotRestoreLease(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	result := make(chan error, 1)
	go func() { result <- client.Send(context.Background(), desktop.TextAction{Text: "in flight"}) }()
	request := host.await(t, protocol.InputText)
	other := testOther
	host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{ControllerConnectionID: &other, LeaseEpoch: "2"})
	host.send(t, protocol.InputAccepted, request.RequestID, protocol.InputAcceptedPayload{InputSequence: "1"})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if client.State().Controller {
		t.Fatal("late acceptance restored old controller")
	}
	old := testHolder
	host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{ControllerConnectionID: &old, LeaseEpoch: "1"})
	waitState(t, client, func(state State) bool { return state.ControllerConnectionID == testOther })
	if err := client.Send(context.Background(), desktop.TextAction{Text: "stale"}); !errors.Is(err, ErrNotController) {
		t.Fatalf("stale input = %v", err)
	}
}

func TestUnsolicitedOrWrongRequestGrantClosesWithoutControl(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsolicited", true: "wrong-request"}[pending], func(t *testing.T) {
			client, host, _ := readyClient(t)
			result := make(chan error, 1)
			if pending {
				go func() { result <- client.AcquireControl(context.Background()) }()
				host.await(t, protocol.ControlAcquire)
			}
			lease, holder := testLease, testHolder
			host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{ControllerConnectionID: &holder, Lease: &lease, LeaseEpoch: "1"})
			select {
			case <-client.Done():
			case <-time.After(time.Second):
				t.Fatal("unsolicited grant not rejected")
			}
			if !errors.Is(client.Err(), ErrProtocol) || client.State().Controller {
				t.Fatalf("grant state = %#v, %v", client.State(), client.Err())
			}
			if pending {
				if err := <-result; err == nil {
					t.Fatal("wrong request grant completed acquisition")
				}
			}
		})
	}
}

func TestLostInputAckIsUncertainAndNeverRetransmitted(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	result := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { result <- client.Send(ctx, desktop.PasteAction{Text: "one\ntwo"}) }()
	host.await(t, protocol.InputPaste)
	cancel()
	if err := <-result; !errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("missing acknowledgement = %v", err)
	}
	if !errors.Is(client.Err(), ErrDeliveryUncertain) {
		t.Fatalf("disconnect state hid input uncertainty: %v", client.Err())
	}
	if client.State().Connected || client.State().Controller {
		t.Fatal("uncertain connection remained usable")
	}
	if err := client.Send(context.Background(), desktop.TextAction{Text: "offline"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("offline input = %v", err)
	}
	select {
	case request := <-host.requests:
		t.Fatalf("unexpected retransmission %s", request.Type)
	default:
	}
}

type blockedOutput struct {
	entered chan struct{}
	exited  chan struct{}
	once    sync.Once
}

func (output *blockedOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	output.once.Do(func() { close(output.entered) })
	<-ctx.Done()
	close(output.exited)
	return 0, ctx.Err()
}

func TestBlockedPaintCancellationJoinsBeforeRestoration(t *testing.T) {
	output := &blockedOutput{entered: make(chan struct{}), exited: make(chan struct{})}
	client, host, cancel := newClientFixture(t, output)
	screen, rows := snapshot(t, "blocked")
	host.snapshot(t, "1", screen, rows)
	select {
	case <-output.entered:
	case <-time.After(time.Second):
		t.Fatal("paint did not begin")
	}
	other := testOther
	host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{ControllerConnectionID: &other, LeaseEpoch: "3"})
	waitState(t, client, func(state State) bool { return state.LeaseEpoch == "3" })
	cancel()
	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancel did not join painting")
	}
	select {
	case <-output.exited:
	default:
		t.Fatal("Close returned before output exited")
	}
	select {
	case request := <-host.requests:
		t.Fatalf("acknowledged an unpainted frame: %s", request.Type)
	default:
	}
}

func TestHeartbeatReleaseAndResizeUseCurrentLease(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	if err := client.heartbeat(); err != nil {
		t.Fatal(err)
	}
	heartbeat, _ := protocol.DecodePayload[protocol.HeartbeatPayload](host.await(t, protocol.Heartbeat))
	if heartbeat.Lease == nil || *heartbeat.Lease != testLease {
		t.Fatal("heartbeat missing current lease")
	}
	if err := client.Resize(context.Background(), 100, 40); err != nil {
		t.Fatal(err)
	}
	resize, _ := protocol.DecodePayload[protocol.TerminalResizePayload](host.await(t, protocol.TerminalResize))
	if resize.Lease != testLease || resize.Columns != 100 || resize.Rows != 40 {
		t.Fatal("invalid resize")
	}
	host.await(t, protocol.ScreenResync)
	if err := client.ReleaseControl(context.Background()); err != nil {
		t.Fatal(err)
	}
	host.await(t, protocol.ControlRelease)
	if err := client.heartbeat(); err != nil {
		t.Fatal(err)
	}
	if client.State().Controller {
		t.Fatal("release retained local input")
	}
	if err := client.Detach(context.Background()); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	host.await(t, protocol.SessionDetach)
}

func TestChangedGenerationNeverPaintsAndCloses(t *testing.T) {
	client, host, output := readyClient(t)
	before := output.text()
	screen, rows := snapshot(t, "different")
	id := uuid.New().String()
	err := host.sendMessage(protocol.SnapshotBegin, "", testOther, protocol.SnapshotBeginPayload{SnapshotID: id, Revision: "2", Screen: screen})
	_ = rows
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("generation change not detected")
	}
	if !errors.Is(client.Err(), ErrGenerationChanged) || output.text() != before {
		t.Fatal("changed generation was painted")
	}
}

func TestInputValidationDoesNotWriteOrConsumeSequence(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	for _, action := range []desktop.InputAction{desktop.TextAction{Text: "\x1b[6n"}, desktop.PasteAction{Text: strings.Repeat("x", protocol.MaxPasteBytes+1)}} {
		if err := client.Send(context.Background(), action); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	select {
	case request := <-host.requests:
		t.Fatalf("invalid input sent: %s", request.Type)
	default:
	}
	client.mu.Lock()
	sequence := client.sequence
	client.mu.Unlock()
	if sequence != 0 {
		t.Fatal("invalid input advanced sequence")
	}
}

func TestPendingPaintQueueIsBoundedAndReleased(t *testing.T) {
	output := &blockedOutput{entered: make(chan struct{}), exited: make(chan struct{})}
	client, host, _ := newClientFixture(t, output)
	screen, rows := snapshot(t, "blocked")
	host.snapshot(t, "1", screen, rows)
	select {
	case <-output.entered:
	case <-time.After(time.Second):
		t.Fatal("paint did not begin")
	}
	// 64 tiny complete frame messages exercise the message-count bound without
	// depending on large allocations or timing. Byte budget is checked below.
	for i := 0; i < 65; i++ {
		err := host.sendMessage(protocol.ScreenPatch, "", testGeneration, protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen, Rows: []protocol.ChangedRow{}})
		if err != nil {
			break
		}
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("unbounded paint queue")
	}
	if !errors.Is(client.Err(), ErrSlowDisplay) {
		t.Fatalf("queue failure = %v", client.Err())
	}
	_ = client.Close()
	client.mu.Lock()
	used := client.queuedBytes
	client.mu.Unlock()
	if used != 0 {
		t.Fatalf("retained queue bytes after Close = %d", used)
	}

	bounded := &Client{config: Config{SessionID: testSession, Generation: testGeneration}, statePulse: make(chan struct{}), changed: make(chan struct{}, 1), frameQueue: make(chan frameMessage, 2)}
	id := testSession
	gen := testGeneration
	message := protocol.Envelope{Type: protocol.ScreenPatch, SessionID: &id, Generation: &gen}
	if err := bounded.receive(message, maxFrameQueueBytes-1); err != nil {
		t.Fatal(err)
	}
	if err := bounded.receive(message, 2); !errors.Is(err, ErrSlowDisplay) {
		t.Fatalf("byte bound = %v", err)
	}
}

type failedOutput struct{}

func (failedOutput) WriteContext(context.Context, []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRendererFailureClosesWithoutAcknowledgingOrResyncing(t *testing.T) {
	client, host, _ := newClientFixture(t, failedOutput{})
	screen, rows := snapshot(t, "paint error")
	host.snapshot(t, "1", screen, rows)
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("paint failure kept connection alive")
	}
	if !errors.Is(client.Err(), io.ErrClosedPipe) {
		t.Fatal(client.Err())
	}
	select {
	case request := <-host.requests:
		t.Fatalf("failed paint sent %s", request.Type)
	default:
	}
}

func TestWrongInputAcknowledgementDoesNotAdvanceSequence(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	result := make(chan error, 1)
	go func() { result <- client.Send(context.Background(), desktop.TextAction{Text: "once"}) }()
	host.await(t, protocol.InputText)
	host.send(t, protocol.InputAccepted, "", protocol.InputAcceptedPayload{InputSequence: "1"})
	if err := <-result; !errors.Is(err, ErrDeliveryUncertain) || !errors.Is(err, ErrProtocol) {
		t.Fatalf("wrong request ack = %v", err)
	}
	client.mu.Lock()
	sequence := client.sequence
	client.mu.Unlock()
	if sequence != 0 {
		t.Fatal("wrong ack advanced sequence")
	}
}

type gateOutput struct {
	bufferOutput
	entered chan struct{}
	release chan struct{}
	muGate  sync.Mutex
	calls   int
}

func (output *gateOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	output.muGate.Lock()
	output.calls++
	calls := output.calls
	output.muGate.Unlock()
	if calls == 2 {
		close(output.entered)
		select {
		case <-output.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return output.bufferOutput.WriteContext(ctx, data)
}

func TestOrderlyExitDrainsLastCompletePatchBeforeDone(t *testing.T) {
	output := &gateOutput{entered: make(chan struct{}), release: make(chan struct{})}
	client, host, _ := newClientFixture(t, output)
	screen, rows := snapshot(t, "initial")
	host.snapshot(t, "1", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	_, finalRows := snapshot(t, "LAST")
	host.send(t, protocol.ScreenPatch, "", protocol.ScreenPatchPayload{BaseRevision: "1", Revision: "2", Screen: screen, Rows: []protocol.ChangedRow{{Index: 0, Row: finalRows[0]}}})
	select {
	case <-output.entered:
	case <-time.After(time.Second):
		t.Fatal("final paint did not begin")
	}
	code := int64(0)
	host.send(t, protocol.SessionState, "", protocol.SessionStatePayload{State: protocol.Exited, ExitCode: &code})
	waitState(t, client, func(state State) bool { return state.Lifecycle == protocol.Exited })
	_ = host.conn.Close()
	select {
	case <-client.Done():
		t.Fatal("exit discarded pending final paint")
	default:
	}
	close(output.release)
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("exit not completed after paint")
	}
	if !strings.Contains(output.text(), "L") {
		t.Fatal("final patch was not painted")
	}
	if err := client.Err(); err != nil {
		t.Fatalf("orderly exit reported an error: %v", err)
	}
}

func TestStaleCorrelatedGrantCannotRestoreReleasedEpoch(t *testing.T) {
	client, host, _ := readyClient(t)
	result := make(chan error, 1)
	go func() { result <- client.AcquireControl(context.Background()) }()
	request := host.await(t, protocol.ControlAcquire)
	host.send(t, protocol.ControlChanged, "", protocol.ControlChangedPayload{LeaseEpoch: "1"})
	lease, holder := testLease, testHolder
	host.send(t, protocol.ControlChanged, request.RequestID, protocol.ControlChangedPayload{LeaseEpoch: "1", ControllerConnectionID: &holder, Lease: &lease})
	if err := <-result; !errors.Is(err, ErrProtocol) {
		t.Fatalf("stale correlated grant = %v", err)
	}
	if client.State().Controller {
		t.Fatal("stale grant restored released controller")
	}
}

func TestRequestCancellationDoesNotSendActionWaitingForWriter(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	// Hold the framing gate before any bytes can be written. Cancellation must
	// discard this action rather than release it into a later prompt.
	client.writeGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- client.Send(ctx, desktop.TextAction{Text: "unsent"}) }()
	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		pending := client.pending != nil
		client.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) || errors.Is(err, ErrDeliveryUncertain) {
		t.Fatal(err)
	}
	<-client.writeGate
	if client.State().Connected {
		t.Fatal("canceled action left live connection")
	}
	select {
	case request := <-host.requests:
		t.Fatalf("queued action was sent: %s", request.Type)
	default:
	}
}

func TestWriteCancellationClosesPartialFrame(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{conn: server, ctx: ctx, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}, 1), statePulse: make(chan struct{}),
		writeGate: make(chan struct{}, 1), frameQueue: make(chan frameMessage), stopParent: func() bool { return false }, state: State{Connected: true}}
	client.writer, _ = ipc.NewFrameWriter(server, protocol.MaxInboundMessageBytes)
	defer client.Close()
	message, err := encode(protocol.InputText, testSession, testGeneration, protocol.InputTextPayload{Lease: testLease, InputSequence: "1", Text: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	writeCtx, cancelWrite := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- client.write(writeCtx, message) }()
	header := make([]byte, 4)
	if _, err := io.ReadFull(peer, header); err != nil {
		t.Fatal(err)
	}
	cancelWrite()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("partial write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("partial write did not cancel")
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("poisoned connection remained open: %v", err)
	}
}

// A rejected session.attach (for example the viewer limit) must end WaitReady
// promptly with the broker's reason instead of waiting for a snapshot.
func TestAttachRejectionEndsWaitReadyWithRemoteReason(t *testing.T) {
	client, host, _ := newClientFixture(t, &bufferOutput{})
	host.send(t, protocol.Error, host.attach.RequestID, protocol.ErrorPayload{Code: protocol.LimitExceeded, Message: "session resource limit reached"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.WaitReady(ctx)
	remote, ok := errors.AsType[*RemoteError](err)
	if !ok || remote.Code != protocol.LimitExceeded || !strings.Contains(err.Error(), "session resource limit reached") {
		t.Fatalf("WaitReady after attach rejection = %v", err)
	}
	if client.State().Connected {
		t.Fatal("rejected attachment left an unattached connection open")
	}
}

// JSON HTML escaping would expand each '<' to six bytes and push a valid
// maximum-size paste past the inbound message limit.
func TestMaximumPasteOfHTMLCharactersFitsMessageLimit(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	text := strings.Repeat("<", protocol.MaxPasteBytes)
	result := make(chan error, 1)
	go func() { result <- client.Send(context.Background(), desktop.PasteAction{Text: text}) }()
	request := host.await(t, protocol.InputPaste)
	payload, err := protocol.DecodePayload[protocol.InputPastePayload](request)
	if err != nil || payload.Text != text {
		t.Fatalf("paste payload = %d bytes, %v", len(payload.Text), err)
	}
	host.send(t, protocol.InputAccepted, request.RequestID, protocol.InputAcceptedPayload{InputSequence: payload.InputSequence})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// The broker rejects control while this viewer's newest snapshot is unapplied.
// The client stays attached and observing, and can acquire after applying it.
func TestAcquireResyncRequiredReturnsToObserver(t *testing.T) {
	client, host, _ := readyClient(t)
	result := make(chan error, 1)
	go func() { result <- client.AcquireControl(context.Background()) }()
	request := host.await(t, protocol.ControlAcquire)
	host.send(t, protocol.Error, request.RequestID, protocol.ErrorPayload{Code: protocol.ResyncRequired, Message: "apply the current snapshot before taking control"})
	var remote *RemoteError
	if err := <-result; !errors.As(err, &remote) || remote.Code != protocol.ResyncRequired {
		t.Fatalf("acquire = %v", err)
	}
	if state := client.State(); !state.Connected || state.Ready || state.Controller {
		t.Fatalf("state after rejected acquire = %#v", state)
	}
	screen, rows := snapshot(t, "current")
	host.snapshot(t, "9007199254740994", screen, rows)
	host.await(t, protocol.SnapshotApplied)
	waitState(t, client, func(state State) bool { return state.Ready })
	acquire(t, client, host, "1")
}

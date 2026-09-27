package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

type streamProcess struct {
	*viewerProcess
	mu    sync.Mutex
	input strings.Builder
}

func (process *streamProcess) Write(data []byte) (int, error) {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.input.Write(data)
}

func (process *streamProcess) inputText() string {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.input.String()
}

func newStreamFixture(t *testing.T) (*managementFixture, *Session, *streamProcess) {
	t.Helper()
	fixture := newManagementFixture(t)
	process := &streamProcess{viewerProcess: newViewerProcess()}
	var session *Session
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		config.InputQueueCapacity = DefaultInputQueueCapacity
		config.PTYOutputQueue = DefaultPTYOutputQueue
		ctx, cancel := context.WithCancel(context.Background())
		core, err := terminal.New(ctx, terminal.Config{Columns: config.Process.Columns, Rows: config.Process.Rows})
		if err != nil {
			cancel()
			return nil, err
		}
		session, err = startSessionRuntime(ctx, cancel, parent.Done(), config, process, core)
		return session, err
	}
	if _, err := fixture.server.createSession(context.Background(), protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "86666666-6666-4666-8666-666666666666", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	return fixture, session, process
}

type streamClient struct {
	conn     net.Conn
	writer   *ipc.FrameWriter
	messages chan protocol.Envelope
	done     chan struct{}
	err      error // protected by done close
}

func startStreamClient(t *testing.T, server *ManagementServer, kind protocol.IPCClientKind) *streamClient {
	t.Helper()
	return startWrappedStreamClient(t, server, kind, func(conn net.Conn) net.Conn { return conn })
}

func startWrappedStreamClient(t *testing.T, server *ManagementServer, kind protocol.IPCClientKind, wrap func(net.Conn) net.Conn) *streamClient {
	t.Helper()
	pipeConn, serverConn := net.Pipe()
	clientConn := wrap(pipeConn)
	go func() { _ = server.serveConnection(context.Background(), serverConn) }()
	if _, err := ipc.ClientHandshake(context.Background(), clientConn, kind, "stream-test"); err != nil {
		t.Fatal(err)
	}
	writer, _ := ipc.NewFrameWriter(clientConn, protocol.MaxInboundMessageBytes)
	reader, _ := ipc.NewFrameReader(clientConn, protocol.MaxOutboundMessageBytes)
	client := &streamClient{conn: clientConn, writer: writer, messages: make(chan protocol.Envelope, 64), done: make(chan struct{})}
	go func() {
		defer close(client.done)
		for {
			data, err := reader.ReadFrame()
			if err != nil {
				client.err = err
				return
			}
			envelope, err := protocol.DecodeHost(data)
			if err != nil {
				client.err = err
				return
			}
			select {
			case client.messages <- envelope:
			case <-time.After(3 * time.Second):
				client.err = errors.New("test client stopped consuming messages")
				return
			}
		}
	}()
	t.Cleanup(func() { _ = clientConn.Close(); <-client.done })
	client.send(t, protocol.Hello, nil, nil, protocol.HelloPayload{
		ClientKind: protocol.Desktop, ClientBuild: "test", CapabilityProfile: protocol.PhoneTerminalV1, SupportedVersions: []int64{1},
	})
	return client
}

func (client *streamClient) send(t *testing.T, kind protocol.MessageType, sessionID, generation *string, payload any) string {
	t.Helper()
	id, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(protocol.Envelope{Version: 1, Type: kind, RequestID: id, SessionID: sessionID, Generation: generation, Payload: data})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeClient(message); err != nil {
		t.Fatal(err)
	}
	if err := client.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.writer.WriteFrame(message); err != nil {
		t.Fatal(err)
	}
	return id
}

func (client *streamClient) action(t *testing.T, kind protocol.MessageType, payload any) string {
	id, generation := rpcSessionID, rpcSessionGen
	return client.send(t, kind, &id, &generation, payload)
}

func (client *streamClient) await(t *testing.T, kind protocol.MessageType, requestID string) protocol.Envelope {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case message := <-client.messages:
			if (kind == "" || message.Type == kind) && (requestID == "" || message.RequestID == requestID) {
				return message
			}
		case <-client.done:
			// A final frame can be buffered immediately before EOF.
			for {
				select {
				case message := <-client.messages:
					if (kind == "" || message.Type == kind) && (requestID == "" || message.RequestID == requestID) {
						return message
					}
				default:
					t.Fatalf("stream ended waiting for %s: %v", kind, client.err)
				}
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func (client *streamClient) attach(t *testing.T) protocol.SnapshotBeginPayload {
	t.Helper()
	client.await(t, protocol.Welcome, "")
	client.action(t, protocol.SessionAttach, protocol.EmptyPayload{})
	begin := client.await(t, protocol.SnapshotBegin, "")
	payload, err := protocol.DecodePayload[protocol.SnapshotBeginPayload](begin)
	if err != nil {
		t.Fatal(err)
	}
	client.await(t, protocol.SnapshotEnd, "")
	client.action(t, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: payload.SnapshotID, Revision: payload.Revision})
	return payload
}

func (client *streamClient) acquire(t *testing.T) protocol.ControlChangedPayload {
	t.Helper()
	request := client.action(t, protocol.ControlAcquire, protocol.EmptyPayload{})
	message := client.await(t, protocol.ControlChanged, request)
	grant, err := protocol.DecodePayload[protocol.ControlChangedPayload](message)
	if err != nil || grant.Lease == nil || grant.ControllerConnectionID == nil {
		t.Fatalf("grant = %#v, %v", grant, err)
	}
	return grant
}

func TestSessionStreamControlAndReconnect(t *testing.T) {
	fixture, session, process := newStreamFixture(t)
	first := startStreamClient(t, fixture.server, protocol.CLI)
	first.attach(t)
	grant := first.acquire(t)
	request := first.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "one"})
	first.await(t, protocol.InputAccepted, request)
	request = first.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "one"})
	first.await(t, protocol.InputAccepted, request)
	request = first.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "3", Text: "gap"})
	rejected, _ := protocol.DecodePayload[protocol.InputRejectedPayload](first.await(t, protocol.InputRejected, request))
	if rejected.Code != protocol.InputGap {
		t.Fatalf("gap rejection = %#v", rejected)
	}
	waitStreamInput(t, process, "one")
	second := startStreamClient(t, fixture.server, protocol.CLI)
	second.attach(t)
	secondGrant := second.acquire(t)
	if *secondGrant.Lease == *grant.Lease || secondGrant.LeaseEpoch == grant.LeaseEpoch {
		t.Fatal("takeover reused lease identity")
	}
	request = first.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "2", Text: "stale"})
	rejected, _ = protocol.DecodePayload[protocol.InputRejectedPayload](first.await(t, protocol.InputRejected, request))
	if rejected.Code != protocol.NotController {
		t.Fatalf("old connection rejection = %#v", rejected)
	}
	before, err := session.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = second.conn.Close()
	<-second.done
	third := startStreamClient(t, fixture.server, protocol.CLI)
	third.attach(t)
	request = third.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *secondGrant.Lease, InputSequence: "1", Text: "replay"})
	rejected, _ = protocol.DecodePayload[protocol.InputRejectedPayload](third.await(t, protocol.InputRejected, request))
	if rejected.Code != protocol.NotController {
		t.Fatalf("reconnect auto-acquired control: %#v", rejected)
	}
	after, err := session.Status(context.Background())
	if err != nil || after.PID != before.PID || after.Generation != before.Generation || after.State != SessionRunning {
		t.Fatalf("viewer disconnect changed child: %#v -> %#v, %v", before, after, err)
	}
	if got := process.inputText(); got != "one" {
		t.Fatalf("duplicated/stale input entered child: %q", got)
	}
}

func TestSessionStreamRequiresAppliedSnapshotAndLocalDesktop(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	gateway := startStreamClient(t, fixture.server, protocol.Gateway)
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](gateway.await(t, protocol.Error, ""))
	if failure.Code != protocol.Unauthorized {
		t.Fatalf("gateway impersonated desktop: %#v", failure)
	}
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.await(t, protocol.Welcome, "")
	client.action(t, protocol.SessionAttach, protocol.EmptyPayload{})
	client.await(t, protocol.SnapshotEnd, "")
	request := client.action(t, protocol.ControlAcquire, protocol.EmptyPayload{})
	failure, _ = protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.ResyncRequired {
		t.Fatalf("control before snapshot applied = %#v", failure)
	}
}

func TestSessionStreamQueriesHistoryResizeAndResync(t *testing.T) {
	fixture, session, process := newStreamFixture(t)
	process.Emit([]byte("\x1b[6n"))
	waitStreamInput(t, process, "\x1b[1;1R")
	first := startStreamClient(t, fixture.server, protocol.CLI)
	first.attach(t)
	process.Emit([]byte("\x1b[6n"))
	waitStreamInput(t, process, "\x1b[1;1R\x1b[1;1R")
	second := startStreamClient(t, fixture.server, protocol.CLI)
	second.attach(t)
	process.Emit([]byte("\x1b[6n"))
	waitStreamInput(t, process, "\x1b[1;1R\x1b[1;1R\x1b[1;1R")
	grant := first.acquire(t)
	first.action(t, protocol.TerminalResize, protocol.TerminalResizePayload{Lease: *grant.Lease, Columns: 100, Rows: 30})
	begin, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](first.await(t, protocol.SnapshotBegin, ""))
	first.await(t, protocol.SnapshotEnd, "")
	if begin.Screen.Columns != 100 || begin.Screen.Rows != 30 {
		t.Fatalf("resize = %#v", begin.Screen)
	}
	first.action(t, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})
	request := first.action(t, protocol.HistoryRead, protocol.HistoryReadPayload{
		HistoryEpoch: begin.Screen.HistoryEpoch, HistoryRevision: begin.Screen.HistoryRevision, Offset: 0, Limit: 200,
	})
	first.await(t, protocol.HistoryPage, request)
	request = first.action(t, protocol.HistoryRead, protocol.HistoryReadPayload{HistoryEpoch: "0", HistoryRevision: "0", Offset: 0, Limit: 200})
	first.await(t, protocol.MessageTypeHistoryChanged, request)
	first.action(t, protocol.ScreenResync, protocol.ScreenResyncPayload{Reason: protocol.BaseMismatch})
	resynced, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](first.await(t, protocol.SnapshotBegin, ""))
	first.await(t, protocol.SnapshotEnd, "")
	if resynced.SnapshotID == begin.SnapshotID {
		t.Fatal("resync did not capture a new transaction")
	}
	canonical, err := session.Snapshot(context.Background())
	if err != nil || resynced.Revision != strconv.FormatUint(canonical.Revision, 10) {
		t.Fatalf("resync not canonical: %#v, %v", resynced, err)
	}
}

func waitStreamInput(t *testing.T, process *streamProcess, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if process.inputText() == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("child input = %q, want %q", process.inputText(), expected)
}

func TestSessionWriterConcurrentFramesAndCancellation(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writer := newSessionWriter(server)
	reader, _ := ipc.NewFrameReader(client, protocol.MaxOutboundMessageBytes)
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- writer.Send(context.Background(), []byte(`{"type":"test"}`), func() {})
		}()
	}
	for i := 0; i < 8; i++ {
		data, err := reader.ReadFrame()
		if err != nil || string(data) != `{"type":"test"}` {
			t.Fatalf("interleaved frame %q, %v", data, err)
		}
	}
	workers.Wait()
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := writer.Send(ctx, []byte(`{"blocked":true}`), func() { t.Error("failed frame reported drained") }); err == nil {
		t.Fatal("stalled writer did not time out")
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("partial stream was not closed: %v", err)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.used != 0 {
		t.Fatalf("writer leaked %d bytes", writer.used)
	}
}

func TestSessionWriterEnforcesConnectionBudget(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writer := newSessionWriter(server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message := make([]byte, protocol.MaxOutboundMessageBytes)
	result := make(chan error, 1)
	go func() { result <- writer.Send(ctx, message, func() { t.Error("stalled frame reported drained") }) }()
	deadline := time.Now().Add(time.Second)
	for {
		writer.mu.Lock()
		used := writer.used
		writer.mu.Unlock()
		if used > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first frame never reserved its budget")
		}
		time.Sleep(time.Millisecond)
	}
	if err := writer.Send(ctx, message, func() {}); !errors.Is(err, ErrViewerSlow) {
		t.Fatalf("over-budget frame = %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("over-budget connection remained open")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.used != 0 {
		t.Fatalf("writer retained %d bytes after closure", writer.used)
	}
}

func TestSessionWriterBudgetIncludesQueuedActorNotices(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writer := newSessionWriter(server)
	viewer := &ViewerSubscription{budget: writer}
	// Model retained actor notices before they enter the sink, then a large
	// history response already blocked in the connection writer.
	const notices = 32 * 1024
	if err := viewer.reserveBudget(notices); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- writer.Send(ctx, make([]byte, protocol.MaxOutboundMessageBytes), func() {}) }()
	deadline := time.Now().Add(time.Second)
	for len(writer.gate) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("history response never entered the writer")
		}
		time.Sleep(time.Millisecond)
	}
	// This frame fits each old independent budget but not the shared bound.
	if err := viewer.reserveBudget(protocol.MaxOutboundMessageBytes - 1024); !errors.Is(err, ErrViewerSlow) {
		t.Fatalf("separate actor and response budgets accepted an oversized connection: %v", err)
	}
	_ = client.Close()
	if err := <-result; err == nil {
		t.Fatal("blocked history response completed")
	}
	viewer.releaseBudget(notices)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.used != 0 {
		t.Fatalf("connection leaked %d bytes", writer.used)
	}
}

func TestSessionStreamStructuredInputEncoding(t *testing.T) {
	fixture, session, process := newStreamFixture(t)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	grant := client.acquire(t)
	// Modes are interpreted solely in the authoritative emulator. They never
	// reach a desktop parser as raw child output.
	process.Emit([]byte("\x1b[?2004h\x1b[?1h\x1b[?1000h"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		frame, err := session.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Revision > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mode output not interpreted")
		}
		time.Sleep(time.Millisecond)
	}
	character := "c"
	inputs := []struct {
		kind    protocol.MessageType
		payload any
	}{
		{protocol.InputKey, protocol.InputKeyPayload{Lease: *grant.Lease, InputSequence: "1", Key: protocol.Character, Character: &character, Modifiers: protocol.Modifiers{Ctrl: true}}},
		{protocol.InputKey, protocol.InputKeyPayload{Lease: *grant.Lease, InputSequence: "2", Key: protocol.ArrowUp}},
		{protocol.InputPaste, protocol.InputPastePayload{Lease: *grant.Lease, InputSequence: "3", Text: "one\ntwo"}},
		{protocol.InputMouse, protocol.InputMousePayload{Lease: *grant.Lease, InputSequence: "4", Kind: protocol.Press, Button: protocol.Left, Column: 0, Row: 0}},
	}
	for _, input := range inputs {
		request := client.action(t, input.kind, input.payload)
		client.await(t, protocol.InputAccepted, request)
	}
	waitStreamInput(t, process, "\x03\x1bOA\x1b[200~one\ntwo\x1b[201~\x1b[M !!")
}

// stallingConn models a briefly slow desktop reader: once stalled, it reads at
// most the configured bytes and then blocks until resumed.
type stallingConn struct {
	net.Conn
	mu      sync.Mutex
	allow   int // negative: unlimited
	resumed chan struct{}
}

func (conn *stallingConn) stallAfter(bytes int) {
	conn.mu.Lock()
	conn.allow, conn.resumed = bytes, make(chan struct{})
	conn.mu.Unlock()
}

func (conn *stallingConn) stalled() bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return conn.allow == 0
}

func (conn *stallingConn) resume() {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.allow >= 0 {
		conn.allow = -1
		close(conn.resumed)
	}
}

func (conn *stallingConn) Read(data []byte) (int, error) {
	conn.mu.Lock()
	allow, resumed := conn.allow, conn.resumed
	conn.mu.Unlock()
	if allow == 0 {
		<-resumed
		return conn.Conn.Read(data)
	}
	if allow > 0 && len(data) > allow {
		data = data[:allow]
	}
	count, err := conn.Conn.Read(data)
	conn.mu.Lock()
	if conn.allow > 0 {
		conn.allow -= count
	}
	conn.mu.Unlock()
	return count, err
}

// stallResizeAndResync blocks the broker writer inside a partial resize
// snapshot frame, then supersedes that delivery with a client resync.
func stallResizeAndResync(t *testing.T) (*Session, *streamProcess, *streamClient, *stallingConn, protocol.ControlChangedPayload) {
	t.Helper()
	fixture, session, process := newStreamFixture(t)
	stall := &stallingConn{allow: -1}
	client := startWrappedStreamClient(t, fixture.server, protocol.CLI, func(conn net.Conn) net.Conn {
		stall.Conn = conn
		return stall
	})
	t.Cleanup(stall.resume)
	client.attach(t)
	grant := client.acquire(t)
	// The resize snapshot's first frame stops after its header and two payload
	// bytes, so the broker's single writer is blocked inside a partial frame.
	stall.stallAfter(6)
	client.action(t, protocol.TerminalResize, protocol.TerminalResizePayload{Lease: *grant.Lease, Columns: 100, Rows: 30})
	deadline := time.Now().Add(3 * time.Second)
	for !stall.stalled() {
		if time.Now().After(deadline) {
			t.Fatal("resize snapshot never reached the stalled reader")
		}
		time.Sleep(time.Millisecond)
	}
	// The desktop client sends this after its own resize. The following
	// lease-less heartbeat has no reply; its write returns only after the
	// dispatcher has finished handling the resync.
	client.action(t, protocol.ScreenResync, protocol.ScreenResyncPayload{Reason: protocol.BaseMismatch})
	client.action(t, protocol.Heartbeat, protocol.HeartbeatPayload{})
	time.Sleep(50 * time.Millisecond) // let any cancellation callback run
	return session, process, client, stall, grant
}

func TestReviewResyncDuringBlockedWriteClosesConnection(t *testing.T) {
	_, process, client, stall, grant := stallResizeAndResync(t)
	stall.resume()

	var begin protocol.SnapshotBeginPayload
	for {
		message := client.await(t, "", "")
		if message.Type == protocol.SnapshotBegin {
			begin, _ = protocol.DecodePayload[protocol.SnapshotBeginPayload](message)
		}
		if message.Type == protocol.SnapshotEnd {
			end, _ := protocol.DecodePayload[protocol.SnapshotEndPayload](message)
			if end.SnapshotID != begin.SnapshotID {
				t.Fatalf("snapshot end %s does not close begin %s", end.SnapshotID, begin.SnapshotID)
			}
			break
		}
	}
	if begin.Screen.Columns != 100 || begin.Screen.Rows != 30 {
		t.Fatalf("replacement snapshot = %#v", begin.Screen)
	}
	client.action(t, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})
	request := client.action(t, protocol.InputText, protocol.InputTextPayload{Lease: *grant.Lease, InputSequence: "1", Text: "ok"})
	client.await(t, protocol.InputAccepted, request)
	waitStreamInput(t, process, "ok")
}

func TestSupersededBlockedWriteStillClosesOnShutdown(t *testing.T) {
	session, _, client, _, _ := stallResizeAndResync(t)
	// The superseded frame is still blocked. Viewer shutdown must close the
	// connection now, not after that frame's write deadline.
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(DefaultViewerTimeout / 2)
	for {
		// A zero-byte pipe write is consumed by the dispatcher's frame reader
		// without changing the stream, or fails once the broker closes it.
		err := client.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		if err == nil {
			_, err = client.conn.Write(nil)
		}
		if errors.Is(err, io.ErrClosedPipe) {
			return
		} else if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("stopped viewer left its connection open behind a blocked write")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReviewControlGrantedWhileNewSnapshotUnapplied(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	first := startStreamClient(t, fixture.server, protocol.CLI)
	first.attach(t)
	second := startStreamClient(t, fixture.server, protocol.CLI)
	second.attach(t)
	grant := first.acquire(t)
	first.action(t, protocol.TerminalResize, protocol.TerminalResizePayload{Lease: *grant.Lease, Columns: 100, Rows: 30})
	begin, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](second.await(t, protocol.SnapshotBegin, ""))
	second.await(t, protocol.SnapshotEnd, "")
	if begin.Screen.Columns != 100 || begin.Screen.Rows != 30 {
		t.Fatalf("resize snapshot = %#v", begin.Screen)
	}
	// The dispatcher saw an earlier snapshot.applied; the actor knows this
	// viewer's current snapshot is still unapplied.
	request := second.action(t, protocol.ControlAcquire, protocol.EmptyPayload{})
	reply := second.await(t, "", request)
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](reply)
	if reply.Type != protocol.Error || failure.Code != protocol.ResyncRequired {
		t.Fatalf("control granted over an unapplied snapshot: %s %s", reply.Type, reply.Payload)
	}
	second.action(t, protocol.SnapshotApplied, protocol.SnapshotAppliedPayload{SnapshotID: begin.SnapshotID, Revision: begin.Revision})
	second.acquire(t)
}

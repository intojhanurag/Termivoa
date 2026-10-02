package broker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	rpcProjectID  = "81111111-1111-4111-8111-111111111111"
	rpcProfileID  = "82222222-2222-4222-8222-222222222222"
	rpcProjectTwo = "83333333-3333-4333-8333-333333333333"
	rpcSessionID  = "84444444-4444-4444-8444-444444444444"
	rpcSessionGen = "85555555-5555-4555-8555-555555555555"
)

type managementFixture struct {
	server      *ManagementServer
	registry    *Registry
	persistence *store.Store
	starts      atomic.Int32
	terminate   *fakeManagedSession

	specMu sync.Mutex
	specs  []ptyhost.Spec
}

func newManagementFixture(t *testing.T) *managementFixture {
	t.Helper()
	persistence := openRegistryStore(t, filepath.Join(t.TempDir(), "broker.db"))
	now := time.Now().UTC().Add(-time.Minute)
	if _, err := persistence.BeginBrokerGeneration(context.Background(), registryBrokerGeneration, now); err != nil {
		t.Fatalf("begin broker generation: %v", err)
	}
	for _, project := range []store.Project{
		{ID: rpcProjectID, DisplayName: "Termivoa", CanonicalPath: "/tmp/phone-terminal", CreatedAt: now, UpdatedAt: now},
		{ID: rpcProjectTwo, DisplayName: "Second Project", CanonicalPath: "/tmp/second-project", CreatedAt: now, UpdatedAt: now},
	} {
		if err := persistence.PutProject(context.Background(), project); err != nil {
			t.Fatalf("put project: %v", err)
		}
	}
	if err := persistence.PutShellProfile(context.Background(), store.ShellProfile{
		ID: rpcProfileID, DisplayName: "zsh", Platform: store.PlatformDarwin,
		ExecutablePath: "/bin/zsh", Arguments: []string{"-l"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("put shell profile: %v", err)
	}

	fixture := &managementFixture{persistence: persistence}
	ids := []string{rpcSessionID, rpcSessionGen}
	var idMu sync.Mutex
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		Store: persistence, BrokerGeneration: registryBrokerGeneration,
		newID: func() (string, error) {
			idMu.Lock()
			defer idMu.Unlock()
			if len(ids) == 0 {
				return "", errors.New("test ID sequence exhausted")
			}
			id := ids[0]
			ids = ids[1:]
			return id, nil
		},
		start: func(_ context.Context, config SessionConfig) (registrySession, error) {
			fixture.starts.Add(1)
			fixture.specMu.Lock()
			fixture.specs = append(fixture.specs, config.Process)
			fixture.specMu.Unlock()
			session := newFakeManagedSession(config.ID, config.Generation, SessionRunning)
			fixture.terminate = session
			return session, nil
		},
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	fixture.registry = registry
	server, err := NewManagementServer(ManagementServerConfig{
		Registry: registry, Store: persistence, BrokerGeneration: registryBrokerGeneration,
		HostBuild: "test-build", HostPlatform: store.PlatformDarwin,
	})
	if err != nil {
		t.Fatalf("new management server: %v", err)
	}
	fixture.server = server
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Errorf("close registry: %v", err)
		}
		if err := persistence.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return fixture
}

func (fixture *managementFixture) lastSpec(t *testing.T) ptyhost.Spec {
	t.Helper()
	fixture.specMu.Lock()
	defer fixture.specMu.Unlock()
	if len(fixture.specs) == 0 {
		t.Fatal("no process spec was captured")
	}
	return fixture.specs[len(fixture.specs)-1]
}

type managementClient struct {
	connection net.Conn
	reader     *ipc.FrameReader
	writer     *ipc.FrameWriter
	result     <-chan error
}

func startManagementClient(t *testing.T, ctx context.Context, server *ManagementServer) *managementClient {
	t.Helper()
	clientConnection, serverConnection := net.Pipe()
	result := make(chan error, 1)
	go func() { result <- server.serveConnection(ctx, serverConnection) }()
	if _, err := ipc.ClientHandshake(ctx, clientConnection, protocol.CLI, "test-client"); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	reader, err := ipc.NewFrameReader(clientConnection, protocol.MaxOutboundMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := ipc.NewFrameWriter(clientConnection, protocol.MaxInboundMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	client := &managementClient{
		connection: clientConnection, reader: reader, writer: writer, result: result,
	}
	t.Cleanup(func() { _ = clientConnection.Close() })
	return client
}

func (client *managementClient) roundTrip(t *testing.T, request []byte) protocol.BrokerRPCEnvelope {
	t.Helper()
	if err := client.writer.WriteFrame(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := client.reader.ReadFrame()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	envelope, err := protocol.DecodeBrokerRPCResponse(response)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func managementRequest(
	t *testing.T,
	requestID string,
	messageType protocol.BrokerRPCMessageType,
	sessionID, generation *string,
	payload any,
) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: messageType, RequestID: requestID,
		SessionID: sessionID, Generation: generation, Payload: payloadJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeBrokerRPCRequest(data); err != nil {
		t.Fatalf("test request is invalid: %v", err)
	}
	return data
}

func TestManagementDispatchResolvesSavedConfigurationAndReplaysCreate(t *testing.T) {
	fixture := newManagementFixture(t)
	client := startManagementClient(t, context.Background(), fixture.server)
	createOperationID := operationID("rpc-create")
	firstRequestID := operationID("rpc-create-request-one")
	request := managementRequest(t, firstRequestID, protocol.SessionCreateRequest, nil, nil,
		protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: createOperationID, ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		})
	first := client.roundTrip(t, request)
	if first.RequestID != firstRequestID || first.Type != protocol.SessionCreateResult {
		t.Fatalf("first response correlation = %#v", first)
	}
	firstPayload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](first)
	if err != nil {
		t.Fatal(err)
	}
	if firstPayload.OperationID != createOperationID || firstPayload.Session.ID != rpcSessionID ||
		firstPayload.Session.Generation != rpcSessionGen {
		t.Fatalf("create result = %#v", firstPayload)
	}
	if fixture.starts.Load() != 1 {
		t.Fatalf("process starts = %d, want 1", fixture.starts.Load())
	}
	spec := fixture.lastSpec(t)
	if spec.Executable != "/bin/zsh" || spec.Directory != "/tmp/phone-terminal" ||
		len(spec.Args) != 1 || spec.Args[0] != "-l" || spec.Columns != 80 || spec.Rows != 24 {
		t.Fatalf("resolved process spec = %#v", spec)
	}
	if len(spec.Env) != 2 || spec.Env[0] != "TERM=xterm-256color" || spec.Env[1] != "COLORTERM=truecolor" {
		t.Fatalf("terminal environment = %#v", spec.Env)
	}
	if err := client.connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-client.result:
		if err != nil {
			t.Fatalf("first gateway connection close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first gateway connection did not stop")
	}
	disabledAt := time.Now().UTC()
	project, err := fixture.persistence.GetProject(context.Background(), rpcProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.DisplayName = "Changed after create"
	project.CanonicalPath = "/tmp/changed-after-create"
	project.UpdatedAt = disabledAt
	project.DisabledAt = &disabledAt
	if err := fixture.persistence.PutProject(context.Background(), project); err != nil {
		t.Fatalf("disable replayed project: %v", err)
	}
	profile, err := fixture.persistence.GetShellProfile(context.Background(), rpcProfileID)
	if err != nil {
		t.Fatal(err)
	}
	profile.ExecutablePath = "/bin/changed-after-create"
	profile.Arguments = []string{"--changed"}
	profile.UpdatedAt = disabledAt
	profile.DisabledAt = &disabledAt
	if err := fixture.persistence.PutShellProfile(context.Background(), profile); err != nil {
		t.Fatalf("disable replayed shell profile: %v", err)
	}
	client = startManagementClient(t, context.Background(), fixture.server)

	secondRequestID := operationID("rpc-create-request-two")
	replay := client.roundTrip(t, managementRequest(t, secondRequestID, protocol.SessionCreateRequest, nil, nil,
		protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: createOperationID, ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		}))
	replayPayload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](replay)
	if err != nil {
		t.Fatal(err)
	}
	if replay.RequestID != secondRequestID || replayPayload.OperationID != createOperationID ||
		!reflect.DeepEqual(replayPayload.Session, firstPayload.Session) || fixture.starts.Load() != 1 {
		t.Fatalf("create replay = envelope %#v payload %#v starts %d", replay, replayPayload, fixture.starts.Load())
	}

	conflictRequestID := operationID("rpc-create-conflict")
	conflict := client.roundTrip(t, managementRequest(t, conflictRequestID,
		protocol.SessionCreateRequest, nil, nil, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: createOperationID, ProjectID: rpcProjectTwo, ShellProfileID: rpcProfileID,
		}))
	conflictPayload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCErrorPayload](conflict)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.RequestID != conflictRequestID || conflict.Type != protocol.BrokerError ||
		conflictPayload.Code != protocol.OperationConflict || fixture.starts.Load() != 1 {
		t.Fatalf("create conflict = envelope %#v payload %#v starts %d", conflict, conflictPayload, fixture.starts.Load())
	}
}

func TestManagementDispatchLifecycleAndTypedErrors(t *testing.T) {
	fixture := newManagementFixture(t)
	client := startManagementClient(t, context.Background(), fixture.server)
	createOperation := operationID("rpc-lifecycle-create")
	create := client.roundTrip(t, managementRequest(t, operationID("rpc-lifecycle-create-request"),
		protocol.SessionCreateRequest, nil, nil, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: createOperation, ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		}))
	created, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](create)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, generation := created.Session.ID, created.Session.Generation

	renameOperation := operationID("rpc-rename")
	renameRequestID := operationID("rpc-rename-request")
	rename := client.roundTrip(t, managementRequest(t, renameRequestID,
		protocol.SessionRenameRequest, &sessionID, &generation,
		protocol.BrokerRPCSessionRenameRequestPayload{OperationID: renameOperation, Title: "Renamed"}))
	renamed, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](rename)
	if err != nil {
		t.Fatal(err)
	}
	if rename.RequestID != renameRequestID || renamed.OperationID != renameOperation || renamed.Session.Title != "Renamed" {
		t.Fatalf("rename response = %#v %#v", rename, renamed)
	}
	secondRenameOperation := operationID("rpc-rename-second")
	secondRename := client.roundTrip(t, managementRequest(t, operationID("rpc-rename-second-request"),
		protocol.SessionRenameRequest, &sessionID, &generation,
		protocol.BrokerRPCSessionRenameRequestPayload{OperationID: secondRenameOperation, Title: "Renamed again"}))
	secondRenamed, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](secondRename)
	if err != nil || secondRenamed.Session.Title != "Renamed again" {
		t.Fatalf("second rename response = %#v %#v, err=%v", secondRename, secondRenamed, err)
	}
	replayedRename := client.roundTrip(t, managementRequest(t, operationID("rpc-rename-replay-request"),
		protocol.SessionRenameRequest, &sessionID, &generation,
		protocol.BrokerRPCSessionRenameRequestPayload{OperationID: renameOperation, Title: "Renamed"}))
	replayedRenamed, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](replayedRename)
	if err != nil || replayedRenamed.OperationID != renameOperation ||
		!reflect.DeepEqual(replayedRenamed.Session, renamed.Session) {
		t.Fatalf("rename replay = %#v %#v, err=%v; want session %#v", replayedRename, replayedRenamed, err, renamed.Session)
	}

	statusRequestID := operationID("rpc-status-request")
	status := client.roundTrip(t, managementRequest(t, statusRequestID,
		protocol.SessionStatusRequest, &sessionID, &generation, protocol.BrokerRPCEmptyPayload{}))
	statusPayload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionResultPayload](status)
	if err != nil {
		t.Fatal(err)
	}
	if status.RequestID != statusRequestID || statusPayload.Session.Title != "Renamed again" {
		t.Fatalf("status response = %#v %#v", status, statusPayload)
	}

	terminateOperation := operationID("rpc-terminate")
	firstTerminateRequestID := operationID("rpc-terminate-one")
	firstTerminate := client.roundTrip(t, managementRequest(t, firstTerminateRequestID,
		protocol.SessionTerminateRequest, &sessionID, &generation,
		protocol.BrokerRPCSessionTerminateRequestPayload{OperationID: terminateOperation}))
	firstTerminated, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](firstTerminate)
	if err != nil || firstTerminate.RequestID != firstTerminateRequestID ||
		firstTerminated.OperationID != terminateOperation || firstTerminated.Session.State != protocol.Exiting {
		t.Fatalf("first terminate response = %#v %#v, err=%v", firstTerminate, firstTerminated, err)
	}
	if fixture.terminate.terminateCalls.Load() != 1 {
		t.Fatalf("terminate calls = %d, want 1", fixture.terminate.terminateCalls.Load())
	}
	exitCode := 0
	fixture.terminate.finish(SessionExited, time.Now().UTC(), &exitCode)
	waitForPersistedSessionState(t, fixture.persistence, sessionID, store.SessionExited)
	secondTerminateRequestID := operationID("rpc-terminate-two")
	replayedTerminate := client.roundTrip(t, managementRequest(t, secondTerminateRequestID,
		protocol.SessionTerminateRequest, &sessionID, &generation,
		protocol.BrokerRPCSessionTerminateRequestPayload{OperationID: terminateOperation}))
	replayedTerminated, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](replayedTerminate)
	if err != nil || replayedTerminate.RequestID != secondTerminateRequestID ||
		replayedTerminated.OperationID != terminateOperation ||
		!reflect.DeepEqual(replayedTerminated.Session, firstTerminated.Session) {
		t.Fatalf("terminate replay = %#v %#v, err=%v; want session %#v",
			replayedTerminate, replayedTerminated, err, firstTerminated.Session)
	}
	if fixture.terminate.terminateCalls.Load() != 1 {
		t.Fatalf("terminate calls after terminal replay = %d, want 1", fixture.terminate.terminateCalls.Load())
	}

	missing := client.roundTrip(t, managementRequest(t, operationID("rpc-missing-request"),
		protocol.SessionCreateRequest, nil, nil, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: operationID("rpc-missing-operation"),
			ProjectID:   operationID("missing-project"), ShellProfileID: rpcProfileID,
		}))
	missingPayload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCErrorPayload](missing)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Type != protocol.BrokerError || missingPayload.Code != protocol.ProjectMissing ||
		missingPayload.Message != "saved project is unavailable" {
		t.Fatalf("missing project error = %#v %#v", missing, missingPayload)
	}
}

func TestManagementDispatchPaginationAndInvalidCursor(t *testing.T) {
	fixture := newManagementFixture(t)
	client := startManagementClient(t, context.Background(), fixture.server)
	client.roundTrip(t, managementRequest(t, operationID("rpc-page-create-request"),
		protocol.SessionCreateRequest, nil, nil, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: operationID("rpc-page-create"), ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
		}))
	first := client.roundTrip(t, managementRequest(t, operationID("rpc-list-one"),
		protocol.SessionListRequest, nil, nil,
		protocol.BrokerRPCSessionListRequestPayload{Limit: 1}))
	page, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionListResultPayload](first)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.NextCursor != nil {
		t.Fatalf("single session page = %#v", page)
	}

	invalid := "not-a-valid-offset"
	response := client.roundTrip(t, managementRequest(t, operationID("rpc-list-invalid"),
		protocol.SessionListRequest, nil, nil,
		protocol.BrokerRPCSessionListRequestPayload{Limit: 1, Cursor: &invalid}))
	payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCErrorPayload](response)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Code != protocol.InvalidRequest {
		t.Fatalf("invalid cursor error = %#v", payload)
	}
}

func TestManagementConnectionAcceptsFragmentedAndCoalescedFrames(t *testing.T) {
	fixture := newManagementFixture(t)
	client := startManagementClient(t, context.Background(), fixture.server)
	firstID, secondID := operationID("rpc-fragment-one"), operationID("rpc-fragment-two")
	first := managementRequest(t, firstID, protocol.SessionListRequest, nil, nil,
		protocol.BrokerRPCSessionListRequestPayload{Limit: 10})
	second := managementRequest(t, secondID, protocol.SessionListRequest, nil, nil,
		protocol.BrokerRPCSessionListRequestPayload{Limit: 10})
	framed := append(frameBytes(first), frameBytes(second)...)
	writeDone := make(chan error, 1)
	go func() {
		for offset := 0; offset < len(framed); {
			width := 3
			if remaining := len(framed) - offset; width > remaining {
				width = remaining
			}
			if _, err := client.connection.Write(framed[offset : offset+width]); err != nil {
				writeDone <- err
				return
			}
			offset += width
		}
		writeDone <- nil
	}()
	for _, expected := range []string{firstID, secondID} {
		data, err := client.reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		response, err := protocol.DecodeBrokerRPCResponse(data)
		if err != nil || response.RequestID != expected || response.Type != protocol.SessionListResult {
			t.Fatalf("response = %#v, %v", response, err)
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagementConnectionRejectsMalformedDirectionAndOversize(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(*testing.T, net.Conn)
	}{
		{
			name: "response sent toward broker",
			write: func(t *testing.T, connection net.Conn) {
				requestID := operationID("wrong-direction")
				payload, _ := json.Marshal(protocol.BrokerRPCSessionListResultPayload{Sessions: []protocol.BrokerRPCSessionRecord{}})
				data, _ := json.Marshal(protocol.BrokerRPCEnvelope{
					Version: protocol.Version, Type: protocol.SessionListResult,
					RequestID: requestID, Payload: payload,
				})
				writer, _ := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
				if err := writer.WriteFrame(data); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "malformed JSON",
			write: func(t *testing.T, connection net.Conn) {
				writer, _ := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
				if err := writer.WriteFrame([]byte(`{"version":1`)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized frame",
			write: func(t *testing.T, connection net.Conn) {
				var header [4]byte
				binary.BigEndian.PutUint32(header[:], uint32(protocol.MaxInboundMessageBytes+1))
				if _, err := connection.Write(header[:]); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagementFixture(t)
			client := startManagementClient(t, context.Background(), fixture.server)
			test.write(t, client.connection)
			_ = client.connection.SetReadDeadline(time.Now().Add(time.Second))
			var one [1]byte
			if _, err := client.connection.Read(one[:]); err == nil {
				t.Fatal("malformed connection remained open")
			}
			select {
			case err := <-client.result:
				if err == nil {
					t.Fatal("server accepted malformed management frame")
				}
			case <-time.After(time.Second):
				t.Fatal("server did not close malformed connection")
			}
		})
	}
}

func TestManagementServeCancellationPreservesBrokerRuntime(t *testing.T) {
	fixture := newManagementFixture(t)
	serveContext, cancelServe := context.WithCancel(context.Background())
	runtimeDir, err := os.MkdirTemp("/tmp", "pterm-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	listener, err := ipc.ListenBroker(serveContext, ipc.ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: registryBrokerGeneration,
	})
	if err != nil {
		t.Fatalf("listen broker: %v", err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- fixture.server.Serve(serveContext, listener) }()

	connection, _, _, err := ipc.DialBroker(context.Background(), runtimeDir, protocol.CLI, "cli-test")
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	requestID := operationID("rpc-serve-create-request")
	if err := writer.WriteFrame(managementRequest(t, requestID, protocol.SessionCreateRequest, nil, nil,
		protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: operationID("rpc-serve-create"),
			ProjectID:   rpcProjectID, ShellProfileID: rpcProfileID,
		})); err != nil {
		t.Fatal(err)
	}
	response, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := protocol.DecodeBrokerRPCResponse(response)
	if err != nil || envelope.RequestID != requestID {
		t.Fatalf("serve response = %#v, %v", envelope, err)
	}
	created, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	cancelServe()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("serve cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("management serve did not stop")
	}
	_ = connection.Close()
	if _, err := fixture.registry.Get(created.Session.ID); err != nil {
		t.Fatalf("management connection shutdown ended broker session: %v", err)
	}
	if _, err := fixture.persistence.GetSession(context.Background(), created.Session.ID); err != nil {
		t.Fatalf("management connection shutdown closed broker store: %v", err)
	}
}

func frameBytes(payload []byte) []byte {
	framed := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(payload)))
	copy(framed[4:], payload)
	return framed
}

func TestManagementConnectionCancellationClosesOnlyTheViewerConnection(t *testing.T) {
	fixture := newManagementFixture(t)
	connectionContext, cancelConnection := context.WithCancel(context.Background())
	client := startManagementClient(t, connectionContext, fixture.server)
	if err := client.connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	cancelConnection()
	var one [1]byte
	if _, err := client.connection.Read(one[:]); err == nil {
		t.Fatal("canceled management connection remained open")
	}
	select {
	case <-client.result:
	case <-time.After(time.Second):
		t.Fatal("management connection did not stop after cancellation")
	}
	if _, err := fixture.registry.List(context.Background()); err != nil {
		t.Fatalf("connection cancellation closed broker registry: %v", err)
	}
}

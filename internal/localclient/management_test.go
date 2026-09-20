package localclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	testRequest1   = "00000000-0000-4000-8000-000000000001"
	testRequest2   = "00000000-0000-4000-8000-000000000002"
	testOperation  = "00000000-0000-4000-8000-000000000003"
	testSession    = "00000000-0000-4000-8000-000000000004"
	testGeneration = "00000000-0000-4000-8000-000000000005"
	testProject    = "00000000-0000-4000-8000-000000000006"
	testProfile    = "00000000-0000-4000-8000-000000000007"
	testBroker     = "00000000-0000-4000-8000-000000000008"
)

func TestDialManagementAuthenticatesAsCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tempBase := ""
	if runtime.GOOS == "darwin" {
		tempBase = "/tmp"
	}
	runtimeDir, err := os.MkdirTemp(tempBase, "ptcli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBroker,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		hello, err := ipc.ServerHandshake(ctx, connection, testBroker, "broker-test")
		if err != nil {
			serverDone <- err
			return
		}
		if hello.ClientKind != protocol.CLI {
			serverDone <- errors.New("management client did not authenticate as CLI")
			return
		}
		reader, _ := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
		data, err := reader.ReadFrame()
		if err != nil {
			serverDone <- err
			return
		}
		request, err := protocol.DecodeBrokerRPCRequest(data)
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- writer.WriteFrame(responseJSON(t, request, protocol.SessionListResult,
			protocol.BrokerRPCSessionListResultPayload{Sessions: []protocol.BrokerRPCSessionRecord{}}))
	}()

	client, err := DialManagement(ctx, runtimeDir, "cli-test")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListPage(ctx, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 0 {
		t.Fatalf("unexpected sessions: %+v", page.Sessions)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagementClientListAndStatusCorrelatePartialFrames(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	requestIDs := []string{testRequest1, testRequest2}
	client := newTestClient(t, clientConnection, func() (string, error) {
		value := requestIDs[0]
		requestIDs = requestIDs[1:]
		return value, nil
	})
	defer client.Close()

	serverDone := make(chan error, 1)
	go func() {
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		defer serverConnection.Close()
		listData, err := reader.ReadFrame()
		if err != nil {
			serverDone <- err
			return
		}
		listRequest, err := protocol.DecodeBrokerRPCRequest(listData)
		if err != nil {
			serverDone <- err
			return
		}
		listResponse := responseJSON(t, listRequest, protocol.SessionListResult,
			protocol.BrokerRPCSessionListResultPayload{Sessions: []protocol.BrokerRPCSessionRecord{testRecord(protocol.Running)}})
		if err := writeFrameInPieces(serverConnection, listResponse); err != nil {
			serverDone <- err
			return
		}

		statusData, err := reader.ReadFrame()
		if err != nil {
			serverDone <- err
			return
		}
		statusRequest, err := protocol.DecodeBrokerRPCRequest(statusData)
		if err != nil {
			serverDone <- err
			return
		}
		statusResponse := responseJSON(t, statusRequest, protocol.SessionStatusResult,
			protocol.BrokerRPCSessionResultPayload{Session: testRecord(protocol.Running)})
		serverDone <- writeFrameInPieces(serverConnection, statusResponse)
	}()

	page, err := client.ListPage(context.Background(), nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].ID != testSession {
		t.Fatalf("unexpected page: %+v", page)
	}
	status, err := client.Status(context.Background(), SessionRef{ID: testSession, Generation: testGeneration})
	if err != nil {
		t.Fatal(err)
	}
	if status.Generation != testGeneration {
		t.Fatalf("unexpected status: %+v", status)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagementClientCreateCarriesOnlySavedIDs(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	serverDone := make(chan error, 1)
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(serverConnection, protocol.MaxOutboundMessageBytes)
		data, err := reader.ReadFrame()
		if err != nil {
			serverDone <- err
			return
		}
		text := string(data)
		for _, forbidden := range []string{"executable", "arguments", "directory", "environment", "workdir", "path"} {
			if strings.Contains(strings.ToLower(text), forbidden) {
				serverDone <- errors.New("create request exposed process configuration")
				return
			}
		}
		request, err := protocol.DecodeBrokerRPCRequest(data)
		if err != nil {
			serverDone <- err
			return
		}
		response := responseJSON(t, request, protocol.SessionCreateResult,
			protocol.BrokerRPCSessionMutationResultPayload{OperationID: testOperation, Session: testRecord(protocol.Running)})
		serverDone <- writer.WriteFrame(response)
	}()

	result, err := client.Create(context.Background(), testOperation, testProject, testProfile)
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID != testOperation {
		t.Fatalf("unexpected operation ID %q", result.OperationID)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagementClientTerminateLostAcknowledgementIsUncertainAndNotRetried(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	var requests atomic.Int32
	serverDone := make(chan error, 1)
	go func() {
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		_, err := reader.ReadFrame()
		if err == nil {
			requests.Add(1)
		}
		serverDone <- serverConnection.Close()
	}()

	_, err := client.Terminate(context.Background(),
		SessionRef{ID: testSession, Generation: testGeneration}, testOperation)
	if !IsDeliveryUncertain(err) {
		t.Fatalf("expected uncertain delivery, got %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("got %d requests, want one", requests.Load())
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagementClientCancellationInterruptsBlockedRead(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	requestRead := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		_, err := reader.ReadFrame()
		if err != nil {
			serverDone <- err
			return
		}
		close(requestRead)
		var one [1]byte
		_, err = serverConnection.Read(one[:])
		serverDone <- err
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.ListPage(ctx, nil, 10)
		result <- err
	}()
	<-requestRead
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled read did not return")
	}
	_ = serverConnection.Close()
	<-serverDone
}

func TestManagementClientCancellationWhileWaitingDoesNotWriteSecondRequest(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	requestIDs := []string{testRequest1, testRequest2}
	client := newTestClient(t, clientConnection, func() (string, error) {
		value := requestIDs[0]
		requestIDs = requestIDs[1:]
		return value, nil
	})
	defer client.Close()
	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests atomic.Int32
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(serverConnection, protocol.MaxOutboundMessageBytes)
		data, err := reader.ReadFrame()
		if err != nil {
			return
		}
		requests.Add(1)
		close(firstRead)
		<-releaseFirst
		request, _ := protocol.DecodeBrokerRPCRequest(data)
		_ = writer.WriteFrame(responseJSON(t, request, protocol.SessionListResult,
			protocol.BrokerRPCSessionListResultPayload{Sessions: []protocol.BrokerRPCSessionRecord{}}))
	}()

	firstResult := make(chan error, 1)
	go func() {
		_, err := client.ListPage(context.Background(), nil, 10)
		firstResult <- err
	}()
	<-firstRead
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.ListPage(ctx, nil, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context canceled", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("waiting request wrote to broker; count=%d", requests.Load())
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
}

func TestManagementClientBoundsOversizedResponse(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		_, _ = reader.ReadFrame()
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(protocol.MaxOutboundMessageBytes+1))
		_, _ = serverConnection.Write(header[:])
	}()
	_, err := client.ListPage(context.Background(), nil, 10)
	if !errors.Is(err, ipc.ErrFrameTooLarge) {
		t.Fatalf("expected bounded response rejection, got %v", err)
	}
}

func TestManagementClientOperationMismatchIsUncertain(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(serverConnection, protocol.MaxOutboundMessageBytes)
		data, _ := reader.ReadFrame()
		request, _ := protocol.DecodeBrokerRPCRequest(data)
		_ = writer.WriteFrame(responseJSON(t, request, protocol.SessionTerminateResult,
			protocol.BrokerRPCSessionMutationResultPayload{OperationID: testRequest2, Session: testRecord(protocol.Exiting)}))
	}()
	_, err := client.Terminate(context.Background(),
		SessionRef{ID: testSession, Generation: testGeneration}, testOperation)
	if !IsDeliveryUncertain(err) || !strings.Contains(err.Error(), "operation ID mismatch") {
		t.Fatalf("expected uncertain operation mismatch, got %v", err)
	}
}

func TestManagementClientRejectsMismatchedCorrelation(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(serverConnection, protocol.MaxOutboundMessageBytes)
		data, _ := reader.ReadFrame()
		request, _ := protocol.DecodeBrokerRPCRequest(data)
		request.RequestID = testRequest2
		_ = writer.WriteFrame(responseJSON(t, request, protocol.SessionListResult,
			protocol.BrokerRPCSessionListResultPayload{Sessions: []protocol.BrokerRPCSessionRecord{}}))
	}()
	_, err := client.ListPage(context.Background(), nil, 10)
	if err == nil || !strings.Contains(err.Error(), "request ID mismatch") {
		t.Fatalf("expected request correlation failure, got %v", err)
	}
}

func TestManagementClientReturnsTypedRemoteError(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	client := newTestClient(t, clientConnection, fixedRequestID)
	defer client.Close()
	go func() {
		defer serverConnection.Close()
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		writer, _ := ipc.NewFrameWriter(serverConnection, protocol.MaxOutboundMessageBytes)
		data, _ := reader.ReadFrame()
		request, _ := protocol.DecodeBrokerRPCRequest(data)
		_ = writer.WriteFrame(responseJSON(t, request, protocol.BrokerError,
			protocol.BrokerRPCErrorPayload{Code: protocol.SessionMissing, Message: "session does not exist"}))
	}()
	_, err := client.Status(context.Background(), SessionRef{ID: testSession, Generation: testGeneration})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != protocol.SessionMissing || remote.SessionRef == nil ||
		remote.SessionRef.ID != testSession {
		t.Fatalf("unexpected remote error: %#v (%v)", remote, err)
	}
}

func newTestClient(t *testing.T, connection net.Conn, source RequestIDSource) *ManagementClient {
	t.Helper()
	client, err := newManagementClient(connection, source)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func fixedRequestID() (string, error) { return testRequest1, nil }

func testRecord(state protocol.State) protocol.BrokerRPCSessionRecord {
	record := protocol.BrokerRPCSessionRecord{
		ID: testSession, Generation: testGeneration, Title: "Terminal",
		ProjectID: testProject, ShellProfileID: testProfile, State: state,
		Columns: 80, Rows: 24, CreatedAt: "2026-09-21T00:00:00Z", UpdatedAt: "2026-09-21T00:00:01Z",
	}
	if state == protocol.Exited || state == protocol.Lost {
		ended := "2026-09-21T00:00:01Z"
		record.EndedAt = &ended
	}
	return record
}

func responseJSON(
	t *testing.T,
	request protocol.BrokerRPCEnvelope,
	messageType protocol.BrokerRPCMessageType,
	payload any,
) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response := protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: messageType, RequestID: request.RequestID, Payload: payloadJSON,
	}
	if request.SessionID != nil {
		response.SessionID = request.SessionID
		response.Generation = request.Generation
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeBrokerRPCResponse(data); err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFrameInPieces(connection net.Conn, payload []byte) error {
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	for _, value := range frame {
		if _, err := connection.Write([]byte{value}); err != nil {
			return err
		}
	}
	return nil
}

var _ io.Closer = (*ManagementClient)(nil)

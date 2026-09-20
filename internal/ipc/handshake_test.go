package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const testBrokerGeneration = "22222222-2222-4222-8222-222222222222"

func TestHandshakeNegotiatesAndConfirmsBrokerGeneration(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverResult := make(chan error, 1)
	go func() {
		hello, err := ServerHandshake(context.Background(), server, testBrokerGeneration, "host-dev")
		if err == nil && (hello.ClientKind != protocol.Gateway || hello.ClientBuild != "gateway-dev") {
			err = errors.New("unexpected client hello")
		}
		serverResult <- err
	}()
	welcome, err := ClientHandshake(context.Background(), client, protocol.Gateway, "gateway-dev")
	if err != nil {
		t.Fatal(err)
	}
	if welcome.BrokerGeneration != testBrokerGeneration || welcome.NegotiatedVersion != protocol.Version {
		t.Fatalf("welcome = %#v", welcome)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestClientHandshakeRejectsMismatchedRequestID(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		reader, _ := NewFrameReader(server, protocol.MaxInboundMessageBytes)
		_, _ = reader.ReadFrame()
		response, _ := json.Marshal(protocol.IPCEnvelope{
			Version: protocol.Version, Type: protocol.IPCWelcome,
			RequestID: "33333333-3333-4333-8333-333333333333",
			Payload: testJSON(t, protocol.IPCWelcomeClass{
				BrokerGeneration: testBrokerGeneration, HostBuild: "host-dev", NegotiatedVersion: protocol.Version,
			}),
		})
		writer, _ := NewFrameWriter(server, protocol.MaxOutboundMessageBytes)
		_ = writer.WriteFrame(response)
	}()
	if _, err := ClientHandshake(context.Background(), client, protocol.CLI, "cli-dev"); err == nil {
		t.Fatal("mismatched request ID was accepted")
	}
}

func TestClientHandshakeReturnsTypedRemoteError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		reader, _ := NewFrameReader(server, protocol.MaxInboundMessageBytes)
		request, _ := reader.ReadFrame()
		envelope, _ := protocol.DecodeIPCClient(request)
		response, _ := json.Marshal(protocol.IPCEnvelope{
			Version: protocol.Version, Type: protocol.IPCError, RequestID: envelope.RequestID,
			Payload: testJSON(t, protocol.IPCErrorClass{Code: protocol.HostUnavailable, Message: "broker unavailable"}),
		})
		writer, _ := NewFrameWriter(server, protocol.MaxOutboundMessageBytes)
		_ = writer.WriteFrame(response)
	}()
	_, err := ClientHandshake(context.Background(), client, protocol.CLI, "cli-dev")
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != protocol.HostUnavailable {
		t.Fatalf("remote error = %#v, %v", remote, err)
	}
}

func testJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHandshakeTimesOutAndHonorsCanceledContext(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	started := time.Now()
	if _, err := clientHandshake(context.Background(), client, protocol.CLI, "cli-dev", 20*time.Millisecond); err == nil {
		t.Fatal("stalled handshake did not time out")
	}
	if time.Since(started) > time.Second {
		t.Fatal("stalled handshake exceeded bounded timeout")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ClientHandshake(ctx, client, protocol.CLI, "cli-dev"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled handshake error = %v", err)
	}

	activeClient, activeServer := net.Pipe()
	defer activeClient.Close()
	defer activeServer.Close()
	activeContext, activeCancel := context.WithCancel(context.Background())
	activeResult := make(chan error, 1)
	go func() {
		_, err := ClientHandshake(activeContext, activeClient, protocol.CLI, "cli-dev")
		activeResult <- err
	}()
	activeCancel()
	if err := <-activeResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation error = %v", err)
	}
}

func TestServerHandshakeHonorsInFlightCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := ServerHandshake(ctx, server, testBrokerGeneration, "host-dev")
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("server cancellation error = %v", err)
	}
}

func TestServerHandshakeRejectsInvalidWelcomeBeforeWriting(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	clientResult := make(chan error, 1)
	go func() {
		_, err := ClientHandshake(context.Background(), client, protocol.CLI, "cli-dev")
		clientResult <- err
	}()
	_, serverErr := serverHandshake(context.Background(), server, "not-a-uuid", "host-dev", 50*time.Millisecond)
	_ = server.Close()
	if serverErr == nil {
		t.Fatal("invalid broker generation was accepted")
	}
	if err := <-clientResult; err == nil {
		t.Fatal("client accepted invalid welcome")
	}
}

func TestServerHandshakeReturnsTypedProtocolMismatch(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverResult := make(chan error, 1)
	go func() {
		_, err := ServerHandshake(context.Background(), server, testBrokerGeneration, "host-dev")
		serverResult <- err
	}()
	request := []byte(`{"version":2,"type":"ipc.hello","requestId":"11111111-1111-4111-8111-111111111111","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	writer, _ := NewFrameWriter(client, protocol.MaxInboundMessageBytes)
	if err := writer.WriteFrame(request); err != nil {
		t.Fatal(err)
	}
	reader, _ := NewFrameReader(client, protocol.MaxOutboundMessageBytes)
	response, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := protocol.DecodeIPCHost(response)
	if err != nil || envelope.Type != protocol.IPCError {
		t.Fatalf("error envelope = %#v, %v", envelope, err)
	}
	if envelope.RequestID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("error response request ID = %q", envelope.RequestID)
	}
	payload, err := protocol.DecodeIPCPayload[protocol.IPCErrorClass](envelope)
	if err != nil || payload.Code != protocol.ProtocolMismatch {
		t.Fatalf("error payload = %#v, %v", payload, err)
	}
	if err := <-serverResult; err == nil {
		t.Fatal("server accepted mismatched version")
	}
}

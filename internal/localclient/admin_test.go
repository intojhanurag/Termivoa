package localclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	testDevice  = "00000000-0000-4000-8000-000000000009"
	testPending = "00000000-0000-4000-8000-00000000000a"
	testGrant   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
)

func TestDialAdminAuthenticatesAsCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tempBase := ""
	if runtime.GOOS == "darwin" {
		tempBase = "/tmp"
	}
	runtimeDir, err := os.MkdirTemp(tempBase, "ptadm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{RuntimeDir: runtimeDir, BrokerGeneration: testBroker})
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
			serverDone <- errors.New("admin client did not authenticate as CLI")
			return
		}
		serverDone <- answerAdmin(t, connection, func(request protocol.AdminRPCEnvelope) []byte {
			if request.Type != protocol.AdminDeviceList {
				return nil
			}
			return adminResponseJSON(t, request.RequestID, protocol.AdminDeviceListResult,
				protocol.AdminRPCDeviceListResultPayload{Devices: []protocol.AdminRPCDevice{testDeviceRecord()}})
		})
	}()

	client, err := DialAdmin(ctx, runtimeDir, "cli-test")
	if err != nil {
		t.Fatal(err)
	}
	devices, err := client.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != testDevice {
		t.Fatalf("unexpected devices: %+v", devices)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestAdminClientPairingGrantAndOrigin(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	origin := "https://host.example.ts.net"
	go func() {
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminPairGrantResult,
				protocol.AdminRPCPairGrantResultPayload{Grant: testGrant, ExpiresAt: "2026-09-27T00:02:00Z", Origin: &origin})
		})
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCOriginPayload](request)
			if err != nil || payload.Origin == nil || *payload.Origin != origin {
				return nil
			}
			return adminResponseJSON(t, request.RequestID, protocol.AdminConfigOriginResult, payload)
		})
	}()
	grant, err := client.CreatePairingGrant(context.Background())
	if err != nil || grant.Grant != testGrant || grant.Origin == nil || *grant.Origin != origin {
		t.Fatalf("grant = %+v, %v", grant, err)
	}
	configured, err := client.PairingOrigin(context.Background(), &origin)
	if err != nil || configured == nil || *configured != origin {
		t.Fatalf("origin = %v, %v", configured, err)
	}
}

func TestAdminClientCorrelationMismatchPoisons(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	go func() {
		_ = answerAdmin(t, serverConnection, func(protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, testRequest2, protocol.AdminDeviceListResult,
				protocol.AdminRPCDeviceListResultPayload{Devices: []protocol.AdminRPCDevice{}})
		})
	}()
	_, err := client.Devices(context.Background())
	if err == nil || !strings.Contains(err.Error(), "request ID mismatch") {
		t.Fatalf("expected correlation failure, got %v", err)
	}
	if _, err := client.Projects(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("poisoned client accepted another request: %v", err)
	}
}

func TestAdminClientRejectsWrongResultType(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	go func() {
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminProfileDetectResult,
				protocol.AdminRPCProfileListResultPayload{Profiles: []protocol.AdminRPCShellProfile{}})
		})
	}()
	_, err := client.ShellProfiles(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected admin response type") {
		t.Fatalf("expected result type rejection, got %v", err)
	}
	if _, err := client.ShellProfiles(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("client was not poisoned: %v", err)
	}
}

func TestAdminClientReturnsTypedRemoteError(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	go func() {
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminError,
				protocol.BrokerRPCErrorPayload{Code: protocol.PairingExpired, Message: "pending pairing expired"})
		})
	}()
	err := client.ApprovePairing(context.Background(), testPending)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != protocol.PairingExpired || IsDeliveryUncertain(err) {
		t.Fatalf("unexpected remote error: %#v (%v)", remote, err)
	}
}

func TestAdminClientMutationIDMismatchIsUncertain(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		client, serverConnection := newAdminPipe(t)
		go func() {
			_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
				return adminResponseJSON(t, request.RequestID, protocol.AdminPairApproveResult,
					protocol.AdminRPCPendingIDPayload{PendingID: testDevice})
			})
		}()
		err := client.ApprovePairing(context.Background(), testPending)
		if !IsDeliveryUncertain(err) || !strings.Contains(err.Error(), "pending ID mismatch") {
			t.Fatalf("expected uncertain approval mismatch, got %v", err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		client, serverConnection := newAdminPipe(t)
		go func() {
			_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
				return adminResponseJSON(t, request.RequestID, protocol.AdminDeviceRevokeResult,
					protocol.AdminRPCDeviceRevokeResultPayload{DeviceID: testPending, ClosedStreams: 1})
			})
		}()
		_, err := client.RevokeDevice(context.Background(), testDevice)
		if !IsDeliveryUncertain(err) || !strings.Contains(err.Error(), "device ID mismatch") {
			t.Fatalf("expected uncertain revocation mismatch, got %v", err)
		}
		if _, err := client.Devices(context.Background()); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("client was not poisoned: %v", err)
		}
	})
}

func TestAdminClientRevokeLostAcknowledgementIsUncertain(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	go func() {
		reader, _ := ipc.NewFrameReader(serverConnection, protocol.MaxInboundMessageBytes)
		_, _ = reader.ReadFrame()
		_ = serverConnection.Close()
	}()
	_, err := client.RevokeDevice(context.Background(), testDevice)
	if !IsDeliveryUncertain(err) {
		t.Fatalf("expected uncertain delivery, got %v", err)
	}
}

func TestAdminClientValidatesRequestsBeforeWriting(t *testing.T) {
	client, _ := newAdminPipe(t)
	// net.Pipe blocks writes with no reader, so a validation bypass would hang
	// until the test times out rather than returning this error.
	if _, err := client.RevokeDevice(context.Background(), "not-a-uuid"); err == nil || IsDeliveryUncertain(err) {
		t.Fatalf("invalid device ID was not rejected locally: %v", err)
	}
	bad := "http://example.com"
	if _, err := client.PairingOrigin(context.Background(), &bad); err == nil || IsDeliveryUncertain(err) {
		t.Fatalf("invalid origin was not rejected locally: %v", err)
	}
}

func newAdminPipe(t *testing.T) (*AdminClient, net.Conn) {
	t.Helper()
	clientConnection, serverConnection := net.Pipe()
	client, err := newAdminClient(clientConnection, fixedRequestID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverConnection.Close()
	})
	return client, serverConnection
}

// answerAdmin reads one validated admin request and writes respond's frame.
// A nil response closes the connection.
func answerAdmin(t *testing.T, connection net.Conn, respond func(protocol.AdminRPCEnvelope) []byte) error {
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	data, err := reader.ReadFrame()
	if err != nil {
		return err
	}
	request, err := protocol.DecodeAdminRPCRequest(data)
	if err != nil {
		return err
	}
	response := respond(request)
	if response == nil {
		_ = connection.Close()
		return errors.New("unexpected admin request")
	}
	return writer.WriteFrame(response)
}

func adminResponseJSON(t *testing.T, requestID string, messageType protocol.AdminRPCMessageType, payload any) []byte {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Error(err)
		return nil
	}
	data, err := json.Marshal(protocol.AdminRPCEnvelope{
		Version: protocol.Version, Type: messageType, RequestID: requestID, Payload: payloadJSON,
	})
	if err != nil {
		t.Error(err)
		return nil
	}
	if _, err := protocol.DecodeAdminRPCResponse(data); err != nil {
		t.Error(err)
		return nil
	}
	return data
}

func testDeviceRecord() protocol.AdminRPCDevice {
	return protocol.AdminRPCDevice{
		ID: testDevice, Label: "Phone", Status: protocol.Active,
		CreatedAt: "2026-09-27T00:00:00Z", LastUsedAt: "2026-09-27T00:01:00Z",
	}
}

func TestAdminClientRenameDeviceRequiresExactAcknowledgement(t *testing.T) {
	for _, echoed := range []string{"Work phone", "Other"} {
		client, serverConnection := newAdminPipe(t)
		go func() {
			_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
				payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceRenamePayload](request)
				if err != nil || payload.DeviceID != testDevice || payload.Label != "Work phone" {
					return nil
				}
				payload.Label = echoed
				return adminResponseJSON(t, request.RequestID, protocol.AdminDeviceRenameResult, payload)
			})
		}()
		err := client.RenameDevice(context.Background(), testDevice, "Work phone")
		if (echoed == "Work phone") != (err == nil) || (err != nil && !IsDeliveryUncertain(err)) {
			t.Fatalf("rename acknowledged as %q = %v", echoed, err)
		}
	}
}

func TestAdminClientComputerAdministration(t *testing.T) {
	client, serverConnection := newAdminPipe(t)
	origin, via, allow := "https://mac.tailx.ts.net", "mac", true
	go func() {
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairGrantPayload](request)
			if err != nil || payload.Via == nil || *payload.Via != via || payload.AllowTagged == nil {
				return nil
			}
			return adminResponseJSON(t, request.RequestID, protocol.AdminPairGrantResult, protocol.AdminRPCPairGrantResultPayload{
				Grant: testGrant, ExpiresAt: "2026-09-27T00:02:00Z", AppOrigin: &origin,
				AppNode: &protocol.TailnetNode{ID: "nMac", Tags: []string{"tag:server"}}})
		})
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminDeviceRevokeViaResult,
				protocol.AdminRPCRevokeViaResultPayload{AppOrigin: origin, DeviceIDS: []string{testDevice}, ClosedStreams: 1})
		})
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminComputersListResult, protocol.AdminRPCComputerListResultPayload{
				Computers: []protocol.AdminRPCComputer{{DeviceID: testDevice, Origin: origin, Label: "mac", CreatedAt: "2026-09-27T00:00:00Z"}}})
		})
		_ = answerAdmin(t, serverConnection, func(request protocol.AdminRPCEnvelope) []byte {
			return adminResponseJSON(t, request.RequestID, protocol.AdminComputersRemoveResult,
				protocol.AdminRPCComputerRemoveResultPayload{Origin: "https://other.tailx.ts.net", Removed: 1})
		})
	}()
	ctx := context.Background()
	grant, err := client.CreateBoundPairingGrant(ctx, protocol.AdminRPCPairGrantPayload{Via: &via, AllowTagged: &allow})
	if err != nil || grant.AppOrigin == nil || *grant.AppOrigin != origin || grant.AppNode.ID != "nMac" {
		t.Fatalf("bound grant = %+v, %v", grant, err)
	}
	revoked, err := client.RevokeDevicesVia(ctx, origin)
	if err != nil || len(revoked.DeviceIDS) != 1 || revoked.ClosedStreams != 1 {
		t.Fatalf("revoke via = %+v, %v", revoked, err)
	}
	computers, err := client.Computers(ctx)
	if err != nil || len(computers) != 1 || computers[0].Origin != origin {
		t.Fatalf("computers = %+v, %v", computers, err)
	}
	// An acknowledgement for another origin is uncertain delivery.
	if _, err := client.RemoveComputer(ctx, origin); !IsDeliveryUncertain(err) {
		t.Fatalf("mismatched removal = %v", err)
	}
}

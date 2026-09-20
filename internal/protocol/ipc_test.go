package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const ipcRequestID = "11111111-1111-4111-8111-111111111111"

func TestDecodeIPCHandshake(t *testing.T) {
	hello := []byte(`{"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"gateway","clientBuild":"dev","supportedVersions":[1]}}`)
	envelope, err := DecodeIPCClient(hello)
	if err != nil || envelope.Type != IPCHello {
		t.Fatalf("hello = %#v, %v", envelope, err)
	}
	welcome := []byte(`{"version":1,"type":"ipc.welcome","requestId":"` + ipcRequestID + `","payload":{"brokerGeneration":"22222222-2222-4222-8222-222222222222","hostBuild":"dev","negotiatedVersion":1}}`)
	envelope, err = DecodeIPCHost(welcome)
	if err != nil || envelope.Type != IPCWelcome {
		t.Fatalf("welcome = %#v, %v", envelope, err)
	}
}

func TestDecodeIPCErrorMatchesCanonicalErrorCodes(t *testing.T) {
	codes := []ErrorCode{
		Unauthorized, PairingExpired, PairingPending, Revoked, ProtocolMismatch, SessionMissing,
		GenerationChanged, SessionLost, NotController, StaleLease, InputGap, LimitExceeded,
		HistoryChanged, ResyncRequired, HostUnavailable, InternalError,
	}
	for _, code := range codes {
		payload, err := json.Marshal(IPCErrorClass{Code: code, Message: "safe"})
		if err != nil {
			t.Fatal(err)
		}
		message, err := json.Marshal(IPCEnvelope{
			Version: Version, Type: IPCError, RequestID: ipcRequestID, Payload: payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeIPCHost(message); err != nil {
			t.Errorf("canonical IPC error code %q rejected: %v", code, err)
		}
	}
	invalid := []byte(`{"version":1,"type":"ipc.error","requestId":"` + ipcRequestID + `","payload":{"code":"not_real","message":"safe"}}`)
	if _, err := DecodeIPCHost(invalid); err == nil {
		t.Fatal("unknown IPC error code was accepted")
	}
}

func TestDecodeIPCRejectsDirectionVersionAndUnknownFields(t *testing.T) {
	hello := []byte(`{"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCHost(hello); err == nil {
		t.Fatal("host accepted client hello")
	}
	wrongVersion := []byte(`{"version":2,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCClient(wrongVersion); err == nil {
		t.Fatal("wrong IPC version was accepted")
	}
	unknown := []byte(`{"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","extra":true,"payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCClient(unknown); err == nil {
		t.Fatal("unknown envelope field was accepted")
	}
}

func TestDecodeIPCRejectsDuplicateAndMalformedPayloads(t *testing.T) {
	duplicate := []byte(`{"version":1,"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCClient(duplicate); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	badKind := []byte(`{"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"browser","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCClient(badKind); err == nil {
		t.Fatal("browser IPC client was accepted")
	}
	unknownPayload := []byte(`{"version":1,"type":"ipc.hello","requestId":"` + ipcRequestID + `","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1],"secret":"x"}}`)
	if _, err := DecodeIPCClient(unknownPayload); err == nil {
		t.Fatal("unknown payload field was accepted")
	}
}

func TestDecodeIPCEnforcesSizeAndIdentityBounds(t *testing.T) {
	if _, err := DecodeIPCClient([]byte(strings.Repeat("x", MaxInboundMessageBytes+1))); err == nil {
		t.Fatal("oversized IPC message was accepted")
	}
	badID := []byte(`{"version":1,"type":"ipc.hello","requestId":"not-a-uuid","payload":{"clientKind":"cli","clientBuild":"dev","supportedVersions":[1]}}`)
	if _, err := DecodeIPCClient(badID); err == nil {
		t.Fatal("invalid IPC request ID was accepted")
	}
}

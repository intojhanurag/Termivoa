package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGatewayUploadMessagesStayWithinIPCLimits(t *testing.T) {
	full := base64.StdEncoding.EncodeToString(make([]byte, MaxUploadChunkBytes))
	message := rpcJSON(t, "gateway.upload.chunk", GatewayRPCUploadChunkPayload{Data: full})
	if len(message) > MaxInboundMessageBytes {
		t.Fatalf("a full chunk is %d bytes, over the %d byte IPC limit", len(message), MaxInboundMessageBytes)
	}
	if _, err := DecodeGatewayRPCRequest(message); err != nil {
		t.Fatalf("full chunk rejected: %v", err)
	}
	over := base64.StdEncoding.EncodeToString(make([]byte, MaxUploadChunkBytes+3))
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.upload.chunk", GatewayRPCUploadChunkPayload{Data: over})); err == nil {
		t.Fatal("chunk over 128 KiB accepted")
	}
	begin := GatewayRPCUploadBeginPayload{Credential: rpcTestSecret, SessionID: rpcTestID, Generation: rpcTestID, Size: MaxUploadBytes}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.upload.begin", begin)); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*GatewayRPCUploadBeginPayload){
		func(payload *GatewayRPCUploadBeginPayload) { payload.Size = 0 },
		func(payload *GatewayRPCUploadBeginPayload) { payload.Size = MaxUploadBytes + 1 },
		func(payload *GatewayRPCUploadBeginPayload) { payload.SessionID = "x" },
		func(payload *GatewayRPCUploadBeginPayload) { payload.Credential = "short" },
	} {
		payload := begin
		mutate(&payload)
		if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.upload.begin", payload)); err == nil {
			t.Errorf("accepted %+v", payload)
		}
	}
	good := HTTPUploadResponse{Path: ".termivoa/uploads/20260927-143012-a1b2c3d4.png", Size: 10, ContentType: ImagePNG}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.upload.commit.result", good)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"../.termivoa/uploads/20260927-143012-a1b2c3d4.png", ".termivoa/uploads/photo.png",
		".termivoa/uploads/20260927-143012-a1b2c3d4.svg", "/tmp/20260927-143012-a1b2c3d4.png",
	} {
		bad := good
		bad.Path = path
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.upload.commit.result", bad)); err == nil {
			t.Errorf("result path %q accepted", path)
		}
	}
}

func TestDecodeUploadData(t *testing.T) {
	if data, err := DecodeUploadData("aGVsbG8=", 5); err != nil || string(data) != "hello" {
		t.Fatalf("hello = %q, %v", data, err)
	}
	for _, text := range []string{"", "aGVsbG8", "aGVsbG8=\n", "-_-_", "aGVsbG8h", strings.Repeat("A", 12)} {
		if _, err := DecodeUploadData(text, 5); err == nil {
			t.Errorf("%q accepted", text)
		}
	}
}

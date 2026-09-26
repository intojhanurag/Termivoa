package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	rpcTestRequest = "00000000-0000-4000-8000-0000000000a1"
	rpcTestID      = "00000000-0000-4000-8000-0000000000a2"
	rpcTestSecret  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func rpcJSON(t *testing.T, messageType string, payload any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{
		"version": 1, "type": messageType, "requestId": rpcTestRequest, "payload": json.RawMessage(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestGatewayRPCValidatesDirectionCredentialsAndPollInvariants(t *testing.T) {
	credential := GatewayRPCCredentialPayload{Credential: rpcTestSecret}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.auth.check", credential)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.auth.check", credential)); err == nil {
		t.Fatal("request accepted as a response")
	}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.auth.check.result", credential)); err == nil {
		t.Fatal("result accepted as a request")
	}
	for name, message := range map[string][]byte{
		"short credential":      rpcJSON(t, "gateway.settings", GatewayRPCCredentialPayload{Credential: rpcTestSecret[:42]}),
		"padded credential":     rpcJSON(t, "gateway.settings", GatewayRPCCredentialPayload{Credential: rpcTestSecret[:42] + "="}),
		"unknown field":         rpcJSON(t, "gateway.settings", map[string]string{"credential": rpcTestSecret, "deviceId": rpcTestID}),
		"admin through gateway": rpcJSON(t, "admin.pair.grant", BrokerRPCEmptyPayload{}),
		"control label":         rpcJSON(t, "gateway.pair.redeem", HTTPPairRedeemRequest{Grant: rpcTestSecret, DeviceLabel: "phone\x1b[2J"}),
		"long label":            rpcJSON(t, "gateway.pair.redeem", HTTPPairRedeemRequest{Grant: rpcTestSecret, DeviceLabel: strings.Repeat("界", 22)}),
		"duplicate key":         []byte(`{"version":1,"type":"gateway.settings","requestId":"` + rpcTestRequest + `","payload":{"credential":"` + rpcTestSecret + `","credential":"` + rpcTestSecret + `"}}`),
	} {
		if _, err := DecodeGatewayRPCRequest(message); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	device := &HTTPDeviceSummary{ID: rpcTestID, Label: "Phone"}
	secret, expires := rpcTestSecret, "2026-09-27T10:00:00Z"
	valid := GatewayRPCPairPollResultPayload{Response: HTTPPairPollResponse{Status: Approved, Device: device}, Credential: &secret, CredentialExpiresAt: &expires}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.pair.poll.result", valid)); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]GatewayRPCPairPollResultPayload{
		"approved without credential": {Response: HTTPPairPollResponse{Status: Approved, Device: device}},
		"pending with credential":     {Response: HTTPPairPollResponse{Status: Pending}, Credential: &secret, CredentialExpiresAt: &expires},
		"pending with device":         {Response: HTTPPairPollResponse{Status: Pending, Device: device}},
	} {
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.pair.poll.result", payload)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestAdminRPCValidatesPayloadsAndOrigins(t *testing.T) {
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.device.revoke", AdminRPCDeviceIDPayload{DeviceID: rpcTestID})); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "gateway.live.open", GatewayRPCCredentialPayload{Credential: rpcTestSecret})); err == nil {
		t.Fatal("gateway request accepted as local administration")
	}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.pending.result", AdminRPCPairPendingResultPayload{
		Pending: []AdminRPCPendingPairing{{ID: rpcTestID, VerificationCode: "12-3456", DeviceLabel: "Phone", ExpiresAt: "2026-09-27T10:00:00Z"}},
	})); err == nil {
		t.Fatal("malformed verification code accepted")
	}
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.shutdown", map[string]bool{"force": true})); err == nil {
		t.Fatal("shutdown request with an unknown field accepted")
	}
	for count, valid := range map[int64]bool{0: true, 64: true, -1: false, 65: false} {
		_, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.shutdown.result", AdminRPCShutdownResultPayload{TerminatedSessions: count}))
		if (err == nil) != valid {
			t.Fatalf("shutdown result with %d sessions: %v", count, err)
		}
	}
	for _, origin := range []string{
		"https://mac.example.ts.net", "https://mac.example.ts.net:8443", "http://127.0.0.1:8788", "http://localhost:5173",
	} {
		if err := ValidatePairingOrigin(origin); err != nil {
			t.Fatalf("%s rejected: %v", origin, err)
		}
	}
	for _, origin := range []string{
		"http://mac.example.ts.net", "http://127.0.0.1", "https://mac.example.ts.net/", "https://mac.example.ts.net/pair",
		"https://user@mac.example.ts.net", "https://mac.example.ts.net?x", "https://mac.example.ts.net#x",
		"https://Mac.example.ts.net", "ftp://mac.example.ts.net", "https://mac.example.ts.net:0", "https://mac.example.ts.net:08443",
		"https://", "javascript:alert(1)",
	} {
		if err := ValidatePairingOrigin(origin); err == nil {
			t.Fatalf("%s accepted", origin)
		}
	}
}

func TestAuthenticationRPCSchemasListEveryMessageExactlyOnce(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	for file, results := range map[string][]string{
		"gateway-rpc.schema.json": rpcTypeNames(GatewayRPCResults, string(GatewayError)),
		"admin-rpc.schema.json":   rpcTypeNames(AdminRPCResults, string(AdminError)),
	} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "protocol", "v1", file))
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			OneOf []struct {
				Ref string `json:"$ref"`
			} `json:"oneOf"`
			Defs struct {
				MessageType struct {
					Enum []string `json:"enum"`
				} `json:"messageType"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		got := append([]string(nil), schema.Defs.MessageType.Enum...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, results) || len(schema.OneOf) != len(results) {
			t.Fatalf("%s messages = %q, validators = %q, oneOf = %d", file, got, results, len(schema.OneOf))
		}
	}
}

func rpcTypeNames[T ~string](results map[T]T, errorType string) []string {
	names := []string{errorType}
	for request, result := range results {
		names = append(names, string(request), string(result))
	}
	sort.Strings(names)
	return names
}

package protocol

import "testing"

func TestPreviewPortsOnSessionState(t *testing.T) {
	const since = "2026-09-27T10:00:00.000Z"
	ok := []PreviewPort{{Port: 3000, Since: since}, {Port: 5173, Since: since}}
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Running, Previews: ok}, true)); err != nil {
		t.Fatalf("running previews: %v", err)
	}
	for name, payload := range map[string]SessionStatePayload{
		"not running": {State: Exiting, Previews: ok},
		"unsorted":    {State: Running, Previews: []PreviewPort{ok[1], ok[0]}},
		"duplicate":   {State: Running, Previews: []PreviewPort{ok[0], ok[0]}},
		"port zero":   {State: Running, Previews: []PreviewPort{{Port: 0, Since: since}}},
		"port big":    {State: Running, Previews: []PreviewPort{{Port: 65536, Since: since}}},
		"bad since":   {State: Running, Previews: []PreviewPort{{Port: 80, Since: "yesterday at noon!!"}}},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, SessionState, payload, true)); err == nil {
			t.Errorf("%s previews were accepted", name)
		}
	}
}

func TestPresentPreviewListIsNeverEmpty(t *testing.T) {
	if ValidatePreviewPorts([]PreviewPort{}) == nil {
		t.Fatal("empty previews accepted")
	}
}

func TestGatewayPreviewCheckMessages(t *testing.T) {
	valid := GatewayRPCPreviewCheckPayload{Credential: rpcTestSecret, SessionID: rpcTestID, Port: 5173}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.preview.check", valid)); err != nil {
		t.Fatalf("valid check: %v", err)
	}
	for name, payload := range map[string]GatewayRPCPreviewCheckPayload{
		"port zero":   {Credential: rpcTestSecret, SessionID: rpcTestID, Port: 0},
		"port big":    {Credential: rpcTestSecret, SessionID: rpcTestID, Port: 70000},
		"bad session": {Credential: rpcTestSecret, SessionID: "session", Port: 80},
		"credential":  {Credential: "short", SessionID: rpcTestID, Port: 80},
	} {
		if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.preview.check", payload)); err == nil {
			t.Errorf("%s check was accepted", name)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.preview.check.result", map[string]string{"host": host})); err != nil {
			t.Errorf("host %s: %v", host, err)
		}
	}
	for _, host := range []string{"10.0.0.1", "localhost", "0.0.0.0", ""} {
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.preview.check.result", map[string]string{"host": host})); err == nil {
			t.Errorf("host %q was accepted", host)
		}
	}
}

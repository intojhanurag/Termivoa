package gateway

import (
	"net/http"
	"testing"
)

func TestChatRouteTurnsChatOffOnly(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	response := do(handler, http.MethodPatch, "/api/v1/chat", `{"enabled":false}`, requestOptions{cookie: prodCookie})
	if response.Code != http.StatusOK || response.Body.String() != `{"enabled":false}`+"\n" && response.Body.String() != `{"enabled":false}` || !broker.called("chat-update") {
		t.Fatalf("off = %d %s", response.Code, response.Body)
	}
	for name, test := range map[string]struct {
		method, body string
		options      requestOptions
		status       int
	}{
		"on":            {http.MethodPatch, `{"enabled":true}`, requestOptions{cookie: prodCookie}, 400},
		"no cookie":     {http.MethodPatch, `{"enabled":false}`, requestOptions{}, 401},
		"unknown field": {http.MethodPatch, `{"enabled":false,"x":1}`, requestOptions{cookie: prodCookie}, 400},
		"GET":           {http.MethodGet, "", requestOptions{cookie: prodCookie}, 405},
	} {
		if response := do(handler, test.method, "/api/v1/chat", test.body, test.options); response.Code != test.status {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body)
		}
	}
}

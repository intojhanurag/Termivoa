package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

const homeOrigin = "https://home.tailx.ts.net"

func TestPeer(t *testing.T) {
	server := httptest.NewServer(newPeer(map[string]bool{homeOrigin: true}))
	defer server.Close()
	do := func(method, path, origin, cookie string) (*http.Response, echo) {
		request, _ := http.NewRequest(method, server.URL+path, nil)
		request.Header.Set("Origin", origin)
		if cookie != "" {
			request.Header.Set("Cookie", cookieName+"="+cookie)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var e echo
		_ = json.NewDecoder(response.Body).Decode(&e)
		return response, e
	}

	// Credentialed CORS for the exact home origin only.
	if r, _ := do(http.MethodOptions, "/echo", homeOrigin, ""); r.StatusCode != 204 ||
		r.Header.Get("Access-Control-Allow-Origin") != homeOrigin || r.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("home preflight: %d %v", r.StatusCode, r.Header)
	}
	for _, origin := range []string{"https://evil.taily.ts.net", "https://home.tailx.ts.net.evil.com", "null", ""} {
		if r, _ := do(http.MethodOptions, "/echo", origin, ""); r.StatusCode != 403 || r.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("preflight from %q: %d %v", origin, r.StatusCode, r.Header)
		}
	}

	// /set issues a __Host- Strict HttpOnly cookie, and keeps the value it already has.
	r, set := do(http.MethodGet, "/set", homeOrigin, "")
	cookie := r.Header.Get("Set-Cookie")
	for _, part := range []string{cookieName + "=" + set.Set, "Path=/", "Max-Age=2592000", "HttpOnly", "Secure", "SameSite=Strict"} {
		if set.Set == "" || !strings.Contains(cookie, part) {
			t.Fatalf("Set-Cookie %q lacks %q", cookie, part)
		}
	}
	if _, again := do(http.MethodGet, "/set", homeOrigin, set.Set); again.Set != set.Set || again.Received != set.Set {
		t.Fatalf("refresh changed the value: %+v", again)
	}
	if _, e := do(http.MethodGet, "/echo", homeOrigin, set.Set); e.Received != set.Set {
		t.Fatalf("echo: %+v", e)
	}

	// WebSocket: exact origin only, sends what it received, then echoes.
	ws := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx := context.Background()
	header := http.Header{"Origin": {homeOrigin}, "Cookie": {cookieName + "=" + set.Set}}
	conn, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var hello echo
	if _, data, err := conn.Read(ctx); err != nil || json.Unmarshal(data, &hello) != nil || hello.Received != set.Set {
		t.Fatalf("hello: %s %v", data, err)
	}
	if conn.Write(ctx, websocket.MessageText, []byte("ping")) != nil {
		t.Fatal("write")
	}
	if _, data, err := conn.Read(ctx); err != nil || string(data) != "ping" {
		t.Fatalf("echo: %s %v", data, err)
	}
	header.Set("Origin", "https://evil.taily.ts.net")
	if _, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: header}); err == nil {
		t.Fatal("WebSocket from another origin was accepted")
	}
}

func TestParseOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://Peer.tailX.ts.net:8443": "https://peer.tailx.ts.net:8443",
		"https://peer.tailx.ts.net/":     "https://peer.tailx.ts.net",
		"http://peer.tailx.ts.net":       "",
		"https://peer.tailx.ts.net/x":    "",
		"https://u@peer.tailx.ts.net":    "",
		"peer.tailx.ts.net":              "",
	} {
		if got, err := parseOrigin(raw); got != want || (want == "") != (err != nil) {
			t.Errorf("parseOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Issue #149. Behind Tailscale Serve every client reaches the gateway from
// 127.0.0.1. No number of failed requests from another machine may refuse a
// paired phone: not on the API (200 and 204 answers), the live socket,
// index.html, the selection or the preview proxy, and not on a fresh handler
// (a gateway restart during the flood).
func TestFailuresOfAnotherClientDoNotLockOutAPairedDevice(t *testing.T) {
	const flood = 200
	broker := newFakeComputers()
	broker.list.Computers = []protocol.HTTPComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps"}}
	main := newPeerHandler(t, broker) // fresh: it has seen no request yet
	preview, err := newPreviewHandler(main, ProductionPreviewListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preview.stop)
	server := httptest.NewServer(preview) // its clients are 127.0.0.1, as behind Serve
	t.Cleanup(server.Close)
	dev := newDevServer(t)
	broker.setPreview(dev.port, "127.0.0.1")
	fixture := &previewFixture{broker: broker.fakeBroker, main: main, preview: preview, server: server, dev: dev}

	app := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		return do(main, method, path, body, requestOptions{cookie: cookie, remote: "127.0.0.1:9", omitOrigin: method == http.MethodGet && path == "/"})
	}
	attack := func() {
		t.Helper()
		for attempt := 0; attempt < flood; attempt++ {
			// A new well-formed fake credential each time, a refused one, and
			// a malformed one.
			guess := productionCookieName + "=" + strings.Repeat("F", 39) + strconv.Itoa(1000+attempt)
			for name, status := range map[string]int{
				"api guess":     app(http.MethodGet, "/api/v1/settings", "", guess).Code,
				"api revoked":   app(http.MethodGet, "/api/v1/settings", "", productionCookieName+"="+revokedCredential).Code,
				"api malformed": app(http.MethodGet, "/api/v1/settings", "", productionCookieName+"=short").Code,
				"api 204 route": app(http.MethodDelete, "/api/v1/computers/vps.tailx.ts.net", `{}`, guess).Code,
				"selection":     fixture.get(t, fixture.selectPath(), framedBy(testOrigin, cookieHeader(guess))).StatusCode,
				"proxy":         fixture.get(t, "/", cookieHeader(guess, fixture.selection())).StatusCode,
			} {
				if status != http.StatusUnauthorized {
					t.Fatalf("attack %d, %s = %d", attempt, name, status)
				}
			}
			if policy := app(http.MethodGet, "/", "", guess).Header().Get("Content-Security-Policy"); strings.Contains(policy, "vps.tailx.ts.net") {
				t.Fatalf("attack %d got a device's CSP entries: %s", attempt, policy)
			}
		}
	}
	phone := func(when string) {
		t.Helper()
		if response := app(http.MethodGet, "/api/v1/settings", "", prodCookie); response.Code != http.StatusOK {
			t.Errorf("%s: API = %d", when, response.Code)
		}
		if response := app(http.MethodDelete, "/api/v1/computers/vps.tailx.ts.net", `{}`, prodCookie); response.Code != http.StatusNoContent {
			t.Errorf("%s: 204 route = %d", when, response.Code)
		}
		if policy := app(http.MethodGet, "/", "", prodCookie).Header().Get("Content-Security-Policy"); !strings.Contains(policy, "https://vps.tailx.ts.net:8443") {
			t.Errorf("%s: index.html has no per-device CSP entries: %s", when, policy)
		}
		if response := fixture.get(t, fixture.selectPath(), framedBy(testOrigin, cookieHeader(prodCookie))); response.StatusCode != http.StatusOK {
			t.Errorf("%s: selection = %d", when, response.StatusCode)
		}
		if response := fixture.get(t, "/", cookieHeader(prodCookie, fixture.selection())); response.StatusCode != http.StatusOK || body(t, response) != "dev site /" {
			t.Errorf("%s: preview proxy = %d", when, response.StatusCode)
		}
	}
	// The attack starts before the phone's first request (the restart case).
	attack()
	phone("first request after a restart, during the flood")
	attack()
	phone("later in the flood")
}

func TestFailuresOfAnotherClientDoNotLockOutTheLiveSocket(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	for attempt := 0; attempt < 200; attempt++ {
		credential := strings.Repeat("F", 39) + strconv.Itoa(1000+attempt)
		if attempt%2 == 1 {
			credential = revokedCredential
		}
		_, response, err := dialLive(t, bound, liveHeader(bound, credential))
		if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attack %d: %v %v", attempt, response, err)
		}
	}
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatalf("phone's live socket during the flood: %v", err)
	}
	conn.CloseNow()
}

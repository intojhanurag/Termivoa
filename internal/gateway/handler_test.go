package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	testHost   = "mac.tailnet.ts.net"
	testOrigin = "https://mac.tailnet.ts.net"
	testBound  = "127.0.0.1:8787"
)

// newTestHandler is a production-profile handler with a fixed test site.
func newTestHandler(t *testing.T, broker gatewayapi.Broker) *gatewayHandler {
	t.Helper()
	handler, err := newServer(Config{Profile: platform.ProductionProfile, Origins: []string{testOrigin}, Broker: broker}, testBound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	handler.assets = fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>app</title>")},
		"sw.js":                    {Data: []byte("self.addEventListener('fetch', () => {})")},
		"manifest.webmanifest":     {Data: []byte(`{"name":"Phone Terminal"}`)},
		"assets/app-3f9a1c.js":     {Data: []byte("console.log(1)")},
		"assets/app-3f9a1c.css":    {Data: []byte("body{}")},
		"assets/font-77aa.woff2":   {Data: []byte("wOF2")},
		"assets/app-3f9a1c.js.map": {Data: []byte(`{"sources":["src/secret.ts"]}`)},
		".vite/manifest.json":      {Data: []byte("{}")},
		".DS_Store":                {Data: []byte("x")},
	}
	return handler
}

func newDevHandler(t *testing.T, broker gatewayapi.Broker) *gatewayHandler {
	t.Helper()
	handler, err := newServer(Config{Profile: platform.DevelopmentProfile, Broker: broker}, "127.0.0.1:9080", uuidC)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type requestOptions struct {
	host, origin, contentType, pterm, cookie string
	omitOrigin, omitHeader                   bool
	remote                                   string
}

func do(handler http.Handler, method, target, body string, options requestOptions) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+testHost+target, strings.NewReader(body))
	request.Host = testHost
	if options.host != "" {
		request.Host = options.host
	}
	if !options.omitOrigin {
		request.Header.Set("Origin", testOrigin)
		if options.origin != "" {
			request.Header.Set("Origin", options.origin)
		}
	}
	if !options.omitHeader {
		request.Header.Set("X-Pterm-Request", "1")
		if options.pterm != "" {
			request.Header.Set("X-Pterm-Request", options.pterm)
		}
	}
	request.Header.Set("Content-Type", "application/json")
	if options.contentType != "" {
		request.Header.Set("Content-Type", options.contentType)
	}
	if options.cookie != "" {
		request.Header.Set("Cookie", options.cookie)
	}
	if options.remote != "" {
		request.RemoteAddr = options.remote
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) protocol.ErrorCode {
	t.Helper()
	var payload protocol.HTTPErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error body %q: %v", response.Body.String(), err)
	}
	return payload.Error.Code
}

var (
	redeemBody = `{"grant":"` + strings.Repeat("g", 42) + `-","deviceLabel":"Anurag's phone"}`
	pollBody   = `{"pendingId":"` + uuidA + `","pollSecret":"` + strings.Repeat("s", 42) + `_"}`
)

var prodCookie = productionCookieName + "=" + validCredential

func TestHostValidationIsExact(t *testing.T) {
	handler := newTestHandler(t, newFakeBroker())
	for _, test := range []struct {
		host string
		want int
	}{
		{testHost, http.StatusOK},
		{"MAC.Tailnet.TS.net", http.StatusOK},
		{testBound, http.StatusOK},
		{"localhost:8787", http.StatusOK},
		{"mac.tailnet.ts.net.evil", http.StatusMisdirectedRequest},
		{"evil.mac.tailnet.ts.net", http.StatusMisdirectedRequest},
		{"mac.tailnet.ts.net.", http.StatusMisdirectedRequest},
		{"mac.tailnet.ts.net:8443", http.StatusMisdirectedRequest},
		{"mac-tailnet.ts.net", http.StatusMisdirectedRequest},
		{"127.0.0.1:8788", http.StatusMisdirectedRequest},
		{"127.0.0.1", http.StatusMisdirectedRequest},
	} {
		response := do(handler, http.MethodGet, "/healthz", "", requestOptions{host: test.host})
		if response.Code != test.want {
			t.Errorf("Host %q status = %d, want %d", test.host, response.Code, test.want)
		}
	}
	// Forwarded headers never substitute for Host.
	request := httptest.NewRequest(http.MethodGet, "http://evil.example/healthz", nil)
	request.Host = "evil.example"
	request.Header.Set("X-Forwarded-Host", testHost)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("X-Forwarded-Host bypass status = %d", response.Code)
	}
}

func TestMutationBoundary(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, test := range []struct {
		name    string
		body    string
		options requestOptions
		status  int
		code    protocol.ErrorCode
	}{
		{"missing origin", redeemBody, requestOptions{omitOrigin: true}, 403, protocol.InvalidRequest},
		{"forged origin", redeemBody, requestOptions{origin: "https://evil.example"}, 403, protocol.InvalidRequest},
		{"near-match origin", redeemBody, requestOptions{origin: "https://mac.tailnet.ts.net.evil"}, 403, protocol.InvalidRequest},
		{"http origin", redeemBody, requestOptions{origin: "http://mac.tailnet.ts.net"}, 403, protocol.InvalidRequest},
		{"null origin", redeemBody, requestOptions{origin: "null"}, 403, protocol.InvalidRequest},
		{"missing header", redeemBody, requestOptions{omitHeader: true}, 403, protocol.InvalidRequest},
		{"wrong header", redeemBody, requestOptions{pterm: "true"}, 403, protocol.InvalidRequest},
		{"text content type", redeemBody, requestOptions{contentType: "text/plain"}, 415, protocol.InvalidRequest},
		{"form content type", redeemBody, requestOptions{contentType: "application/x-www-form-urlencoded"}, 415, protocol.InvalidRequest},
		{"latin1 charset", redeemBody, requestOptions{contentType: "application/json; charset=latin1"}, 415, protocol.InvalidRequest},
		{"oversize", `{"grant":"` + strings.Repeat("a", 70*1024) + `"}`, requestOptions{}, 413, protocol.LimitExceeded},
		{"unknown field", `{"grant":"` + testSecret + `","deviceLabel":"p","admin":true}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"duplicate key", `{"grant":"` + testSecret + `","deviceLabel":"p","deviceLabel":"q"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"two values", redeemBody + `{}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"array", `[]`, requestOptions{}, 400, protocol.InvalidRequest},
		{"invalid utf8", "{\"grant\":\"" + testSecret + "\",\"deviceLabel\":\"\xff\"}", requestOptions{}, 400, protocol.InvalidRequest},
		{"lone surrogate", `{"grant":"` + testSecret + `","deviceLabel":"\ud800"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"short grant", `{"grant":"abc","deviceLabel":"p"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"control label", `{"grant":"` + testSecret + `","deviceLabel":"a\u001bb"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"long label", `{"grant":"` + testSecret + `","deviceLabel":"` + strings.Repeat("é", 33) + `"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"missing label", `{"grant":"` + testSecret + `"}`, requestOptions{}, 400, protocol.InvalidRequest},
		{"case-folded field", `{"GRANT":"` + testSecret + `","deviceLabel":"p"}`, requestOptions{remote: "192.0.2.60:1"}, 400, protocol.InvalidRequest},
		{"case-folded duplicate", `{"grant":"` + testSecret + `","deviceLabel":"p","Grant":"` + validCredential + `"}`, requestOptions{remote: "192.0.2.61:1"}, 400, protocol.InvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := do(handler, http.MethodPost, "/api/v1/pair/redeem", test.body, test.options)
			if response.Code != test.status || errorCode(t, response) != test.code {
				t.Fatalf("status = %d body = %s", response.Code, response.Body)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
	if broker.called("redeem") {
		t.Fatal("rejected requests reached the broker")
	}
	// Malformed attempts that passed the boundary consumed this client's
	// redeem budget; use a fresh client for the happy path.
	response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody,
		requestOptions{contentType: "application/json; charset=UTF-8", remote: "192.0.2.50:1"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"verificationCode":"123-456"`) {
		t.Fatalf("redeem = %d %s", response.Code, response.Body)
	}
	if response := do(handler, http.MethodGet, "/api/v1/pair/redeem", "", requestOptions{}); response.Code != 405 {
		t.Fatalf("GET redeem = %d", response.Code)
	}
}

func TestPollSetsCookieOnlyOnceOnApproval(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler func(*testing.T, *fakeBroker) *gatewayHandler
		cookie  string
		secure  bool
	}{
		{"production", func(t *testing.T, broker *fakeBroker) *gatewayHandler {
			return newTestHandler(t, broker)
		}, productionCookieName, true},
		{"development", func(t *testing.T, broker *fakeBroker) *gatewayHandler { return newDevHandler(t, broker) }, developmentCookieName, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker := newFakeBroker()
			expires := time.Now().Add(30 * 24 * time.Hour)
			device := &protocol.HTTPDeviceSummary{ID: uuidA, Label: "phone"}
			broker.pollResults = []gatewayapi.PollResult{
				{Response: protocol.HTTPPairPollResponse{Status: protocol.Pending}},
				{Response: protocol.HTTPPairPollResponse{Status: protocol.Approved, Device: device}, Credential: validCredential, CredentialExpiresAt: expires},
			}
			broker.pollErr = &gatewayapi.Error{Code: protocol.PairingExpired, Message: "pairing expired"}
			handler := test.handler(t, broker)
			options := requestOptions{}
			if test.name == "development" {
				options = requestOptions{host: "127.0.0.1:9080", origin: "http://127.0.0.1:9080"}
			}

			pending := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, options)
			if pending.Code != 200 || pending.Header().Get("Set-Cookie") != "" || !strings.Contains(pending.Body.String(), `"pending"`) {
				t.Fatalf("pending = %d %v %s", pending.Code, pending.Header(), pending.Body)
			}
			approved := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, options)
			cookies := approved.Result().Cookies()
			if approved.Code != 200 || len(cookies) != 1 {
				t.Fatalf("approved = %d %v", approved.Code, approved.Header())
			}
			cookie := cookies[0]
			raw := approved.Header().Get("Set-Cookie")
			if cookie.Name != test.cookie || cookie.Value != validCredential || cookie.Path != "/" || cookie.Domain != "" ||
				!cookie.HttpOnly || cookie.Secure != test.secure || cookie.SameSite != http.SameSiteStrictMode ||
				strings.Contains(raw, "Domain") || cookie.MaxAge < 30*24*3600-5 || cookie.MaxAge > 30*24*3600 {
				t.Fatalf("cookie = %q", raw)
			}
			again := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, options)
			if again.Code != http.StatusGone || again.Header().Get("Set-Cookie") != "" || errorCode(t, again) != protocol.PairingExpired {
				t.Fatalf("replayed poll = %d %v", again.Code, again.Header())
			}
		})
	}
}

func TestPollRejectsBrokerContractViolations(t *testing.T) {
	broker := newFakeBroker()
	broker.pollResults = []gatewayapi.PollResult{
		{Response: protocol.HTTPPairPollResponse{Status: protocol.Approved}},
		{Response: protocol.HTTPPairPollResponse{Status: protocol.Pending}, Credential: validCredential, CredentialExpiresAt: time.Now().Add(time.Hour)},
		{Response: protocol.HTTPPairPollResponse{Status: protocol.Approved}, Credential: validCredential, CredentialExpiresAt: time.Now().Add(-time.Hour)},
		// Expired by under a second: Max-Age would round to 0, which omits
		// the attribute and yields a non-expiring session cookie.
		{Response: protocol.HTTPPairPollResponse{Status: protocol.Approved}, Credential: validCredential, CredentialExpiresAt: time.Now().Add(-time.Millisecond)},
	}
	handler := newTestHandler(t, broker)
	for range 4 {
		response := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, requestOptions{})
		if response.Code != 500 || response.Header().Get("Set-Cookie") != "" || errorCode(t, response) != protocol.InternalError {
			t.Fatalf("violation = %d %v", response.Code, response.Header())
		}
	}
}

func TestAuthenticatedRoutes(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	createBody := `{"operationId":"` + uuidA + `","projectId":"` + uuidB + `","shellProfileId":"` + uuidC + `"}`
	renameBody := `{"operationId":"` + uuidA + `","generation":"` + uuidC + `","title":"build ✓"}`
	terminateBody := `{"operationId":"` + uuidA + `","generation":"` + uuidC + `"}`
	for _, test := range []struct {
		method, path, body, call, want string
	}{
		{http.MethodGet, "/api/v1/settings", "", "settings", `"hostBuild":"test"`},
		{http.MethodGet, "/api/v1/sessions", "", "list", `"sessions":[`},
		{http.MethodPost, "/api/v1/sessions", createBody, "create", `"operationId":"` + uuidA},
		{http.MethodPatch, "/api/v1/sessions/" + uuidB, renameBody, "rename:" + uuidB, `"session":{`},
		{http.MethodPost, "/api/v1/sessions/" + uuidB + "/terminate", terminateBody, "terminate:" + uuidB, `"session":{`},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := do(handler, test.method, test.path, test.body, requestOptions{cookie: prodCookie})
			if response.Code != 200 || !strings.Contains(response.Body.String(), test.want) || !broker.called(test.call) {
				t.Fatalf("status = %d body = %s", response.Code, response.Body)
			}
			header := response.Header()
			if header.Get("Cache-Control") != "no-store" || header.Get("Content-Type") != "application/json" ||
				header.Get("Content-Security-Policy") != contentSecurityPolicy || header.Get("Referrer-Policy") != "no-referrer" ||
				header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("headers = %v", header)
			}

			// Without a cookie, with a revoked one, with the development
			// cookie name, or with a duplicated (possibly tossed) cookie, the
			// route is unauthorized.
			for _, cookie := range []string{"", productionCookieName + "=" + revokedCredential, developmentCookieName + "=" + validCredential,
				prodCookie + "; " + productionCookieName + "=" + revokedCredential} {
				response := do(handler, test.method, test.path, test.body, requestOptions{cookie: cookie, remote: "192.0.2.9:1"})
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("cookie %q status = %d", cookie, response.Code)
				}
			}
		})
	}
	revoked := do(handler, http.MethodGet, "/api/v1/settings", "", requestOptions{cookie: productionCookieName + "=" + revokedCredential, remote: "192.0.2.10:1"})
	if errorCode(t, revoked) != protocol.Revoked || !strings.Contains(revoked.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("revoked = %s %v", revoked.Body, revoked.Header())
	}
	if response := do(handler, http.MethodPatch, "/api/v1/sessions/not-a-uuid", renameBody, requestOptions{cookie: prodCookie}); response.Code != 400 {
		t.Fatalf("bad session id = %d", response.Code)
	}
	if response := do(handler, http.MethodPatch, "/api/v1/sessions/"+uuidB, strings.Replace(renameBody, "build ✓", "a\\u0007", 1), requestOptions{cookie: prodCookie}); response.Code != 400 {
		t.Fatalf("control title = %d", response.Code)
	}
	if response := do(handler, http.MethodPost, "/api/v1/sessions", strings.Replace(createBody, uuidC, strings.ToUpper(uuidC), 1), requestOptions{cookie: prodCookie}); response.Code != 400 {
		t.Fatalf("uppercase uuid = %d", response.Code)
	}
	if response := do(handler, http.MethodPost, "/api/v1/sessions", createBody, requestOptions{cookie: prodCookie, omitOrigin: true}); response.Code != 403 {
		t.Fatalf("create without origin = %d", response.Code)
	}
}

func TestBrokerErrorMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   protocol.ErrorCode
	}{
		{&gatewayapi.Error{Code: protocol.SessionMissing, Message: "session not found"}, 404, protocol.SessionMissing},
		{&gatewayapi.Error{Code: protocol.GenerationChanged, Message: "generation changed"}, 409, protocol.GenerationChanged},
		{&gatewayapi.Error{Code: protocol.SessionLost, Message: "lost"}, 409, protocol.SessionLost},
		{&gatewayapi.Error{Code: protocol.LimitExceeded, Message: "limit"}, 429, protocol.LimitExceeded},
		{&gatewayapi.Error{Code: protocol.HostUnavailable, Message: "down"}, 503, protocol.HostUnavailable},
		{&gatewayapi.Error{Code: protocol.InternalError, Message: "panic: secret /Users/x"}, 500, protocol.InternalError},
		{errors.New("dial unix /secret/path: refused"), 503, protocol.HostUnavailable},
	} {
		broker := newFakeBroker()
		broker.opErr = test.err
		handler := newTestHandler(t, broker)
		response := do(handler, http.MethodGet, "/api/v1/sessions", "", requestOptions{cookie: prodCookie})
		if response.Code != test.status || errorCode(t, response) != test.code || strings.Contains(response.Body.String(), "secret") {
			t.Errorf("%v -> %d %s", test.err, response.Code, response.Body)
		}
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, cookie := range []string{prodCookie, productionCookieName + "=" + revokedCredential, ""} {
		response := do(handler, http.MethodPost, "/api/v1/logout", `{}`, requestOptions{cookie: cookie})
		setCookie := response.Header().Get("Set-Cookie")
		if response.Code != http.StatusNoContent || !strings.HasPrefix(setCookie, productionCookieName+"=;") ||
			!strings.Contains(setCookie, "Max-Age=0") || !strings.Contains(setCookie, "Secure") {
			t.Fatalf("logout(%q) = %d %q", cookie, response.Code, setCookie)
		}
	}
	if !broker.called("logout") {
		t.Fatal("broker logout not called")
	}
	if response := do(handler, http.MethodPost, "/api/v1/logout", `{"all":true}`, requestOptions{cookie: prodCookie}); response.Code != 400 {
		t.Fatalf("logout with fields = %d", response.Code)
	}
	if response := do(handler, http.MethodPost, "/api/v1/logout", `{}`, requestOptions{cookie: prodCookie, origin: "https://evil.example"}); response.Code != 403 {
		t.Fatalf("forged logout = %d", response.Code)
	}
}

func TestRateLimits(t *testing.T) {
	handler := newTestHandler(t, newFakeBroker())
	limited := func(path, body string, options requestOptions, attempts int) {
		t.Helper()
		for attempt := 1; attempt <= attempts; attempt++ {
			response := do(handler, http.MethodPost, path, body, options)
			if attempt < attempts && response.Code == http.StatusTooManyRequests {
				t.Fatalf("%s limited early at attempt %d", path, attempt)
			}
			if attempt == attempts && (response.Code != http.StatusTooManyRequests || errorCode(t, response) != protocol.LimitExceeded) {
				t.Fatalf("%s attempt %d = %d", path, attempt, response.Code)
			}
		}
	}
	limited("/api/v1/pair/redeem", redeemBody, requestOptions{}, 6)
	limited("/api/v1/pair/poll", pollBody, requestOptions{}, 11)
	// Another client is unaffected.
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{remote: "192.0.2.77:5"}); response.Code != 200 {
		t.Fatalf("other client = %d", response.Code)
	}
	// Failed authentication: 20 failures, then even a valid cookie is refused.
	bad := requestOptions{cookie: productionCookieName + "=" + revokedCredential, remote: "192.0.2.2:5"}
	for attempt := 0; attempt < 20; attempt++ {
		if response := do(handler, http.MethodGet, "/api/v1/settings", "", bad); response.Code != 401 {
			t.Fatalf("failure %d = %d", attempt, response.Code)
		}
	}
	if response := do(handler, http.MethodGet, "/api/v1/settings", "", requestOptions{cookie: prodCookie, remote: "192.0.2.2:6"}); response.Code != 429 {
		t.Fatalf("after failures = %d", response.Code)
	}
}

func TestLimiterRefillsAndBoundsClients(t *testing.T) {
	limit := newLimiter(1, time.Second)
	now := time.Unix(0, 0)
	limit.now = func() time.Time { return now }
	if !limit.take("a") || limit.take("a") {
		t.Fatal("burst of one not enforced")
	}
	now = now.Add(time.Second)
	if !limit.take("a") {
		t.Fatal("bucket did not refill")
	}
	for index := 0; index < maxLimiterClients; index++ {
		limit.take(strconv.Itoa(index))
	}
	if len(limit.buckets) > maxLimiterClients {
		t.Fatalf("buckets = %d", len(limit.buckets))
	}
	now = now.Add(2 * time.Second)
	if !limit.take("new") || len(limit.buckets) > maxLimiterClients {
		t.Fatalf("idle buckets were not pruned: %d", len(limit.buckets))
	}
}

func TestStaticSite(t *testing.T) {
	handler := newTestHandler(t, nil)
	for _, test := range []struct {
		path, contentType, cache, body string
		status                         int
	}{
		{"/", "text/html; charset=utf-8", "no-store", "<title>app</title>", 200},
		{"/pair", "text/html; charset=utf-8", "no-store", "<title>app</title>", 200},
		{"/sessions/abc", "text/html; charset=utf-8", "no-store", "<title>app</title>", 200},
		{"/sw.js", "text/javascript; charset=utf-8", "no-store", "addEventListener", 200},
		{"/manifest.webmanifest", "application/manifest+json", "no-store", "Phone Terminal", 200},
		{"/assets/app-3f9a1c.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", "console", 200},
		{"/assets/app-3f9a1c.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable", "body", 200},
		{"/assets/font-77aa.woff2", "font/woff2", "public, max-age=31536000, immutable", "wOF2", 200},
		{"/assets/missing.js", "", "no-store", "", 404},
		{"/missing.png", "", "no-store", "", 404},
		{"/assets/app-3f9a1c.js.map", "", "no-store", "", 404},
		{"/.vite/manifest.json", "", "no-store", "", 404},
		{"/.DS_Store", "", "no-store", "", 404},
	} {
		response := do(handler, http.MethodGet, test.path, "", requestOptions{})
		header := response.Header()
		if response.Code != test.status || (test.contentType != "" && header.Get("Content-Type") != test.contentType) ||
			header.Get("Cache-Control") != test.cache || !strings.Contains(response.Body.String(), test.body) ||
			header.Get("Content-Security-Policy") != contentSecurityPolicy || header.Get("X-Content-Type-Options") != "nosniff" ||
			header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s = %d %v %q", test.path, response.Code, header, response.Body)
		}
	}
	if response := do(handler, http.MethodPost, "/", "", requestOptions{}); response.Code != 405 {
		t.Fatalf("POST / = %d", response.Code)
	}
	// With no broker the API is absent, and unknown API paths never fall back to HTML.
	for _, path := range []string{"/api/v1/sessions", "/api/v1/unknown", "/api"} {
		response := do(handler, http.MethodGet, path, "", requestOptions{})
		if response.Code != 404 || errorCode(t, response) != protocol.InvalidRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body)
		}
	}
}

func TestEmbeddedPlaceholderServes(t *testing.T) {
	handler, err := newServer(Config{Profile: platform.ProductionProfile}, testBound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	response := do(handler, http.MethodGet, "/pair", "", requestOptions{host: testBound})
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Phone Terminal") {
		t.Fatalf("placeholder = %d %s", response.Code, response.Body)
	}
}

func TestOriginConfiguration(t *testing.T) {
	for _, test := range []struct {
		origin string
		dev    bool
		ok     bool
	}{
		{"https://mac.tailnet.ts.net", false, true},
		{"https://MAC.tailnet.ts.net/", false, true},
		{"http://mac.tailnet.ts.net", false, false},
		{"https://mac.tailnet.ts.net/app", false, false},
		{"https://user@mac.tailnet.ts.net", false, false},
		{"https://mac.tailnet.ts.net?x", false, false},
		{"mac.tailnet.ts.net", false, false},
		{"*", false, false},
		{"http://localhost:5173", true, true},
		{"http://127.0.0.1:5173", true, true},
		{"https://mac.tailnet.ts.net", true, false},
		{"https://mac.tailnet.ts.net:443", false, false}, // browsers omit the default port
		{"http://localhost:80", true, false},
		{"http://[::1]:5173", true, true},
	} {
		_, _, err := validateOrigin(test.origin, test.dev)
		if (err == nil) != test.ok {
			t.Errorf("validateOrigin(%q, dev=%v) = %v", test.origin, test.dev, err)
		}
	}
	handler := newDevHandler(t, newFakeBroker())
	if !handler.originAllowed(&http.Request{Header: http.Header{"Origin": {"http://127.0.0.1:9080"}}}) ||
		handler.originAllowed(&http.Request{Header: http.Header{"Origin": {"https://127.0.0.1:9080"}}}) ||
		handler.originAllowed(&http.Request{Header: http.Header{"Origin": {"http://127.0.0.1:9080", "http://127.0.0.1:9080"}}}) {
		t.Fatal("development origins are not exact")
	}
}

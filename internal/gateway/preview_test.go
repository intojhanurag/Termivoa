package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/platform"
)

const previewHost = testHost + ":8443"

// devServer records what the proxy forwards and answers like a dev server.
type devServer struct {
	*httptest.Server
	port uint16
	mu   sync.Mutex
	last *http.Request
}

func newDevServer(t *testing.T) *devServer {
	t.Helper()
	dev := &devServer{}
	dev.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		dev.mu.Lock()
		dev.last = request.Clone(context.Background())
		dev.mu.Unlock()
		if request.Header.Get("Upgrade") != "" {
			conn, err := websocket.Accept(response, request, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				kind, data, err := conn.Read(request.Context())
				if err != nil {
					return
				}
				_ = conn.Write(request.Context(), kind, data)
			}
		}
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(response, request, "http://localhost:"+strconv.Itoa(int(dev.port))+"/next?a=1", http.StatusFound)
		case "/elsewhere":
			http.Redirect(response, request, "https://example.com/", http.StatusFound)
		default:
			response.Header().Add("Set-Cookie", productionCookieName+"=tossed; Path=/; Secure")
			response.Header().Add("Set-Cookie", productionPreviewCookie+"=tossed; Path=/; Secure")
			response.Header().Add("Set-Cookie", "app=2; Path=/")
			_, _ = io.WriteString(response, "dev site "+request.URL.Path)
		}
	}))
	t.Cleanup(dev.Close)
	_, port, _ := net.SplitHostPort(dev.Listener.Addr().String())
	parsed, _ := strconv.Atoi(port)
	dev.port = uint16(parsed)
	return dev
}

func (dev *devServer) request() *http.Request {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.last
}

type previewFixture struct {
	broker  *fakeBroker
	main    *gatewayHandler
	preview *previewHandler
	server  *httptest.Server
	dev     *devServer
}

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	broker := newFakeBroker()
	main := newTestHandler(t, broker)
	preview, err := newPreviewHandler(main, ProductionPreviewListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preview.stop)
	server := httptest.NewServer(preview)
	t.Cleanup(server.Close)
	dev := newDevServer(t)
	broker.setPreview(dev.port, "127.0.0.1")
	return &previewFixture{broker: broker, main: main, preview: preview, server: server, dev: dev}
}

func (fixture *previewFixture) selection() string {
	return productionPreviewCookie + "=" + uuidB + "." + strconv.Itoa(int(fixture.dev.port))
}

// get sends a request to the preview listener as the browser would through
// Tailscale Serve on :8443. Redirects are not followed.
func (fixture *previewFixture) get(t *testing.T, target string, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, fixture.server.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = previewHost
	for name, values := range header {
		request.Header[name] = values
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func cookieHeader(values ...string) http.Header {
	return http.Header{"Cookie": {strings.Join(values, "; ")}}
}

func body(t *testing.T, response *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPreviewRequiresThePairedCookie(t *testing.T) {
	fixture := newPreviewFixture(t)
	port := strconv.Itoa(int(fixture.dev.port))
	selectPath := previewSelectPath + "?session=" + uuidB + "&port=" + port
	for name, header := range map[string]http.Header{
		"no cookie":      nil,
		"malformed":      cookieHeader(productionCookieName + "=short"),
		"unknown":        cookieHeader(productionCookieName + "=" + strings.Repeat("Z", 43)),
		"revoked":        cookieHeader(productionCookieName + "=" + revokedCredential),
		"selection only": cookieHeader(fixture.selection()),
	} {
		if response := fixture.get(t, selectPath, header); response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s select = %d", name, response.StatusCode)
		}
		withSelection := header.Clone()
		if withSelection == nil {
			withSelection = http.Header{}
		}
		withSelection.Add("Cookie", fixture.selection())
		if response := fixture.get(t, "/", withSelection); response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s / = %d", name, response.StatusCode)
		}
	}
	if request := fixture.dev.request(); request != nil {
		t.Fatalf("an unauthenticated request reached the dev server: %s", request.URL)
	}
}

func TestPreviewSelectAllowsOnlyOwnedPorts(t *testing.T) {
	fixture := newPreviewFixture(t)
	auth := cookieHeader(prodCookie)
	port := strconv.Itoa(int(fixture.dev.port))
	response := fixture.get(t, previewSelectPath+"?session="+uuidB+"&port="+port, auth)
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("select = %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != productionPreviewCookie || cookies[0].Value != uuidB+"."+port ||
		!cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("selection cookie = %#v", cookies)
	}
	for target, want := range map[string]int{
		previewSelectPath + "?session=" + uuidB + "&port=5432":             http.StatusNotFound, // not in the session
		previewSelectPath + "?session=" + uuidA + "&port=" + port:          http.StatusNotFound, // another session
		previewSelectPath + "?session=" + uuidB + "&port=0":                http.StatusBadRequest,
		previewSelectPath + "?session=" + uuidB + "&port=070000":           http.StatusBadRequest,
		previewSelectPath + "?session=" + uuidB + "&port=" + port + "&x=1": http.StatusBadRequest,
		previewSelectPath + "?session=nope&port=" + port:                   http.StatusBadRequest,
		"/__termivoa/other": http.StatusNotFound,
	} {
		response := fixture.get(t, target, auth)
		if response.StatusCode != want || len(response.Cookies()) != 0 {
			t.Errorf("%s = %d (cookies %v), want %d", target, response.StatusCode, response.Cookies(), want)
		}
	}
	// A stale selection of a port that closed is refused on the next request.
	fixture.broker.setPreview(fixture.dev.port, "")
	if response := fixture.get(t, "/", cookieHeader(prodCookie, fixture.selection())); response.StatusCode != http.StatusNotFound {
		t.Fatalf("closed port = %d", response.StatusCode)
	}
}

func TestPreviewProxyRewritesHostCookiesAndRedirects(t *testing.T) {
	fixture := newPreviewFixture(t)
	header := cookieHeader("app=1", prodCookie, fixture.selection(), developmentCookieName+"=x", "theme=dark")
	header.Set("X-Pterm-Request", "1")
	header.Set("Tailscale-User-Login", "owner@example.com")
	header.Set("Origin", "https://"+previewHost)
	header.Set("Referer", "https://"+previewHost+"/page?q=1")
	response := fixture.get(t, "/api/v1/settings?x=1", header)
	if response.StatusCode != http.StatusOK || body(t, response) != "dev site /api/v1/settings" {
		t.Fatalf("proxied = %d", response.StatusCode)
	}
	forwarded := fixture.dev.request()
	port := strconv.Itoa(int(fixture.dev.port))
	if forwarded.Host != "localhost:"+port || forwarded.URL.RawQuery != "x=1" {
		t.Fatalf("host %q query %q", forwarded.Host, forwarded.URL.RawQuery)
	}
	if cookie := forwarded.Header.Get("Cookie"); cookie != "app=1; theme=dark" {
		t.Fatalf("forwarded cookie %q", cookie)
	}
	for _, name := range []string{"X-Pterm-Request", "Tailscale-User-Login", "X-Forwarded-For", "X-Forwarded-Host"} {
		if value := forwarded.Header.Get(name); value != "" {
			t.Errorf("forwarded %s = %q", name, value)
		}
	}
	if origin, referer := forwarded.Header.Get("Origin"), forwarded.Header.Get("Referer"); origin != "http://localhost:"+port ||
		referer != "http://localhost:"+port+"/page?q=1" {
		t.Fatalf("origin %q referer %q", origin, referer)
	}
	cookies := response.Header.Values("Set-Cookie")
	if len(cookies) != 1 || !strings.HasPrefix(cookies[0], "app=2") {
		t.Fatalf("dev server may set only its own cookies: %q", cookies)
	}

	auth := cookieHeader(prodCookie, fixture.selection())
	if location := fixture.get(t, "/redirect", auth).Header.Get("Location"); location != "https://"+previewHost+"/next?a=1" {
		t.Fatalf("self redirect Location = %q", location)
	}
	if location := fixture.get(t, "/elsewhere", auth).Header.Get("Location"); location != "https://example.com/" {
		t.Fatalf("other-host redirect Location = %q", location)
	}
}

func TestPreviewRejectsForeignHostsAndOrigins(t *testing.T) {
	fixture := newPreviewFixture(t)
	auth := cookieHeader(prodCookie, fixture.selection())
	for _, origin := range []string{testOrigin, "https://evil.example", "null", "http://" + previewHost} {
		header := auth.Clone()
		header.Set("Origin", origin)
		if response := fixture.get(t, "/", header); response.StatusCode != http.StatusForbidden {
			t.Errorf("origin %s = %d", origin, response.StatusCode)
		}
	}
	for _, host := range []string{testHost, "evil.example:8443", testHost + ":8444", "127.0.0.1:8787"} {
		request, _ := http.NewRequest(http.MethodGet, fixture.server.URL+"/", nil)
		request.Host = host
		request.Header = auth.Clone()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("host %s = %d", host, response.StatusCode)
		}
	}
	// Without a selection the listener shows a short text, never the app.
	response := fixture.get(t, "/", cookieHeader(prodCookie))
	if text := body(t, response); response.StatusCode != http.StatusBadRequest || strings.Contains(text, "<title>app") {
		t.Fatalf("no selection = %d %q", response.StatusCode, text)
	}
}

func TestPreviewProxiesWebSocketsAndClosesThemOnRevocation(t *testing.T) {
	previous := previewRecheckEvery
	previewRecheckEvery = 20 * time.Millisecond
	t.Cleanup(func() { previewRecheckEvery = previous })
	fixture := newPreviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := cookieHeader(prodCookie, fixture.selection())
	header.Set("Origin", "https://"+previewHost)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/hmr",
		&websocket.DialOptions{HTTPHeader: header, Host: previewHost})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.Read(ctx); err != nil || string(data) != "ping" {
		t.Fatalf("echo = %q, %v", data, err)
	}
	if forwarded := fixture.dev.request(); forwarded.Host != "localhost:"+strconv.Itoa(int(fixture.dev.port)) ||
		forwarded.Header.Get("Cookie") != "" {
		t.Fatalf("upgrade forwarded host %q cookie %q", forwarded.Host, forwarded.Header.Get("Cookie"))
	}
	fixture.broker.setPreview(fixture.dev.port, "") // the port closed or access was revoked
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("socket stayed open after the recheck failed: %v", err)
	}
}

// The main app refuses every request from the preview origin, which shares
// its host name and so its cookie.
func TestMainOriginRejectsPreviewOrigin(t *testing.T) {
	fixture := newPreviewFixture(t)
	for _, origin := range []string{"https://" + previewHost, "HTTPS://MAC.TAILNET.TS.NET:8443", "http://127.0.0.1:8789", "http://localhost:8789"} {
		options := requestOptions{origin: origin, cookie: prodCookie}
		for _, request := range []struct{ method, target, body string }{
			{http.MethodGet, "/api/v1/sessions", ""},
			{http.MethodGet, "/api/v1/settings", ""},
			{http.MethodPost, "/api/v1/logout", "{}"},
			{http.MethodGet, "/", ""},
		} {
			if response := do(fixture.main, request.method, request.target, request.body, options); response.Code != http.StatusForbidden {
				t.Errorf("%s %s from %s = %d", request.method, request.target, origin, response.Code)
			}
		}
		upgrade := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/ws", nil)
		upgrade.Host = testHost
		upgrade.Header.Set("Origin", origin)
		upgrade.Header.Set("Cookie", prodCookie)
		upgrade.Header.Set("Connection", "Upgrade")
		upgrade.Header.Set("Upgrade", "websocket")
		upgrade.Header.Set("Sec-WebSocket-Version", "13")
		upgrade.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		recorder := httptest.NewRecorder()
		fixture.main.ServeHTTP(recorder, upgrade)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("WebSocket from %s = %d", origin, recorder.Code)
		}
	}
	if fixture.broker.called("open") || fixture.broker.called("logout") || fixture.broker.called("list") {
		t.Fatalf("a preview-origin request reached the broker: %v", fixture.broker.calls)
	}
	if response := do(fixture.main, http.MethodGet, "/api/v1/sessions", "", requestOptions{cookie: prodCookie}); response.Code != http.StatusOK {
		t.Fatalf("app origin = %d", response.Code)
	}
}

func TestAppCSPFramesOnlyThePreviewOrigin(t *testing.T) {
	fixture := newPreviewFixture(t)
	for host, want := range map[string]string{
		testHost:         "frame-src https://" + previewHost,
		"127.0.0.1:8787": "frame-src http://127.0.0.1:8789",
		"localhost:8787": "frame-src http://localhost:8789",
	} {
		policy := do(fixture.main, http.MethodGet, "/", "", requestOptions{host: host, omitOrigin: true}).Header().Get("Content-Security-Policy")
		if !strings.HasSuffix(policy, "; "+want) || strings.Count(policy, "frame-src") != 1 || !strings.Contains(policy, "frame-ancestors 'none'") {
			t.Errorf("%s CSP = %q", host, policy)
		}
	}
	plain := newTestHandler(t, newFakeBroker())
	if policy := do(plain, http.MethodGet, "/", "", requestOptions{omitOrigin: true}).Header().Get("Content-Security-Policy"); strings.Contains(policy, "frame-src") {
		t.Fatalf("CSP without previews = %q", policy)
	}
}

func TestPreviewConfigEndpoint(t *testing.T) {
	fixture := newPreviewFixture(t)
	response := do(fixture.main, http.MethodGet, "/api/v1/preview", "", requestOptions{cookie: prodCookie})
	var payload struct{ Origin string }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK || payload.Origin != "https://"+previewHost {
		t.Fatalf("preview config = %d %s", response.Code, response.Body)
	}
	if response := do(fixture.main, http.MethodGet, "/api/v1/preview", "", requestOptions{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("preview config without cookie = %d", response.Code)
	}
	plain := newTestHandler(t, newFakeBroker())
	if response := do(plain, http.MethodGet, "/api/v1/preview", "", requestOptions{cookie: prodCookie}); strings.TrimSpace(response.Body.String()) != "{}" {
		t.Fatalf("preview config without previews = %s", response.Body)
	}
}

func TestPreviewListenAddressRules(t *testing.T) {
	for _, test := range []struct {
		dev     bool
		address string
		ok      bool
	}{
		{false, ProductionPreviewListenAddress, true},
		{false, "127.0.0.1:9999", false},
		{true, DevelopmentPreviewListenAddress, true},
		{true, "127.0.0.1:0", true},
		{true, ProductionPreviewListenAddress, false},
		{true, ProductionListenAddress, false},
		{true, "0.0.0.0:8790", false},
		{true, "localhost:8790", false},
		{true, "[::1]:8790", false},
	} {
		profile := testProfile(test.dev)
		if err := ValidatePreviewListenAddress(profile, test.address); (err == nil) != test.ok {
			t.Errorf("dev=%v %s: %v", test.dev, test.address, err)
		}
	}
}

func testProfile(dev bool) platform.Profile {
	if dev {
		return platform.DevelopmentProfile
	}
	return platform.ProductionProfile
}

// Run serves the preview listener on its own loopback address; it answers
// only preview requests, never the app.
func TestRunServesTheSeparatePreviewListener(t *testing.T) {
	runtimeDir := shortGatewayRuntimeDir(t)
	brokerCtx, cancelBroker := context.WithCancel(context.Background())
	listener, err := ipc.ListenBroker(brokerCtx, ipc.ListenerConfig{RuntimeDir: runtimeDir, BrokerGeneration: gatewayTestGeneration})
	if err != nil {
		t.Fatal(err)
	}
	brokerDone := serveHandshakes(brokerCtx, listener)
	t.Cleanup(func() {
		cancelBroker()
		_ = listener.Close()
		<-brokerDone
	})
	gatewayCtx, cancelGateway := context.WithCancel(context.Background())
	ready := make(chan ReadyState, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(gatewayCtx, Config{
			RuntimeDir: runtimeDir, HostBuild: "gateway-test", ListenAddress: "127.0.0.1:0",
			PreviewListenAddress: "127.0.0.1:0", Profile: platform.DevelopmentProfile, Broker: newFakeBroker(),
			Ready: func(state ReadyState) { ready <- state },
		})
	}()
	state := waitReady(t, ready)
	if state.PreviewAddress == "" || state.PreviewAddress == state.Address {
		t.Fatalf("preview address = %q (app %q)", state.PreviewAddress, state.Address)
	}
	for _, path := range []string{"/", "/healthz", "/api/v1/settings", "/index.html"} {
		response, err := http.Get("http://" + state.PreviewAddress + path)
		if err != nil {
			t.Fatal(err)
		}
		text := body(t, response)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized || strings.Contains(text, "<") {
			t.Errorf("preview %s = %d %q", path, response.StatusCode, text)
		}
	}
	cancelGateway()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get("http://" + state.PreviewAddress + "/"); err == nil {
		t.Fatal("preview listener still serves after shutdown")
	}
}

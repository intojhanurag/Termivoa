package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
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
		case "/cookies":
			// Sets exactly the Set-Cookie lines the test asks for, then one more.
			response.Header()["Set-Cookie"] = append(request.Header.Values("X-Dev-Set-Cookie"), "app=2; Path=/")
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
// Tailscale Serve on :8443, from a page of the preview origin itself
// (Sec-Fetch-Site: same-origin; a nil header value removes it). Redirects are
// not followed.
func (fixture *previewFixture) get(t *testing.T, target string, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, fixture.server.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = previewHost
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for name, values := range header {
		if values == nil {
			request.Header.Del(name)
			continue
		}
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

// framedBy is the selection as a browser sends it from the app's frame
// (referrerpolicy="origin"): a same-site navigation without Origin.
func framedBy(app string, header http.Header) http.Header {
	header = header.Clone()
	if header == nil {
		header = http.Header{}
	}
	header["Sec-Fetch-Site"] = []string{"same-site"}
	header["Referer"] = []string{app + "/"}
	return header
}

func (fixture *previewFixture) selectPath() string {
	return previewSelectPath + "?session=" + uuidB + "&port=" + strconv.Itoa(int(fixture.dev.port))
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
		if response := fixture.get(t, selectPath, framedBy(testOrigin, header)); response.StatusCode != http.StatusUnauthorized {
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
	auth := framedBy(testOrigin, cookieHeader(prodCookie))
	port := strconv.Itoa(int(fixture.dev.port))
	response := fixture.get(t, fixture.selectPath(), auth)
	// A page of its own, not a redirect and not previewPage: it moves the
	// frame to / from the preview origin, and only the selecting app may
	// frame it.
	header := response.Header
	if text := body(t, response); response.StatusCode != http.StatusOK || text != previewSelectedPage ||
		!strings.Contains(text, `<meta http-equiv="refresh" content="0;url=/">`) || strings.Contains(text, "<script") ||
		header.Get("Location") != "" || header.Get("Content-Type") != "text/html; charset=utf-8" ||
		header.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors "+testOrigin ||
		header.Get("Referrer-Policy") != "no-referrer" || header.Get("Cache-Control") != "no-store" ||
		header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("select = %d %q %v", response.StatusCode, text, header)
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
	} {
		response := fixture.get(t, target, auth)
		if response.StatusCode != want || len(response.Cookies()) != 0 {
			t.Errorf("%s = %d (cookies %v), want %d", target, response.StatusCode, response.Cookies(), want)
		}
	}
	if response := fixture.get(t, "/__termivoa/other", cookieHeader(prodCookie)); response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown reserved path = %d", response.StatusCode)
	}
	// A stale selection of a port that closed is refused on the next request.
	fixture.broker.setPreview(fixture.dev.port, "")
	response = fixture.get(t, "/", cookieHeader(prodCookie, fixture.selection()))
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("closed port = %d", response.StatusCode)
	}
	// The listener's text pages must show inside the app's frame (#144).
	if policy := response.Header.Get("Content-Security-Policy"); policy != "default-src 'none'" ||
		response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("text page: CSP %q, type %q", policy, response.Header.Get("Content-Type"))
	}
}

// Rule L1 (docs/peer-preview.md): only the preview origin itself may send a
// request other than the selection. Refusals come before the cookie and
// before the dev server.
func TestPreviewRequestsOnlyFromThePreviewOrigin(t *testing.T) {
	fixture := newPreviewFixture(t)
	own := "https://" + previewHost
	for _, test := range []struct {
		name   string
		header http.Header
		want   int
	}{
		{"own Origin, no Sec-Fetch (WebSocket handshake, Chrome)", http.Header{"Origin": {own}, "Sec-Fetch-Site": nil}, http.StatusOK},
		{"own Origin, upper case", http.Header{"Origin": {"HTTPS://MAC.TAILNET.TS.NET:8443"}, "Sec-Fetch-Site": nil}, http.StatusOK},
		{"no Origin, same-origin", nil, http.StatusOK},
		{"no Origin, same-site", http.Header{"Sec-Fetch-Site": {"same-site"}}, http.StatusForbidden},
		{"no Origin, cross-site", http.Header{"Sec-Fetch-Site": {"cross-site"}}, http.StatusForbidden},
		{"no Origin, none (typed URL)", http.Header{"Sec-Fetch-Site": {"none"}}, http.StatusForbidden},
		{"no Origin, no Sec-Fetch (curl)", http.Header{"Sec-Fetch-Site": nil}, http.StatusForbidden},
		{"no Origin, two Sec-Fetch-Site", http.Header{"Sec-Fetch-Site": {"same-origin", "same-origin"}}, http.StatusForbidden},
		{"app Origin, same-origin", http.Header{"Origin": {testOrigin}}, http.StatusForbidden},
		{"bound app Origin", http.Header{"Origin": {homeOrigin}}, http.StatusForbidden},
		{"other preview Origin", http.Header{"Origin": {"https://vps.tailx.ts.net:8443"}}, http.StatusForbidden},
		{"null Origin", http.Header{"Origin": {"null"}}, http.StatusForbidden},
		{"two own Origins", http.Header{"Origin": {own, own}}, http.StatusForbidden},
		{"app Referer does not replace L1", framedBy(testOrigin, nil), http.StatusForbidden},
	} {
		header := cookieHeader(prodCookie, fixture.selection())
		for name, values := range test.header {
			header[name] = values
		}
		fixture.dev.mu.Lock()
		fixture.dev.last = nil
		fixture.dev.mu.Unlock()
		response := fixture.get(t, "/page", header)
		if response.StatusCode != test.want {
			t.Errorf("%s = %d, want %d", test.name, response.StatusCode, test.want)
		}
		if reached := fixture.dev.request() != nil; reached != (test.want == http.StatusOK) {
			t.Errorf("%s: reached the dev server = %v", test.name, reached)
		}
	}
	// Refused before the cookie: no 401, and no broker call.
	fixture.broker.mu.Lock()
	fixture.broker.calls = nil
	fixture.broker.mu.Unlock()
	if response := fixture.get(t, "/", http.Header{"Sec-Fetch-Site": {"same-site"}}); response.StatusCode != http.StatusForbidden || len(fixture.broker.calls) != 0 {
		t.Fatalf("foreign request without a cookie = %d, calls %v", response.StatusCode, fixture.broker.calls)
	}
}

// Rule L2: the selection needs exactly one Referer that names this computer's
// app or a bound app; a bound one reaches the broker as the forwarded origin.
// Matching is exact, as for CORS.
func TestPreviewSelectNeedsAnAppReferer(t *testing.T) {
	fixture := newPreviewFixture(t)
	fixture.main.mu.Lock()
	fixture.main.bound = originSet([]string{homeOrigin}, protocol.MaxBoundOrigins)
	fixture.main.pairing = originSet([]string{pairOrigin}, protocol.MaxPairingOrigins)
	fixture.main.mu.Unlock()
	call := "preview:" + uuidB + ":" + strconv.Itoa(int(fixture.dev.port)) + "@"
	for _, test := range []struct {
		name      string
		referer   []string
		forwarded string // "-" = refused before the broker
	}{
		{"own", []string{testOrigin + "/"}, ""},
		{"own, upper case (as /api accepts it)", []string{"HTTPS://MAC.TAILNET.TS.NET/"}, ""},
		{"bound", []string{homeOrigin + "/"}, homeOrigin},
		{"unbound", []string{"https://other.tailx.ts.net/"}, "-"},
		{"pairing origin", []string{pairOrigin + "/"}, "-"},
		{"missing", nil, "-"},
		{"empty", []string{""}, "-"},
		{"doubled", []string{testOrigin + "/", testOrigin + "/"}, "-"},
		{"own and bound", []string{testOrigin + "/", homeOrigin + "/"}, "-"},
		{"with a path", []string{testOrigin + "/sessions/1"}, "-"},
		{"with a path and a slash", []string{homeOrigin + "/sessions/"}, "-"},
		{"two slashes", []string{homeOrigin + "//"}, "-"},
		{"own without the trailing /", []string{testOrigin}, "-"},
		{"bound without the trailing /", []string{homeOrigin}, "-"},
		{"bound, upper case", []string{"https://HOME.tailx.ts.net/"}, "-"},
		{"own, explicit :443", []string{testOrigin + ":443/"}, "-"},
		{"bound, explicit :443", []string{homeOrigin + ":443/"}, "-"},
		{"bound, trailing dot", []string{homeOrigin + "./"}, "-"},
		{"bound, http", []string{"http://home.tailx.ts.net/"}, "-"},
		{"near miss", []string{homeOrigin + ".evil.com/"}, "-"},
		{"own preview origin", []string{"https://" + previewHost + "/"}, "-"},
		{"bound computer's preview origin", []string{homeOrigin + ":8443/"}, "-"},
		{"null", []string{"null"}, "-"},
		{"null with a slash", []string{"null/"}, "-"},
	} {
		fixture.broker.mu.Lock()
		fixture.broker.calls = nil
		fixture.broker.mu.Unlock()
		header := cookieHeader(prodCookie)
		header["Sec-Fetch-Site"] = []string{"same-site"}
		if test.referer != nil {
			header["Referer"] = test.referer
		}
		response := fixture.get(t, fixture.selectPath(), header)
		if test.forwarded == "-" {
			if response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 || len(fixture.broker.calls) != 0 ||
				response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Errorf("%s = %d, cookies %v, calls %v", test.name, response.StatusCode, response.Cookies(), fixture.broker.calls)
			}
			continue
		}
		want := strings.ToLower(strings.TrimSuffix(test.referer[0], "/"))
		if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || !fixture.broker.called(call+test.forwarded) || len(fixture.broker.calls) != 1 ||
			response.Header.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors "+want {
			t.Errorf("%s = %d, calls %v, CSP %q", test.name, response.StatusCode, fixture.broker.calls, response.Header.Get("Content-Security-Policy"))
		}
	}
	// The selection is a navigation: any Origin header refuses it, the preview
	// origin's own included.
	for _, origin := range []string{"https://" + previewHost, testOrigin, homeOrigin, "null"} {
		header := framedBy(homeOrigin, cookieHeader(prodCookie))
		header.Set("Origin", origin)
		if response := fixture.get(t, fixture.selectPath(), header); response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 {
			t.Errorf("select with Origin %s = %d", origin, response.StatusCode)
		}
	}
	// Only the selection forwards the origin: later requests and the recheck
	// carry none (the broker checks a bound device's trust on every call).
	fixture.broker.mu.Lock()
	fixture.broker.calls = nil
	fixture.broker.mu.Unlock()
	if response := fixture.get(t, "/", cookieHeader(prodCookie, fixture.selection())); response.StatusCode != http.StatusOK ||
		!fixture.broker.called(call) || len(fixture.broker.calls) != 1 {
		t.Fatalf("proxied request = %d, calls %v", response.StatusCode, fixture.broker.calls)
	}
}

// The app's reachability probe is a no-cors fetch of the selection path
// without credentials: it gets an answer, never a page or a cookie.
func TestPreviewProbeAnswersWithoutACookie(t *testing.T) {
	fixture := newPreviewFixture(t)
	for name, test := range map[string]struct {
		header http.Header
		want   int
	}{
		"no Referer (the app's policy is no-referrer)": {http.Header{"Sec-Fetch-Site": {"same-site"}, "Sec-Fetch-Mode": {"no-cors"}}, http.StatusForbidden},
		"app Referer": {framedBy(testOrigin, nil), http.StatusUnauthorized},
	} {
		response := fixture.get(t, previewSelectPath, test.header)
		if response.StatusCode != test.want || len(response.Cookies()) != 0 || response.Header.Get("Access-Control-Allow-Origin") != "" ||
			response.Header.Get("Content-Security-Policy") != "default-src 'none'" {
			t.Errorf("%s = %d %v", name, response.StatusCode, response.Header)
		}
	}
	if len(fixture.broker.calls) != 0 {
		t.Fatalf("the probe reached the broker: %v", fixture.broker.calls)
	}
}

// The broker refuses an app origin that is not the credential's own, and a
// bound device whose computer it no longer trusts, with invalid_request.
func TestPreviewShowsABrokerRefusalAsForbidden(t *testing.T) {
	fixture := newPreviewFixture(t)
	fixture.broker.mu.Lock()
	fixture.broker.previewErr = &gatewayapi.Error{Code: protocol.InvalidRequest, Message: "request origin is not allowed here"}
	fixture.broker.mu.Unlock()
	for target, header := range map[string]http.Header{
		fixture.selectPath(): framedBy(testOrigin, cookieHeader(prodCookie)),
		"/":                  cookieHeader(prodCookie, fixture.selection()),
	} {
		response := fixture.get(t, target, header)
		if text := body(t, response); response.StatusCode != http.StatusForbidden || text != "This app may not use this computer.\n" ||
			len(response.Cookies()) != 0 || response.Header.Get("Content-Security-Policy") != "default-src 'none'" {
			t.Errorf("%s = %d %q %v", target, response.StatusCode, text, response.Header)
		}
	}
	if fixture.dev.request() != nil {
		t.Fatal("a refused request reached the dev server")
	}
}

// protocol_mismatch is 400 on the API like invalid_request, but it is not a
// refusal of the app: the preview keeps the "not available" answer.
func TestPreviewShowsAProtocolMismatchAsUnavailable(t *testing.T) {
	fixture := newPreviewFixture(t)
	fixture.broker.mu.Lock()
	fixture.broker.previewErr = &gatewayapi.Error{Code: protocol.ProtocolMismatch, Message: "protocol mismatch"}
	fixture.broker.mu.Unlock()
	for target, header := range map[string]http.Header{
		fixture.selectPath(): framedBy(testOrigin, cookieHeader(prodCookie)),
		"/":                  cookieHeader(prodCookie, fixture.selection()),
	} {
		response := fixture.get(t, target, header)
		if text := body(t, response); response.StatusCode != http.StatusServiceUnavailable || text != "The computer is not available.\n" || len(response.Cookies()) != 0 {
			t.Errorf("%s = %d %q", target, response.StatusCode, text)
		}
	}
}

// withLines asks the dev server's /cookies path to set these Set-Cookie lines.
func withLines(fixture *previewFixture, lines ...string) http.Header {
	header := cookieHeader(prodCookie, fixture.selection())
	header["X-Dev-Set-Cookie"] = lines
	return header
}

// A dev server's Set-Cookie may not set a Termivoa cookie by any spelling a
// browser could read as one: an empty name with the name in the value, other
// letter case (the __Host- prefix is matched without case), a quoted name, or
// a second cookie folded into the line. Its own cookies pass byte for byte.
func TestPreviewDropsTermivoaSetCookieShapes(t *testing.T) {
	fixture := newPreviewFixture(t)
	hostile := map[string]string{
		"empty name":             "=__Host-pterm_preview=x; Path=/",
		"empty name session":     "=__Host-pterm_session=x; Path=/",
		"empty name dev":         "=pterm_dev_session=x",
		"empty name spaced":      "= __Host-pterm_session=x",
		"empty name upper":       "=__HOST-PTERM_SESSION=x",
		"empty name quoted":      `="__Host-pterm_session"=x`,
		"upper prefix":           "__HOST-pterm_session=x; Path=/",
		"lower":                  "__host-pterm_preview=x",
		"upper dev":              "PTERM_DEV_SESSION=x",
		"quoted name":            `"__Host-pterm_session"=x`,
		"space before name":      " __Host-pterm_session=x",
		"tab before name":        "\t__Host-pterm_session=x",
		"space before equals":    "__Host-pterm_session =x",
		"folded after semicolon": "app=3; __Host-pterm_session=x",
		"folded after comma":     "app=3, __Host-pterm_session=x",
		"folded empty name":      "app=3, =__Host-pterm_session=x",
	}
	for name, line := range hostile {
		cookies := fixture.get(t, "/cookies", withLines(fixture, line)).Header.Values("Set-Cookie")
		if len(cookies) != 1 || cookies[0] != "app=2; Path=/" {
			t.Errorf("%s: %q passed through: %q", name, line, cookies)
		}
	}
	own := []string{"app=1; Path=/; HttpOnly", "theme=dark; Max-Age=60", "a=__Host-pterm_session", "session=1, 2", "=; Path=/", "x=y=z"}
	response := fixture.get(t, "/cookies", withLines(fixture, own...))
	if cookies := response.Header.Values("Set-Cookie"); !slices.Equal(cookies, append(own, "app=2; Path=/")) {
		t.Errorf("dev cookies changed: %q", cookies)
	}
}

// The same shapes in the browser's Cookie header never reach the dev server,
// and the dev site's own cookies arrive byte for byte.
func TestPreviewDoesNotForwardTermivoaCookieShapes(t *testing.T) {
	fixture := newPreviewFixture(t)
	hostile := map[string]string{
		"empty name":         "=__Host-pterm_session=x",
		"empty name preview": "=__Host-pterm_preview=x",
		"empty name dev":     "=pterm_dev_session=x",
		"empty name spaced":  "= __Host-pterm_session=x",
		"empty name upper":   "=__HOST-PTERM_SESSION=x",
		"empty name quoted":  `="__Host-pterm_session"=x`,
		"upper prefix":       "__HOST-pterm_session=x",
		"upper dev":          "PTERM_DEV_PREVIEW=x",
		"quoted name":        `"__Host-pterm_session"=x`,
		"tab before name":    "\tpterm_dev_session=x",
		"folded comma":       "app=3, __Host-pterm_session=x",
		"folded semicolons":  "pterm_dev_session=x; =__Host-pterm_preview=y; pterm_dev_preview=z",
	}
	for name, pairs := range hostile {
		if response := fixture.get(t, "/", cookieHeader("app=1", prodCookie, fixture.selection(), pairs, "theme=dark")); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", name, response.StatusCode)
		}
		if cookie := fixture.dev.request().Header.Get("Cookie"); cookie != "app=1; theme=dark" {
			t.Errorf("%s: %q forwarded as %q", name, pairs, cookie)
		}
	}
	fixture.get(t, "/", cookieHeader(prodCookie, fixture.selection(), "app=1", "a=__Host-pterm_session", "x=y=z", "session=1,2"))
	if cookie := fixture.dev.request().Header.Get("Cookie"); cookie != "app=1; a=__Host-pterm_session; x=y=z; session=1,2" {
		t.Errorf("dev cookies changed: %q", cookie)
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
	fixture.broker.setPreview(fixture.dev.port, "") // the port closed, access was revoked, or trust in the app's computer ended
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("socket stayed open after the recheck failed: %v", err)
	}
	// The handshake above had only Origin (Chrome sends no Sec-Fetch headers
	// on it). Without the preview origin as Origin there is no socket.
	fixture.broker.setPreview(fixture.dev.port, "127.0.0.1")
	for name, origin := range map[string]string{"no Origin": "", "app Origin": testOrigin, "other preview Origin": "https://vps.tailx.ts.net:8443"} {
		header := cookieHeader(prodCookie, fixture.selection())
		if origin != "" {
			header.Set("Origin", origin)
		}
		_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/hmr",
			&websocket.DialOptions{HTTPHeader: header, Host: previewHost})
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Errorf("WebSocket with %s: %v, %v", name, response, err)
		}
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
		// The same origin, and only it, is in connect-src for the app's probe.
		origin := strings.TrimPrefix(want, "frame-src ")
		if !strings.HasSuffix(policy, "; "+want) || strings.Count(policy, "frame-src") != 1 || !strings.Contains(policy, "frame-ancestors 'none'") ||
			!strings.Contains(policy, "; connect-src 'self' "+origin+";") || strings.Contains(policy, "wss:") {
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
		request, _ := http.NewRequest(http.MethodGet, "http://"+state.PreviewAddress+path, nil)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := http.DefaultClient.Do(request)
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

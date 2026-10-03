package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	homeOrigin = "https://home.tailx.ts.net"
	pairOrigin = "https://laptop.tailx.ts.net"
)

// fakeComputers is a broker with the multi-computer RPCs. It records each
// call with the Origin the gateway forwarded ("call@origin"; own origin
// leaves it empty).
type fakeComputers struct {
	*fakeBroker
	probe   protocol.GatewayRPCOriginResultPayload
	list    protocol.GatewayRPCComputerListResultPayload
	expires time.Time
	added   []protocol.HTTPComputerAddRequest
	opErr   error
	approve bool // PollPairing approves and binds the forwarded origin.
}

func newFakeComputers() *fakeComputers {
	return &fakeComputers{
		fakeBroker: newFakeBroker(),
		probe: protocol.GatewayRPCOriginResultPayload{
			BoundOrigins: []string{homeOrigin, "https://xn--mnchen-3ya.tailx.ts.net"}, PairingOrigins: []string{pairOrigin},
		},
		list:    protocol.GatewayRPCComputerListResultPayload{Computers: []protocol.HTTPComputer{}, PendingOrigins: []string{}},
		expires: time.Now().Add(10 * 24 * time.Hour),
	}
}

func (broker *fakeComputers) saw(ctx context.Context, call string) {
	origin, _ := gatewayapi.ForwardedOrigin(ctx)
	broker.record(call + "@" + origin)
}

func (broker *fakeComputers) OriginProbe(context.Context) (protocol.GatewayRPCOriginResultPayload, error) {
	return broker.probe, nil
}

func (broker *fakeComputers) ListComputers(ctx context.Context, credential string) (protocol.GatewayRPCComputerListResultPayload, error) {
	broker.saw(ctx, "computers")
	return broker.list, broker.authorize(credential)
}

func (broker *fakeComputers) AddComputer(ctx context.Context, credential string, request protocol.HTTPComputerAddRequest) (protocol.HTTPComputerAddResponse, error) {
	broker.saw(ctx, "add-computer")
	if err := broker.authorize(credential); err != nil {
		return protocol.HTTPComputerAddResponse{}, err
	}
	broker.added = append(broker.added, request)
	return protocol.HTTPComputerAddResponse{
		Computer: protocol.HTTPComputer{Origin: request.Origin, Label: request.Label}, State: request.State,
		Node: protocol.TailnetNode{ID: "nVPS1", Tags: []string{}},
	}, broker.opErr
}

func (broker *fakeComputers) RemoveComputer(ctx context.Context, credential, origin string) error {
	broker.saw(ctx, "remove-computer:"+origin)
	if err := broker.authorize(credential); err != nil {
		return err
	}
	return broker.opErr
}

func (broker *fakeComputers) Authenticate(ctx context.Context, credential string) (gatewayapi.Principal, error) {
	broker.saw(ctx, "authenticate")
	return gatewayapi.Principal{DeviceID: uuidA, DeviceLabel: "phone", CredentialID: uuidB, ExpiresAt: broker.expires},
		broker.authorize(credential)
}

func (broker *fakeComputers) Settings(ctx context.Context, credential string) (protocol.HTTPSettingsResponse, error) {
	broker.saw(ctx, "settings")
	return broker.fakeBroker.Settings(ctx, credential)
}

func (broker *fakeComputers) RedeemPairing(ctx context.Context, request protocol.HTTPPairRedeemRequest) (protocol.HTTPPairRedeemResponse, error) {
	broker.saw(ctx, "redeem")
	return broker.fakeBroker.RedeemPairing(ctx, request)
}

func (broker *fakeComputers) PollPairing(ctx context.Context, request protocol.HTTPPairPollRequest) (gatewayapi.PollResult, error) {
	broker.saw(ctx, "poll")
	if !broker.approve {
		return gatewayapi.PollResult{Response: protocol.HTTPPairPollResponse{Status: protocol.HTTPPairPollStatusPending}}, nil
	}
	// Like the broker: the approved poll creates a device bound to the origin.
	origin, _ := gatewayapi.ForwardedOrigin(ctx)
	broker.probe.BoundOrigins = append(broker.probe.BoundOrigins, origin)
	return gatewayapi.PollResult{
		Response:   protocol.HTTPPairPollResponse{Status: protocol.Approved, Device: &protocol.HTTPDeviceSummary{ID: uuidA, Label: "phone"}},
		Credential: validCredential, CredentialExpiresAt: broker.expires,
	}, nil
}

func (broker *fakeComputers) OpenLive(ctx context.Context, credential string) (gatewayapi.LiveStream, error) {
	broker.saw(ctx, "open")
	return broker.fakeBroker.OpenLive(ctx, credential)
}

// newPeerHandler is a production peer whose probe already ran.
func newPeerHandler(t *testing.T, broker *fakeComputers) *gatewayHandler {
	t.Helper()
	handler := newTestHandler(t, broker)
	handler.refreshOrigin(context.Background())
	return handler
}

func crossSite(origin string) requestOptions {
	return requestOptions{origin: origin, cookie: prodCookie,
		header: http.Header{"Sec-Fetch-Site": {"same-site"}, "Sec-Fetch-Mode": {"cors"}}}
}

func TestCORSOnlyForExactBoundOrigins(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	for _, test := range []struct {
		name, origin string
		want         int
	}{
		{"bound", homeOrigin, http.StatusOK},
		{"own", testOrigin, http.StatusOK},
		{"unbound", "https://other.tailx.ts.net", http.StatusForbidden},
		{"pairing origin off pair routes", pairOrigin, http.StatusForbidden},
		{"null", "null", http.StatusForbidden},
		{"uppercase", "https://HOME.tailx.ts.net", http.StatusForbidden},
		{"explicit port", homeOrigin + ":443", http.StatusForbidden},
		{"trailing dot", homeOrigin + ".", http.StatusForbidden},
		{"http", "http://home.tailx.ts.net", http.StatusForbidden},
		{"prefix", "https://xhome.tailx.ts.net", http.StatusForbidden},
		{"suffix", homeOrigin + ".evil.com", http.StatusForbidden},
		{"path", homeOrigin + "/", http.StatusForbidden},
		{"control", homeOrigin + "\t", http.StatusForbidden},
		{"punycode near miss", "https://xn--hme-0na.tailx.ts.net", http.StatusForbidden},
		{"bound punycode", "https://xn--mnchen-3ya.tailx.ts.net", http.StatusOK},
	} {
		response := do(handler, http.MethodGet, "/api/v1/sessions", "", crossSite(test.origin))
		allow := response.Header().Get("Access-Control-Allow-Origin")
		if response.Code != test.want {
			t.Errorf("%s: status %d, want %d", test.name, response.Code, test.want)
		}
		if test.want == http.StatusOK && test.name != "own" {
			if allow != test.origin || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Errorf("bound: CORS headers %v", response.Header())
			}
		} else if allow != "" || response.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: CORS headers sent: %v", test.name, response.Header())
		}
		if !strings.Contains(strings.Join(response.Header().Values("Vary"), ","), "Origin") {
			t.Errorf("%s: no Vary: Origin", test.name)
		}
	}
	// The bound origin reaches the broker; the own origin is never forwarded.
	do(handler, http.MethodGet, "/api/v1/settings", "", crossSite(testOrigin))
	if !broker.called("settings@") || broker.called("settings@"+testOrigin) {
		t.Fatalf("calls = %v", broker.calls)
	}
	do(handler, http.MethodGet, "/api/v1/settings", "", crossSite(homeOrigin))
	if !broker.called("authenticate@"+homeOrigin) || !broker.called("settings@"+homeOrigin) {
		t.Fatalf("bound origin was not forwarded: %v", broker.calls)
	}
	// A mutation from the bound origin passes the exact-origin boundary.
	if response := do(handler, http.MethodPost, "/api/v1/logout", `{}`, crossSite(homeOrigin)); response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Origin") != homeOrigin {
		t.Fatalf("bound logout = %d %v", response.Code, response.Header())
	}
	// The union is replaced on every probe: a removed origin loses CORS.
	broker.probe = protocol.GatewayRPCOriginResultPayload{}
	handler.refreshOrigin(context.Background())
	if response := do(handler, http.MethodGet, "/api/v1/sessions", "", crossSite(homeOrigin)); response.Code != http.StatusForbidden ||
		response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unbound after probe = %d %v", response.Code, response.Header())
	}
}

func TestPreflight(t *testing.T) {
	handler := newPeerHandler(t, newFakeComputers())
	preflight := func(target, origin string) *httptest.ResponseRecorder {
		return do(handler, http.MethodOptions, target, "", requestOptions{origin: origin, omitHeader: true, header: http.Header{
			"Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"content-type,x-pterm-request"},
			"Sec-Fetch-Mode": {"cors"}, "Sec-Fetch-Site": {"same-site"},
		}})
	}
	for _, test := range []struct {
		name, target, origin string
		answered             bool
	}{
		{"bound on API", "/api/v1/sessions", homeOrigin, true},
		{"bound on pairing", "/api/v1/pair/redeem", homeOrigin, true},
		{"pairing origin on pairing", "/api/v1/pair/redeem", pairOrigin, true},
		{"pairing origin on poll", "/api/v1/pair/poll", pairOrigin, true},
		{"pairing origin elsewhere", "/api/v1/sessions", pairOrigin, false},
		{"pairing origin on settings", "/api/v1/settings", pairOrigin, false},
		{"unbound", "/api/v1/pair/redeem", "https://other.tailx.ts.net", false},
		{"null", "/api/v1/pair/redeem", "null", false},
		{"own", "/api/v1/sessions", testOrigin, false},
		{"static", "/", homeOrigin, false},
	} {
		response := preflight(test.target, test.origin)
		header := response.Header()
		if !test.answered {
			if response.Code < 400 || header.Get("Access-Control-Allow-Origin") != "" || header.Get("Access-Control-Allow-Methods") != "" {
				t.Errorf("%s: answered %d %v", test.name, response.Code, header)
			}
			continue
		}
		if response.Code != http.StatusNoContent || header.Get("Access-Control-Allow-Origin") != test.origin ||
			header.Get("Access-Control-Allow-Credentials") != "true" ||
			header.Get("Access-Control-Allow-Methods") != "GET, POST, PATCH, DELETE" ||
			header.Get("Access-Control-Allow-Headers") != "Content-Type, X-Pterm-Request" ||
			header.Get("Access-Control-Max-Age") != "600" {
			t.Errorf("%s: preflight %d %v", test.name, response.Code, header)
		}
	}
}

func TestPairingOriginsOnPairRoutesOnly(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	options := crossSite(pairOrigin)
	options.cookie = ""
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, options); response.Code != http.StatusOK ||
		response.Header().Get("Access-Control-Allow-Origin") != pairOrigin {
		t.Fatalf("redeem = %d %v %s", response.Code, response.Header(), response.Body)
	}
	if response := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, options); response.Code != http.StatusOK {
		t.Fatalf("poll = %d %s", response.Code, response.Body)
	}
	if !broker.called("redeem@"+pairOrigin) || !broker.called("poll@"+pairOrigin) {
		t.Fatalf("pairing origin not forwarded: %v", broker.calls)
	}
	// Own-origin pairing forwards nothing.
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{}); response.Code != http.StatusOK || !broker.called("redeem@") {
		t.Fatalf("own redeem = %d %v", response.Code, broker.calls)
	}
	options.cookie = prodCookie
	if response := do(handler, http.MethodGet, "/api/v1/settings", "", options); response.Code != http.StatusForbidden {
		t.Fatalf("pairing origin on settings = %d", response.Code)
	}
}

func TestSecFetchRules(t *testing.T) {
	handler := newPeerHandler(t, newFakeComputers())
	get := func(options requestOptions) int {
		options.cookie = prodCookie
		return do(handler, http.MethodGet, "/api/v1/sessions", "", options).Code
	}
	for _, test := range []struct {
		name    string
		options requestOptions
		want    int
	}{
		{"same-origin without Origin", requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusOK},
		{"no Origin, no Sec-Fetch", requestOptions{omitOrigin: true}, http.StatusForbidden},
		{"same-site without Origin", requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-site"}}}, http.StatusForbidden},
		{"cross-site without Origin", requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"cross-site"}}}, http.StatusForbidden},
		{"two Sec-Fetch-Site", requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin", "same-origin"}}}, http.StatusForbidden},
		{"no-cors same-origin", requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin"}, "Sec-Fetch-Mode": {"no-cors"}}}, http.StatusForbidden},
		{"no-cors own Origin", requestOptions{header: http.Header{"Sec-Fetch-Mode": {"no-cors"}}}, http.StatusForbidden},
		{"no-cors bound Origin", requestOptions{origin: homeOrigin, header: http.Header{"Sec-Fetch-Mode": {"NO-CORS"}}}, http.StatusForbidden},
		{"own Origin", requestOptions{}, http.StatusOK},
		{"two Origins", requestOptions{header: http.Header{"Origin": {testOrigin, homeOrigin}}}, http.StatusForbidden},
		{"unbound Origin with same-origin", requestOptions{origin: "https://other.tailx.ts.net", header: http.Header{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusForbidden},
	} {
		if code := get(test.options); code != test.want {
			t.Errorf("%s: %d, want %d", test.name, code, test.want)
		}
	}
	// Static pages and health stay outside the API rules.
	if code := do(handler, http.MethodGet, "/", "", requestOptions{omitOrigin: true}).Code; code != http.StatusOK {
		t.Fatalf("static = %d", code)
	}
}

func TestWebSocketOriginIsCheckedPerRequest(t *testing.T) {
	broker := newFakeComputers()
	server := httptest.NewUnstartedServer(nil)
	bound := server.Listener.Addr().String()
	handler, err := newServer(Config{Profile: platform.DevelopmentProfile, Broker: broker}, bound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	handler.refreshOrigin(context.Background())
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() {
		handler.stopLive()
		handler.live.Wait()
		server.Close()
	})
	dial := func(origin string) (*websocket.Conn, int) {
		header := liveHeader(bound, validCredential)
		header.Set("Origin", origin)
		conn, response, err := dialLive(t, bound, header)
		if err != nil {
			if response == nil {
				t.Fatalf("dial %s: %v", origin, err)
			}
			return nil, response.StatusCode
		}
		return conn, http.StatusSwitchingProtocols
	}
	conn, code := dial(homeOrigin)
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("bound origin = %d", code)
	}
	openedStream(t, broker.fakeBroker)
	conn.CloseNow()
	if !broker.called("open@" + homeOrigin) {
		t.Fatalf("live open without the bound origin: %v", broker.calls)
	}
	for _, origin := range []string{pairOrigin, "https://other.tailx.ts.net", "null", homeOrigin + ":443"} {
		if _, code := dial(origin); code != http.StatusForbidden {
			t.Fatalf("origin %s = %d", origin, code)
		}
	}
	// Unbinding takes effect on the next upgrade, without a restart.
	broker.probe = protocol.GatewayRPCOriginResultPayload{}
	handler.refreshOrigin(context.Background())
	if _, code := dial(homeOrigin); code != http.StatusForbidden {
		t.Fatalf("unbound origin = %d", code)
	}
	if conn, code := dial("http://" + bound); code != http.StatusSwitchingProtocols {
		t.Fatalf("own origin = %d", code)
	} else {
		openedStream(t, broker.fakeBroker)
		conn.CloseNow()
	}
}

func TestSettingsRefreshesCookie(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	cookieOf := func(response *httptest.ResponseRecorder) *http.Cookie {
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == productionCookieName {
				return cookie
			}
		}
		return nil
	}
	remaining := 3*24*time.Hour + 30*time.Minute
	broker.expires = time.Now().Add(remaining)
	for _, origin := range []string{testOrigin, homeOrigin} {
		response := do(handler, http.MethodGet, "/api/v1/settings", "", crossSite(origin))
		cookie := cookieOf(response)
		if response.Code != http.StatusOK || cookie == nil {
			t.Fatalf("%s: settings = %d, cookie %v", origin, response.Code, cookie)
		}
		if cookie.Value != validCredential || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode ||
			cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge <= 0 || cookie.MaxAge > int(remaining/time.Second) ||
			cookie.MaxAge < int(remaining/time.Second)-5 {
			t.Fatalf("%s: refreshed cookie %+v", origin, cookie)
		}
	}
	// Other reads never refresh it.
	if cookie := cookieOf(do(handler, http.MethodGet, "/api/v1/sessions", "", crossSite(homeOrigin))); cookie != nil {
		t.Fatalf("sessions refreshed the cookie: %+v", cookie)
	}
	// Revoked, unknown and expired credentials get only the clearing cookie.
	for _, value := range []string{revokedCredential, strings.Repeat("Z", 43)} {
		options := crossSite(homeOrigin)
		options.cookie = productionCookieName + "=" + value
		response := do(handler, http.MethodGet, "/api/v1/settings", "", options)
		if cookie := cookieOf(response); response.Code != http.StatusUnauthorized || cookie == nil || cookie.Value != "" || cookie.MaxAge >= 0 {
			t.Fatalf("%s: %d %v", value[:1], response.Code, response.Header().Values("Set-Cookie"))
		}
	}
	broker.expires = time.Now().Add(-time.Second)
	response := do(handler, http.MethodGet, "/api/v1/settings", "", crossSite(homeOrigin))
	if cookie := cookieOf(response); cookie != nil && cookie.Value != "" {
		t.Fatalf("expired credential refreshed: %+v", cookie)
	}
	// A rejected request never refreshes, even with a valid cookie.
	broker.expires = time.Now().Add(time.Hour)
	if response := do(handler, http.MethodGet, "/api/v1/settings", "", crossSite("https://other.tailx.ts.net")); cookieOf(response) != nil {
		t.Fatalf("rejected request refreshed the cookie")
	}
}

func TestDocumentCSPListsDeviceComputers(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	policy := func(cookie string) string {
		return do(handler, http.MethodGet, "/add-computer", "", requestOptions{omitOrigin: true, cookie: cookie}).Header().Get("Content-Security-Policy")
	}
	// The add-computer page is an app route like any other.
	if response := do(handler, http.MethodGet, "/add-computer", "", requestOptions{omitOrigin: true}); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "<title>app") || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("/add-computer = %d %s", response.Code, response.Body)
	}
	if got := policy(""); got != contentSecurityPolicy {
		t.Fatalf("no cookie: %s", got)
	}
	if got := policy(prodCookie); got != contentSecurityPolicy {
		t.Fatalf("no computers: %s", got)
	}
	broker.list.Computers = []protocol.HTTPComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps"}}
	broker.list.PendingOrigins = []string{"https://linux.tailx.ts.net", "https://vps.tailx.ts.net", "https://BAD.tailx.ts.net"}
	// A confirmed computer also gets its preview origin: in frame-src, and in
	// connect-src for the probe. A pending one never does, and no preview
	// origin is a wss source. This computer's previews are off here, so
	// frame-src lists only the peer.
	want := strings.Replace(contentSecurityPolicy, "connect-src 'self'", "connect-src 'self' https://vps.tailx.ts.net wss://vps.tailx.ts.net "+
		"https://vps.tailx.ts.net:8443 https://linux.tailx.ts.net wss://linux.tailx.ts.net", 1) + "; frame-src https://vps.tailx.ts.net:8443"
	if got := policy(prodCookie); got != want {
		t.Fatalf("CSP = %s\nwant  %s", got, want)
	}
	// Only pending computers: no frame-src at all.
	broker.list.Computers = nil
	if got := policy(prodCookie); strings.Contains(got, "frame-src") || strings.Contains(got, ":8443") {
		t.Fatalf("pending computers in the preview policy: %s", got)
	}
	broker.list.Computers = []protocol.HTTPComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps"}}
	if !broker.called("computers@") {
		t.Fatalf("calls = %v", broker.calls)
	}
	// Assets and other users' cookies keep the plain policy.
	if got := do(handler, http.MethodGet, "/assets/app-3f9a1c.js", "", requestOptions{omitOrigin: true, cookie: prodCookie}).Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Fatalf("asset CSP = %s", got)
	}
	if got := policy(productionCookieName + "=" + revokedCredential); got != contentSecurityPolicy {
		t.Fatalf("revoked cookie CSP = %s", got)
	}
	// At most 8 computers.
	broker.list.Computers = nil
	broker.list.PendingOrigins = nil
	for index := range 10 {
		broker.list.PendingOrigins = append(broker.list.PendingOrigins, "https://c"+strconv.Itoa(index)+".tailx.ts.net")
	}
	if got := policy(prodCookie); strings.Count(got, "wss://") != protocol.MaxComputers || strings.Contains(got, "c8.") {
		t.Fatalf("more than 8 computers: %s", got)
	}
}

// Rule H1 (docs/peer-preview.md): the app document may frame its own preview
// origin and the preview origin of each confirmed computer of the cookie's
// device, nothing else.
func TestDocumentCSPFramesConfirmedComputersOnly(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	preview, err := newPreviewHandler(handler, ProductionPreviewListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preview.stop)
	own := "https://" + previewHost
	plain := strings.Replace(contentSecurityPolicy, "connect-src 'self'", "connect-src 'self' "+own, 1) + "; frame-src " + own
	for _, test := range []struct {
		name               string
		computers, pending []string
		cookie             string
		connect, frames    string // added after 'self' and after the own preview origin
	}{
		{"no cookie", []string{"https://vps.tailx.ts.net"}, nil, "", "", ""},
		{"revoked cookie", []string{"https://vps.tailx.ts.net"}, nil, productionCookieName + "=" + revokedCredential, "", ""},
		{"no computers", nil, nil, prodCookie, "", ""},
		{"confirmed", []string{"https://vps.tailx.ts.net"}, nil, prodCookie,
			"https://vps.tailx.ts.net wss://vps.tailx.ts.net https://vps.tailx.ts.net:8443 ", " https://vps.tailx.ts.net:8443"},
		{"pending only", nil, []string{"https://linux.tailx.ts.net"}, prodCookie, "https://linux.tailx.ts.net wss://linux.tailx.ts.net ", ""},
		{"confirmed and pending", []string{"https://vps.tailx.ts.net", "https://pi.tailx.ts.net"}, []string{"https://linux.tailx.ts.net", "https://vps.tailx.ts.net"}, prodCookie,
			"https://vps.tailx.ts.net wss://vps.tailx.ts.net https://vps.tailx.ts.net:8443 https://pi.tailx.ts.net wss://pi.tailx.ts.net https://pi.tailx.ts.net:8443 " +
				"https://linux.tailx.ts.net wss://linux.tailx.ts.net ", " https://vps.tailx.ts.net:8443 https://pi.tailx.ts.net:8443"},
		{"invalid origins", []string{"https://BAD.tailx.ts.net", "https://vps.tailx.ts.net:8443", "http://vps.tailx.ts.net"}, nil, prodCookie, "", ""},
	} {
		broker.list.Computers = nil
		for _, origin := range test.computers {
			broker.list.Computers = append(broker.list.Computers, protocol.HTTPComputer{Origin: origin, Label: "x"})
		}
		broker.list.PendingOrigins = test.pending
		got := do(handler, http.MethodGet, "/", "", requestOptions{omitOrigin: true, cookie: test.cookie}).Header().Get("Content-Security-Policy")
		want := strings.Replace(contentSecurityPolicy, "connect-src 'self'", "connect-src 'self' "+test.connect+own, 1) + "; frame-src " + own + test.frames
		if got != want {
			t.Errorf("%s: CSP = %s\nwant  %s", test.name, got, want)
		}
		if strings.Contains(got, "wss://vps.tailx.ts.net:8443") || strings.Contains(got, "wss://"+previewHost) || strings.Count(got, "frame-src") != 1 {
			t.Errorf("%s: a wss preview source or two frame-src: %s", test.name, got)
		}
		// Only the app document gets the device's computers.
		if asset := do(handler, http.MethodGet, "/assets/app-3f9a1c.js", "", requestOptions{omitOrigin: true, cookie: test.cookie}).Header().Get("Content-Security-Policy"); asset != plain {
			t.Errorf("%s: asset CSP = %s", test.name, asset)
		}
	}
}

func TestComputerRoutes(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	broker.list.Computers = []protocol.HTTPComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps"}}
	broker.list.PendingOrigins = []string{"https://linux.tailx.ts.net"}
	response := do(handler, http.MethodGet, "/api/v1/computers", "", requestOptions{cookie: prodCookie})
	var list map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list) != 1 ||
		string(list["computers"]) != `[{"label":"vps","origin":"https://vps.tailx.ts.net"}]` {
		t.Fatalf("list = %d %s", response.Code, response.Body)
	}
	add := `{"origin":"https://vps.tailx.ts.net","label":"vps","state":"pending"}`
	if response := do(handler, http.MethodPost, "/api/v1/computers", add, requestOptions{cookie: prodCookie}); response.Code != http.StatusOK ||
		len(broker.added) != 1 || broker.added[0].State != protocol.HTTPComputerStatePending {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}
	for name, body := range map[string]string{
		"port":    `{"origin":"https://vps.tailx.ts.net:443","label":"vps","state":"pending"}`,
		"http":    `{"origin":"http://vps.tailx.ts.net","label":"vps","state":"pending"}`,
		"label":   `{"origin":"https://vps.tailx.ts.net","label":"","state":"pending"}`,
		"state":   `{"origin":"https://vps.tailx.ts.net","label":"vps","state":"maybe"}`,
		"unknown": `{"origin":"https://vps.tailx.ts.net","label":"vps","state":"pending","x":1}`,
	} {
		if code := do(handler, http.MethodPost, "/api/v1/computers", body, requestOptions{cookie: prodCookie}).Code; code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := do(handler, http.MethodPost, "/api/v1/computers", add, requestOptions{cookie: prodCookie, omitHeader: true}).Code; code != http.StatusForbidden {
		t.Fatalf("add without request header = %d", code)
	}
	// The app sends {} like logout (B2).
	if code := do(handler, http.MethodDelete, "/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{cookie: prodCookie}).Code; code != http.StatusNoContent ||
		!broker.called("remove-computer:https://vps.tailx.ts.net@") {
		t.Fatalf("remove = %d %v", code, broker.calls)
	}
	for name, test := range map[string]struct {
		target, body string
		options      requestOptions
		want         int
	}{
		"bad host":     {"/api/v1/computers/VPS.tailx.ts.net", "{}", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"port":         {"/api/v1/computers/vps.tailx.ts.net:443", "{}", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"unknown body": {"/api/v1/computers/vps.tailx.ts.net", `{"origin":"x"}`, requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"no body":      {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"not json":     {"/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{cookie: prodCookie, contentType: "text/plain"}, http.StatusUnsupportedMediaType},
		"no header":    {"/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{cookie: prodCookie, omitHeader: true}, http.StatusForbidden},
		"no origin":    {"/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{cookie: prodCookie, omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusForbidden},
		"no cookie":    {"/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{}, http.StatusUnauthorized},
		"wrong verb":   {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie}, 0},
		"bare delete":  {"/api/v1/computers", "{}", requestOptions{cookie: prodCookie}, http.StatusMethodNotAllowed},
	} {
		method := http.MethodDelete
		if name == "wrong verb" {
			method, test.want = http.MethodGet, http.StatusMethodNotAllowed
		}
		if code := do(handler, method, test.target, test.body, test.options).Code; code != test.want {
			t.Errorf("%s: %d, want %d", name, code, test.want)
		}
	}
	// Broker messages are replaced with generic text.
	broker.opErr = &gatewayapi.Error{Code: protocol.LimitExceeded, Message: "vps says: click evil.example"}
	response = do(handler, http.MethodPost, "/api/v1/computers", add, requestOptions{cookie: prodCookie})
	if response.Code != http.StatusTooManyRequests || strings.Contains(response.Body.String(), "evil") {
		t.Fatalf("limit = %d %s", response.Code, response.Body)
	}
}

// Right after approval the app reads settings from its bound origin. The
// approved poll refreshes the origin sets, so that read is admitted with CORS
// instead of waiting for the next probe (B1).
func TestApprovedPollAdmitsTheNewBoundOriginAtOnce(t *testing.T) {
	broker := newFakeComputers()
	broker.probe = protocol.GatewayRPCOriginResultPayload{PairingOrigins: []string{pairOrigin}}
	broker.approve = true
	handler := newPeerHandler(t, broker)
	options := crossSite(pairOrigin)
	options.cookie = ""
	if response := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, options); response.Code != http.StatusOK ||
		!strings.Contains(response.Header().Get("Set-Cookie"), validCredential) {
		t.Fatalf("approved poll = %d %v %s", response.Code, response.Header(), response.Body)
	}
	options.cookie = prodCookie
	response := do(handler, http.MethodGet, "/api/v1/settings", "", options)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != pairOrigin {
		t.Fatalf("first settings after approval = %d %v", response.Code, response.Header())
	}
}

var (
	offerGrant = strings.Repeat("o", 42) + "_"
	offerBody  = `{"origin":"https://vps.tailx.ts.net","grant":"` + offerGrant + `"}`
)

// noCORS fails when any Access-Control-* header was sent.
func noCORS(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	for header := range response.Header() {
		if strings.HasPrefix(header, "Access-Control-") {
			t.Errorf("%s: CORS header %s sent: %v", name, header, response.Header())
		}
	}
}

// The offer route is for a new computer's broker only: no Origin, no
// Sec-Fetch-*, never CORS, never the cookie (docs/found-on-network.md, H1).
func TestOfferRouteAdmission(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	for _, test := range []struct {
		name, method, body string
		options            requestOptions
		want               int
	}{
		{"own Origin", "POST", offerBody, requestOptions{origin: testOrigin}, http.StatusForbidden},
		{"bound Origin", "POST", offerBody, requestOptions{origin: homeOrigin}, http.StatusForbidden},
		{"pairing Origin", "POST", offerBody, requestOptions{origin: pairOrigin}, http.StatusForbidden},
		{"null Origin", "POST", offerBody, requestOptions{origin: "null"}, http.StatusForbidden},
		{"empty Origin", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Origin": {""}}}, http.StatusForbidden},
		{"two Origins", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Origin": {testOrigin, homeOrigin}}}, http.StatusForbidden},
		{"Sec-Fetch-Site", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusForbidden},
		{"Sec-Fetch-Mode", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Mode": {"cors"}}}, http.StatusForbidden},
		{"Sec-Fetch-Dest", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-Dest": {"empty"}}}, http.StatusForbidden},
		{"empty Sec-Fetch-User", "POST", offerBody, requestOptions{omitOrigin: true, header: http.Header{"Sec-Fetch-User": {""}}}, http.StatusForbidden},
		{"preflight from bound Origin", "OPTIONS", "", requestOptions{origin: homeOrigin, omitHeader: true, header: http.Header{
			"Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"content-type,x-pterm-request"}}}, http.StatusForbidden},
		{"OPTIONS without Origin", "OPTIONS", "", requestOptions{omitOrigin: true}, http.StatusMethodNotAllowed},
		{"GET", "GET", "", requestOptions{omitOrigin: true}, http.StatusMethodNotAllowed},
		{"DELETE", "DELETE", "{}", requestOptions{omitOrigin: true}, http.StatusMethodNotAllowed},
		{"missing request header", "POST", offerBody, requestOptions{omitOrigin: true, omitHeader: true}, http.StatusForbidden},
		{"wrong request header", "POST", offerBody, requestOptions{omitOrigin: true, pterm: "2"}, http.StatusForbidden},
		{"wrong type", "POST", offerBody, requestOptions{omitOrigin: true, contentType: "text/plain"}, http.StatusUnsupportedMediaType},
		{"1 KiB + 1", "POST", offerBody + strings.Repeat(" ", maxOfferBodyBytes+1-len(offerBody)), requestOptions{omitOrigin: true}, http.StatusRequestEntityTooLarge},
		{"bad JSON", "POST", `{"origin":`, requestOptions{omitOrigin: true}, http.StatusBadRequest},
		{"unknown field", "POST", `{"origin":"https://vps.tailx.ts.net","grant":"` + offerGrant + `","label":"x"}`, requestOptions{omitOrigin: true}, http.StatusBadRequest},
		{"http origin", "POST", `{"origin":"http://vps.tailx.ts.net","grant":"` + offerGrant + `"}`, requestOptions{omitOrigin: true}, http.StatusBadRequest},
		{"origin with port", "POST", `{"origin":"https://vps.tailx.ts.net:443","grant":"` + offerGrant + `"}`, requestOptions{omitOrigin: true}, http.StatusBadRequest},
		{"short grant", "POST", `{"origin":"https://vps.tailx.ts.net","grant":"abc"}`, requestOptions{omitOrigin: true}, http.StatusBadRequest},
		{"exactly 1 KiB", "POST", offerBody + strings.Repeat(" ", maxOfferBodyBytes-len(offerBody)), requestOptions{omitOrigin: true}, http.StatusNoContent},
		{"good", "POST", offerBody, requestOptions{omitOrigin: true}, http.StatusNoContent},
		// A cookie, even a malformed one, is never read: no authenticate call
		// and no cookie cleared.
		{"with cookie", "POST", offerBody, requestOptions{omitOrigin: true, cookie: prodCookie}, http.StatusNoContent},
		{"with bad cookie", "POST", offerBody, requestOptions{omitOrigin: true, cookie: productionCookieName + "=bad"}, http.StatusNoContent},
	} {
		response := do(handler, test.method, "/api/v1/computers/offer", test.body, test.options)
		if response.Code != test.want {
			t.Errorf("%s: %d, want %d (%s)", test.name, response.Code, test.want, response.Body)
		}
		noCORS(t, test.name, response)
		if response.Header().Get("Set-Cookie") != "" || strings.Contains(response.Body.String(), offerGrant) ||
			strings.Contains(response.Body.String(), "vps") {
			t.Errorf("%s: cookie set or offer echoed: %v %s", test.name, response.Header(), response.Body)
		}
		if test.want == http.StatusNoContent && response.Body.Len() != 0 {
			t.Errorf("%s: 204 with a body %q", test.name, response.Body)
		}
	}
	if broker.called("authenticate@") || len(broker.offers) != 4 {
		t.Fatalf("calls = %v, offers = %d", broker.calls, len(broker.offers))
	}
	for _, call := range broker.calls {
		if strings.HasPrefix(call, "offer@") && call != "offer@" {
			t.Fatalf("an origin was forwarded with an offer: %v", broker.calls)
		}
	}
	// An offer the broker drops gets the same 204 (no oracle); only a broker
	// that cannot be reached is 503.
	broker.offerErr = &gatewayapi.Error{Code: protocol.InvalidRequest, Message: "dropped " + offerGrant}
	if response := do(handler, http.MethodPost, "/api/v1/computers/offer", offerBody, requestOptions{omitOrigin: true}); response.Code != http.StatusNoContent ||
		response.Body.Len() != 0 {
		t.Fatalf("dropped offer = %d %s", response.Code, response.Body)
	}
	broker.offerErr = errors.New("broken pipe")
	if response := do(handler, http.MethodPost, "/api/v1/computers/offer", offerBody, requestOptions{omitOrigin: true}); response.Code != http.StatusServiceUnavailable ||
		strings.Contains(response.Body.String(), "pipe") {
		t.Fatalf("broker down = %d %s", response.Code, response.Body)
	}
}

// The exception covers exactly the offer path: its neighbours keep every
// usual rule.
func TestOfferExceptionDoesNotWidenOtherRoutes(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	bare := requestOptions{omitOrigin: true, cookie: prodCookie}
	for _, test := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"computer by host", "POST", "/api/v1/computers/x.tailx.ts.net", offerBody, http.StatusForbidden},
		{"remove by host", "DELETE", "/api/v1/computers/vps.tailx.ts.net", "{}", http.StatusForbidden},
		{"list", "GET", "/api/v1/computers", "", http.StatusForbidden},
		{"add", "POST", "/api/v1/computers", offerBody, http.StatusForbidden},
		{"found", "GET", "/api/v1/computers/found", "", http.StatusForbidden},
		{"trailing slash", "POST", "/api/v1/computers/offer/", offerBody, http.StatusForbidden},
		{"suffix", "POST", "/api/v1/computers/offerx", offerBody, http.StatusForbidden},
		{"upper case", "POST", "/api/v1/computers/OFFER", offerBody, http.StatusForbidden},
		{"sessions", "GET", "/api/v1/sessions", "", http.StatusForbidden},
		{"redeem", "POST", "/api/v1/pair/redeem", redeemBody, http.StatusForbidden},
		// The other methods on the offer path reach only the offer handler.
		{"DELETE offer", "DELETE", "/api/v1/computers/offer", "{}", http.StatusMethodNotAllowed},
		// An escaped slash decodes to the offer path, so the exception admits
		// it, but the router sends it to no API handler.
		{"escaped slash", "POST", "/api/v1/computers%2Foffer", offerBody, http.StatusNotFound},
	} {
		response := do(handler, test.method, test.target, test.body, bare)
		if response.Code != test.want {
			t.Errorf("%s: %d, want %d (%s)", test.name, response.Code, test.want, response.Body)
		}
	}
	if len(broker.calls) != 0 || len(broker.offers) != 0 {
		t.Fatalf("a neighbour reached the broker: %v", broker.calls)
	}
	// An escaped letter is the same route, under the same exception.
	if response := do(handler, http.MethodPost, "/api/v1/computers/%6Fffer", offerBody, requestOptions{omitOrigin: true}); response.Code != http.StatusNoContent {
		t.Fatalf("escaped letter = %d %s", response.Code, response.Body)
	}
	if response := do(handler, http.MethodPost, "/api/v1/computers/%6Fffer", offerBody, requestOptions{origin: homeOrigin}); response.Code != http.StatusForbidden {
		t.Fatalf("escaped letter with Origin = %d", response.Code)
	}
}

// The offer bucket (10, then 1 per second) is its own: a flood of offers
// never blocks pairing or a paired phone.
func TestOfferBucketIsSeparate(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	now := time.Unix(1000, 0)
	handler.offerLimit.now = func() time.Time { return now }
	post := func() *httptest.ResponseRecorder {
		return do(handler, http.MethodPost, "/api/v1/computers/offer", offerBody, requestOptions{omitOrigin: true})
	}
	for attempt := 1; attempt <= 10; attempt++ {
		if code := post().Code; code != http.StatusNoContent {
			t.Fatalf("offer %d = %d", attempt, code)
		}
	}
	response := post()
	if response.Code != http.StatusTooManyRequests || errorCode(t, response) != protocol.LimitExceeded ||
		!strings.Contains(response.Body.String(), "too many offers") {
		t.Fatalf("eleventh offer = %d %s", response.Code, response.Body)
	}
	noCORS(t, "429", response)
	if response := do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{}); response.Code != http.StatusOK {
		t.Fatalf("redeem after the flood = %d", response.Code)
	}
	if response := do(handler, http.MethodPost, "/api/v1/pair/poll", pollBody, requestOptions{}); response.Code != http.StatusOK {
		t.Fatalf("poll after the flood = %d", response.Code)
	}
	if response := do(handler, http.MethodGet, "/api/v1/sessions", "", requestOptions{cookie: prodCookie}); response.Code != http.StatusOK {
		t.Fatalf("paired phone after the flood = %d", response.Code)
	}
	if response := do(handler, http.MethodGet, "/api/v1/computers/found", "", requestOptions{cookie: prodCookie}); response.Code != http.StatusOK {
		t.Fatalf("found after the flood = %d", response.Code)
	}
	now = now.Add(time.Second)
	if code := post().Code; code != http.StatusNoContent {
		t.Fatalf("offer after one second = %d", code)
	}
	if code := post().Code; code != http.StatusTooManyRequests {
		t.Fatalf("second offer in that second = %d", code)
	}
	// Redeems and polls never take offer tokens either.
	now = now.Add(10 * time.Second)
	for range 5 {
		do(handler, http.MethodPost, "/api/v1/pair/redeem", redeemBody, requestOptions{})
	}
	for attempt := 1; attempt <= 10; attempt++ {
		if code := post().Code; code != http.StatusNoContent {
			t.Fatalf("offer %d after redeems = %d", attempt, code)
		}
	}
}

func TestFoundRoute(t *testing.T) {
	broker := newFakeComputers()
	handler := newPeerHandler(t, broker)
	broker.found = []protocol.HTTPFoundComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps", Grant: offerGrant, ExpiresAt: "2026-10-03T10:00:00Z"}}
	response := do(handler, http.MethodGet, "/api/v1/computers/found", "", requestOptions{cookie: prodCookie})
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) !=
		`{"computers":[{"expiresAt":"2026-10-03T10:00:00Z","grant":"`+offerGrant+`","label":"vps","origin":"https://vps.tailx.ts.net"}]}` ||
		!broker.called("found@") {
		t.Fatalf("found = %d %s %v", response.Code, response.Body, broker.calls)
	}
	broker.found = nil
	if response := do(handler, http.MethodGet, "/api/v1/computers/found", "", requestOptions{cookie: prodCookie}); response.Code != http.StatusOK ||
		strings.TrimSpace(response.Body.String()) != `{"computers":[]}` {
		t.Fatalf("empty found = %d %s", response.Code, response.Body)
	}
	for _, test := range []struct {
		name    string
		method  string
		options requestOptions
		want    int
	}{
		{"no cookie", "GET", requestOptions{}, http.StatusUnauthorized},
		{"bound app origin", "GET", crossSite(homeOrigin), http.StatusBadRequest},
		{"pairing origin", "GET", crossSite(pairOrigin), http.StatusForbidden},
		{"unbound origin", "GET", crossSite("https://other.tailx.ts.net"), http.StatusForbidden},
		{"POST", "POST", requestOptions{cookie: prodCookie}, http.StatusMethodNotAllowed},
	} {
		if code := do(handler, test.method, "/api/v1/computers/found", "", test.options).Code; code != test.want {
			t.Errorf("%s: %d, want %d", test.name, code, test.want)
		}
	}
	if !broker.called("found@" + homeOrigin) {
		t.Fatalf("the bound origin was not forwarded for the broker to refuse: %v", broker.calls)
	}
	// Broker text never reaches the page: no snapshot is the generic status text.
	broker.fakeBroker.opErr = &gatewayapi.Error{Code: protocol.HostUnavailable, Message: "vps says: click evil.example"}
	response = do(handler, http.MethodGet, "/api/v1/computers/found", "", requestOptions{cookie: prodCookie})
	if response.Code != http.StatusServiceUnavailable || errorCode(t, response) != protocol.HostUnavailable ||
		strings.Contains(response.Body.String(), "evil") || !strings.Contains(response.Body.String(), `"Service Unavailable"`) {
		t.Fatalf("no snapshot = %d %s", response.Code, response.Body)
	}
}

package gateway

import (
	"context"
	"encoding/json"
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
	return gatewayapi.PollResult{Response: protocol.HTTPPairPollResponse{Status: protocol.HTTPPairPollStatusPending}}, nil
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
	want := strings.Replace(contentSecurityPolicy, "connect-src 'self'", "connect-src 'self' https://vps.tailx.ts.net wss://vps.tailx.ts.net "+
		"https://linux.tailx.ts.net wss://linux.tailx.ts.net", 1)
	if got := policy(prodCookie); got != want {
		t.Fatalf("CSP = %s\nwant  %s", got, want)
	}
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
	if code := do(handler, http.MethodDelete, "/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie}).Code; code != http.StatusNoContent ||
		!broker.called("remove-computer:https://vps.tailx.ts.net@") {
		t.Fatalf("remove = %d %v", code, broker.calls)
	}
	for name, test := range map[string]struct {
		target, body string
		options      requestOptions
		want         int
	}{
		"bad host":    {"/api/v1/computers/VPS.tailx.ts.net", "", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"port":        {"/api/v1/computers/vps.tailx.ts.net:443", "", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"body":        {"/api/v1/computers/vps.tailx.ts.net", "{}", requestOptions{cookie: prodCookie}, http.StatusBadRequest},
		"no header":   {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie, omitHeader: true}, http.StatusForbidden},
		"no origin":   {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie, omitOrigin: true, header: http.Header{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusForbidden},
		"no cookie":   {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{}, http.StatusUnauthorized},
		"wrong verb":  {"/api/v1/computers/vps.tailx.ts.net", "", requestOptions{cookie: prodCookie}, 0},
		"bare delete": {"/api/v1/computers", "", requestOptions{cookie: prodCookie}, http.StatusMethodNotAllowed},
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
	// A broker without multi-computer support serves no computers routes.
	plain := newTestHandler(t, newFakeBroker())
	if code := do(plain, http.MethodGet, "/api/v1/computers", "", requestOptions{cookie: prodCookie}).Code; code != http.StatusNotFound {
		t.Fatalf("plain broker computers = %d", code)
	}
}

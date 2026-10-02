package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/webassets"
)

// contentSecurityPolicy applies to every response. xterm.js sets inline
// element styles, so style-src needs 'unsafe-inline'; scripts stay 'self'.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; " +
	"frame-ancestors 'none'; form-action 'self'"

var globEscaper = strings.NewReplacer(`*`, `\*`, `?`, `\?`, `[`, `\[`)

type gatewayHandler struct {
	mu     sync.RWMutex
	broker brokerState
	// configured is the origin saved with `pterm config origin`, refreshed
	// from the broker; it joins the static hosts and origins below.
	configured allowedOrigin
	// bound and pairing are the other computers' app origins from the broker
	// probe (docs/multi-computer.md). They only admit cross-origin requests
	// and emit CORS headers; the broker checks each forwarded Origin against
	// the request's own credential, grant or pending pairing.
	bound, pairing map[string]struct{}

	mux     *http.ServeMux
	hosts   map[string]struct{}
	origins map[string]struct{}
	dev     bool
	// previewPort is the bound preview listener port; "" when previews are off.
	previewPort string
	api         gatewayapi.Broker
	// computers is api's multi-computer part; nil when the broker lacks it.
	computers computerBroker
	assets    fs.FS

	redeemLimit, pollLimit, authFailLimit *limiter
	// uploads bounds concurrent upload bodies held in memory.
	uploads    chan struct{}
	liveLimits liveLimits

	// live tracks hijacked WebSocket handlers, which Shutdown cannot see.
	// liveMu orders live.Add against stopLive so Wait never races an Add.
	liveMu     sync.Mutex
	live       sync.WaitGroup
	liveCtx    context.Context
	cancelLive context.CancelFunc
}

// allowedOrigin is one exact serialized origin and the Host value a browser
// sends with it.
type allowedOrigin struct{ origin, host string }

// computerBroker is the multi-computer part of the broker boundary
// (docs/multi-computer.md). The gateway uses it when the broker client
// implements it; otherwise it serves no computers routes and no CORS.
// ponytail: folds into gatewayapi.Broker once the broker side lands.
type computerBroker interface {
	// OriginProbe is gateway.origin with boundOrigins and pairingOrigins.
	OriginProbe(ctx context.Context) (protocol.GatewayRPCOriginResultPayload, error)
	ListComputers(ctx context.Context, credential string) (protocol.GatewayRPCComputerListResultPayload, error)
	AddComputer(ctx context.Context, credential string, request protocol.HTTPComputerAddRequest) (protocol.HTTPComputerAddResponse, error)
	RemoveComputer(ctx context.Context, credential, origin string) error
}

// beginLive registers a WebSocket handler unless the gateway is stopping.
func (handler *gatewayHandler) beginLive() bool {
	handler.liveMu.Lock()
	defer handler.liveMu.Unlock()
	if handler.liveCtx.Err() != nil {
		return false
	}
	handler.live.Add(1)
	return true
}

// stopLive closes every WebSocket with 1001; later upgrades are refused.
func (handler *gatewayHandler) stopLive() {
	handler.liveMu.Lock()
	defer handler.liveMu.Unlock()
	handler.cancelLive()
}

// newServer builds the gateway handler for a listener already bound to the
// loopback address bound.
func newServer(config Config, bound, generation string) (*gatewayHandler, error) {
	address, err := netip.ParseAddrPort(bound)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsLoopback() {
		return nil, errors.New("gateway: bound address is not canonical IPv4 loopback")
	}
	dev := config.Profile == platform.DevelopmentProfile
	localhost := net.JoinHostPort("localhost", fmt.Sprint(address.Port()))
	handler := &gatewayHandler{
		broker:        brokerState{available: true, generation: generation},
		hosts:         map[string]struct{}{bound: {}, localhost: {}},
		origins:       map[string]struct{}{},
		dev:           dev,
		api:           config.Broker,
		assets:        webassets.FS,
		redeemLimit:   newLimiter(5, time.Minute/5),
		pollLimit:     newLimiter(10, time.Second),
		authFailLimit: newLimiter(20, 6*time.Second),
		uploads:       make(chan struct{}, maxConcurrentUploads),
		liveLimits:    defaultLiveLimits,
	}
	handler.liveCtx, handler.cancelLive = context.WithCancel(context.Background())
	handler.computers, _ = config.Broker.(computerBroker)
	// The page served on the bound loopback address is always allowed, so a
	// browser on this computer can pair without any configured origin.
	for _, loopback := range []string{"http://" + bound, "http://" + localhost} {
		handler.origins[loopback] = struct{}{}
	}
	for _, origin := range config.Origins {
		canonical, host, err := validateOrigin(origin, dev)
		if err != nil {
			return nil, err
		}
		handler.origins[canonical] = struct{}{}
		handler.hosts[host] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.serveHealth)
	mux.HandleFunc("/", handler.serveStatic)
	mux.HandleFunc("/api", handler.apiNotFound)
	mux.HandleFunc("/api/", handler.apiNotFound)
	if handler.api != nil {
		mux.HandleFunc("/api/v1/pair/redeem", handler.pairRedeem)
		mux.HandleFunc("/api/v1/pair/poll", handler.pairPoll)
		mux.HandleFunc("/api/v1/settings", handler.settings)
		mux.HandleFunc("/api/v1/sessions", handler.sessions)
		mux.HandleFunc("/api/v1/sessions/{id}", handler.renameSession)
		mux.HandleFunc("/api/v1/sessions/{id}/terminate", handler.terminateSession)
		mux.HandleFunc("/api/v1/sessions/{id}/uploads", handler.upload)
		mux.HandleFunc("/api/v1/device", handler.renameDevice)
		mux.HandleFunc("/api/v1/push", handler.pushConfig)
		mux.HandleFunc("/api/v1/push/subscribe", handler.pushSubscribe)
		mux.HandleFunc("/api/v1/push/unsubscribe", handler.pushUnsubscribe)
		mux.HandleFunc("/api/v1/push/test", handler.pushTest)
		mux.HandleFunc("/api/v1/logout", handler.logout)
		mux.HandleFunc("/api/v1/preview", handler.previewConfig)
		mux.HandleFunc("/api/v1/ws", handler.serveLive)
		if handler.computers != nil {
			mux.HandleFunc("/api/v1/computers", handler.computerList)
			mux.HandleFunc("/api/v1/computers/{host}", handler.removeComputer)
		}
	}
	handler.mux = mux
	return handler, nil
}

// ValidateOrigins checks configured browser origins for a profile before the
// gateway starts; Run applies the same rules.
func ValidateOrigins(profile platform.Profile, origins []string) error {
	for _, origin := range origins {
		if _, _, err := validateOrigin(origin, profile == platform.DevelopmentProfile); err != nil {
			return err
		}
	}
	return nil
}

// validateOrigin returns the lowercase serialized origin and the Host value
// a browser sends for it. Only scheme://host[:port] is accepted.
func validateOrigin(origin string, dev bool) (string, string, error) {
	invalid := fmt.Errorf("gateway: origin %q must be an exact scheme://host[:port]", origin)
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") || strings.ContainsAny(origin, " \t\r\n\\") ||
		// Browsers omit a default or empty port from Origin and Host, so such
		// an origin could never match.
		strings.HasSuffix(parsed.Host, ":") || parsed.Scheme == "http" && parsed.Port() == "80" ||
		parsed.Scheme == "https" && parsed.Port() == "443" {
		return "", "", invalid
	}
	host := strings.ToLower(parsed.Host)
	canonical := strings.ToLower(parsed.Scheme) + "://" + host
	switch {
	case dev && (parsed.Scheme == "http" || parsed.Scheme == "https"):
		name := parsed.Hostname()
		if ip, err := netip.ParseAddr(name); name != "localhost" && (err != nil || !ip.IsLoopback()) {
			return "", "", fmt.Errorf("gateway: development origin %q must be loopback", origin)
		}
	case !dev && parsed.Scheme == "https":
	default:
		return "", "", fmt.Errorf("gateway: origin %q must use https", origin)
	}
	return canonical, host, nil
}

func (handler *gatewayHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	header := response.Header()
	policy := contentSecurityPolicy
	// The app may frame exactly the dev-server preview origin, nothing else.
	if preview := handler.previewOriginFor(request); preview != "" {
		policy += "; frame-src " + preview
	}
	header.Set("Content-Security-Policy", policy)
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	// Host is compared case-insensitively (RFC 9110) but otherwise exactly:
	// no trailing dots, suffixes, or alternate ports. X-Forwarded-* is ignored.
	if !handler.hostAllowed(strings.ToLower(request.Host)) {
		header.Set("Cache-Control", "no-store")
		writeError(response, http.StatusMisdirectedRequest, "invalid_request", "unknown host")
		return
	}
	// A dev site in the preview frame shares the host name (and so the
	// cookie). The app refuses anything it sends, reads included.
	for _, origin := range request.Header.Values("Origin") {
		if handler.isPreviewOrigin(origin) {
			header.Set("Cache-Control", "no-store")
			writeError(response, http.StatusForbidden, "invalid_request", "origin not allowed")
			return
		}
	}
	if request.URL.Path == "/healthz" {
		header.Set("Cache-Control", "no-store")
	}
	if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") {
		header.Set("Cache-Control", "no-store")
		var ok bool
		if request, ok = handler.admitAPI(response, request); !ok {
			return
		}
	}
	handler.mux.ServeHTTP(response, request)
}

// admitAPI applies the request rules of every /api request
// (docs/multi-computer.md section 3). A request passes with an exact own
// Origin, with another computer's exact bound app origin (pairing routes also
// admit an origin of an unexpired bound grant or pending pairing), or with no
// Origin and Sec-Fetch-Site: same-origin. no-cors requests never pass:
// browsers omit Origin on them and would otherwise reach the API with this
// computer's cookie. An admitted cross-origin request gets exact CORS headers
// and carries its Origin to the broker, which checks it against the
// request's own credential, grant or pending pairing.
func (handler *gatewayHandler) admitAPI(response http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	header := response.Header()
	header.Add("Vary", "Origin")
	cross := handler.crossOrigin(request)
	if cross != "" {
		header.Set("Access-Control-Allow-Origin", cross)
		header.Set("Access-Control-Allow-Credentials", "true")
	}
	if request.Method == http.MethodOptions && cross != "" && len(request.Header.Values("Access-Control-Request-Method")) == 1 {
		header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
		header.Set("Access-Control-Allow-Headers", "Content-Type, X-Pterm-Request")
		header.Set("Access-Control-Max-Age", "600")
		response.WriteHeader(http.StatusNoContent)
		return request, false
	}
	for _, mode := range request.Header.Values("Sec-Fetch-Mode") {
		if strings.EqualFold(strings.TrimSpace(mode), "no-cors") {
			writeError(response, http.StatusForbidden, protocol.InvalidRequest, "origin not allowed")
			return request, false
		}
	}
	origins := request.Header.Values("Origin")
	switch {
	case len(origins) == 1 && handler.originAllowedValue(origins[0]):
	case cross != "":
		request = request.WithContext(gatewayapi.WithForwardedOrigin(request.Context(), cross))
	case len(origins) == 0 && sameOriginFetch(request):
	default:
		writeError(response, http.StatusForbidden, protocol.InvalidRequest, "origin not allowed")
		return request, false
	}
	return request, true
}

// sameOriginFetch reports exactly one Sec-Fetch-Site: same-origin. Browsers
// set it and pages cannot.
func sameOriginFetch(request *http.Request) bool {
	values := request.Header.Values("Sec-Fetch-Site")
	return len(values) == 1 && values[0] == "same-origin"
}

// crossOrigin returns the request's single Origin when it is not this
// computer's own and exactly matches a bound app origin, or, on
// /api/v1/pair/*, a pairing origin. Matching is exact: https, default port,
// lowercase, no trailing dot; "null", near misses and other spellings fail.
func (handler *gatewayHandler) crossOrigin(request *http.Request) string {
	values := request.Header.Values("Origin")
	if len(values) != 1 || handler.originAllowedValue(values[0]) || protocol.ValidateComputerOrigin(values[0]) != nil {
		return ""
	}
	origin := values[0]
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	if _, ok := handler.bound[origin]; ok {
		return origin
	}
	if _, ok := handler.pairing[origin]; ok && strings.HasPrefix(request.URL.Path, "/api/v1/pair/") {
		return origin
	}
	return ""
}

// originAllowed requires exactly one Origin: an exact own origin, or the
// bound app origin admitAPI admitted for this request. A missing one fails.
func (handler *gatewayHandler) originAllowed(request *http.Request) bool {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	if forwarded, ok := gatewayapi.ForwardedOrigin(request.Context()); ok && forwarded == values[0] {
		return true
	}
	return handler.originAllowedValue(values[0])
}

// originAllowedValue reports whether origin is one of the exact allowed ones.
func (handler *gatewayHandler) originAllowedValue(origin string) bool {
	origin = strings.ToLower(origin)
	if _, ok := handler.origins[origin]; ok {
		return true
	}
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	return handler.configured.origin != "" && handler.configured.origin == origin
}

func (handler *gatewayHandler) hostAllowed(host string) bool {
	if _, ok := handler.hosts[host]; ok {
		return true
	}
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	return handler.configured.host != "" && handler.configured.host == host
}

// applyConfiguredOrigin replaces the broker-configured origin. A value that is
// not valid for this profile (http in production, non-loopback in
// development) is dropped instead of widening the allow-list.
func (handler *gatewayHandler) applyConfiguredOrigin(origin string) {
	var next allowedOrigin
	if origin != "" {
		if canonical, host, err := validateOrigin(origin, handler.dev); err == nil {
			next = allowedOrigin{origin: canonical, host: host}
		}
	}
	handler.mu.Lock()
	handler.configured = next
	handler.mu.Unlock()
}

// refreshOrigin asks the broker for the configured origin and, with a
// multi-computer broker, the bound and pairing app origins. On failure it
// keeps the last known values: a broker that does not answer serves no API.
func (handler *gatewayHandler) refreshOrigin(ctx context.Context) {
	if handler.api == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if handler.computers == nil {
		if origin, err := handler.api.Origin(ctx); err == nil {
			handler.applyConfiguredOrigin(origin)
		}
		return
	}
	probe, err := handler.computers.OriginProbe(ctx)
	if err != nil {
		return
	}
	origin := ""
	if probe.Origin != nil {
		origin = *probe.Origin
	}
	handler.applyConfiguredOrigin(origin)
	bound, pairing := originSet(probe.BoundOrigins, protocol.MaxBoundOrigins), originSet(probe.PairingOrigins, protocol.MaxPairingOrigins)
	handler.mu.Lock()
	handler.bound, handler.pairing = bound, pairing
	handler.mu.Unlock()
}

// originSet keeps at most limit exact computer origins; invalid ones are
// dropped instead of widening CORS.
func originSet(origins []string, limit int) map[string]struct{} {
	set := map[string]struct{}{}
	for _, origin := range origins {
		if len(set) < limit && protocol.ValidateComputerOrigin(origin) == nil {
			set[origin] = struct{}{}
		}
	}
	return set
}

func (handler *gatewayHandler) serveHealth(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	handler.mu.RLock()
	state := handler.broker
	handler.mu.RUnlock()
	status := http.StatusOK
	payload := struct {
		Status           string `json:"status"`
		Broker           string `json:"broker"`
		BrokerGeneration string `json:"brokerGeneration,omitempty"`
	}{Status: "ok", Broker: "available", BrokerGeneration: state.generation}
	if !state.available {
		status = http.StatusServiceUnavailable
		payload.Status = "unavailable"
		payload.Broker = "unavailable"
		payload.BrokerGeneration = ""
	}
	writeJSON(response, status, payload)
}

func (handler *gatewayHandler) apiNotFound(response http.ResponseWriter, _ *http.Request) {
	writeError(response, http.StatusNotFound, "invalid_request", "unknown route")
}

var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".webp":        "image/webp",
	".woff2":       "font/woff2",
	".wasm":        "application/wasm",
	".txt":         "text/plain; charset=utf-8",
}

// serveStatic serves the embedded build. Unknown extensionless GET paths
// (such as /pair) fall back to index.html; missing files with an extension or
// under /assets/ are 404 so a script request never receives HTML.
func (handler *gatewayHandler) serveStatic(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	info, err := fs.Stat(handler.assets, name)
	// Source maps and dotfiles (.vite/manifest.json, .DS_Store) are build
	// byproducts, never part of the served site.
	if hidden := path.Ext(name) == ".map" || strings.HasPrefix(name, ".") || strings.Contains(name, "/."); hidden || err != nil || !info.Mode().IsRegular() {
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			response.Header().Set("Cache-Control", "no-store")
			http.NotFound(response, request)
			return
		}
		name = "index.html"
	}
	data, err := fs.ReadFile(handler.assets, name)
	if err != nil {
		response.Header().Set("Cache-Control", "no-store")
		http.NotFound(response, request)
		return
	}
	if name == "index.html" {
		handler.deviceConnectSources(response, request)
	}
	contentType := contentTypes[path.Ext(name)]
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(name))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	response.Header().Set("Content-Type", contentType)
	if strings.HasPrefix(name, "assets/") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// index.html, the service worker, manifest and icons must revalidate
		// so a new build is picked up.
		response.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(data))
}

// deviceConnectSources adds the exact https and wss origins of the cookie
// device's confirmed and pending computers (at most 8) to the app document's
// connect-src, so the app can reach them and nothing else. Without a usable
// cookie or broker answer the policy stays as it is.
func (handler *gatewayHandler) deviceConnectSources(response http.ResponseWriter, request *http.Request) {
	credential, present := handler.sessionCookie(request)
	if handler.computers == nil || !present || !validSecret(credential) || !handler.authFailLimit.allowed(clientKey(request)) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), probeTimeout)
	defer cancel()
	list, err := handler.computers.ListComputers(ctx, credential)
	if err != nil {
		return
	}
	var origins []string
	for _, computer := range list.Computers {
		origins = append(origins, computer.Origin)
	}
	origins = append(origins, list.PendingOrigins...)
	var sources []string
	seen := map[string]bool{}
	for _, origin := range origins {
		if len(seen) == protocol.MaxComputers || seen[origin] || protocol.ValidateComputerOrigin(origin) != nil {
			continue
		}
		seen[origin] = true
		sources = append(sources, origin, "wss://"+strings.TrimPrefix(origin, "https://"))
	}
	if len(sources) == 0 {
		return
	}
	header := response.Header()
	header.Set("Content-Security-Policy", strings.Replace(header.Get("Content-Security-Policy"),
		"connect-src 'self'", "connect-src 'self' "+strings.Join(sources, " "), 1))
}

func allowMethod(response http.ResponseWriter, request *http.Request, methods ...string) bool {
	for _, method := range methods {
		if request.Method == method {
			return true
		}
	}
	response.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(response, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
	return false
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(append(data, '\n'))
}

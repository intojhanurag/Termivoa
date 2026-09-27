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

	mux     *http.ServeMux
	hosts   map[string]struct{}
	origins map[string]struct{}
	// originPatterns keeps coder/websocket's own origin check enabled as a
	// second layer; the exact check in originAllowed is authoritative.
	originPatterns []string
	dev            bool
	// previewPort is the bound preview listener port; "" when previews are off.
	previewPort string
	api         gatewayapi.Broker
	assets      fs.FS

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
	// The page served on the bound loopback address is always allowed, so a
	// browser on this computer can pair without any configured origin.
	for _, loopback := range []string{"http://" + bound, "http://" + localhost} {
		handler.origins[loopback] = struct{}{}
		handler.originPatterns = append(handler.originPatterns, globEscaper.Replace(loopback))
	}
	for _, origin := range config.Origins {
		canonical, host, err := validateOrigin(origin, dev)
		if err != nil {
			return nil, err
		}
		handler.origins[canonical] = struct{}{}
		// coder/websocket matches patterns with path.Match; escape IPv6
		// brackets and other metacharacters so the pattern stays exact.
		handler.originPatterns = append(handler.originPatterns, globEscaper.Replace(canonical))
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
	if request.URL.Path == "/healthz" || request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") {
		header.Set("Cache-Control", "no-store")
	}
	handler.mux.ServeHTTP(response, request)
}

// originAllowed requires an exact configured Origin; a missing one fails.
func (handler *gatewayHandler) originAllowed(request *http.Request) bool {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return false
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

// refreshOrigin asks the broker for the configured origin. On failure it
// keeps the last known value: a broker that does not answer serves no API.
func (handler *gatewayHandler) refreshOrigin(ctx context.Context) {
	if handler.api == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if origin, err := handler.api.Origin(ctx); err == nil {
		handler.applyConfiguredOrigin(origin)
	}
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

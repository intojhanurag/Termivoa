package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Dev-server preview. A second loopback listener proxies one owned dev
// server port per browser. It is a separate origin (the owner exposes it with
// `tailscale serve --https=8443`), so a dev site never shares an origin with
// the Termivoa app. It never serves the app or its API; see docs/preview.md.
const (
	ProductionPreviewListenAddress  = "127.0.0.1:8789"
	DevelopmentPreviewListenAddress = "127.0.0.1:8790"
	// PreviewPublicPort is the Tailscale Serve HTTPS port of the preview origin.
	PreviewPublicPort = "8443"

	previewSelectPath         = "/__termivoa/select"
	previewReservedPrefix     = "/__termivoa/"
	productionPreviewCookie   = "__Host-pterm_preview"
	developmentPreviewCookie  = "pterm_dev_preview"
	maxPreviewRequestBody     = 32 << 20
	maxPreviewUpgrades        = 32
	previewResponseHeaderWait = 2 * time.Minute

	// previewSelectedPage moves the frame to the dev site root. The move is a
	// replace navigation started by the preview origin itself, so the request
	// for / carries Sec-Fetch-Site: same-origin and no Referer.
	previewSelectedPage = `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/"><title>Preview</title>` + "\n"
)

// previewRecheckEvery bounds how long a revoked phone or a closed port keeps
// an open preview socket. A variable only so tests can shorten it.
var previewRecheckEvery = 5 * time.Second

// termivoaCookies are never forwarded to a dev server, and a dev server may
// not set them.
var termivoaCookies = map[string]bool{
	productionCookieName: true, developmentCookieName: true,
	productionPreviewCookie: true, developmentPreviewCookie: true,
}

type previewTargetKey struct{}

type previewTarget struct {
	host string // "127.0.0.1" or "::1"
	port uint16
	own  string // the preview origin the browser used
}

type previewHandler struct {
	main     *gatewayHandler // cookie name, configured origin, broker, limits
	bound    string          // the preview listener address
	port     string
	proxy    *httputil.ReverseProxy
	upgrades chan struct{}
	recheck  time.Duration
	ctx      context.Context // canceled on shutdown; closes proxied sockets
	stop     context.CancelFunc
}

// ValidatePreviewListenAddress applies the gateway listener rules to the
// preview listener: literal IPv4 loopback, fixed in production.
func ValidatePreviewListenAddress(profile platform.Profile, address string) error {
	if profile == platform.ProductionProfile && address != ProductionPreviewListenAddress {
		return errors.New("gateway: production preview listener is fixed")
	}
	if profile == platform.DevelopmentProfile && (address == ProductionPreviewListenAddress || address == ProductionListenAddress) {
		return errors.New("gateway: development preview listener must not reuse a production address")
	}
	if err := ValidateListenAddress(platform.DevelopmentProfile, address); err != nil {
		return errors.New("gateway: preview listener must be a literal IPv4 loopback address and port")
	}
	return nil
}

func newPreviewHandler(main *gatewayHandler, bound string) (*previewHandler, error) {
	address, err := netip.ParseAddrPort(bound)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsLoopback() {
		return nil, errors.New("gateway: preview address is not canonical IPv4 loopback")
	}
	handler := &previewHandler{
		main: main, bound: bound, port: strconv.Itoa(int(address.Port())),
		upgrades: make(chan struct{}, maxPreviewUpgrades), recheck: previewRecheckEvery,
	}
	handler.ctx, handler.stop = context.WithCancel(context.Background())
	handler.proxy = &httputil.ReverseProxy{
		Rewrite:        handler.rewrite,
		ModifyResponse: handler.modifyResponse,
		ErrorHandler: func(response http.ResponseWriter, request *http.Request, _ error) {
			previewPage(response, http.StatusBadGateway, "The dev server did not answer.")
		},
		FlushInterval: -1, // stream server-sent events and chunked output at once
		Transport: &http.Transport{
			Proxy:                 nil, // never an environment proxy
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: previewResponseHeaderWait,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       30 * time.Second,
			DisableCompression:    true,
		},
	}
	main.previewPort = handler.port
	return handler, nil
}

// previewOriginFor is the preview origin that matches a request to the main
// app: https://<host>:8443 for the private HTTPS name, else the loopback
// preview listener on the same host name. "" when previews are off.
func (handler *gatewayHandler) previewOriginFor(request *http.Request) string {
	if handler.previewPort == "" {
		return ""
	}
	host := strings.ToLower(request.Host)
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	if handler.originAllowedValue("https://" + host) {
		return "https://" + net.JoinHostPort(strings.Trim(name, "[]"), PreviewPublicPort)
	}
	return "http://" + net.JoinHostPort(strings.Trim(name, "[]"), handler.previewPort)
}

// isPreviewOrigin reports whether origin belongs to the preview listener, as
// seen from any host this gateway serves. The main app refuses such requests.
func (handler *gatewayHandler) isPreviewOrigin(origin string) bool {
	if handler.previewPort == "" {
		return false
	}
	parsed, err := url.Parse(strings.ToLower(origin))
	if err != nil || parsed.Host == "" {
		return false
	}
	switch {
	case parsed.Scheme == "https" && parsed.Port() == PreviewPublicPort:
		return handler.hostAllowed(parsed.Hostname())
	case parsed.Scheme == "http" && parsed.Port() == handler.previewPort:
		return true
	}
	return false
}

// previewConfig serves GET /api/v1/preview.
func (handler *gatewayHandler) previewConfig(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	if _, err := handler.api.Authenticate(request.Context(), credential); err != nil {
		handler.respond(response, request, nil, err)
		return
	}
	payload := map[string]string{}
	if origin := handler.previewOriginFor(request); origin != "" {
		payload["origin"] = origin
	}
	writeJSON(response, http.StatusOK, payload)
}

// ownOrigin is the origin a browser uses for this preview listener request.
func (handler *previewHandler) ownOrigin(request *http.Request) string {
	host := strings.ToLower(request.Host)
	if _, port, err := net.SplitHostPort(host); err == nil && port == PreviewPublicPort {
		return "https://" + host
	}
	return "http://" + host
}

func (handler *previewHandler) hostAllowed(host string) bool {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	switch port {
	case handler.port:
		return host == handler.bound || name == "localhost"
	case PreviewPublicPort:
		// Only the private HTTPS names of the main app, on the Serve port.
		return !handler.main.dev && handler.main.originAllowedValue("https://"+name)
	}
	return false
}

func (handler *previewHandler) cookieName() string {
	if handler.main.dev {
		return developmentPreviewCookie
	}
	return productionPreviewCookie
}

func (handler *previewHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	host := strings.ToLower(request.Host)
	if !handler.hostAllowed(host) {
		previewPage(response, http.StatusMisdirectedRequest, "Unknown host.")
		return
	}
	// Who may send a request is decided before the cookie is read and before
	// the dev server is reached (docs/peer-preview.md, L1 and L2). The
	// selection is the one request the app's frame starts: a navigation has no
	// Origin, so its Referer must name this computer's app or a bound app. The
	// app's reachability probe is this path too: without a Referer it answers
	// 403, with one and no cookie 401; any answer means the listener is served.
	// Everything else must come from the preview origin itself.
	selecting := request.URL.Path == previewSelectPath
	var app string
	var forward bool
	if selecting {
		var ok bool
		if app, forward, ok = handler.appReferer(request); !ok {
			previewPage(response, http.StatusForbidden, "Open the preview from the Termivoa app.")
			return
		}
	} else if !handler.fromPreviewOrigin(request) {
		previewPage(response, http.StatusForbidden, "Origin not allowed.")
		return
	}
	limitKey := clientKey(request)
	if !handler.main.authFailLimit.allowed(limitKey) {
		previewPage(response, http.StatusTooManyRequests, "Too many failed attempts.")
		return
	}
	credential, ok := handler.main.sessionCookie(request)
	if !ok || protocol.ValidateSecretText(credential) != nil {
		previewPage(response, http.StatusUnauthorized, "Open the preview from your paired phone.")
		return
	}
	if selecting {
		handler.selectPort(response, request, credential, app, forward)
		return
	}
	if strings.HasPrefix(request.URL.Path, previewReservedPrefix) {
		previewPage(response, http.StatusNotFound, "Unknown preview route.")
		return
	}
	sessionID, port, ok := handler.selection(request)
	if !ok {
		previewPage(response, http.StatusBadRequest, "Open the preview from the Termivoa app.")
		return
	}
	// Every request re-checks the credential and port ownership (the broker
	// caches its OS scan for at most 2 s).
	target, err := handler.main.api.PreviewTarget(request.Context(), credential, sessionID, port)
	if err != nil {
		handler.fail(response, err)
		return
	}
	upgrade := request.Header.Get("Upgrade") != ""
	if upgrade {
		select {
		case handler.upgrades <- struct{}{}:
			defer func() { <-handler.upgrades }()
		default:
			previewPage(response, http.StatusTooManyRequests, "Too many open preview sockets.")
			return
		}
	} else {
		request.Body = http.MaxBytesReader(response, request.Body, maxPreviewRequestBody)
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	defer context.AfterFunc(handler.ctx, cancel)()
	// Long-lived responses (HMR WebSockets, event streams) end within one
	// recheck of revocation or the port closing.
	go handler.watch(ctx, cancel, credential, sessionID, port)
	ctx = context.WithValue(ctx, previewTargetKey{}, previewTarget{host: target, port: port, own: handler.ownOrigin(request)})
	handler.proxy.ServeHTTP(response, request.WithContext(ctx))
}

func (handler *previewHandler) watch(ctx context.Context, cancel context.CancelFunc, credential, sessionID string, port uint16) {
	ticker := time.NewTicker(handler.recheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, stop := context.WithTimeout(ctx, probeTimeout*4)
			_, err := handler.main.api.PreviewTarget(check, credential, sessionID, port)
			stop()
			if err != nil && ctx.Err() == nil {
				cancel()
				return
			}
		}
	}
}

// fromPreviewOrigin is rule L1, the /api rule on the preview listener: exactly
// one Origin equal to the preview origin (form posts, module scripts, and
// WebSocket handshakes, which carry no Sec-Fetch headers in Chrome), or no
// Origin and Sec-Fetch-Site: same-origin. It refuses a script, image, frame,
// no-cors fetch or socket from the app and from any other page of the tailnet.
func (handler *previewHandler) fromPreviewOrigin(request *http.Request) bool {
	origins := request.Header.Values("Origin")
	if len(origins) == 1 {
		return strings.ToLower(origins[0]) == handler.ownOrigin(request)
	}
	return len(origins) == 0 && sameOriginFetch(request)
}

// appReferer is rule L2. The selection must carry no Origin and exactly one
// Referer that is an app origin plus "/" (the frame has
// referrerpolicy="origin"): this computer's own origin, or a bound app origin
// of another computer, which the broker must check against the credential
// (forward). A page cannot send another origin's Referer.
func (handler *previewHandler) appReferer(request *http.Request) (origin string, forward, ok bool) {
	referers := request.Header.Values("Referer")
	if len(request.Header.Values("Origin")) != 0 || len(referers) != 1 {
		return "", false, false
	}
	candidate, slash := strings.CutSuffix(referers[0], "/")
	switch {
	case !slash:
		return "", false, false
	case handler.main.originAllowedValue(candidate):
		return strings.ToLower(candidate), false, true
	case handler.main.boundOrigin(candidate):
		return candidate, true, true
	}
	return "", false, false
}

// selectPort authorizes session and port, remembers them in a preview-only
// cookie and answers a page that moves the frame to the dev site root. A
// redirect would not do: the request for / would still count as started by
// the app's page (Sec-Fetch-Site: same-site) and fail rule L1.
func (handler *previewHandler) selectPort(response http.ResponseWriter, request *http.Request, credential, app string, forward bool) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		previewPage(response, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	query := request.URL.Query()
	sessionID := query.Get("session")
	port, err := strconv.ParseUint(query.Get("port"), 10, 16)
	if len(query) != 2 || !protocol.ValidUUID(sessionID) || err != nil || port == 0 || strconv.FormatUint(port, 10) != query.Get("port") {
		previewPage(response, http.StatusBadRequest, "Invalid preview request.")
		return
	}
	ctx := request.Context()
	if forward {
		ctx = gatewayapi.WithForwardedOrigin(ctx, app)
	}
	if _, err := handler.main.api.PreviewTarget(ctx, credential, sessionID, uint16(port)); err != nil {
		if isAuthFailure(err) {
			handler.main.authFailLimit.take(clientKey(request))
		}
		handler.fail(response, err)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name: handler.cookieName(), Value: sessionID + "." + strconv.FormatUint(port, 10), Path: "/",
		Secure: !handler.main.dev, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	header := response.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	// A meta refresh needs no script allowance. Only the app that selected
	// may frame this page.
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors "+app)
	_, _ = io.WriteString(response, previewSelectedPage)
}

// selection reads the preview cookie. It names what to show, not who may
// see it: every request is authorized again.
func (handler *previewHandler) selection(request *http.Request) (string, uint16, bool) {
	cookies := request.CookiesNamed(handler.cookieName())
	if len(cookies) != 1 {
		return "", 0, false
	}
	sessionID, portText, ok := strings.Cut(cookies[0].Value, ".")
	port, err := strconv.ParseUint(portText, 10, 16)
	if !ok || !protocol.ValidUUID(sessionID) || err != nil || port == 0 {
		return "", 0, false
	}
	return sessionID, uint16(port), true
}

func (handler *previewHandler) fail(response http.ResponseWriter, err error) {
	// invalid_request: the broker refused the app origin for this credential,
	// or the app computer's Tailscale tags changed. Matched by code, not by
	// status: protocol_mismatch is 400 too, and that is not a refusal.
	var brokerErr *gatewayapi.Error
	if errors.As(err, &brokerErr) && brokerErr.Code == protocol.InvalidRequest {
		previewPage(response, http.StatusForbidden, "This app may not use this computer.")
		return
	}
	switch status := brokerErrorStatus(err); status {
	case http.StatusUnauthorized:
		previewPage(response, status, "This phone is no longer paired. Open Termivoa to pair again.")
	case http.StatusNotFound:
		previewPage(response, status, "No dev server of this session listens on that port.")
	case http.StatusConflict:
		previewPage(response, status, "The session is not running.")
	default:
		previewPage(response, http.StatusServiceUnavailable, "The computer is not available.")
	}
}

// rewrite builds the request to the dev server: its own loopback address,
// Host localhost:<port> (dev servers such as Vite reject unknown hosts), no
// Termivoa cookies and no gateway or Tailscale identity headers. Forwarded
// headers were already removed by ReverseProxy.
func (handler *previewHandler) rewrite(proxied *httputil.ProxyRequest) {
	target := proxied.In.Context().Value(previewTargetKey{}).(previewTarget)
	port := strconv.Itoa(int(target.port))
	proxied.SetURL(&url.URL{Scheme: "http", Host: net.JoinHostPort(target.host, port)})
	proxied.Out.Host = "localhost:" + port
	own := target.own
	local := "http://localhost:" + port
	header := proxied.Out.Header
	for name := range header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-pterm-") || strings.HasPrefix(lower, "tailscale-") {
			header.Del(name)
		}
	}
	if cookies := forwardedCookies(header.Values("Cookie")); cookies != "" {
		header.Set("Cookie", cookies)
	} else {
		header.Del("Cookie")
	}
	// The dev server sees a same-origin request, as if opened on its host.
	if origin := header.Get("Origin"); origin != "" && strings.EqualFold(origin, own) {
		header.Set("Origin", local)
	}
	if referer := header.Get("Referer"); referer != "" {
		if rest, ok := cutPrefixFold(referer, own); ok {
			header.Set("Referer", local+rest)
		} else {
			header.Del("Referer")
		}
	}
}

// modifyResponse keeps redirects inside the preview and refuses Termivoa
// cookie names from the dev server.
func (handler *previewHandler) modifyResponse(response *http.Response) error {
	target := response.Request.Context().Value(previewTargetKey{}).(previewTarget)
	header := response.Header
	if location := header.Get("Location"); location != "" {
		header.Set("Location", rewriteLocation(location, target.port, target.own))
	}
	if values := header.Values("Set-Cookie"); len(values) > 0 {
		header.Del("Set-Cookie")
		for _, value := range values {
			name, _, _ := strings.Cut(value, "=")
			if !termivoaCookies[strings.TrimSpace(name)] {
				header.Add("Set-Cookie", value)
			}
		}
	}
	return nil
}

// forwardedCookies drops Termivoa cookies and keeps the dev site's own
// cookies byte for byte.
func forwardedCookies(values []string) string {
	var kept []string
	for _, value := range values {
		for _, pair := range strings.Split(value, ";") {
			pair = strings.TrimSpace(pair)
			name, _, _ := strings.Cut(pair, "=")
			if pair != "" && !termivoaCookies[strings.TrimSpace(name)] {
				kept = append(kept, pair)
			}
		}
	}
	return strings.Join(kept, "; ")
}

// rewriteLocation maps an absolute redirect to the dev server itself
// (localhost, 127.0.0.1 or [::1] on its port) onto the preview origin.
// Relative and other-host redirects pass unchanged; the app's frame-src still
// keeps the frame on the preview origin.
func rewriteLocation(location string, port uint16, own string) string {
	parsed, err := url.Parse(location)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return location
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return location
	}
	if parsed.Port() != strconv.Itoa(int(port)) {
		return location
	}
	parsed.Scheme, parsed.Host = "", ""
	return own + parsed.String()
}

func cutPrefixFold(value, prefix string) (string, bool) {
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	rest := value[len(prefix):]
	if rest != "" && rest[0] != '/' && rest[0] != '?' {
		return "", false
	}
	return rest, true
}

// previewPage writes a small plain-text answer from the gateway itself. It
// has no frame-ancestors: the text is static, and it must be readable inside
// the app's frame, whose parent is another origin.
func previewPage(response http.ResponseWriter, status int, message string) {
	header := response.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("Referrer-Policy", "no-referrer")
	response.WriteHeader(status)
	_, _ = fmt.Fprintln(response, message)
}

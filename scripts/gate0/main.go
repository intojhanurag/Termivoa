// Command gate0 is the Gate 0 test harness for multi-computer
// (docs/multi-computer.md, section 6). It answers one question on a real
// phone: can a Home Screen app on origin A get, keep and send origin B's
// __Host- SameSite=Strict HttpOnly cookie on a credentialed fetch and a
// WebSocket?
//
//	go run ./scripts/gate0 -role home -peer https://peer.tailX.ts.net:8443
//	go run ./scripts/gate0 -role peer -home https://home.tailX.ts.net:8443
//
// Both roles listen on 127.0.0.1 only. See README.md for Tailscale Serve.
// It is a test tool: it is not built into pterm and shares no state with it.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

//go:embed index.html sw.js
var files embed.FS

const (
	cookieName = "__Host-gate0"
	maxAge     = 30 * 24 * time.Hour // the real pterm credential lifetime
	// VAPID contact claim; Apple requires a mailto: or https: subject.
	vapidSubject = "https://github.com/intojhanurag/phone-terminal"
)

func main() {
	role := flag.String("role", "", "home or peer")
	peer := flag.String("peer", "", "home role: the peer origin, for example https://peer.tailX.ts.net:8443")
	home := flag.String("home", "", "peer role: the home origin allowed to call it (comma-separated for more than one)")
	port := flag.Int("port", 0, "port on 127.0.0.1 (default 8441 for home, 8442 for peer)")
	selfSigned := flag.Bool("tls-self-signed", false, "serve HTTPS with a throwaway self-signed certificate (local test only)")
	flag.Parse()

	var handler http.Handler
	switch *role {
	case "home":
		origin, err := parseOrigin(*peer)
		if err != nil {
			log.Fatalf("-peer: %v", err)
		}
		if handler, err = newHome(origin); err != nil {
			log.Fatal(err)
		}
		if *port == 0 {
			*port = 8441
		}
	case "peer":
		allowed := map[string]bool{}
		for _, raw := range strings.Split(*home, ",") {
			origin, err := parseOrigin(strings.TrimSpace(raw))
			if err != nil {
				log.Fatalf("-home: %v", err)
			}
			allowed[origin] = true
		}
		handler = newPeer(allowed)
		if *port == 0 {
			*port = 8442
		}
	default:
		log.Fatal("-role must be home or peer")
	}

	server := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// HTTP/1.1 only, so the WebSocket upgrade works the same as behind Tailscale Serve.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	log.Printf("gate0 %s listening on %s (tls-self-signed=%v)", *role, server.Addr, *selfSigned)
	if !*selfSigned {
		log.Fatal(server.ListenAndServe())
	}
	cert, err := selfSignedCert()
	if err != nil {
		log.Fatal(err)
	}
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	log.Fatal(server.ListenAndServeTLS("", ""))
}

// parseOrigin accepts only an exact https origin: no path, query or fragment.
func parseOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q is not an https origin like https://name.tailX.ts.net:8443", raw)
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// echo is what the peer saw. Received is the test cookie value, empty if none.
type echo struct {
	Via          string `json:"via"`
	Received     string `json:"received"`
	Set          string `json:"set,omitempty"`
	MaxAge       int    `json:"maxAge,omitempty"`
	Expires      string `json:"expires,omitempty"`
	Origin       string `json:"origin"`
	SecFetchSite string `json:"secFetchSite"`
	Client       string `json:"client"` // the phone's tailnet IP (Tailscale Serve sets X-Forwarded-For)
}

func newPeer(allowed map[string]bool) http.Handler {
	patterns := make([]string, 0, len(allowed))
	for origin := range allowed {
		patterns = append(patterns, origin)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		e := echo{Via: r.Method + " " + r.URL.Path, Origin: origin, SecFetchSite: r.Header.Get("Sec-Fetch-Site"), Client: r.RemoteAddr}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			e.Client = xff
		}
		if c, err := r.Cookie(cookieName); err == nil {
			e.Received = c.Value
		}
		log.Printf("peer: %s origin=%q cookie=%v client=%s", e.Via, origin, e.Received != "", e.Client)

		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Add("Vary", "Origin")
		// Credentialed CORS only for the exact home origin(s); never "*", never reflected.
		if allowed[origin] {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		switch {
		case r.Method == http.MethodOptions:
			if !allowed[origin] {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Set("Access-Control-Allow-Headers", "X-Gate0")
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/ws":
			if !allowed[origin] { // CORS does not cover WebSockets; check the Origin exactly.
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
			defer cancel()
			hello, _ := json.Marshal(e)
			if conn.Write(ctx, websocket.MessageText, hello) != nil {
				return
			}
			for { // echo until the page closes the socket
				kind, data, err := conn.Read(ctx)
				if err != nil || conn.Write(ctx, kind, data) != nil {
					return
				}
			}
		case r.URL.Path == "/set":
			// Same value again if the cookie is there (like pterm's rolling refresh), else a new one.
			e.Set = e.Received
			if e.Set == "" {
				e.Set = randomHex(16)
			}
			e.MaxAge = int(maxAge.Seconds())
			e.Expires = time.Now().Add(maxAge).UTC().Format(time.RFC3339)
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: e.Set, Path: "/", MaxAge: e.MaxAge,
				Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			writeJSON(w, e)
		case r.URL.Path == "/clear":
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
				Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			writeJSON(w, e)
		case r.URL.Path == "/echo":
			writeJSON(w, e)
		default:
			http.NotFound(w, r)
		}
	})
}

// pushScopes are the service-worker scopes on home; each has its own VAPID key (check 6).
var pushScopes = []string{"/", "/a/", "/b/"}

type home struct {
	peer string
	keys map[string]webpush.Keys
	icon []byte

	mu      sync.Mutex
	subs    map[string]webpush.Subscription
	reports map[string]json.RawMessage
}

func newHome(peer string) (http.Handler, error) {
	h := &home{peer: peer, keys: map[string]webpush.Keys{}, subs: map[string]webpush.Subscription{}, reports: map[string]json.RawMessage{}}
	for _, scope := range pushScopes {
		keys, err := webpush.GenerateKeys() // in memory: after a restart, subscribe again
		if err != nil {
			return nil, err
		}
		h.keys[scope] = keys
	}
	var err error
	if h.icon, err = iconPNG(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *home) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/":
		serveFile(w, "index.html", "text/html; charset=utf-8")
	case "/sw.js", "/a/sw.js", "/b/sw.js":
		serveFile(w, "sw.js", "text/javascript")
	case "/icon.png":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(h.icon)
	case "/manifest.webmanifest":
		w.Header().Set("Content-Type", "application/manifest+json")
		writeJSON(w, map[string]any{
			"name": "Gate 0", "short_name": "Gate 0", "start_url": "/", "scope": "/", "display": "standalone",
			"background_color": "#0b1020", "theme_color": "#0b1020",
			"icons": []map[string]string{
				{"src": "/icon.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			},
		})
	case "/config":
		keys := map[string]string{}
		for scope, k := range h.keys {
			keys[scope] = k.PublicKey()
		}
		writeJSON(w, map[string]any{"peer": h.peer, "keys": keys})
	case "/subscribe":
		var body struct {
			Scope    string `json:"scope"`
			Endpoint string `json:"endpoint"`
			Keys     struct{ P256dh, Auth string }
		}
		if !readJSON(w, r, &body) {
			return
		}
		p256dh, err1 := base64.RawURLEncoding.DecodeString(body.Keys.P256dh)
		auth, err2 := base64.RawURLEncoding.DecodeString(body.Keys.Auth)
		if _, ok := h.keys[body.Scope]; !ok || err1 != nil || err2 != nil || webpush.ValidateEndpoint(body.Endpoint) != nil {
			http.Error(w, "bad subscription", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.subs[body.Scope] = webpush.Subscription{Endpoint: body.Endpoint, P256dh: p256dh, Auth: auth}
		h.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	case "/push":
		var body struct {
			Scope string `json:"scope"`
			Delay int    `json:"delay"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		h.mu.Lock()
		sub, ok := h.subs[body.Scope]
		h.mu.Unlock()
		if !ok {
			http.Error(w, "no subscription for that scope; subscribe again", http.StatusBadRequest)
			return
		}
		delay := time.Duration(min(max(body.Delay, 0), 120)) * time.Second
		go h.send(body.Scope, sub, delay)
		writeJSON(w, map[string]bool{"ok": true})
	case "/report":
		if r.Method == http.MethodPost { // the service worker reports what the peer saw
			var report map[string]any
			if !readJSON(w, r, &report) {
				return
			}
			scope, _ := report["scope"].(string)
			if _, ok := h.keys[scope]; !ok {
				http.Error(w, "bad scope", http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(report)
			h.mu.Lock()
			h.reports[scope] = raw
			h.mu.Unlock()
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		writeJSON(w, h.reports)
	default:
		http.NotFound(w, r)
	}
}

func (h *home) send(scope string, sub webpush.Subscription, delay time.Duration) {
	time.Sleep(delay)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := webpush.Send(ctx, webpush.NewHTTPClient(), h.keys[scope], sub, []byte("gate0"), vapidSubject, time.Hour, webpush.UrgencyHigh)
	log.Printf("home: push to %s: status=%d err=%v", scope, status, err)
	if err != nil || status >= 300 {
		raw, _ := json.Marshal(map[string]any{"scope": scope, "at": time.Now().UnixMilli(), "error": fmt.Sprintf("push service: status %d, %v", status, err)})
		h.mu.Lock()
		h.reports[scope] = raw
		h.mu.Unlock()
	}
}

func serveFile(w http.ResponseWriter, name, contentType string) {
	data, _ := files.ReadFile(name)
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(v); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	_ = json.NewEncoder(w).Encode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// iconPNG is a plain 512x512 icon, enough for Add to Home Screen and a WebAPK.
func iconPNG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0x0b, 0x10, 0x20, 0xff}}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(136, 136, 376, 376), &image.Uniform{color.RGBA{0x22, 0xd3, 0xee, 0xff}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}

// selfSignedCert is for the local two-host check only; browsers must run with
// --ignore-certificate-errors (or accept the warning).
func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gate0 local test"},
		DNSNames:     []string{"*.tailx.ts.net", "*.taily.ts.net", "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

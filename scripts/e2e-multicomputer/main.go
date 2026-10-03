// Command e2e-multicomputer is the HTTPS front of the multi-computer
// end-to-end harness (README.md). It is a test tool only, never shipped.
//
// It listens on one loopback port as an HTTP CONNECT proxy for a browser
// (Chrome --proxy-server, Playwright proxy) or curl --proxy. Every CONNECT to
// <host>:443 is answered by an in-process TLS server with a throwaway
// self-signed certificate, so pages keep real https origins on the default
// port (explicit ports are refused by design). Routed hosts are reverse
// proxied to a development gateway on loopback; every other test host (any
// name with "tailx" or "taily") gets a blank page, used as an attacker or
// near-miss origin. Other hosts are refused.
//
// A development gateway accepts only loopback Host and Origin values, so for
// a routed host H the proxy rewrites Host to the gateway address and an
// Origin of exactly https://H (the page's own origin) to the gateway's
// loopback origin. Every other Origin, including near misses of H and every
// other computer's origin, reaches the gateway unchanged: the cross-computer
// checks run exactly as in production.
//
// CONNECT <host>:8443 reaches the same computer's preview listener, as
// `tailscale serve --https=8443` does (-preview host=127.0.0.1:port). A
// development gateway knows its preview origin only as http://<loopback>, so
// the front maps that one name both ways and nothing else:
//   - to the preview listener: Host; an Origin of exactly https://H:8443 (the
//     dev site's own origin) becomes the listener's loopback origin; a Referer
//     of exactly https://H/ (this computer's own app) becomes the gateway's
//     loopback origin. Every other Origin and Referer, the other computer's
//     app included, arrives unchanged.
//   - to the gateway: an Origin of exactly https://H:8443 becomes the
//     listener's loopback origin, which the gateway refuses.
//   - to the browser: the listener's loopback origin in the gateway's
//     Content-Security-Policy and in GET /api/v1/preview reads https://H:8443;
//     the gateway's loopback origin in the listener's frame-ancestors reads
//     https://H.
//
// -old-home <file>: while the file exists, each routed host named on one of
// its lines answers POST /api/v1/computers/offer as a home without the route
// (Termivoa before Found on your network) does: 403 "origin not allowed" from
// admitAPI, because the request has no Origin. Check F-old uses it.
//
// `e2e-multicomputer devserver -listen 127.0.0.1:<port> -name <text>` is a
// small dev site for a session to run.
//
// `e2e-multicomputer qr -out <file>` writes the text on stdin as a QR code in
// a one-frame Y4M video: the file Chrome plays as its camera
// (--use-file-for-fake-video-capture) for the Scan QR check.
//
// Each request is logged as one JSON line without secrets: no cookie values,
// no bodies, no query strings.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"rsc.io/qr"
)

const previewPort = "8443"

type routes map[string]string

func (r routes) String() string { return fmt.Sprint(map[string]string(r)) }
func (r routes) Set(value string) error {
	host, backend, ok := strings.Cut(value, "=")
	if !ok || host == "" || backend == "" {
		return errors.New("route must be host=127.0.0.1:port")
	}
	r[strings.ToLower(host)] = backend
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "devserver" {
		devserver(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "qr" {
		flags := flag.NewFlagSet("qr", flag.ExitOnError)
		out := flags.String("out", "", "Y4M file to write")
		_ = flags.Parse(os.Args[2:])
		text, _ := io.ReadAll(os.Stdin)
		video, err := qrVideo(strings.TrimSpace(string(text)))
		if err == nil {
			err = os.WriteFile(*out, video, 0o600)
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	table, previews := routes{}, routes{}
	listen := flag.String("listen", "127.0.0.1:0", "loopback proxy address")
	logPath := flag.String("log", "", "JSON-lines request log (default stderr)")
	oldHome := flag.String("old-home", "", "file of routed hosts that answer the offer route as an old home")
	flag.Var(table, "route", "host=127.0.0.1:port of a development gateway (repeatable)")
	flag.Var(previews, "preview", "host=127.0.0.1:port of that gateway's preview listener (repeatable)")
	flag.Parse()

	logOut := io.Writer(os.Stderr)
	if *logPath != "" {
		file, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		logOut = file
	}
	front, err := newFront(table, previews, logOut)
	if err != nil {
		log.Fatal(err)
	}
	front.oldHome = *oldHome
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	if host, _, _ := net.SplitHostPort(listener.Addr().String()); host != "127.0.0.1" {
		log.Fatal("the proxy listens on 127.0.0.1 only")
	}
	// The driver reads this line: the proxy address and the certificate's
	// SPKI hash for Chrome --ignore-certificate-errors-spki-list.
	fmt.Printf("proxy %s spki %s\n", listener.Addr(), front.spki)
	log.Fatal(http.Serve(listener, http.HandlerFunc(front.connect)))
}

type front struct {
	routes   routes
	previews routes
	key      *ecdsa.PrivateKey
	spki     string
	logMu    sync.Mutex
	log      io.Writer
	certMu   sync.Mutex
	certs    map[string]*tls.Certificate
	tls      *tls.Config
	oldHome  string // see -old-home
}

func newFront(table, previews routes, logOut io.Writer) (*front, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	f := &front{routes: table, previews: previews, key: key, spki: base64.StdEncoding.EncodeToString(sum[:]), log: logOut, certs: map[string]*tls.Certificate{}}
	f.tls = &tls.Config{GetCertificate: f.certificate, NextProtos: []string{"http/1.1"}}
	return f, nil
}

// certificate makes one self-signed leaf per SNI name, all with the same key,
// so one SPKI hash covers every host.
func (f *front) certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	if name == "" {
		name = "localhost"
	}
	f.certMu.Lock()
	defer f.certMu.Unlock()
	if cert, ok := f.certs[name]; ok {
		return cert, nil
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &f.key.PublicKey, f.key)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: f.key}
	f.certs[name] = cert
	return cert, nil
}

// connect answers CONNECT host:443, and host:8443 for a host with a preview
// listener. A routed host whose gateway is down gets 502, which the browser
// reports as a network error, like a sleeping computer.
func (f *front) connect(response http.ResponseWriter, request *http.Request) {
	host, port, err := net.SplitHostPort(request.Host)
	host = strings.ToLower(host)
	table, handler := f.routes, f.site
	if _, ok := f.previews[host]; ok && port == previewPort {
		table, handler = f.previews, f.previewSite
	} else if port != "443" {
		err = errors.New("port")
	}
	if request.Method != http.MethodConnect || err != nil {
		http.Error(response, "CONNECT host:443 (or a preview host:8443) only", http.StatusBadRequest)
		return
	}
	// Only test names: the browser's own background traffic goes nowhere.
	if !strings.Contains(host, "tailx") && !strings.Contains(host, "taily") {
		http.Error(response, "not a test host", http.StatusForbidden)
		return
	}
	if backend, ok := table[host]; ok {
		probe, err := net.DialTimeout("tcp", backend, 2*time.Second)
		if err != nil {
			f.write(entry{Host: request.Host, Method: "CONNECT", Status: http.StatusBadGateway, Note: "gateway down"})
			http.Error(response, "gateway down", http.StatusBadGateway)
			return
		}
		probe.Close()
	}
	hijacker, ok := response.(http.Hijacker)
	if !ok {
		http.Error(response, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		conn.Close()
		return
	}
	tlsConn := tls.Server(&prefixConn{Conn: conn, reader: buffered.Reader}, f.tls)
	server := &http.Server{Handler: handler(host), ErrorLog: log.New(io.Discard, "", 0)}
	_ = server.Serve(&oneConnListener{conn: tlsConn, done: make(chan struct{})})
}

// site serves one https origin: a gateway for routed hosts, a blank page otherwise.
func (f *front) site(host string) http.Handler {
	backend, routed := f.routes[host]
	if !routed {
		return f.logged(host, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			fmt.Fprintf(response, "<!doctype html><meta charset=utf-8><title>%s</title><p>blank test page</p>\n", host)
		}))
	}
	// The preview origin as the gateway names it, and as the browser does.
	preview, public := f.previews[host], "https://"+host+":"+previewPort
	proxy := f.proxy(backend, func(header http.Header) {
		replaceExact(header, "Origin", "https://"+host, "http://"+backend)
		if preview != "" {
			replaceExact(header, "Origin", public, "http://"+preview)
		}
	})
	if preview != "" {
		proxy.ModifyResponse = func(response *http.Response) error {
			header := response.Header
			if policy := header.Get("Content-Security-Policy"); policy != "" {
				header.Set("Content-Security-Policy", strings.ReplaceAll(policy, "http://"+preview, public))
			}
			if response.Request.URL.Path != "/api/v1/preview" || response.StatusCode != http.StatusOK {
				return nil
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				return err
			}
			body = bytes.ReplaceAll(body, []byte("http://"+preview), []byte(public))
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			header.Set("Content-Length", strconv.Itoa(len(body)))
			return nil
		}
	}
	return f.logged(host, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/computers/offer" && f.isOldHome(host) {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusForbidden)
			io.WriteString(response, `{"error":{"code":"invalid_request","message":"origin not allowed"}}`)
			return
		}
		proxy.ServeHTTP(response, request)
	}))
}

// isOldHome reports whether host is listed in the -old-home file now.
func (f *front) isOldHome(host string) bool {
	if f.oldHome == "" {
		return false
	}
	data, err := os.ReadFile(f.oldHome)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == host {
			return true
		}
	}
	return false
}

// previewSite serves https://host:8443: the preview listener of a routed host.
func (f *front) previewSite(host string) http.Handler {
	backend, app := f.previews[host], f.routes[host]
	proxy := f.proxy(backend, func(header http.Header) {
		replaceExact(header, "Origin", "https://"+host+":"+previewPort, "http://"+backend)
		replaceExact(header, "Referer", "https://"+host+"/", "http://"+app+"/")
	})
	proxy.ModifyResponse = func(response *http.Response) error {
		if policy := response.Header.Get("Content-Security-Policy"); policy != "" {
			response.Header.Set("Content-Security-Policy", strings.ReplaceAll(policy, "frame-ancestors http://"+app, "frame-ancestors https://"+host))
		}
		return nil
	}
	return f.logged(host+":"+previewPort, proxy)
}

// replaceExact rewrites the values of one header that equal from, and no others.
func replaceExact(header http.Header, name, from, to string) {
	for index, value := range header.Values(name) {
		if value == from {
			header[name][index] = to
		}
	}
}

func (f *front) proxy(backend string, rewrite func(http.Header)) *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: backend}
	return &httputil.ReverseProxy{
		Rewrite: func(out *httputil.ProxyRequest) {
			out.SetURL(target)
			out.Out.Host = backend
			rewrite(out.Out.Header)
		},
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		// A gateway that stopped mid-connection: drop the connection, so
		// the browser sees a network error, not an HTTP answer.
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			if recorder, ok := response.(*statusRecorder); ok {
				recorder.status = http.StatusBadGateway // logged; also a request the browser gave up
			}
			if hijacker, ok := response.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			response.WriteHeader(http.StatusBadGateway)
		},
	}
}

type entry struct {
	Time         string `json:"t"`
	Host         string `json:"host"`
	Method       string `json:"method"`
	Path         string `json:"path,omitempty"`
	Origin       string `json:"origin,omitempty"`
	Referer      string `json:"referer,omitempty"` // without its query string
	SecFetchSite string `json:"secFetchSite,omitempty"`
	SecFetchMode string `json:"secFetchMode,omitempty"`
	SecFetchDest string `json:"secFetchDest,omitempty"`
	Cookie       bool   `json:"cookie"`
	Status       int    `json:"status"`
	SetCookie    string `json:"setCookie,omitempty"` // attributes only, never the value
	ACAO         string `json:"acao,omitempty"`
	ACAC         string `json:"acac,omitempty"`
	Note         string `json:"note,omitempty"`
}

func (f *front) write(e entry) {
	e.Time = time.Now().Format(time.RFC3339Nano)
	data, _ := json.Marshal(e)
	f.logMu.Lock()
	defer f.logMu.Unlock()
	f.log.Write(append(data, '\n'))
}

func (f *front) logged(host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := &statusRecorder{ResponseWriter: response}
		e := entry{
			Host: host, Method: request.Method, Path: request.URL.Path,
			Origin:       strings.Join(request.Header.Values("Origin"), ","),
			SecFetchSite: request.Header.Get("Sec-Fetch-Site"), SecFetchMode: request.Header.Get("Sec-Fetch-Mode"),
			SecFetchDest: request.Header.Get("Sec-Fetch-Dest"),
			Cookie:       request.Header.Get("Cookie") != "",
		}
		for _, referer := range request.Header.Values("Referer") {
			referer, _, _ = strings.Cut(referer, "?")
			e.Referer = strings.TrimPrefix(e.Referer+","+referer, ",")
		}
		next.ServeHTTP(recorder, request)
		header := response.Header()
		e.Status = recorder.status
		if cookie := header.Get("Set-Cookie"); cookie != "" {
			_, attributes, _ := strings.Cut(cookie, ";")
			e.SetCookie = strings.TrimSpace(attributes)
			if name, value, _ := strings.Cut(strings.SplitN(cookie, ";", 2)[0], "="); value == "" {
				e.SetCookie = name + " cleared; " + e.SetCookie
			}
		}
		e.ACAO, e.ACAC = header.Get("Access-Control-Allow-Origin"), header.Get("Access-Control-Allow-Credentials")
		f.write(e)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack keeps WebSocket upgrades working through the recorder.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("no hijack")
	}
	return hijacker.Hijack()
}

// prefixConn replays bytes the CONNECT handler already buffered.
type prefixConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// oneConnListener serves exactly one connection, then reports closed once
// that connection ends.
type oneConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = &closeNotify{Conn: l.conn, done: l.done} })
	if conn != nil {
		return conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type closeNotify struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (c *closeNotify) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

// qrVideo draws text as a QR code, black on white with a quiet zone, in the
// middle of one 640x480 Y4M frame (Chrome repeats it).
func qrVideo(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	const width, height = 640, 480
	scale := height / (code.Size + 8)
	if scale < 2 {
		return nil, errors.New("qr: the text is too long for the frame")
	}
	left, top := (width-code.Size*scale)/2, (height-code.Size*scale)/2
	luma := bytes.Repeat([]byte{235}, width*height)
	for y := 0; y < code.Size*scale; y++ {
		for x := 0; x < code.Size*scale; x++ {
			if code.Black(x/scale, y/scale) {
				luma[(top+y)*width+left+x] = 16
			}
		}
	}
	video := []byte(fmt.Sprintf("YUV4MPEG2 W%d H%d F10:1 Ip A1:1 C420jpeg\nFRAME\n", width, height))
	video = append(video, luma...)
	return append(video, bytes.Repeat([]byte{128}, width*height/2)...), nil
}

// devserver is the dev site a harness session runs: a page with a marker
// text and a load counter, a script sub-resource, a fetch endpoint, a
// WebSocket echo and a relative redirect.
func devserver(args []string) {
	flags := flag.NewFlagSet("devserver", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:0", "loopback address")
	name := flags.String("name", "dev", "text in the page marker")
	_ = flags.Parse(args)
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("dev site on %s: http://%s\n", *name, listener.Addr())
	log.Fatal(http.Serve(listener, devSite(*name)))
}

const devScript = `const set = (id, text) => { document.getElementById(id).textContent = text; };
set('sub', 'sub ok');
fetch('/ping').then((r) => r.text()).then((t) => set('fetch', 'fetch ' + t), (e) => set('fetch', 'fetch failed ' + e));
const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws');
ws.onopen = () => ws.send('echo');
ws.onmessage = (e) => set('ws', 'ws ' + e.data);
ws.onclose = (e) => set('ws', 'ws closed ' + e.code);
`

func devSite(name string) http.Handler {
	var loads atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(response, `<!doctype html><meta charset="utf-8"><title>dev site</title>
<h1>dev site on %s</h1><p id="load">load %d</p><p id="sub">sub pending</p><p id="fetch">fetch pending</p><p id="ws">ws pending</p>
<script src="/app.js"></script>
`, name, loads.Add(1))
	})
	mux.HandleFunc("/app.js", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(response, devScript)
	})
	mux.HandleFunc("/ping", func(response http.ResponseWriter, _ *http.Request) { io.WriteString(response, "pong") })
	mux.HandleFunc("/go", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Location", "/") // relative: the front has no absolute name for a dev server
		response.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/ws", func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			kind, data, err := conn.Read(request.Context())
			if err != nil || conn.Write(context.Background(), kind, data) != nil {
				return
			}
		}
	})
	return mux
}

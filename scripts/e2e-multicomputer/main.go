// Command e2e-multicomputer is the HTTPS front of the multi-computer
// end-to-end harness (README.md). It is a test tool only, never shipped.
//
// It listens on one loopback port as an HTTP CONNECT proxy for a browser
// (Chrome --proxy-server, Playwright proxy) or curl --proxy. Every CONNECT to
// <host>:443 is answered by an in-process TLS server with a throwaway
// self-signed certificate, so pages keep real https origins on the default
// port (explicit ports are refused by design). Routed hosts are reverse
// proxied to a development gateway on loopback; every other host gets a blank
// page, used as an attacker or near-miss origin.
//
// A development gateway accepts only loopback Host and Origin values, so for
// a routed host H the proxy rewrites Host to the gateway address and an
// Origin of exactly https://H (the page's own origin) to the gateway's
// loopback origin. Every other Origin, including near misses of H and every
// other computer's origin, reaches the gateway unchanged: the cross-computer
// checks run exactly as in production.
//
// Each request is logged as one JSON line without secrets: no cookie values,
// no bodies, no query strings.
package main

import (
	"bufio"
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
	"strings"
	"sync"
	"time"
)

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
	table := routes{}
	listen := flag.String("listen", "127.0.0.1:0", "loopback proxy address")
	logPath := flag.String("log", "", "JSON-lines request log (default stderr)")
	flag.Var(table, "route", "host=127.0.0.1:port of a development gateway (repeatable)")
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
	front, err := newFront(table, logOut)
	if err != nil {
		log.Fatal(err)
	}
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
	routes routes
	key    *ecdsa.PrivateKey
	spki   string
	logMu  sync.Mutex
	log    io.Writer
	certMu sync.Mutex
	certs  map[string]*tls.Certificate
	tls    *tls.Config
}

func newFront(table routes, logOut io.Writer) (*front, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	f := &front{routes: table, key: key, spki: base64.StdEncoding.EncodeToString(sum[:]), log: logOut, certs: map[string]*tls.Certificate{}}
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

// connect answers CONNECT host:443. A routed host whose gateway is down gets
// 502, which the browser reports as a network error, like a sleeping computer.
func (f *front) connect(response http.ResponseWriter, request *http.Request) {
	host, port, err := net.SplitHostPort(request.Host)
	if request.Method != http.MethodConnect || err != nil || port != "443" {
		http.Error(response, "CONNECT host:443 only", http.StatusBadRequest)
		return
	}
	host = strings.ToLower(host)
	if backend, ok := f.routes[host]; ok {
		probe, err := net.DialTimeout("tcp", backend, 2*time.Second)
		if err != nil {
			f.write(entry{Host: host, Method: "CONNECT", Status: http.StatusBadGateway, Note: "gateway down"})
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
	server := &http.Server{Handler: f.site(host), ErrorLog: log.New(io.Discard, "", 0)}
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
	target := &url.URL{Scheme: "http", Host: backend}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(out *httputil.ProxyRequest) {
			out.SetURL(target)
			out.Out.Host = backend
			for index, origin := range out.Out.Header.Values("Origin") {
				if origin == "https://"+host {
					out.Out.Header["Origin"][index] = "http://" + backend
				}
			}
		},
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		// A gateway that stopped mid-connection: drop the connection, so
		// the browser sees a network error, not an HTTP answer.
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			if hijacker, ok := response.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			response.WriteHeader(http.StatusBadGateway)
		},
	}
	return f.logged(host, proxy)
}

type entry struct {
	Time         string `json:"t"`
	Host         string `json:"host"`
	Method       string `json:"method"`
	Path         string `json:"path,omitempty"`
	Origin       string `json:"origin,omitempty"`
	SecFetchSite string `json:"secFetchSite,omitempty"`
	SecFetchMode string `json:"secFetchMode,omitempty"`
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
			Cookie: request.Header.Get("Cookie") != "",
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

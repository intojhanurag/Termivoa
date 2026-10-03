package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The front must rewrite only the page's exact own origin; every other
// Origin, near misses included, reaches the gateway unchanged.
func TestSiteRewritesOnlyTheOwnOrigin(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Host+" "+strings.Join(r.Header.Values("Origin"), ","))
	}))
	defer gateway.Close()
	backend := strings.TrimPrefix(gateway.URL, "http://")
	f, err := newFront(routes{"vps.tailx.ts.net": backend}, routes{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	site := f.site("vps.tailx.ts.net")
	for origin, want := range map[string]string{
		"https://vps.tailx.ts.net":     "http://" + backend,
		"https://mac.tailx.ts.net":     "https://mac.tailx.ts.net",
		"https://vps.tailx.ts.net:443": "https://vps.tailx.ts.net:443",
		"https://vps.tailx.ts.net.":    "https://vps.tailx.ts.net.",
		"https://xvps.tailx.ts.net":    "https://xvps.tailx.ts.net",
		"http://vps.tailx.ts.net":      "http://vps.tailx.ts.net",
		"null":                         "null",
	} {
		request := httptest.NewRequest(http.MethodGet, "https://vps.tailx.ts.net/api/v1/settings", nil)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		site.ServeHTTP(recorder, request)
		if got := recorder.Body.String(); got != backend+" "+want {
			t.Errorf("Origin %q: gateway got %q, want %q", origin, got, backend+" "+want)
		}
	}
}

// The :8443 route reaches the preview listener. Only the dev site's own
// Origin and this computer's own app Referer are mapped to loopback; the
// other computer's app Referer arrives as the browser sent it.
func TestPreviewRouteRewritesOnlyOwnNames(t *testing.T) {
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors "+strings.TrimSuffix(r.Header.Get("Referer"), "/"))
		io.WriteString(w, r.Host+" "+strings.Join(r.Header.Values("Origin"), ",")+" "+strings.Join(r.Header.Values("Referer"), ","))
	}))
	defer listener.Close()
	backend := strings.TrimPrefix(listener.URL, "http://")
	const app = "127.0.0.1:1"
	var logged bytes.Buffer
	f, err := newFront(routes{"vps.tailx.ts.net": app}, routes{"vps.tailx.ts.net": backend}, &logged)
	if err != nil {
		t.Fatal(err)
	}
	site := f.previewSite("vps.tailx.ts.net")
	for _, test := range []struct{ origin, referer, want, ancestors string }{
		{"https://vps.tailx.ts.net:8443", "", "http://" + backend + " ", ""},
		{"https://vps.tailx.ts.net", "", "https://vps.tailx.ts.net ", ""},
		{"https://mac.tailx.ts.net:8443", "", "https://mac.tailx.ts.net:8443 ", ""},
		{"", "https://vps.tailx.ts.net/", " http://" + app + "/", "https://vps.tailx.ts.net"},
		{"", "https://mac.tailx.ts.net/", " https://mac.tailx.ts.net/", "https://mac.tailx.ts.net"},
		{"", "https://evil.tailx.ts.net/", " https://evil.tailx.ts.net/", "https://evil.tailx.ts.net"},
		{"", "https://vps.tailx.ts.net/x?secret=1", " https://vps.tailx.ts.net/x?secret=1", "https://vps.tailx.ts.net/x?secret=1"},
	} {
		request := httptest.NewRequest(http.MethodGet, "https://vps.tailx.ts.net:8443/__termivoa/select?session=s", nil)
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		if test.referer != "" {
			request.Header.Set("Referer", test.referer)
		}
		request.Header.Set("Sec-Fetch-Dest", "iframe")
		recorder := httptest.NewRecorder()
		site.ServeHTTP(recorder, request)
		if got := recorder.Body.String(); got != backend+" "+test.want {
			t.Errorf("Origin %q Referer %q: listener got %q, want %q", test.origin, test.referer, got, backend+" "+test.want)
		}
		if got := recorder.Header().Get("Content-Security-Policy"); got != strings.TrimSpace("default-src 'none'; frame-ancestors "+test.ancestors) {
			t.Errorf("Referer %q: browser got policy %q", test.referer, got)
		}
	}
	// The log names the port, the Referer without its query, and the fetch destination.
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	var last entry
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Host != "vps.tailx.ts.net:8443" || last.Referer != "https://vps.tailx.ts.net/x" || last.SecFetchDest != "iframe" || strings.Contains(logged.String(), "secret") {
		t.Errorf("log entry %+v", last)
	}
}

// The gateway names its preview origin as loopback; the browser must read
// https://H:8443, and the gateway must see the dev site's Origin as its own
// preview origin (which it refuses).
func TestSiteMapsThePreviewOrigin(t *testing.T) {
	const preview = "127.0.0.1:2"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "connect-src 'self' http://"+preview+" https://mac.tailx.ts.net:8443; frame-src http://"+preview)
		io.WriteString(w, `{"origin":"http://`+preview+`","got":"`+r.Header.Get("Origin")+`"}`)
	}))
	defer gateway.Close()
	backend := strings.TrimPrefix(gateway.URL, "http://")
	f, err := newFront(routes{"vps.tailx.ts.net": backend}, routes{"vps.tailx.ts.net": preview}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://vps.tailx.ts.net/api/v1/preview", nil)
	request.Header.Set("Origin", "https://vps.tailx.ts.net:8443")
	recorder := httptest.NewRecorder()
	f.site("vps.tailx.ts.net").ServeHTTP(recorder, request)
	if got, want := recorder.Body.String(), `{"origin":"https://vps.tailx.ts.net:8443","got":"https://vps.tailx.ts.net:8443"}`; got != want {
		// "got" is the gateway's view (loopback), mapped back like every body of this route.
		t.Errorf("body %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Content-Security-Policy"), "connect-src 'self' https://vps.tailx.ts.net:8443 https://mac.tailx.ts.net:8443; frame-src https://vps.tailx.ts.net:8443"; got != want {
		t.Errorf("policy %q, want %q", got, want)
	}
}

// CONNECT to :8443 is accepted only for a host with a preview listener.
func TestConnectPorts(t *testing.T) {
	f, err := newFront(routes{}, routes{"vps.tailx.ts.net": "127.0.0.1:1"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]int{
		"mac.tailx.ts.net:8443": http.StatusBadRequest,
		"vps.tailx.ts.net:8080": http.StatusBadRequest,
		"vps.tailx.ts.net:8443": http.StatusBadGateway, // accepted; nothing listens there
	} {
		request := httptest.NewRequest(http.MethodConnect, "http://"+target, nil)
		request.Host = target
		recorder := httptest.NewRecorder()
		f.connect(recorder, request)
		if recorder.Code != want {
			t.Errorf("CONNECT %s: %d, want %d", target, recorder.Code, want)
		}
	}
}

func TestQRVideo(t *testing.T) {
	video, err := qrVideo("https://vps.tailx.ts.net/add-computer#grant=" + strings.Repeat("G", 43) + "&via=https%3A%2F%2Fmac.tailx.ts.net")
	header := "YUV4MPEG2 W640 H480 F10:1 Ip A1:1 C420jpeg\nFRAME\n"
	if err != nil || !strings.HasPrefix(string(video), header) || len(video) != len(header)+640*480*3/2 {
		t.Fatalf("qrVideo: %d bytes, %v", len(video), err)
	}
	// White at the frame corner (the quiet zone), black inside the top-left finder pattern.
	if frame := video[len(header):]; frame[0] != 235 || !strings.Contains(string(frame[:640*480]), "\x10") {
		t.Fatal("qrVideo: no black-on-white code in the frame")
	}
	if _, err := qrVideo(strings.Repeat("x", 3000)); err == nil {
		t.Fatal("qrVideo: a text that cannot fit was accepted")
	}
}

func TestDevSite(t *testing.T) {
	server := httptest.NewServer(devSite("vps"))
	defer server.Close()
	get := func(path string) string {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	if first, second := get("/"), get("/go"); !strings.Contains(first, "dev site on vps") || !strings.Contains(first, "load 1") || !strings.Contains(second, "load 2") {
		t.Errorf("pages: %q then %q", first, second)
	}
	if get("/ping") != "pong" || !strings.Contains(get("/app.js"), "WebSocket") {
		t.Error("ping or script")
	}
}

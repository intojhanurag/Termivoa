package main

import (
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
	f, err := newFront(routes{"vps.tailx.ts.net": backend}, io.Discard)
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

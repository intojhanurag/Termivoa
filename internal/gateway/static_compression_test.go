package gateway

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticPrecompressedRepresentations(t *testing.T) {
	handler := newTestHandler(t, nil)
	raw := []byte(strings.Repeat("const example = 1;\n", 1000))
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	files := handler.assets.(fstest.MapFS)
	files["assets/large.js"] = &fstest.MapFile{Data: raw}
	files["assets/large.js.gz"] = &fstest.MapFile{Data: encoded.Bytes()}
	for _, test := range []struct {
		accept string
		gzip   bool
	}{
		{"gzip, br", true}, {"GZIP; q=0.5", true}, {"*;q=0.5", true},
		{"gzip;q=0, *;q=1", false}, {"br", false}, {"", false},
		{"gzip;q=garbage", false}, {"gzip;q=NaN", false}, {"gzip;q=1.1", false},
	} {
		response := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {test.accept}}})
		want := raw
		if test.gzip {
			want = encoded.Bytes()
		}
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) || response.Header().Get("Content-Length") != strconv.Itoa(len(want)) {
			t.Fatalf("accept %q: %d, headers=%v bytes=%d", test.accept, response.Code, response.Header(), response.Body.Len())
		}
		if (response.Header().Get("Content-Encoding") == "gzip") != test.gzip || strings.Join(response.Header().Values("Vary"), ",") != "Accept-Encoding,Range" || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Fatalf("headers=%v", response.Header())
		}
		if test.gzip {
			reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			_ = reader.Close()
			if !bytes.Equal(decoded, raw) {
				t.Fatal("compressed contents differ")
			}
		}
		etag := response.Header().Get("ETag")
		if etag == "" {
			t.Fatal("missing representation ETag")
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			notModified := do(handler, method, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {test.accept}, "If-None-Match": {etag}}})
			if notModified.Code != 304 || notModified.Body.Len() != 0 {
				t.Fatalf("conditional %s: %d", method, notModified.Code)
			}
		}
		for _, header := range []http.Header{{"If-Match": {`"other"`}}, {"If-None-Match": {etag}}} {
			header.Set("Accept-Encoding", test.accept)
			conditional := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: header})
			if (conditional.Code != 412 && conditional.Code != 304) || conditional.Header().Get("Content-Length") != "" && conditional.Header().Get("Content-Length") != strconv.Itoa(conditional.Body.Len()) {
				t.Fatalf("conditional %v: %d, Content-Length %q, body %d", header, conditional.Code, conditional.Header().Get("Content-Length"), conditional.Body.Len())
			}
		}
		head := do(handler, http.MethodHead, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {test.accept}}})
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(want)) || head.Header().Get("ETag") != etag {
			t.Fatalf("HEAD: %d %v", head.Code, head.Header())
		}
		ranged := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {test.accept}, "Range": {"bytes=0-9"}}})
		if ranged.Code != 206 || !bytes.Equal(ranged.Body.Bytes(), raw[:10]) || ranged.Header().Get("Content-Range") != "bytes 0-9/"+strconv.Itoa(len(raw)) || ranged.Header().Get("Content-Encoding") != "" {
			t.Fatalf("range: %d %v", ranged.Code, ranged.Header())
		}
	}
	for _, accept := range []string{"identity;q=0, br", "gzip;q=0, identity;q=0", "*;q=0"} {
		response := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {accept}}})
		if response.Code != 406 || response.Header().Get("Content-Encoding") != "" {
			t.Fatalf("unacceptable %q: %d", accept, response.Code)
		}
	}
	multipart := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-9,20-29"}}})
	if multipart.Code != 206 || multipart.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(multipart.Header().Get("Content-Type"), "multipart/byteranges;") {
		t.Fatalf("multipart: %d %v", multipart.Code, multipart.Header())
	}
	encodedRange := do(handler, http.MethodGet, "/assets/large.js", "", requestOptions{header: http.Header{"Accept-Encoding": {"gzip, identity;q=0"}, "Range": {"bytes=0-9,20-29"}}})
	if encodedRange.Code != 200 || encodedRange.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(encodedRange.Body.Bytes(), encoded.Bytes()) {
		t.Fatalf("encoded range: %d %v", encodedRange.Code, encodedRange.Header())
	}
	for _, target := range []string{"/assets/large.js.gz", "/assets/large.js.map", "/.vite/manifest.json"} {
		if response := do(handler, http.MethodGet, target, "", requestOptions{header: http.Header{"Accept-Encoding": {"gzip"}}}); response.Code != 404 {
			t.Fatalf("hidden %s: %d", target, response.Code)
		}
	}
	for _, target := range []string{"/", "/sw.js", "/manifest.webmanifest", "/api/v1/unknown", "/assets/font-77aa.woff2"} {
		response := do(handler, http.MethodGet, target, "", requestOptions{header: http.Header{"Accept-Encoding": {"gzip"}}})
		if response.Header().Get("Content-Encoding") != "" {
			t.Fatalf("unexpected compression: %s", target)
		}
	}
}

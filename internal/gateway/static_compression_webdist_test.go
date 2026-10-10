//go:build webdist

package gateway

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/webassets"
)

func TestProductionAssetCompression(t *testing.T) {
	handler, err := newServer(Config{Profile: platform.ProductionProfile, Origins: []string{testOrigin}}, testBound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(webassets.FS, "assets/TerminalView-*.js")
	if err != nil || len(names) == 0 {
		t.Fatalf("production TerminalView asset missing: %v", err)
	}
	for _, name := range names {
		raw, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		response := do(handler, http.MethodGet, "/"+name, "", requestOptions{header: http.Header{"Accept-Encoding": {"gzip, br"}}})
		if response.Code != 200 || response.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("production compression: %d %v", response.Code, response.Header())
		}
		reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		_ = reader.Close()
		if !bytes.Equal(decoded, raw) || response.Body.Len() >= len(raw) {
			t.Fatal("compressed production contents or transfer size incorrect")
		}
		t.Logf("TerminalView production bytes: identity=%d gzip=%d", len(raw), response.Body.Len())
	}
}

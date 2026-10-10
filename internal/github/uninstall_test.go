package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoredLoginCleanupRevokesBothBeforeDeletion(t *testing.T) {
	dir := t.TempDir()
	acc := &account{login: "fixture", access: newSecret("fixture-access"), refresh: newSecret("fixture-refresh")}
	if err := saveAccount(dir, acc); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/credentials/revoke" || r.Header.Get("Authorization") != "" {
			t.Errorf("incorrect revoke request")
		}
		if _, err := os.Stat(filepath.Join(dir, tokenFileName)); err != nil {
			t.Error("token was deleted before revoke")
		}
		var body struct {
			Credentials []string `json:"credentials"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Credentials) != 2 || body.Credentials[0] != "fixture-access" || body.Credentials[1] != "fixture-refresh" {
			t.Error("incorrect synthetic token pair")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	opts := defaultOptions
	opts.api = server.URL
	opts.open = true
	found, revoked, err := disconnectStoredLogin(context.Background(), dir, opts)
	if !found || !revoked || err != nil || calls.Load() != 1 {
		t.Fatalf("result found=%v revoked=%v err=%v calls=%d", found, revoked, err, calls.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, tokenFileName)); !os.IsNotExist(err) {
		t.Fatal("login not deleted")
	}
	found, revoked, err = disconnectStoredLogin(context.Background(), dir, opts)
	if found || !revoked || err != nil || calls.Load() != 1 {
		t.Fatal("missing login made a request")
	}
}
func TestStoredLoginCleanupOutageTimeoutAndRedirect(t *testing.T) {
	for _, mode := range []string{"server failure", "timeout", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := saveAccount(dir, &account{login: "fixture", access: newSecret("fixture-access")}); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "/other", 302)
				default:
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			opts := defaultOptions
			opts.api = server.URL
			opts.open = true
			opts.requestTimeout = 50 * time.Millisecond
			started := time.Now()
			found, revoked, err := disconnectStoredLogin(context.Background(), dir, opts)
			if !found || revoked || err != nil || calls.Load() != 1 {
				t.Fatalf("unexpected result found=%v revoked=%v err=%v calls=%d", found, revoked, err, calls.Load())
			}
			if time.Since(started) > time.Second {
				t.Fatal("cleanup exceeded bounded fixture deadline")
			}
			if _, err := os.Stat(filepath.Join(dir, tokenFileName)); !os.IsNotExist(err) {
				t.Fatal("outage retained consented local login")
			}
		})
	}
}
func TestStoredLoginCleanupLocalFailureAndPreCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, tokenFileName)
	if err := os.WriteFile(path, []byte("fixture-secret-invalid-json"), 0600); err != nil {
		t.Fatal(err)
	}
	found, revoked, err := DisconnectStoredLogin(context.Background(), dir)
	if found || revoked || err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("bad local read result")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("damaged login was discarded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := DisconnectStoredLogin(ctx, dir); err != context.Canceled {
		t.Fatal("pre-canceled cleanup did not stop")
	}
}

func TestStoredLoginCleanupParentCancellationKeepsLogin(t *testing.T) {
	dir := t.TempDir()
	if err := saveAccount(dir, &account{login: "fixture", access: newSecret("fixture-access")}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	opts := defaultOptions
	opts.api = server.URL
	opts.open = true
	opts.requestTimeout = time.Second
	done := make(chan error, 1)
	go func() { _, _, err := disconnectStoredLogin(ctx, dir, opts); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("parent cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup did not cancel")
	}
	if _, err := os.Stat(filepath.Join(dir, tokenFileName)); err != nil {
		t.Fatal("parent cancellation discarded login")
	}
}

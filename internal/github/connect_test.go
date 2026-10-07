//go:build darwin || linux

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestSecretIsRedacted(t *testing.T) {
	acc := &account{login: "octo", access: newSecret("ghu_SECRET"), refresh: newSecret("ghr_SECRET")}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		for _, value := range []any{acc.access, acc, *acc} {
			if out := fmt.Sprintf(verb, value); strings.Contains(out, "SECRET") || strings.Contains(out, "5345435245") {
				t.Fatalf("%s printed %q", verb, out)
			}
		}
	}
	if out := fmt.Sprintf("%v %+v %#v", acc.access, acc.access, acc.access); out != "[REDACTED] [REDACTED] [REDACTED]" {
		t.Fatalf("got %q", out)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("x", "token", acc.access, "account", acc)
	if strings.Contains(logged.String(), "SECRET") || !strings.Contains(logged.String(), "[REDACTED]") {
		t.Fatalf("slog printed %s", logged.String())
	}
	if _, err := json.Marshal(acc.access); err == nil {
		t.Fatal("a secret encoded to JSON")
	}
}

func TestTokenFileIsPrivateAndAtomic(t *testing.T) {
	dir := t.TempDir()
	acc := &account{login: "octo", id: 42, access: newSecret("ghu_1"), refresh: newSecret("ghr_1"),
		accessExpiry: time.Unix(2000000000, 0), refreshExpiry: time.Unix(2100000000, 0), updated: time.Unix(1900000000, 0)}
	if err := saveAccount(dir, acc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, tokenFileName)
	first, err := os.Stat(path)
	if err != nil || first.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", first.Mode(), err)
	}
	acc.access = newSecret("ghu_2")
	if err := saveAccount(dir, acc); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Stat(path)
	// A rename puts a new file in place; a write in place keeps the inode.
	if first.Sys().(*syscall.Stat_t).Ino == second.Sys().(*syscall.Stat_t).Ino || second.Mode().Perm() != 0o600 {
		t.Fatal("the file was not replaced by a rename of a 0600 temp file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
	loaded, err := loadAccount(dir)
	if err != nil || loaded.login != "octo" || loaded.id != 42 || loaded.access.reveal() != "ghu_2" ||
		loaded.refresh.reveal() != "ghr_1" || !loaded.accessExpiry.Equal(acc.accessExpiry) || !loaded.refreshExpiry.Equal(acc.refreshExpiry) {
		t.Fatalf("loaded %+v, %v", loaded, err)
	}
	if err := deleteAccount(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file is still there")
	}
	if loaded, err := loadAccount(dir); loaded != nil || err != nil {
		t.Fatal("a deleted login loaded")
	}
}

func TestNewIsOffWithoutClientID(t *testing.T) {
	saved := ClientID
	ClientID = "" // a build without an app; this build has one
	t.Cleanup(func() { ClientID = saved })
	service, err := newService(Config{Home: t.TempDir(), DataDir: t.TempDir()}, Deps{}, defaultOptions)
	if service != nil || err != nil {
		t.Fatalf("got %v, %v", service, err)
	}
	var none *Service
	if none.Info().State != protocol.HTTPGitHubStateNotConnected || none.State("a").State != protocol.HTTPGitHubStateNotConnected {
		t.Fatal("a nil service answered a state")
	}
	none.Close()
}

func TestConnectOwnedByOneDeviceAndSlowDown(t *testing.T) {
	fake := newFake(t)
	fake.with(func() {
		fake.pollAnswers = []any{
			map[string]any{"error": "authorization_pending"},
			map[string]any{"error": "slow_down"},
			500, // GitHub down for one poll: keep polling
			"tokens",
		}
	})
	h := newHarness(t, fake, false, nil)
	response, err := h.service.Connect(context.Background(), "phone-a")
	if err != nil || response.State != protocol.Connecting || response.Connect == nil || response.Connect.UserCode != "ABCD-1234" {
		t.Fatalf("connect: %+v, %v", response, err)
	}
	other, err := h.service.Connect(context.Background(), "phone-b")
	if !errors.Is(err, ErrBusy) || other.Connect != nil || other.State != protocol.Connecting {
		t.Fatalf("other device: %+v, %v", other, err)
	}
	if again, err := h.service.Connect(context.Background(), "phone-a"); err != nil || again.Connect == nil {
		t.Fatalf("same device again: %+v, %v", again, err)
	}
	if h.service.State("phone-b").Connect != nil {
		t.Fatal("another device saw the code")
	}
	waitFor(t, "ready", func() bool { return h.service.State("phone-a").State == protocol.HTTPGitHubStateReady })
	state := h.service.State("phone-a")
	if state.Login == nil || *state.Login != "octo" || state.Problem != nil || state.Connect != nil {
		t.Fatalf("state %+v", state)
	}
	fake.mu.Lock()
	polls := append([]time.Time(nil), fake.polls...)
	fake.mu.Unlock()
	if len(polls) != 4 {
		t.Fatalf("%d polls", len(polls))
	}
	unit := 10 * time.Millisecond
	if gap := polls[1].Sub(polls[0]); gap < unit {
		t.Fatalf("first gap %v", gap)
	}
	if gap := polls[2].Sub(polls[1]); gap < 6*unit {
		t.Fatalf("gap after slow_down %v, want at least 6 intervals", gap)
	}
	if gap := polls[3].Sub(polls[2]); gap < 6*unit {
		t.Fatalf("the slower interval did not stay: %v", gap)
	}
	loaded, err := loadAccount(h.config.DataDir)
	if err != nil || loaded == nil || loaded.access.reveal() != fake.token() {
		t.Fatalf("file: %v", err)
	}
}

func TestSlowDownUsesGitHubsInterval(t *testing.T) {
	fake := newFake(t)
	fake.with(func() { fake.pollAnswers = []any{map[string]any{"error": "slow_down", "interval": 12}, "tokens"} })
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready", func() bool { return h.service.State("a").State == protocol.HTTPGitHubStateReady })
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if gap := fake.polls[1].Sub(fake.polls[0]); gap < 12*10*time.Millisecond {
		t.Fatalf("gap %v", gap)
	}
}

func TestConnectProblems(t *testing.T) {
	cases := []struct {
		name    string
		answers []any
		expires int
		want    protocol.HTTPGitHubProblem
	}{
		{"expired_token", []any{map[string]any{"error": "expired_token"}}, 900, protocol.CodeExpired},
		{"token_expired", []any{map[string]any{"error": "token_expired"}}, 900, protocol.CodeExpired},
		{"incorrect_device_code", []any{map[string]any{"error": "incorrect_device_code"}}, 900, protocol.CodeExpired},
		{"access_denied", []any{map[string]any{"error": "access_denied"}}, 900, protocol.Denied},
		{"device_flow_disabled", []any{map[string]any{"error": "device_flow_disabled"}}, 900, protocol.HTTPGitHubProblemGitHubDown},
		{"incorrect_client_credentials", []any{map[string]any{"error": "incorrect_client_credentials"}}, 900, protocol.HTTPGitHubProblemGitHubDown},
		{"unsupported_grant_type", []any{map[string]any{"error": "unsupported_grant_type"}}, 900, protocol.HTTPGitHubProblemGitHubDown},
		{"expiry", nil, 4, protocol.CodeExpired},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fake := newFake(t)
			fake.with(func() { fake.pollAnswers = test.answers })
			fake.with(func() { fake.deviceCode["expires_in"] = test.expires })
			h := newHarness(t, fake, false, nil)
			if _, err := h.service.Connect(context.Background(), "a"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the end", func() bool { return h.service.State("a").State != protocol.Connecting })
			state := h.service.State("a")
			if state.State != protocol.HTTPGitHubStateNotConnected || state.Problem == nil || *state.Problem != test.want {
				t.Fatalf("state %+v problem %v", state, state.Problem)
			}
			// A new Connect clears the problem.
			if again, _ := h.service.Connect(context.Background(), "a"); again.Problem != nil {
				t.Fatal("a new Connect kept the problem")
			}
		})
	}
}

func TestConnectRefusesABadUserCode(t *testing.T) {
	fake := newFake(t)
	fake.with(func() { fake.deviceCode["user_code"] = "abcd-1234<script>" })
	h := newHarness(t, fake, false, nil)
	response, err := h.service.Connect(context.Background(), "a")
	if !errors.Is(err, ErrGitHubDown) || response.State != protocol.HTTPGitHubStateNotConnected || response.Connect != nil {
		t.Fatalf("%+v, %v", response, err)
	}
}

func TestCancelNeverDeletesALogin(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a poll", func() bool { return fake.count("/login/oauth/access_token") > 0 })
	if state := h.service.Cancel("a"); state.State != protocol.HTTPGitHubStateNotConnected || state.Problem != nil {
		t.Fatalf("cancel: %+v", state)
	}
	polls := fake.count("/login/oauth/access_token")
	time.Sleep(100 * time.Millisecond)
	if fake.count("/login/oauth/access_token") > polls+1 {
		t.Fatal("the poll went on after Cancel")
	}

	fake.with(func() { fake.pollAnswers = []any{"tokens"} })
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready", func() bool { return h.service.State("a").State == protocol.HTTPGitHubStateReady })
	if state := h.service.Cancel("a"); state.State != protocol.HTTPGitHubStateReady || state.Login == nil {
		t.Fatalf("cancel after the end: %+v", state)
	}
	if loaded, _ := loadAccount(h.config.DataDir); loaded == nil {
		t.Fatal("cancel deleted the login")
	}
}

func TestCloseEndsTheConnectPoll(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { h.service.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the poll")
	}
}

// expiring makes the stored access token expire in a minute.
func expiring(t *testing.T, h *harness, refreshLeft time.Duration) {
	t.Helper()
	h.service.mu.Lock()
	acc := *h.service.account
	acc.accessExpiry = time.Now().Add(time.Minute)
	acc.refreshExpiry = time.Now().Add(refreshLeft)
	h.service.account = &acc
	h.service.mu.Unlock()
}

func TestRefreshRotatesAndCommitsFirst(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	login, token, expires, err := h.service.Credential(context.Background())
	if err != nil || login != "octo" || token != "ghu_start" || time.Until(expires) < 7*time.Hour {
		t.Fatalf("fresh token: %q %q %v %v", login, token, expires, err)
	}
	if fake.refreshes() != "" {
		t.Fatal("a fresh token was refreshed")
	}
	expiring(t, h, 30*24*time.Hour)
	_, token, _, err = h.service.Credential(context.Background())
	if err != nil || token != "ghu_1" {
		t.Fatalf("refresh: %q %v", token, err)
	}
	if loaded, _ := loadAccount(h.config.DataDir); loaded == nil || loaded.access.reveal() != "ghu_1" || loaded.refresh.reveal() != "ghr_1" {
		t.Fatal("the new pair was not in the file when Credential returned")
	}
	expiring(t, h, 30*24*time.Hour)
	if _, token, _, err = h.service.Credential(context.Background()); err != nil || token != "ghu_2" {
		t.Fatalf("second refresh: %q %v", token, err)
	}
	if fake.refreshes() != "ghr_start,ghr_1" {
		t.Fatalf("refresh tokens sent: %v", fake.refreshes())
	}
}

func TestRefreshTriesOnceMoreAfterALostAnswer(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, func(_ *Config, opts *options) { opts.requestTimeout = 300 * time.Millisecond })
	expiring(t, h, 30*24*time.Hour)
	fake.with(func() {
		fake.refreshHook = func(call int, w http.ResponseWriter) bool {
			if call == 1 {
				time.Sleep(time.Second) // the answer is lost
				return true
			}
			return false
		}
	})
	_, token, _, err := h.service.Credential(context.Background())
	if err != nil || token != "ghu_1" {
		t.Fatalf("%q %v", token, err)
	}
	if fake.refreshes() != "ghr_start,ghr_start" {
		t.Fatalf("refresh tokens sent: %v", fake.refreshes())
	}
}

func TestRefreshNetworkDownKeepsTheLogin(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	expiring(t, h, 30*24*time.Hour)
	fake.with(func() {
		fake.refreshHook = func(_ int, w http.ResponseWriter) bool { w.WriteHeader(http.StatusBadGateway); return true }
	})
	if _, _, _, err := h.service.Credential(context.Background()); !errors.Is(err, ErrGitHubDown) {
		t.Fatal(err)
	}
	if h.service.State("a").Login == nil {
		t.Fatal("a GitHub outage deleted the login")
	}
}

func TestBadRefreshToken(t *testing.T) {
	for _, test := range []struct {
		name string
		left time.Duration
		want protocol.HTTPGitHubProblem
	}{
		{"not old: another program", 30 * 24 * time.Hour, protocol.OtherProgram},
		{"old: ended", 30 * time.Second, protocol.Ended},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFake(t)
			h := newHarness(t, fake, true, nil)
			expiring(t, h, test.left)
			fake.with(func() { fake.refresh = "ghr_thief" })
			if _, _, _, err := h.service.Credential(context.Background()); !errors.Is(err, ErrNotConnected) {
				t.Fatal(err)
			}
			state := h.service.State("a")
			if state.State != protocol.HTTPGitHubStateNotConnected || state.Problem == nil || *state.Problem != test.want {
				t.Fatalf("%+v", state)
			}
			if _, err := os.Stat(filepath.Join(h.config.DataDir, tokenFileName)); !os.IsNotExist(err) {
				t.Fatal("the file is still there")
			}
		})
	}
}

func TestUnauthorizedEndsTheLogin(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	fake.with(func() { fake.access = "ghu_revoked_elsewhere" })
	if _, err := h.service.Repos(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	state := h.service.State("a")
	if state.State != protocol.HTTPGitHubStateNotConnected || state.Problem == nil || *state.Problem != protocol.Ended {
		t.Fatalf("%+v", state)
	}
}

func TestDisconnectRevokesAndDeletes(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := newFake(t)
			fake.with(func() { fake.revokeStatus = status })
			h := newHarness(t, fake, true, nil)
			state, revoked, err := h.service.Disconnect(context.Background(), "a")
			if err != nil || revoked != (status == http.StatusAccepted) || state.State != protocol.HTTPGitHubStateNotConnected || state.Login != nil || state.Problem != nil {
				t.Fatalf("%+v %v %v", state, revoked, err)
			}
			fake.with(func() {
				if len(fake.revokes) != 1 || strings.Join(fake.revokes[0].credentials, ",") != "ghu_start,ghr_start" || fake.revokes[0].authorization != "" {
					t.Errorf("revoke calls %+v", fake.revokes)
				}
			})
			if _, err := os.Stat(filepath.Join(h.config.DataDir, tokenFileName)); !os.IsNotExist(err) {
				t.Fatal("the file is still there")
			}
		})
	}
}

func TestClientRefusesOtherHosts(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	for _, target := range []string{"http://example.com/x", "https://" + strings.TrimPrefix(fake.server.URL, "http://") + "/x", "http://user@" + strings.TrimPrefix(fake.server.URL, "http://")} {
		if _, err := h.service.do(context.Background(), request{method: http.MethodGet, url: target}, nil); !errors.Is(err, errHost) {
			t.Fatalf("%s: %v", target, err)
		}
	}
	production := &Service{opts: defaultOptions}
	for target, want := range map[string]bool{
		"https://github.com/login/device/code": true, "https://api.github.com/user": true,
		"http://api.github.com/user": false, "https://gist.github.com/": false, "https://github.com.evil/": false,
	} {
		parsed, _ := http.NewRequest(http.MethodGet, target, nil)
		if production.allowed(parsed.URL) != want {
			t.Fatalf("%s allowed=%v", target, !want)
		}
	}
	if err := publicAddressOnly("tcp", "127.0.0.1:443", nil); err == nil {
		t.Fatal("loopback allowed")
	}
	if err := publicAddressOnly("tcp", "140.82.112.3:443", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDamagedTokenFileReadsAsNotConnected(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, func(config *Config, _ *options) {
		_ = os.WriteFile(filepath.Join(config.DataDir, tokenFileName), []byte("{not json"), 0o600)
	})
	if state := h.service.State("a"); state.State != protocol.HTTPGitHubStateNotConnected {
		t.Fatalf("%+v", state)
	}
}

func TestDisconnectDuringLoginRevokesTheNewPair(t *testing.T) {
	fake := newFake(t)
	fake.with(func() {
		fake.pollAnswers = []any{"tokens"}
		fake.apiDelay = 400 * time.Millisecond
	})
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the /user call", func() bool { return fake.count("/user") > 0 })
	if _, _, err := h.service.Disconnect(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the revoke", func() bool {
		var seen bool
		fake.with(func() {
			for _, call := range fake.revokes {
				seen = seen || strings.Join(call.credentials, ",") == "ghu_1,ghr_1"
			}
		})
		return seen
	})
	waitFor(t, "the end of the Connect", func() bool { return h.service.State("a").State != protocol.Connecting })
	if state := h.service.State("a"); state.State != protocol.HTTPGitHubStateNotConnected || state.Login != nil {
		t.Fatalf("%+v", state)
	}
	if _, err := os.Stat(filepath.Join(h.config.DataDir, tokenFileName)); !os.IsNotExist(err) {
		t.Fatal("the new pair was saved after Disconnect")
	}
}

func TestCancelFromAnotherDeviceChangesNothing(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if state := h.service.Cancel("b"); state.State != protocol.Connecting {
		t.Fatalf("%+v", state)
	}
	if state := h.service.State("a"); state.Connect == nil {
		t.Fatal("another device's Cancel ended the Connect")
	}
	if state := h.service.Cancel("a"); state.State != protocol.HTTPGitHubStateNotConnected {
		t.Fatalf("%+v", state)
	}
}

func TestConnectOutlivesThePhonesRequest(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if response, err := h.service.Connect(ctx, "a"); err != nil || response.Connect == nil {
		t.Fatalf("%+v %v", response, err)
	}
}

func TestRefreshRetryThenBadTokenIsEnded(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, func(_ *Config, opts *options) { opts.requestTimeout = 300 * time.Millisecond })
	expiring(t, h, 30*24*time.Hour)
	fake.with(func() {
		fake.refreshHook = func(call int, w http.ResponseWriter) bool {
			if call == 1 {
				time.Sleep(time.Second) // GitHub rotated; the answer is lost
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"error": "bad_refresh_token"})
			return true
		}
	})
	if _, _, _, err := h.service.Credential(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	if state := h.service.State("a"); state.Problem == nil || *state.Problem != protocol.Ended {
		t.Fatalf("%+v", state)
	}
}

func TestDeleteDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, tokenFileName)); err != nil {
		t.Fatal(err)
	}
	if err := deleteAccount(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Fatalf("the link's target was written: %q", data)
	}
	if _, err := os.Lstat(filepath.Join(dir, tokenFileName)); !os.IsNotExist(err) {
		t.Fatal("the link is still there")
	}
}

func TestNoNewWorkAfterClose(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	h.service.Close()
	if _, err := h.service.Connect(context.Background(), "a"); !errors.Is(err, ErrGitHubDown) {
		t.Fatal(err)
	}
	if _, err := h.service.Setup(context.Background(), "a", "o/n"); !errors.Is(err, ErrGitHubDown) {
		t.Fatal(err)
	}
	if _, err := h.service.Repos(context.Background()); !errors.Is(err, ErrGitHubDown) {
		t.Fatal(err)
	}
	h.service.State("a") // no install check starts
	h.service.Close()
}

// A Cancel while GitHub has not answered the code request yet is the user's
// own act: the Connect answers the state, not "GitHub cannot be reached".
func TestCancelDuringTheCodeRequestIsNotAnError(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	fake.with(func() { fake.codeDelay = 5 * time.Second })
	type result struct {
		response protocol.HTTPGitHubResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := h.service.Connect(context.Background(), "a")
		done <- result{response, err}
	}()
	waitFor(t, "the code request to reach the fake", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.counts["/login/device/code"] > 0
	})
	h.service.Cancel("a")
	got := <-done
	if got.err != nil || got.response.Problem != nil || got.response.State != protocol.HTTPGitHubStateNotConnected {
		t.Fatalf("%+v %v", got.response, got.err)
	}
}

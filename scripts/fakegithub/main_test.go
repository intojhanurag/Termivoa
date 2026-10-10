package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newFake(t *testing.T, git bool) (*server, *httptest.Server) {
	t.Helper()
	s := &server{client: "Iv1.test", expiresIn: 900, interval: 1, now: time.Now}
	s.repos = fixtures(s.now())
	if git {
		var err error
		if s.git, err = makeGit(t.TempDir(), s.repos); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

func post(t *testing.T, ts *httptest.Server, path string, form url.Values) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out
}

func get(t *testing.T, ts *httptest.Server, path, token string, out any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_ = json.NewDecoder(res.Body).Decode(out)
	return res
}

// connect runs the device flow to a token pair.
func connect(t *testing.T, ts *httptest.Server) (access, refresh string) {
	t.Helper()
	code := post(t, ts, "/login/device/code", url.Values{"client_id": {"Iv1.test"}})
	poll := url.Values{"client_id": {"Iv1.test"}, "device_code": {code["device_code"].(string)}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	if got := post(t, ts, "/login/oauth/access_token", poll)["error"]; got != "authorization_pending" {
		t.Fatalf("before authorize: %v", got)
	}
	post(t, ts, "/_test/authorize?code="+code["user_code"].(string), nil)
	pair := post(t, ts, "/login/oauth/access_token", poll)
	if got := post(t, ts, "/login/oauth/access_token", poll)["error"]; got == nil {
		t.Fatal("a used code gave a second pair")
	}
	return pair["access_token"].(string), pair["refresh_token"].(string)
}

func TestDeviceGrantRefreshAndRevoke(t *testing.T) {
	_, ts := newFake(t, false)
	code := post(t, ts, "/login/device/code", url.Values{"client_id": {"Iv1.test"}})
	if user, _ := code["user_code"].(string); len(user) != 9 || user[4] != '-' || code["verification_uri"] != "https://github.com/login/device" || code["interval"] != 1.0 {
		t.Fatalf("device code %v", code)
	}
	if got := post(t, ts, "/login/device/code", url.Values{"client_id": {"other"}})["error"]; got != "incorrect_client_credentials" {
		t.Fatalf("other client: %v", got)
	}
	post(t, ts, "/_test/slowdown", nil)
	poll := url.Values{"client_id": {"Iv1.test"}, "device_code": {code["device_code"].(string)}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	if got := post(t, ts, "/login/oauth/access_token", poll); got["error"] != "slow_down" || got["interval"] != 6.0 {
		t.Fatalf("slow_down: %v", got)
	}
	post(t, ts, "/_test/deny", nil)
	if got := post(t, ts, "/login/oauth/access_token", poll)["error"]; got != "access_denied" {
		t.Fatalf("deny: %v", got)
	}

	access, refresh := connect(t, ts)
	if !strings.HasPrefix(access, "ghu_") || len(access) != 40 || !strings.HasPrefix(refresh, "ghr_") {
		t.Fatalf("tokens %q %q", access, refresh)
	}
	var user map[string]any
	if res := get(t, ts, "/user", access, &user); res.StatusCode != 200 || user["login"] != "octo-demo" {
		t.Fatalf("/user %d %v", res.StatusCode, user)
	}
	ref := url.Values{"client_id": {"Iv1.test"}, "grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	next := post(t, ts, "/login/oauth/access_token", ref)
	if next["refresh_token"] == nil || next["refresh_token"] == refresh {
		t.Fatalf("refresh %v", next)
	}
	if got := post(t, ts, "/login/oauth/access_token", ref)["error"]; got != "bad_refresh_token" {
		t.Fatalf("old refresh token: %v", got)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/credentials/revoke", strings.NewReader(`{"credentials":["`+next["access_token"].(string)+`"]}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 202 {
		t.Fatalf("revoke %v %v", res, err)
	}
	if res := get(t, ts, "/user", next["access_token"].(string), &user); res.StatusCode != 401 {
		t.Fatalf("revoked token: %d", res.StatusCode)
	}
}

func TestInstallationsPaging(t *testing.T) {
	_, ts := newFake(t, false)
	access, _ := connect(t, ts)
	var installs struct{ Installations []map[string]any }
	get(t, ts, "/user/installations", access, &installs)
	if len(installs.Installations) != 0 {
		t.Fatalf("before install: %v", installs)
	}
	post(t, ts, "/_test/install?count=2&extra=150", nil)
	var names []string
	for page, total := 1, 0; ; page++ {
		var body struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				FullName string `json:"full_name"`
			}
		}
		res := get(t, ts, "/user/installations/1001/repositories?per_page=100&page="+strconv.Itoa(page), access, &body)
		total = body.TotalCount
		for _, r := range body.Repositories {
			names = append(names, r.FullName)
		}
		if res.Header.Get("Link") == "" {
			if len(names) != total || total != 5+150 {
				t.Fatalf("%d names, total %d", len(names), total)
			}
			break
		}
	}
	var one map[string]any
	if res := get(t, ts, "/repos/octo-demo/missing", access, &one); res.StatusCode != 404 {
		t.Fatalf("missing repo: %d", res.StatusCode)
	}
}

func TestGitCloneNeedsALiveToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	s, ts := newFake(t, true)
	access, _ := connect(t, ts)
	clone := func(password string) error {
		u, _ := url.Parse(ts.URL + "/octo-demo/pdf-editor.git")
		u.User = url.UserPassword("octo-demo", password)
		cmd := exec.Command("git", "clone", "-q", u.String(), t.TempDir()+"/c")
		cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
		return cmd.Run()
	}
	if clone("ghu_wrong") == nil {
		t.Fatal("a clone with a wrong token worked")
	}
	if err := clone(access); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.gitAuths)
	var last gitAuth
	if n > 0 {
		last = s.gitAuths[n-1]
	}
	s.mu.Unlock()
	if n == 0 {
		t.Fatal("clone recorded no authentication")
	}
	if !last.OK || last.N != 1 || last.User != "octo-demo" {
		t.Fatalf("auth record %+v", last)
	}
}

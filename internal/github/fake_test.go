//go:build darwin || linux

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is one httptest server for the login endpoints, the API and
// git (git http-backend through CGI, behind HTTP Basic auth with the access
// token as password).
type fakeGitHub struct {
	t      *testing.T
	server *httptest.Server
	gitBin string

	mu           sync.Mutex
	deviceCode   map[string]any
	pollAnswers  []any // map[string]any as JSON, int as a status, "tokens" to issue a pair
	polls        []time.Time
	access       string
	refresh      string
	issued       int
	refreshCalls []string
	refreshHook  func(call int, w http.ResponseWriter) bool
	revokes      []revokeCall
	revokeStatus int
	installs     []int64
	repos        map[int64][]map[string]any
	repoInfo     map[string]map[string]any
	counts       map[string]int
	apiDelay     time.Duration
	gitRoot      string
	gitBlock     chan struct{}
	gitAuthSeen  int
}

type revokeCall struct {
	credentials   []string
	authorization string
}

func newFake(t *testing.T) *fakeGitHub {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	if gitBin, err = filepath.EvalSymlinks(gitBin); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGitHub{
		t: t, gitBin: gitBin,
		deviceCode: map[string]any{
			"device_code": "dc-1", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device",
			"expires_in": 900, "interval": 1,
		},
		access: "ghu_start", refresh: "ghr_start", revokeStatus: http.StatusAccepted,
		installs: []int64{7}, repos: map[int64][]map[string]any{}, repoInfo: map[string]map[string]any{},
		counts: map[string]int{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *fakeGitHub) with(change func()) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	change()
}

func (fake *fakeGitHub) token() string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.access
}

func (fake *fakeGitHub) refreshes() string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return strings.Join(fake.refreshCalls, ",")
}

func (fake *fakeGitHub) count(path string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.counts[path]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (fake *fakeGitHub) issue() map[string]any {
	fake.issued++
	fake.access = fmt.Sprintf("ghu_%d", fake.issued)
	fake.refresh = fmt.Sprintf("ghr_%d", fake.issued)
	return map[string]any{
		"access_token": fake.access, "expires_in": 28800, "refresh_token": fake.refresh,
		"refresh_token_expires_in": 15811200, "token_type": "bearer",
	}
}

func (fake *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.Contains(path, ".git/") {
		fake.serveGit(w, r)
		return
	}
	fake.mu.Lock()
	fake.counts[path]++
	delay := fake.apiDelay
	fake.mu.Unlock()
	if r.Header.Get("User-Agent") != userAgent {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "no user agent"})
		return
	}
	switch {
	case path == "/login/device/code":
		fake.mu.Lock()
		answer := fake.deviceCode
		fake.mu.Unlock()
		writeJSON(w, http.StatusOK, answer)
	case path == "/login/oauth/access_token":
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "refresh_token" {
			fake.serveRefresh(w, r.Form.Get("refresh_token"))
			return
		}
		fake.mu.Lock()
		fake.polls = append(fake.polls, time.Now())
		var answer any = map[string]any{"error": "authorization_pending"}
		if len(fake.pollAnswers) > 0 {
			answer, fake.pollAnswers = fake.pollAnswers[0], fake.pollAnswers[1:]
		}
		if answer == "tokens" {
			answer = fake.issue()
		}
		fake.mu.Unlock()
		if status, ok := answer.(int); ok {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, http.StatusOK, answer)
	case path == "/credentials/revoke":
		var body struct {
			Credentials []string `json:"credentials"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fake.mu.Lock()
		fake.revokes = append(fake.revokes, revokeCall{body.Credentials, r.Header.Get("Authorization")})
		status := fake.revokeStatus
		fake.mu.Unlock()
		w.WriteHeader(status)
	default:
		time.Sleep(delay)
		fake.mu.Lock()
		authorized := r.Header.Get("Authorization") == "Bearer "+fake.access
		fake.mu.Unlock()
		if !authorized {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
			return
		}
		fake.serveAPI(w, r)
	}
}

func (fake *fakeGitHub) serveRefresh(w http.ResponseWriter, token string) {
	fake.mu.Lock()
	fake.refreshCalls = append(fake.refreshCalls, token)
	call, hook := len(fake.refreshCalls), fake.refreshHook
	fake.mu.Unlock()
	if hook != nil && hook(call, w) {
		return
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if token != fake.refresh {
		writeJSON(w, http.StatusOK, map[string]any{"error": "bad_refresh_token"})
		return
	}
	writeJSON(w, http.StatusOK, fake.issue())
}

func (fake *fakeGitHub) serveAPI(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/user":
		writeJSON(w, http.StatusOK, map[string]any{"login": "octo", "id": 42})
	case path == "/user/installations":
		list := []map[string]any{}
		for _, id := range fake.installs {
			list = append(list, map[string]any{"id": id})
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(list), "installations": list})
	case strings.HasPrefix(path, "/user/installations/"):
		id, _ := strconv.ParseInt(strings.Split(path, "/")[3], 10, 64)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		all := fake.repos[id]
		start, end := min((page-1)*100, len(all)), min(page*100, len(all))
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(all), "repositories": all[start:end]})
	case strings.HasPrefix(path, "/repos/"):
		info, ok := fake.repoInfo[strings.ToLower(strings.TrimPrefix(path, "/repos/"))]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		writeJSON(w, http.StatusOK, info)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (fake *fakeGitHub) serveGit(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	block, access, root := fake.gitBlock, fake.access, fake.gitRoot
	fake.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	_, password, ok := r.BasicAuth()
	if !ok || password != access {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fake.mu.Lock()
	fake.gitAuthSeen++
	fake.mu.Unlock()
	home := fake.t.TempDir()
	handler := &cgi.Handler{
		Path: fake.gitBin, Args: []string{"http-backend"}, Dir: home,
		Env: []string{
			"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "HOME=" + home,
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH=/usr/bin:/bin",
		},
	}
	handler.ServeHTTP(w, r)
}

// gitEnv is a safe environment for the tests' own git runs: a temp HOME,
// no system or global config.
func gitEnv(t *testing.T) []string {
	home := t.TempDir()
	return []string{
		"HOME=" + home, "PATH=/usr/bin:/bin:" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, "global"), "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
}

func runTestGit(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env, command.Dir = env, dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

// addFixture makes the bare repository owner/name with one commit and a
// .gitattributes that names the lfs filter, served by the fake.
func (fake *fakeGitHub) addFixture(t *testing.T, owner, name string) {
	t.Helper()
	env := gitEnv(t)
	if fake.gitRoot == "" {
		fake.gitRoot = t.TempDir()
	}
	work := t.TempDir()
	runTestGit(t, env, work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, env, work, "add", ".")
	runTestGit(t, env, work, "commit", "-q", "-m", "first")
	runTestGit(t, env, work, "clone", "-q", "--bare", work, filepath.Join(fake.gitRoot, owner, name+".git"))
	fake.mu.Lock()
	fake.repoInfo[strings.ToLower(owner+"/"+name)] = map[string]any{"full_name": owner + "/" + name, "size": 1}
	fake.mu.Unlock()
}

// projects is an in-memory project list for Deps.
type projects struct {
	mu   sync.Mutex
	list []Project
	adds []string
}

func (list *projects) deps() Deps {
	return Deps{
		Projects: func(context.Context) ([]Project, error) {
			list.mu.Lock()
			defer list.mu.Unlock()
			return append([]Project(nil), list.list...), nil
		},
		AddProject: func(_ context.Context, path string) (string, error) {
			list.mu.Lock()
			defer list.mu.Unlock()
			list.adds = append(list.adds, path)
			for _, project := range list.list {
				if project.Path == path {
					return project.ID, nil
				}
			}
			id := "p" + strconv.Itoa(len(list.list)+1)
			list.list = append(list.list, Project{ID: id, Path: path})
			return id, nil
		},
	}
}

type harness struct {
	fake     *fakeGitHub
	service  *Service
	config   Config
	projects *projects
}

// newHarness builds a Service against fake. connected writes a token file
// first (the fake's current pair, 8 h left).
func newHarness(t *testing.T, fake *fakeGitHub, connected bool, change func(*Config, *options)) *harness {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		ClientID: "Iv1.test", AppSlug: "termivoa-test", Home: home, DataDir: t.TempDir(),
		GitPaths: []string{fake.gitBin}, Pterm: writeHelper(t, fake), Profile: "test", RuntimeDir: "/run/termivoa-test",
	}
	opts := options{
		web: fake.server.URL, api: fake.server.URL, git: fake.server.URL, open: true,
		second: 10 * time.Millisecond, requestTimeout: 2 * time.Second, cloneLimit: time.Minute,
	}
	if change != nil {
		change(&config, &opts)
	}
	if connected {
		fake.mu.Lock()
		acc := &account{
			login: "octo", id: 42, access: newSecret(fake.access), refresh: newSecret(fake.refresh),
			accessExpiry: time.Now().Add(8 * time.Hour), refreshExpiry: time.Now().Add(180 * 24 * time.Hour), updated: time.Now(),
		}
		fake.mu.Unlock()
		if err := saveAccount(config.DataDir, acc); err != nil {
			t.Fatal(err)
		}
	}
	list := &projects{}
	service, err := newService(config, list.deps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return &harness{fake: fake, service: service, config: config, projects: list}
}

// writeHelper writes a stand-in for `pterm`: as `pterm github-credential
// get` it prints the fake's current access token. It reads the token from
// a file the fake's test controls, so no token is in any environment.
func writeHelper(t *testing.T, fake *fakeGitHub) string {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	fake.mu.Lock()
	access := fake.access
	fake.mu.Unlock()
	if err := os.WriteFile(tokenFile, []byte(access), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "pterm")
	body := "#!/bin/sh\n[ \"$1\" = github-credential ] || exit 0\nshift\n[ \"$1\" = --dev ] && shift\n[ \"$1\" = --runtime-dir ] && shift 2\n[ \"$1\" = get ] || exit 0\ncat >/dev/null\necho username=x\necho \"password=$(cat '" + tokenFile + "')\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	return string(data)
}

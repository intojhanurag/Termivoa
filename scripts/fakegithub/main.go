// Command fakegithub is a fake GitHub for the e2e harness
// (scripts/e2e-multicomputer/github.mjs; docs/github-integration.md). One
// HTTP server on 127.0.0.1 is the web host (device flow), the API host and
// the git host. It never contacts the real github.com.
//
//	fakegithub -port 0 -client Iv1.e2e      prints "listening http://127.0.0.1:<port>"
//
// Device flow: POST /login/device/code, POST /login/oauth/access_token (the
// device grant answers authorization_pending until a test control decides
// the code; the refresh grant rotates the pair, and the old refresh token is
// then bad_refresh_token). API: /user, /user/installations,
// /user/installations/{id}/repositories (per_page, page, total_count, Link),
// /repos/{owner}/{name}, POST /credentials/revoke. Git: smart HTTP at
// /<owner>/<name>.git/... through `git http-backend` over bare fixture
// repositories made at start; HTTP Basic auth whose password is a live access
// token, each one recorded.
//
// Test controls (no auth):
//
//	POST /_test/authorize?code=ABCD-1234   the user authorizes (no code: the latest one)
//	POST /_test/deny?code=…                the user taps Cancel on GitHub
//	POST /_test/expire?code=…              the code runs out
//	POST /_test/slowdown                   the next device poll answers slow_down
//	POST /_test/install?count=N&extra=M    N installations (0, 1: octo-demo, 2: and acme-labs); M generated repositories in octo-demo
//	POST /_test/gitdelay?ms=N              each git request waits N ms (a clone that lasts)
//	GET  /_test/revoked                    the revoked tokens
//	GET  /_test/gitauth                    each git auth: user, token, its number, path
//	GET  /_test/tokens                     every issued pair, in order
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is one fixture repository; files nil means the API lists it but git does not serve it.
type Repo struct {
	Owner, Name string
	Private     bool
	SizeKB      int
	Pushed      time.Time
	Files       map[string]string
}

func (r Repo) full() string { return r.Owner + "/" + r.Name }

// fixtures: .ssh-keys must never be offered; game-assets is the large one; pdf-editor uses Git LFS.
func fixtures(now time.Time) []Repo {
	at := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	return []Repo{
		{Owner: "octo-demo", Name: "shop-api", Private: true, SizeKB: 420, Pushed: at(1), Files: map[string]string{"README.md": "# shop-api\n"}},
		{Owner: "octo-demo", Name: "pdf-editor", SizeKB: 900, Pushed: at(2), Files: map[string]string{
			"README.md": "# pdf-editor\n", "main.go": "package main\n\nfunc main() {}\n",
			".gitattributes":  "*.pdf filter=lfs diff=lfs merge=lfs -text\n",
			"docs/sample.pdf": "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n"}},
		{Owner: "octo-demo", Name: "notes", Private: true, SizeKB: 12, Pushed: at(3), Files: map[string]string{"README.md": "# notes\n"}},
		{Owner: "octo-demo", Name: ".ssh-keys", Private: true, SizeKB: 4, Pushed: at(4)},
		{Owner: "octo-demo", Name: "game-assets", Private: true, SizeKB: 1_300_000, Pushed: at(5)},
		{Owner: "acme-labs", Name: "billing-service", Private: true, SizeKB: 2_048, Pushed: at(6), Files: map[string]string{"README.md": "# billing-service\n"}},
	}
}

type code struct {
	device, user string
	expires      time.Time
	state        string // pending, authorized, denied, expired, used
}

type pair struct {
	N              int    `json:"n"`
	Access         string `json:"access"`
	Refresh        string `json:"refresh"`
	AccessExpires  int64  `json:"accessExpires"`
	RefreshExpires int64  `json:"refreshExpires"`
	RefreshUsed    bool   `json:"refreshUsed"`
	AccessRevoked  bool   `json:"accessRevoked"`
	RefreshRevoked bool   `json:"refreshRevoked"`
	FromRefreshOf  int    `json:"fromRefreshOf,omitempty"`
}

type gitAuth struct {
	User  string `json:"user"`
	Token string `json:"token"`
	N     int    `json:"n"` // the pair the token belongs to; 0: not a live access token
	OK    bool   `json:"ok"`
	Path  string `json:"path"`
	At    int64  `json:"at"`
}

type server struct {
	mu        sync.Mutex
	client    string
	expiresIn int
	interval  int
	now       func() time.Time
	repos     []Repo
	codes     []*code
	pairs     []*pair
	revoked   []string
	gitAuths  []gitAuth
	installs  int
	extra     int
	slow      bool
	gitDelay  time.Duration
	git       http.Handler
}

func main() {
	port := flag.Int("port", 0, "port on 127.0.0.1 (0: any free one)")
	client := flag.String("client", "Iv1.e2e", "the app's client ID")
	expiresIn := flag.Int("expires-in", 900, "device code lifetime, seconds")
	interval := flag.Int("interval", 1, "device poll interval, seconds")
	dir := flag.String("dir", "", "folder for the bare repositories (default: a new temp folder, left for the caller)")
	flag.Parse()
	s := &server{client: *client, expiresIn: *expiresIn, interval: *interval, now: time.Now}
	s.repos = fixtures(s.now())
	root := *dir
	var err error
	if root == "" {
		if root, err = os.MkdirTemp("", "fakegithub."); err != nil {
			log.Fatal(err)
		}
	}
	if s.git, err = makeGit(root, s.repos); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("listening http://%s\n", listener.Addr())
	log.Fatal(http.Serve(listener, s))
}

// makeGit writes one bare repository for each fixture with files, under
// root/<owner>/<name>.git, and returns the http-backend handler over them.
// Every git here runs with a temp HOME and no system or global config.
func makeGit(root string, repos []Repo) (http.Handler, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C",
		"GIT_AUTHOR_NAME=Octo Demo", "GIT_AUTHOR_EMAIL=octo-demo@example.com", "GIT_COMMITTER_NAME=Octo Demo", "GIT_COMMITTER_EMAIL=octo-demo@example.com",
		"GIT_AUTHOR_DATE=2026-10-01T12:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T12:00:00Z"}
	git := func(dir string, args ...string) error {
		cmd := exec.Command(gitPath, args...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
		return nil
	}
	for _, repo := range repos {
		if repo.Files == nil {
			continue
		}
		work := filepath.Join(root, "work", repo.Owner, repo.Name)
		for name, text := range repo.Files {
			path := filepath.Join(work, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				return nil, err
			}
		}
		bare := filepath.Join(root, "git", repo.Owner, repo.Name+".git")
		for _, step := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "first commit"},
			{"clone", "-q", "--bare", work, bare}, {"--git-dir", bare, "config", "http.receivepack", "true"}} {
			if err := git(work, step...); err != nil {
				return nil, err
			}
		}
	}
	return &cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Dir: root,
		Env: append(env, "GIT_PROJECT_ROOT="+filepath.Join(root, "git"), "GIT_HTTP_EXPORT_ALL=1")}, nil
}

var gitPath = regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git/`)

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case gitPath.MatchString(path):
		s.serveGit(w, r)
	case strings.HasPrefix(path, "/_test/"):
		s.serveTest(w, r)
	case path == "/login/device/code" && r.Method == http.MethodPost:
		s.deviceCode(w, r)
	case path == "/login/oauth/access_token" && r.Method == http.MethodPost:
		s.accessToken(w, r)
	case path == "/credentials/revoke" && r.Method == http.MethodPost:
		s.revoke(w, r)
	default:
		s.serveAPI(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// loginAnswer answers a /login route as GitHub does: JSON when asked for it, else a form.
func loginAnswer(w http.ResponseWriter, r *http.Request, v map[string]any) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, v)
		return
	}
	form := url.Values{}
	for k, x := range v {
		form.Set(k, fmt.Sprint(x))
	}
	w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	_, _ = w.Write([]byte(form.Encode()))
}

// params reads a form or a JSON body (GitHub takes both).
func params(r *http.Request) url.Values {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]string
		_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&m)
		v := url.Values{}
		for k, x := range m {
			v.Set(k, x)
		}
		return v
	}
	_ = r.ParseForm()
	return r.Form
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func random(chars string, n int) string {
	b := make([]byte, n)
	for i := range b {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			panic(err)
		}
		b[i] = chars[x.Int64()]
	}
	return string(b)
}

func (s *server) deviceCode(w http.ResponseWriter, r *http.Request) {
	p := params(r)
	if p.Get("client_id") != s.client {
		loginAnswer(w, r, map[string]any{"error": "incorrect_client_credentials"})
		return
	}
	s.mu.Lock()
	c := &code{device: random(tokenAlphabet, 40), user: random(alphabet, 4) + "-" + random(alphabet, 4),
		expires: s.now().Add(time.Duration(s.expiresIn) * time.Second), state: "pending"}
	s.codes = append(s.codes, c)
	s.mu.Unlock()
	loginAnswer(w, r, map[string]any{"device_code": c.device, "user_code": c.user, "verification_uri": "https://github.com/login/device",
		"expires_in": s.expiresIn, "interval": s.interval})
}

// issue makes a new pair; the caller holds mu.
func (s *server) issue(from int) *pair {
	now := s.now()
	p := &pair{N: len(s.pairs) + 1, Access: "ghu_" + random(tokenAlphabet, 36), Refresh: "ghr_" + random(tokenAlphabet, 36),
		AccessExpires: now.Add(8 * time.Hour).Unix(), RefreshExpires: now.Add(15897600 * time.Second).Unix(), FromRefreshOf: from}
	s.pairs = append(s.pairs, p)
	return p
}

func tokens(p *pair) map[string]any {
	return map[string]any{"access_token": p.Access, "expires_in": 28800, "refresh_token": p.Refresh, "refresh_token_expires_in": 15897600,
		"token_type": "bearer", "scope": ""}
}

func (s *server) accessToken(w http.ResponseWriter, r *http.Request) {
	p := params(r)
	if p.Get("client_id") != s.client {
		loginAnswer(w, r, map[string]any{"error": "incorrect_client_credentials"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch p.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		var c *code
		for _, x := range s.codes {
			if x.device == p.Get("device_code") {
				c = x
			}
		}
		if c == nil {
			loginAnswer(w, r, map[string]any{"error": "incorrect_device_code"})
			return
		}
		if c.state == "pending" && s.now().After(c.expires) {
			c.state = "expired"
		}
		switch c.state {
		case "pending":
			if s.slow {
				s.slow = false
				s.interval += 5
				loginAnswer(w, r, map[string]any{"error": "slow_down", "interval": s.interval})
				return
			}
			loginAnswer(w, r, map[string]any{"error": "authorization_pending"})
		case "denied":
			loginAnswer(w, r, map[string]any{"error": "access_denied"})
		case "expired":
			loginAnswer(w, r, map[string]any{"error": "expired_token"})
		case "used":
			loginAnswer(w, r, map[string]any{"error": "token_expired"}) // GitHub answers a used code like this
		case "authorized":
			c.state = "used"
			loginAnswer(w, r, tokens(s.issue(0)))
		}
	case "refresh_token":
		for _, x := range s.pairs {
			if x.Refresh == p.Get("refresh_token") && !x.RefreshUsed && !x.RefreshRevoked && s.now().Unix() < x.RefreshExpires {
				x.RefreshUsed = true
				loginAnswer(w, r, tokens(s.issue(x.N)))
				return
			}
		}
		loginAnswer(w, r, map[string]any{"error": "bad_refresh_token"})
	default:
		loginAnswer(w, r, map[string]any{"error": "unsupported_grant_type"})
	}
}

func (s *server) revoke(w http.ResponseWriter, r *http.Request) {
	var body struct{ Credentials []string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || len(body.Credentials) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	for _, t := range body.Credentials {
		s.revoked = append(s.revoked, t)
		for _, p := range s.pairs {
			p.AccessRevoked = p.AccessRevoked || p.Access == t
			p.RefreshRevoked = p.RefreshRevoked || p.Refresh == t
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Accepted"})
}

// live is the pair whose access token t is and that still works; the caller holds mu.
func (s *server) live(t string) *pair {
	for _, p := range s.pairs {
		if p.Access == t && !p.AccessRevoked && s.now().Unix() < p.AccessExpires {
			return p
		}
	}
	return nil
}

func (s *server) bearer(r *http.Request) *pair {
	h := r.Header.Get("Authorization")
	for _, prefix := range []string{"Bearer ", "bearer ", "token "} {
		if t, ok := strings.CutPrefix(h, prefix); ok {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.live(t)
		}
	}
	return nil
}

var installationRepos = regexp.MustCompile(`^/user/installations/(\d+)/repositories$`)
var oneRepo = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)$`)

type installation struct {
	id    int
	owner string
}

// installations the app has now; the caller holds mu.
func (s *server) installations() []installation {
	all := []installation{{1001, "octo-demo"}, {1002, "acme-labs"}}
	return all[:min(s.installs, len(all))]
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if s.bearer(r) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/user":
		writeJSON(w, http.StatusOK, map[string]any{"login": "octo-demo", "id": 4242, "type": "User"})
	case path == "/user/installations":
		var list []any
		for _, in := range s.installations() {
			list = append(list, map[string]any{"id": in.id, "app_slug": "termivoa-e2e", "repository_selection": "all",
				"account": map[string]any{"login": in.owner, "type": "User"}})
		}
		page(w, r, "installations", list)
	case installationRepos.MatchString(path):
		id, _ := strconv.Atoi(installationRepos.FindStringSubmatch(path)[1])
		var owner string
		for _, in := range s.installations() {
			if in.id == id {
				owner = in.owner
			}
		}
		if owner == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		var list []any
		for _, repo := range s.all() {
			if repo.Owner == owner {
				list = append(list, apiRepo(repo))
			}
		}
		page(w, r, "repositories", list)
	case oneRepo.MatchString(path):
		m := oneRepo.FindStringSubmatch(path)
		for _, repo := range s.all() {
			if strings.EqualFold(repo.Owner, m[1]) && strings.EqualFold(repo.Name, m[2]) {
				writeJSON(w, http.StatusOK, apiRepo(repo))
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

// all is the fixtures plus the generated ones; the caller holds mu.
func (s *server) all() []Repo {
	repos := append([]Repo(nil), s.repos...)
	base := s.repos[len(s.repos)-1].Pushed
	for i := 1; i <= s.extra; i++ {
		repos = append(repos, Repo{Owner: "octo-demo", Name: fmt.Sprintf("demo-%03d", i), SizeKB: 10 + i, Pushed: base.Add(-time.Duration(i) * time.Minute)})
	}
	return repos
}

func apiRepo(r Repo) map[string]any {
	return map[string]any{"id": len(r.full()) * 7919, "name": r.Name, "full_name": r.full(), "owner": map[string]any{"login": r.Owner},
		"private": r.Private, "fork": false, "archived": false, "size": r.SizeKB, "pushed_at": r.Pushed.UTC().Format(time.RFC3339),
		"default_branch": "main", "clone_url": "https://github.com/" + r.full() + ".git"}
}

// page answers one page of list under key, with total_count and a Link header like GitHub's.
func page(w http.ResponseWriter, r *http.Request, key string, list []any) {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 30
	}
	per = min(per, 100)
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	n = max(n, 1)
	from, to := min((n-1)*per, len(list)), min(n*per, len(list))
	if to < len(list) {
		next := *r.URL
		q := next.Query()
		q.Set("page", strconv.Itoa(n+1))
		q.Set("per_page", strconv.Itoa(per))
		next.RawQuery = q.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, next.RequestURI()))
	}
	items := list[from:to]
	if items == nil {
		items = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(list), key: items})
}

func (s *server) serveGit(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	s.mu.Lock()
	delay := s.gitDelay
	var p *pair
	if ok {
		p = s.live(password)
		entry := gitAuth{User: user, Token: password, OK: p != nil, Path: r.URL.Path, At: s.now().UnixMilli()}
		if p != nil {
			entry.N = p.N
		}
		s.gitAuths = append(s.gitAuths, entry)
	}
	s.mu.Unlock()
	if p == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}
	time.Sleep(delay)
	s.git.ServeHTTP(w, r)
}

func (s *server) serveTest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	decide := func(state string) {
		var c *code
		for _, x := range s.codes {
			if x.state == "pending" && (q.Get("code") == "" || x.user == q.Get("code")) {
				c = x
			}
		}
		if c == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending code " + q.Get("code")})
			return
		}
		c.state = state
		writeJSON(w, http.StatusOK, map[string]string{"code": c.user, "state": state})
	}
	switch r.URL.Path {
	case "/_test/authorize":
		decide("authorized")
	case "/_test/deny":
		decide("denied")
	case "/_test/expire":
		decide("expired")
	case "/_test/slowdown":
		s.slow = true
		writeJSON(w, http.StatusOK, map[string]bool{"slow": true})
	case "/_test/install":
		s.installs, _ = strconv.Atoi(q.Get("count"))
		s.extra, _ = strconv.Atoi(q.Get("extra"))
		s.extra = min(max(s.extra, 0), 2000)
		writeJSON(w, http.StatusOK, map[string]int{"installations": len(s.installations()), "extra": s.extra})
	case "/_test/gitdelay":
		ms, _ := strconv.Atoi(q.Get("ms"))
		s.gitDelay = time.Duration(min(max(ms, 0), 30000)) * time.Millisecond
		writeJSON(w, http.StatusOK, map[string]int{"ms": ms})
	case "/_test/revoked":
		writeJSON(w, http.StatusOK, append([]string{}, s.revoked...))
	case "/_test/gitauth":
		writeJSON(w, http.StatusOK, append([]gitAuth{}, s.gitAuths...))
	case "/_test/tokens":
		writeJSON(w, http.StatusOK, append([]*pair{}, s.pairs...))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such control"})
	}
}

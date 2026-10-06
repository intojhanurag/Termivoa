package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	maxInstallations = 10
	maxRepos         = 1000
	reposCacheFor    = 60 * time.Second
	reposDeadline    = 15 * time.Second
	installRecheck   = 5 * time.Second
)

// repoPattern is owner/name as the phone may send it: no dot in the owner,
// no name that starts with "." or "-" (design 6.4).
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,39}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

// splitRepo checks repo and returns its owner and name.
func splitRepo(repo string) (owner, name string, ok bool) {
	if !repoPattern.MatchString(repo) || strings.HasSuffix(strings.ToLower(repo), ".git") {
		return "", "", false
	}
	owner, name, _ = strings.Cut(repo, "/")
	return owner, name, true
}

// reposRun is the one list run; its waiters read the result after done.
type reposRun struct {
	done   chan struct{}
	result protocol.HTTPGitHubReposResponse
	err    error
}

type reposCache struct {
	at      time.Time
	account *account
	result  protocol.HTTPGitHubReposResponse
}

// dropCache forgets the list. The caller holds mu.
func (service *Service) dropCache() { service.cache, service.cacheGen = nil, service.cacheGen+1 }

// Repos is GET /api/v1/github/repos: at most 1000 rows, cached 60 s, one
// shared run, 15 s in total, with here and taken for each row.
// Errors: ErrNotConnected, ErrGitHubDown, or ctx's error.
func (service *Service) Repos(ctx context.Context) (protocol.HTTPGitHubReposResponse, error) {
	empty := protocol.HTTPGitHubReposResponse{Repos: []protocol.HTTPGitHubRepo{}}
	if service == nil {
		return empty, ErrNotConnected
	}
	service.mu.Lock()
	if service.account == nil {
		service.mu.Unlock()
		return empty, ErrNotConnected
	}
	if cache := service.cache; cache != nil && cache.account == service.account && service.now().Sub(cache.at) < reposCacheFor {
		service.mu.Unlock()
		return copyRepos(cache.result), nil
	}
	if service.closed {
		service.mu.Unlock()
		return empty, ErrGitHubDown
	}
	run := service.reposRun
	if run == nil {
		run = &reposRun{done: make(chan struct{})}
		service.reposRun = run
		gen := service.cacheGen
		service.wg.Add(1)
		go func() {
			defer service.wg.Done()
			runCtx, cancel := context.WithTimeout(service.ctx, reposDeadline)
			defer cancel()
			acc := service.current()
			run.result, run.err = service.listRepos(runCtx)
			service.mu.Lock()
			service.reposRun = nil
			if run.err == nil && acc != nil && service.account == acc && service.cacheGen == gen {
				service.cache = &reposCache{at: service.now(), account: acc, result: run.result}
			}
			service.mu.Unlock()
			close(run.done)
		}()
	}
	service.mu.Unlock()
	select {
	case <-run.done:
		if run.err != nil {
			return empty, run.err
		}
		return copyRepos(run.result), nil
	case <-ctx.Done():
		return empty, ctx.Err()
	}
}

func copyRepos(result protocol.HTTPGitHubReposResponse) protocol.HTTPGitHubReposResponse {
	result.Repos = slices.Clone(result.Repos)
	return result
}

type installation struct {
	ID int64 `json:"id"`
}

// installations reads the user's installations of the app (at most 10).
func (service *Service) installations(ctx context.Context) ([]installation, bool, error) {
	var answer struct {
		TotalCount    int            `json:"total_count"`
		Installations []installation `json:"installations"`
	}
	status, err := service.apiGet(ctx, "/user/installations?per_page=100", &answer)
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK {
		return nil, false, ErrGitHubDown
	}
	list := answer.Installations
	truncated := len(list) > maxInstallations || answer.TotalCount > len(list)
	if len(list) > maxInstallations {
		list = list[:maxInstallations]
	}
	service.setInstalled(len(list) > 0)
	return list, truncated, nil
}

func (service *Service) setInstalled(any bool) {
	service.mu.Lock()
	service.installKnown, service.needsInstall = true, !any
	service.mu.Unlock()
}

// checkInstalls reads the installations once; errors keep the last state.
func (service *Service) checkInstalls(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, reposDeadline)
	defer cancel()
	_, _, _ = service.installations(ctx)
}

type githubRepo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Fork     bool   `json:"fork"`
	Archived bool   `json:"archived"`
	Size     int64  `json:"size"`
	PushedAt string `json:"pushed_at"`
}

func (service *Service) listRepos(ctx context.Context) (protocol.HTTPGitHubReposResponse, error) {
	result := protocol.HTTPGitHubReposResponse{Repos: []protocol.HTTPGitHubRepo{}}
	installs, truncated, err := service.installations(ctx)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, install := range installs {
		for page := 1; ; page++ {
			if len(result.Repos) >= maxRepos {
				truncated = true
				break
			}
			var answer struct {
				TotalCount   int          `json:"total_count"`
				Repositories []githubRepo `json:"repositories"`
			}
			path := fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", install.ID, page)
			status, err := service.apiGet(ctx, path, &answer)
			if err != nil {
				return result, err
			}
			if status != http.StatusOK {
				return result, ErrGitHubDown
			}
			for _, repo := range answer.Repositories {
				if len(result.Repos) >= maxRepos {
					truncated = true
					break
				}
				if _, _, ok := splitRepo(repo.FullName); !ok || seen[strings.ToLower(repo.FullName)] {
					continue
				}
				seen[strings.ToLower(repo.FullName)] = true
				row := protocol.HTTPGitHubRepo{
					Repo: repo.FullName, Private: repo.Private, Fork: repo.Fork,
					Archived: repo.Archived, SizeKB: max(repo.Size, 0),
				}
				if pushed, err := time.Parse(time.RFC3339, repo.PushedAt); err == nil {
					ms := pushed.UnixMilli()
					row.PushedAt = &ms
				}
				result.Repos = append(result.Repos, row)
			}
			if len(answer.Repositories) < 100 || page*100 >= answer.TotalCount {
				break
			}
		}
	}
	if truncated {
		result.Truncated = &truncated
	}
	slices.SortStableFunc(result.Repos, func(a, b protocol.HTTPGitHubRepo) int {
		return int64Compare(pushedOrZero(b), pushedOrZero(a))
	})
	service.markPlaces(ctx, result.Repos)
	return result, nil
}

func pushedOrZero(repo protocol.HTTPGitHubRepo) int64 {
	if repo.PushedAt == nil {
		return 0
	}
	return *repo.PushedAt
}

func int64Compare(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// markPlaces sets here and taken for each row (design 6.3).
func (service *Service) markPlaces(ctx context.Context, rows []protocol.HTTPGitHubRepo) {
	saved := service.savedOrigins(ctx)
	entries := service.homeEntries()
	for index := range rows {
		owner, name, _ := splitRepo(rows[index].Repo)
		if _, ok := saved[strings.ToLower(owner+"/"+name)]; ok {
			rows[index].Here = true
			continue
		}
		switch service.place(ctx, entries, owner, name) {
		case placeSame:
			rows[index].Here = true
		case placeOther:
			rows[index].Taken = true
		}
	}
}

type placeKind int

const (
	placeFree placeKind = iota
	placeSame
	placeOther
)

// homeEntries maps the lower-case names in the home folder to their names.
func (service *Service) homeEntries() map[string][]string {
	entries, _ := os.ReadDir(service.home)
	names := make(map[string][]string, len(entries))
	for _, entry := range entries {
		key := strings.ToLower(entry.Name())
		names[key] = append(names[key], entry.Name())
	}
	return names
}

// place reads ~/<name>: free; the same repository (a real directory owned
// by the user, with a real .git directory whose origin is owner/name); or
// anything else, also a name that differs only in case.
func (service *Service) place(ctx context.Context, entries map[string][]string, owner, name string) placeKind {
	names := entries[strings.ToLower(name)]
	switch {
	case len(names) == 0:
		return placeFree
	case !slices.Contains(names, name):
		return placeOther
	}
	origin, ok := service.originOf(ctx, filepath.Join(service.home, name))
	if ok && strings.EqualFold(origin, owner+"/"+name) {
		return placeSame
	}
	return placeOther
}

// savedOrigins is the set of lower-case owner/name of the saved projects'
// origins, with the project ID.
func (service *Service) savedOrigins(ctx context.Context) map[string]string {
	origins := map[string]string{}
	if service.deps.Projects == nil {
		return origins
	}
	projects, err := service.deps.Projects(ctx)
	if err != nil {
		return origins
	}
	for _, project := range projects {
		if origin, ok := service.originOf(ctx, project.Path); ok {
			if _, dup := origins[strings.ToLower(origin)]; !dup {
				origins[strings.ToLower(origin)] = project.ID
			}
		}
	}
	return origins
}

type originEntry struct {
	modified time.Time
	origin   string
	ok       bool
}

// originOf reads owner/name of dir's origin on GitHub. dir must be a real
// directory owned by the user with a real .git directory; the origin is
// read with `git config --file`, never with git running in dir. Cached by
// path and the config file's change time.
func (service *Service) originOf(ctx context.Context, dir string) (string, bool) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || !ownedByMe(info) {
		return "", false
	}
	gitDir, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil || !gitDir.IsDir() {
		return "", false
	}
	configPath := filepath.Join(dir, ".git", "config")
	config, err := os.Lstat(configPath)
	if err != nil || !config.Mode().IsRegular() {
		return "", false
	}
	service.mu.Lock()
	cached, hit := service.origins[dir]
	service.mu.Unlock()
	if hit && cached.modified.Equal(config.ModTime()) {
		return cached.origin, cached.ok
	}
	output, err := service.git(ctx, gitShort, nil, "config", "--file", configPath, "--get", "remote.origin.url")
	entry := originEntry{modified: config.ModTime()}
	if err == nil {
		owner, name, ok := service.parseOrigin(strings.TrimSpace(string(output)))
		entry.origin, entry.ok = owner+"/"+name, ok
	}
	service.mu.Lock()
	if len(service.origins) >= 4*protocol.MaxAdminProjects {
		service.origins = map[string]originEntry{} // ponytail: drop all at the cap
	}
	service.origins[dir] = entry
	service.mu.Unlock()
	return entry.origin, entry.ok
}

var scpOrigin = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([^@:/]+):(.+)$`)

// parseOrigin reads owner and name of a GitHub remote URL: https (with or
// without user@, www., the default port), [user@]host:o/n and
// ssh://[user@]host[:port]/o/n, with an optional ".git" in any case. The
// host is the git host of this build.
func (service *Service) parseOrigin(remote string) (owner, name string, ok bool) {
	base, err := url.Parse(service.opts.git)
	if err != nil {
		return "", "", false
	}
	sameHost := func(host string) bool {
		host = strings.ToLower(host)
		want := strings.ToLower(base.Hostname())
		return host == want || host == "www."+want
	}
	var path string
	if match := scpOrigin.FindStringSubmatch(remote); match != nil && !strings.Contains(remote, "://") {
		if !sameHost(match[1]) {
			return "", "", false
		}
		path = match[2]
	} else {
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", "", false
		}
		switch parsed.Scheme {
		case "ssh":
			if !sameHost(parsed.Hostname()) {
				return "", "", false
			}
		case base.Scheme:
			if !sameHost(parsed.Hostname()) || effectivePort(parsed) != effectivePort(base) {
				return "", "", false
			}
		default:
			return "", "", false
		}
		path = parsed.Path
	}
	path = strings.Trim(path, "/")
	if strings.HasSuffix(strings.ToLower(path), ".git") {
		path = path[:len(path)-len(".git")]
	}
	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// effectivePort is the URL's port, or its scheme's default.
func effectivePort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	if target.Scheme == "http" {
		return "80"
	}
	return "443"
}

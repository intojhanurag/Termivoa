//go:build darwin || linux

package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func repoRows(owner string, count int, start time.Time) []map[string]any {
	rows := make([]map[string]any, count)
	for index := range rows {
		rows[index] = map[string]any{
			"full_name": fmt.Sprintf("%s/r%d", owner, index), "private": index%2 == 0, "size": index,
			"pushed_at": start.Add(time.Duration(index) * time.Minute).UTC().Format(time.RFC3339),
		}
	}
	return rows
}

func TestReposPagingCapCacheAndOneRun(t *testing.T) {
	fake := newFake(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake.with(func() {
		fake.installs = []int64{1, 2}
		fake.repos[1] = append(repoRows("a", 750, start),
			map[string]any{"full_name": "a/.ssh"}, map[string]any{"full_name": "a/x.git"}, map[string]any{"full_name": "a.b/x"})
		fake.repos[2] = repoRows("b", 400, start.Add(time.Hour))
		fake.apiDelay = 20 * time.Millisecond
	})
	h := newHarness(t, fake, true, nil)
	var wait sync.WaitGroup
	results := make([]protocol.HTTPGitHubReposResponse, 5)
	for index := range results {
		wait.Go(func() {
			result, err := h.service.Repos(context.Background())
			if err != nil {
				t.Error(err)
			}
			results[index] = result
		})
	}
	wait.Wait()
	if fake.count("/user/installations") != 1 {
		t.Fatalf("%d runs for 5 callers", fake.count("/user/installations"))
	}
	result := results[0]
	if len(result.Repos) != maxRepos || result.Truncated == nil || !*result.Truncated {
		t.Fatalf("%d rows, truncated %v", len(result.Repos), result.Truncated)
	}
	for index := 1; index < len(result.Repos); index++ {
		if *result.Repos[index-1].PushedAt < *result.Repos[index].PushedAt {
			t.Fatal("not sorted by the last push")
		}
	}
	for _, row := range result.Repos {
		if row.Repo == "a/.ssh" || row.Repo == "a/x.git" || row.Repo == "a.b/x" {
			t.Fatalf("%s was offered", row.Repo)
		}
	}
	if top := result.Repos[0]; top.Repo != "a/r749" || top.SizeKB != 749 || top.Private {
		t.Fatalf("top row %+v", top)
	}
	pages := fake.count("/user/installations/1/repositories") + fake.count("/user/installations/2/repositories")
	if _, err := h.service.Repos(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.count("/user/installations") != 1 || fake.count("/user/installations/1/repositories")+fake.count("/user/installations/2/repositories") != pages {
		t.Fatal("the cache was not used")
	}
	if pages != 8+3 {
		t.Fatalf("%d pages read", pages)
	}
}

func TestNeedsInstall(t *testing.T) {
	fake := newFake(t)
	fake.with(func() { fake.installs = nil })
	h := newHarness(t, fake, true, nil)
	result, err := h.service.Repos(context.Background())
	if err != nil || len(result.Repos) != 0 {
		t.Fatal(err)
	}
	if state := h.service.State("a"); state.State != protocol.NeedsInstall || state.App != "termivoa-test" {
		t.Fatalf("%+v", state)
	}
	// The user installs the app on GitHub: State looks again by itself.
	fake.with(func() { fake.installs = []int64{3} })
	h.service.mu.Lock()
	h.service.installChecked = time.Time{}
	h.service.mu.Unlock()
	waitFor(t, "ready", func() bool { return h.service.State("a").State == protocol.HTTPGitHubStateReady })
}

// fakeRepo makes dir a git repository whose origin is origin ("" for none).
func fakeRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "[core]\n\tbare = false\n"
	if origin != "" {
		config += "[remote \"origin\"]\n\turl = " + origin + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHereAndTaken(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	base := fake.server.URL
	host := h.service.opts.git[len("http://"):]
	home := h.service.home
	fakeRepo(t, filepath.Join(home, "same-https"), base+"/O/Same-Https.git")
	fakeRepo(t, filepath.Join(home, "same-user"), "http://x-access-token@"+host+"/o/same-user")
	fakeRepo(t, filepath.Join(home, "same-ssh"), "git@127.0.0.1:o/same-ssh.git")
	fakeRepo(t, filepath.Join(home, "same-ssh-url"), "ssh://git@127.0.0.1/o/same-ssh-url.git")
	fakeRepo(t, filepath.Join(home, "other"), base+"/someone/other.git")
	fakeRepo(t, filepath.Join(home, "no-origin"), "")
	fakeRepo(t, filepath.Join(home, "Case"), base+"/o/case.git")
	if err := os.Mkdir(filepath.Join(home, "plain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "afile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	fakeRepo(t, filepath.Join(elsewhere, "linked"), base+"/o/link.git")
	if err := os.Symlink(filepath.Join(elsewhere, "linked"), filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	fakeRepo(t, filepath.Join(elsewhere, "project"), base+"/o/saved.git")
	h.projects.list = []Project{{ID: "p9", Path: filepath.Join(elsewhere, "project")}}

	names := []string{"same-https", "same-user", "same-ssh", "same-ssh-url", "other", "no-origin", "case", "plain", "afile", "link", "saved", "absent"}
	rows := make([]map[string]any, len(names))
	for index, name := range names {
		rows[index] = map[string]any{"full_name": "o/" + name}
	}
	fake.with(func() { fake.repos[7] = rows })
	result, err := h.service.Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]bool{
		"o/same-https": {true, false}, "o/same-user": {true, false}, "o/same-ssh": {true, false}, "o/same-ssh-url": {true, false},
		"o/other": {false, true}, "o/no-origin": {false, true}, "o/case": {false, true}, "o/plain": {false, true},
		"o/afile": {false, true}, "o/link": {false, true}, "o/saved": {true, false}, "o/absent": {false, false},
	}
	for _, row := range result.Repos {
		if got := [2]bool{row.Here, row.Taken}; got != want[row.Repo] {
			t.Errorf("%s: here, taken = %v, want %v", row.Repo, got, want[row.Repo])
		}
	}
	if len(result.Repos) != len(want) {
		t.Fatalf("%d rows", len(result.Repos))
	}
}

func TestParseOrigin(t *testing.T) {
	service := &Service{opts: defaultOptions}
	for remote, want := range map[string]string{
		"https://github.com/o/n.git":          "o/n",
		"https://github.com/o/n":              "o/n",
		"https://user:pw@github.com/O/N/":     "O/N",
		"git@github.com:o/n.git":              "o/n",
		"ssh://git@github.com/o/n.git":        "o/n",
		"https://gitlab.com/o/n.git":          "",
		"http://github.com/o/n.git":           "",
		"git@gitlab.com:o/n.git":              "",
		"https://github.com/o/n/extra.git":    "",
		"https://github.com/o":                "",
		"file:///home/u/n":                    "",
		"https://github.com.evil.com/o/n.git": "",
	} {
		owner, name, ok := service.parseOrigin(remote)
		got := ""
		if ok {
			got = owner + "/" + name
		}
		if got != want {
			t.Errorf("%s: %q, want %q", remote, got, want)
		}
	}
}

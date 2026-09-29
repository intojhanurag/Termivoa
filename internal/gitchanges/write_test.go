package gitchanges

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// writableRepo is newRepo with a first commit and a repository identity, so
// Commit and Push run as the user's git would, without the user's config.
func writableRepo(t *testing.T) string {
	t.Helper()
	dir := newRepo(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	git(t, dir, "config", "user.name", "test")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
	for _, name := range []string{"keep.txt", "staged.txt", "del.txt", "old.txt"} {
		write(t, dir, name, name+"\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitOnlySelectedPaths(t *testing.T) {
	dir := writableRepo(t)
	write(t, dir, "keep.txt", "changed\n")
	write(t, dir, "staged.txt", "staged\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "new file.txt", "untracked\n")
	if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "old.txt", "renamed.txt")

	result := Commit(context.Background(), dir, "feat: pick", []string{"keep.txt", "new file.txt", "del.txt", "renamed.txt"})
	if result.State != protocol.Committed || result.Commit == nil || *result.Commit != output(t, dir, "rev-parse", "--short", "HEAD") {
		t.Fatalf("commit = %#v", result)
	}
	if got := output(t, dir, "log", "-1", "--format=%s"); got != "feat: pick" {
		t.Fatalf("message = %q", got)
	}
	got := output(t, dir, "show", "--name-status", "--format=", "-M", "HEAD")
	want := "D\tdel.txt\nM\tkeep.txt\nA\tnew file.txt\nR100\told.txt\trenamed.txt"
	if got != want {
		t.Fatalf("committed files:\n%s\nwant:\n%s", got, want)
	}
	// The other staged file stays staged and out of the commit.
	if got := output(t, dir, "status", "--porcelain"); got != "M  staged.txt" {
		t.Fatalf("status after commit = %q", got)
	}
}

func TestCommitRefusesAndReportsFailures(t *testing.T) {
	dir := writableRepo(t)
	write(t, dir, "keep.txt", "changed\n")
	head := output(t, dir, "rev-parse", "HEAD")

	for name, paths := range map[string][]string{
		"unchanged":  {"staged.txt"},
		"unlisted":   {"keep.txt", "missing.txt"},
		"outside":    {"../keep.txt"},
		"git folder": {".git/config"},
	} {
		if result := Commit(context.Background(), dir, "m", paths); result.State != protocol.CommitStateFailed ||
			result.Output == nil || !strings.Contains(*result.Output, "not in the current changes") {
			t.Errorf("%s: %#v", name, result)
		}
	}
	if result := Commit(context.Background(), dir, "a\x00b", []string{"keep.txt"}); result.State != protocol.CommitStateFailed {
		t.Errorf("NUL message: %#v", result)
	}
	if result := Commit(context.Background(), t.TempDir(), "m", []string{"keep.txt"}); result.State != protocol.CommitStateFailed ||
		!strings.Contains(*result.Output, "not in a git repository") {
		t.Errorf("not git: %#v", result)
	}

	// After git rm --cached the path is deleted and untracked; committing it
	// as it is in the working tree changes nothing.
	git(t, dir, "rm", "-q", "--cached", "staged.txt")
	if result := Commit(context.Background(), dir, "m", []string{"staged.txt"}); result.State != protocol.Nothing {
		t.Errorf("nothing: %#v", result)
	}

	// A missing identity names the problem.
	git(t, dir, "config", "--unset", "user.email")
	git(t, dir, "config", "user.useConfigOnly", "true")
	t.Setenv("EMAIL", "")
	if result := Commit(context.Background(), dir, "m", []string{"keep.txt"}); result.State != protocol.CommitStateFailed ||
		!strings.Contains(*result.Output, "user.email") {
		t.Errorf("no identity: %#v", result)
	}
	git(t, dir, "config", "user.email", "test@example.com")

	if runtime.GOOS != "windows" {
		hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint: keep.txt is badly formatted' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if result := Commit(context.Background(), dir, "m", []string{"keep.txt"}); result.State != protocol.CommitStateFailed ||
			*result.Output != "lint: keep.txt is badly formatted" {
			t.Errorf("hook: %#v", result)
		}
		if err := os.Remove(hook); err != nil {
			t.Fatal(err)
		}
	}

	// A merge in progress is refused before git runs.
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result := Commit(context.Background(), dir, "m", []string{"keep.txt"}); result.State != protocol.CommitStateFailed ||
		!strings.Contains(*result.Output, "merge is in progress") {
		t.Errorf("merge: %#v", result)
	}
	if got := output(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("a refused commit moved HEAD to %s", got)
	}
}

func TestPush(t *testing.T) {
	dir := writableRepo(t)
	if result := Push(context.Background(), dir); result.State != protocol.NoRemote {
		t.Fatalf("no remote: %#v", result)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, dir, "init", "-q", "--bare", remote)
	if runtime.GOOS != "windows" {
		// What GitHub prints after a push of a new branch.
		hook := "#!/bin/sh\necho \"Create a pull request for 'main' on GitHub by visiting:\" >&2\n" +
			"echo '     https://github.com/o/r/pull/new/main' >&2\n"
		if err := os.WriteFile(filepath.Join(remote, "hooks", "post-receive"), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "remote", "add", "origin", remote)

	// No upstream yet: push -u origin HEAD.
	result := Push(context.Background(), dir)
	if result.State != protocol.Pushed || result.Branch == nil || *result.Branch != "main" {
		t.Fatalf("first push: %#v", result)
	}
	if runtime.GOOS != "windows" && (result.URL == nil || *result.URL != "https://github.com/o/r/pull/new/main") {
		t.Fatalf("first push url: %#v", result.URL)
	}
	if got := output(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/main" {
		t.Fatalf("upstream = %q", got)
	}
	// With the upstream: plain git push.
	write(t, dir, "keep.txt", "second\n")
	if result := Commit(context.Background(), dir, "second", []string{"keep.txt"}); result.State != protocol.Committed {
		t.Fatalf("commit: %#v", result)
	}
	if result := Push(context.Background(), dir); result.State != protocol.Pushed {
		t.Fatalf("second push: %#v", result)
	}
	if local, pushed := output(t, dir, "rev-parse", "HEAD"), output(t, remote, "rev-parse", "main"); local != pushed {
		t.Fatalf("remote main = %s, want %s", pushed, local)
	}
	// A rejected (non-fast-forward) push fails with git's words; it is never forced.
	git(t, dir, "reset", "-q", "--hard", "HEAD~1")
	write(t, dir, "keep.txt", "diverged\n")
	git(t, dir, "commit", "-q", "-am", "diverged")
	if result := Push(context.Background(), dir); result.State != protocol.PushStateFailed || result.Output == nil ||
		!strings.Contains(*result.Output, "rejected") {
		t.Fatalf("diverged push: %#v", result)
	}
	// Another remote but no origin, and no upstream: refused with a hint.
	git(t, dir, "checkout", "-q", "-b", "side")
	git(t, dir, "remote", "rename", "origin", "upstream")
	if result := Push(context.Background(), dir); result.State != protocol.PushStateFailed ||
		!strings.Contains(*result.Output, "no remote is named origin") {
		t.Fatalf("no origin: %#v", result)
	}
}

func TestPullRequestURL(t *testing.T) {
	for output, want := range map[string]string{
		"remote: \nremote: Create a pull request for 'feat/x' on GitHub by visiting:\nremote:      https://github.com/o/r/pull/new/feat/x\nremote: \nTo github.com:o/r.git\n * [new branch]      HEAD -> feat/x\n": "https://github.com/o/r/pull/new/feat/x",
		"remote: To create a merge request for feat, visit:\nremote:   https://gitlab.com/o/r/-/merge_requests/new?merge_request%5Bsource_branch%5D=feat\x1b[K\n":                                                  "https://gitlab.com/o/r/-/merge_requests/new?merge_request%5Bsource_branch%5D=feat",
		"remote: View it at https://example.com/docs\nremote: https://github.com/o/r/pull/new/x\n":                                                                                                                 "https://github.com/o/r/pull/new/x",
		"Everything up-to-date\n":                                                     "",
		"remote: http://github.com/o/r/pull/new/x\n":                                  "",
		"remote: https://github.com/o/r/pull/new/" + strings.Repeat("x", 2048) + "\n": "",
	} {
		got := pullRequestURL(output)
		if (got == nil) != (want == "") || (got != nil && *got != want) {
			t.Errorf("pullRequestURL(%q) = %v, want %q", output, got, want)
		}
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	var buffer tail
	for range 5000 {
		_, _ = buffer.Write([]byte("é"))
	}
	_, _ = buffer.Write([]byte("  end\n"))
	text := buffer.text()
	if len(buffer) > 2*tailBytes || !strings.HasSuffix(text, "end") || !strings.HasPrefix(text, "é") ||
		len([]rune(text)) != protocol.MaxGitOutput {
		t.Fatalf("tail = %d bytes, text %d runes", len(buffer), len([]rune(text)))
	}
}

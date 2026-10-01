package gitchanges

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// newRepo creates a repository on branch main, isolated from the user's and
// system git configuration.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func files(summary protocol.ChangesSummaryPayload) map[string]protocol.ChangedFile {
	byPath := map[string]protocol.ChangedFile{}
	for _, file := range summary.Files {
		byPath[file.Path] = file
	}
	return byPath
}

func TestSummaryAndPatch(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "mod.txt", "one\ntwo\nthree\n")
	write(t, dir, "del.txt", "gone\n")
	write(t, dir, "old.txt", "a\nb\nc\nd\ne\nf\n")
	write(t, dir, "bin.dat", "\x00one")
	write(t, dir, ".gitignore", "*.log\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")

	write(t, dir, "mod.txt", "one\n2\nthree")
	write(t, dir, "added.txt", "new\n")
	git(t, dir, "add", "added.txt")
	git(t, dir, "rm", "-q", "del.txt")
	git(t, dir, "mv", "old.txt", "new.txt")
	write(t, dir, "bin.dat", "\x00two")
	write(t, dir, "ignored.log", "secret\n")
	weird := "un tracked.txt"
	if runtime.GOOS != "windows" {
		weird = "un tracked\nname.txt"
	}
	write(t, dir, weird, "x\ny")

	summary := Summary(context.Background(), filepath.Join(dir, "."))
	if summary.State != protocol.ChangesStateReady || summary.Truncated || summary.Branch == nil || *summary.Branch != "main" {
		t.Fatalf("summary = %#v", summary)
	}
	if root, _ := filepath.EvalSymlinks(dir); summary.Root == nil || filepath.Clean(*summary.Root) != filepath.Clean(root) {
		t.Fatalf("root = %v, want %s", summary.Root, root)
	}
	got := files(summary)
	for path, want := range map[string]protocol.ChangedFile{
		"mod.txt":   {Status: protocol.Modified, Additions: 2, Deletions: 2},
		"added.txt": {Status: protocol.Added, Additions: 1},
		"del.txt":   {Status: protocol.Deleted, Deletions: 1},
		"new.txt":   {Status: protocol.Renamed},
		"bin.dat":   {Status: protocol.Modified, Binary: true},
		weird:       {Status: protocol.Untracked, Additions: 2},
	} {
		file, ok := got[path]
		want.Path, want.OldPath = path, file.OldPath
		if !ok || file != want {
			t.Errorf("%q = %#v, want %#v", path, file, want)
		}
	}
	if old := got["new.txt"].OldPath; old == nil || *old != "old.txt" {
		t.Errorf("rename old path = %v", old)
	}
	if len(summary.Files) != 6 || summary.Files[0].Path != "added.txt" {
		t.Errorf("files are not the six changes sorted by path: %#v", summary.Files)
	}

	patch := Patch(context.Background(), dir, "mod.txt")
	var text []string
	for _, line := range patch.Lines {
		text = append(text, fmt.Sprintf("%s %d %d %s", line.Kind, valueOrZero(line.OldLine), valueOrZero(line.NewLine), line.Text))
	}
	want := []string{"hunk 0 0 @@ -1,3 +1,3 @@", "context 1 1 one", "remove 2 0 two", "remove 3 0 three", "add 0 2 2", "add 0 3 three"}
	if patch.State != protocol.PatchStateReady || patch.Truncated || strings.Join(text, "|") != strings.Join(want, "|") {
		t.Fatalf("patch = %s, %v:\n%s", patch.State, patch.Truncated, strings.Join(text, "\n"))
	}
	if patch := Patch(context.Background(), dir, weird); patch.State != protocol.PatchStateReady || len(patch.Lines) != 3 ||
		patch.Lines[2].Text != "y" || *patch.Lines[2].NewLine != 2 {
		t.Errorf("untracked patch = %#v", patch)
	}
	if patch := Patch(context.Background(), dir, "bin.dat"); patch.State != protocol.PatchStateBinary || len(patch.Lines) != 0 {
		t.Errorf("binary patch = %#v", patch)
	}
	for _, path := range []string{"new.txt", "old.txt", "del.txt"} {
		if patch := Patch(context.Background(), dir, path); patch.State != protocol.PatchStateReady || patch.Path != path {
			t.Errorf("%s patch = %#v", path, patch)
		}
	}
	for _, path := range []string{"ignored.log", ".gitignore", "nothing.txt"} {
		if patch := Patch(context.Background(), dir, path); patch.State != protocol.Missing || len(patch.Lines) != 0 {
			t.Errorf("%s patch = %#v", path, patch)
		}
	}
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func TestSummaryWithoutCommits(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "staged.txt", "a\nb\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "loose.txt", "c\n")
	summary := Summary(context.Background(), dir)
	got := files(summary)
	if summary.State != protocol.ChangesStateReady || len(got) != 2 ||
		got["staged.txt"].Status != protocol.Added || got["staged.txt"].Additions != 2 || got["loose.txt"].Status != protocol.Untracked {
		t.Fatalf("summary = %#v", summary)
	}
	if patch := Patch(context.Background(), dir, "staged.txt"); patch.State != protocol.PatchStateReady || len(patch.Lines) != 3 {
		t.Fatalf("patch = %#v", patch)
	}
}

func TestSummaryOutsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	dir := t.TempDir()
	if summary := Summary(context.Background(), dir); summary.State != protocol.NotGit || summary.Root != nil || len(summary.Files) != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if patch := Patch(context.Background(), dir, "a.txt"); patch.State != protocol.Missing {
		t.Fatalf("patch = %#v", patch)
	}
	if summary := Summary(context.Background(), filepath.Join(dir, "absent")); summary.State != protocol.ChangesStateUnavailable {
		t.Fatalf("missing folder summary = %#v", summary)
	}
}

func TestLimits(t *testing.T) {
	dir := newRepo(t)
	for i := 0; i <= protocol.MaxChangedFiles; i++ {
		write(t, dir, fmt.Sprintf("f%03d.txt", i), "")
	}
	summary := Summary(context.Background(), dir)
	if len(summary.Files) != protocol.MaxChangedFiles || !summary.Truncated || summary.Files[299].Path != "f299.txt" {
		t.Fatalf("summary has %d files, truncated %v", len(summary.Files), summary.Truncated)
	}

	write(t, dir, "many.txt", strings.Repeat("line\n", 2500))
	if patch := Patch(context.Background(), dir, "many.txt"); len(patch.Lines) != protocol.MaxDiffLines || !patch.Truncated {
		t.Fatalf("many lines: %d, truncated %v", len(patch.Lines), patch.Truncated)
	}
	write(t, dir, "wide.txt", strings.Repeat(strings.Repeat("é", 1500)+"\n", 60))
	patch := Patch(context.Background(), dir, "wide.txt")
	if !patch.Truncated || len(patch.Lines) >= 60 || len([]rune(patch.Lines[1].Text)) != protocol.MaxDiffLineRunes {
		t.Fatalf("wide lines: %d, truncated %v", len(patch.Lines), patch.Truncated)
	}
	write(t, dir, "huge.txt", strings.Repeat("x", maxOutput+1024)) // git is killed at the output limit
	if patch := Patch(context.Background(), dir, "huge.txt"); patch.State != protocol.PatchStateReady || !patch.Truncated || len(patch.Lines) != 2 {
		t.Fatalf("huge line: %s, %d lines, truncated %v", patch.State, len(patch.Lines), patch.Truncated)
	}
	write(t, dir, "bad.txt", "ok\xff\n")
	if patch := Patch(context.Background(), dir, "bad.txt"); patch.Lines[1].Text != "ok\uFFFD" {
		t.Fatalf("invalid UTF-8 line = %q", patch.Lines[1].Text)
	}
}

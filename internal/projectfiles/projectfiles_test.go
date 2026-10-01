package projectfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// newRepo creates a repository isolated from the user's git configuration.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := realDir(t)
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

// plainDir is a folder git must not see as part of any repository.
func plainDir(t *testing.T) string {
	t.Helper()
	dir := realDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	return dir
}

// realDir resolves symlinks (macOS /var is /private/var), as git does.
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// valid checks a reply against the protocol, as the broker does before
// sending it.
func valid(t *testing.T, kind protocol.MessageType, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	session, generation := "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	message, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: kind,
		RequestID: "11111111-1111-4111-8111-111111111111", SessionID: &session, Generation: &generation, Payload: data})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeHost(message); err != nil {
		t.Fatalf("%s reply is not valid: %v", kind, err)
	}
}

func names(entries []protocol.FileEntry) []string {
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func TestListSortsFoldersFirstAndHidesGit(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "b.txt", "bb")
	write(t, repo, "A.txt", "a")
	write(t, repo, "c/x", "")
	write(t, repo, "Z/x", "")
	write(t, repo, "sub/.git", "gitdir: elsewhere\n") // a submodule's .git file
	ctx := context.Background()

	listed := List(ctx, repo, "")
	valid(t, protocol.FilesListed, listed)
	if listed.State != protocol.FilesListStateReady || listed.Root == nil || *listed.Root != filepath.ToSlash(repo) || listed.Truncated {
		t.Fatalf("listed = %#v", listed)
	}
	if got, want := names(listed.Entries), []string{"c", "sub", "Z", "A.txt", "b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
	if entry := listed.Entries[4]; entry.Kind != protocol.File || entry.Size == nil || *entry.Size != 2 {
		t.Fatalf("b.txt = %#v", entry)
	}
	if entry := listed.Entries[0]; entry.Kind != protocol.Dir || entry.Size != nil {
		t.Fatalf("c = %#v", entry)
	}
	if sub := List(ctx, repo, "sub"); sub.State != protocol.FilesListStateReady || len(sub.Entries) != 0 {
		t.Fatalf("sub = %#v", sub)
	}
	for _, name := range []string{".git", ".git/refs", ".GIT", "sub/.git"} {
		if got := List(ctx, repo, name); got.State != protocol.FilesListStateNotFound {
			t.Fatalf("list %q = %#v", name, got)
		}
	}
	for _, name := range []string{".git/config", ".GIT/config", ".git./config", "GIT~1/config", "sub/.git"} {
		if got := Read(ctx, repo, name); got.State != protocol.FilesReadStateNotFound || got.Text != nil {
			t.Fatalf("read %q = %#v", name, got)
		}
	}
	if got := List(ctx, repo, "b.txt"); got.State != protocol.NotDir {
		t.Fatalf("list of a file = %#v", got)
	}
	if got := List(ctx, repo, "missing"); got.State != protocol.FilesListStateNotFound {
		t.Fatalf("list of a missing folder = %#v", got)
	}
	if got := Read(ctx, repo, "c"); got.State != protocol.NotFile {
		t.Fatalf("read of a folder = %#v", got)
	}
	if got := Read(ctx, repo, "missing.txt"); got.State != protocol.FilesReadStateNotFound {
		t.Fatalf("read of a missing file = %#v", got)
	}
}

func TestListTruncatesAtTheEntryLimit(t *testing.T) {
	dir := plainDir(t)
	for index := range protocol.MaxFileEntries + 5 {
		write(t, dir, fmt.Sprintf("f%04d", index), "")
	}
	listed := List(context.Background(), dir, "")
	valid(t, protocol.FilesListed, listed)
	if listed.State != protocol.FilesListStateReady || len(listed.Entries) != protocol.MaxFileEntries || !listed.Truncated ||
		listed.Entries[0].Name != "f0000" || listed.Root == nil || *listed.Root != filepath.ToSlash(dir) {
		t.Fatalf("listed %d entries, truncated %v, first %q", len(listed.Entries), listed.Truncated, listed.Entries[0].Name)
	}

	previous := maxDirScan
	maxDirScan = 300
	t.Cleanup(func() { maxDirScan = previous })
	if scanned := List(context.Background(), dir, ""); len(scanned.Entries) > 600 || !scanned.Truncated {
		t.Fatalf("a folder scan past its limit listed %d, truncated %v", len(scanned.Entries), scanned.Truncated)
	}
}

func TestCleanPathRefusesTraversalAndAbsolutePaths(t *testing.T) {
	for _, name := range []string{"..", "../x", "a/../../x", "a/..", "/etc/passwd", "/", `\x`, `C:\x`, "C:/x", "a\x00b"} {
		if cleaned, err := CleanPath(name); err == nil {
			t.Fatalf("CleanPath(%q) = %q, want an error", name, cleaned)
		}
	}
	for name, want := range map[string]string{"": "", ".": "", "./": "", "a/./b//c/": "a/b/c", "src/main.go": "src/main.go"} {
		if got, err := CleanPath(name); err != nil || got != want {
			t.Fatalf("CleanPath(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestSymlinksCannotEscapeTheRoot(t *testing.T) {
	dir, outside := plainDir(t), realDir(t)
	write(t, outside, "secret.txt", "secret\n")
	write(t, dir, "sub/inner.txt", "inner\n")
	for target, link := range map[string]string{
		outside: "out", filepath.Join(outside, "secret.txt"): "secret", "sub": "inside", filepath.Join(dir, "nowhere"): "broken",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Skipf("symlinks are not available: %v", err)
		}
	}
	ctx := context.Background()
	listed := List(ctx, dir, "")
	valid(t, protocol.FilesListed, listed)
	kinds := map[string]protocol.FileKind{}
	for _, entry := range listed.Entries {
		kinds[entry.Name] = entry.Kind
		if entry.Kind == protocol.Link && entry.Size != nil {
			t.Fatalf("link %q has a size", entry.Name)
		}
	}
	want := map[string]protocol.FileKind{"out": protocol.Link, "secret": protocol.Link, "broken": protocol.Link, "inside": protocol.Dir, "sub": protocol.Dir}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for _, name := range []string{"secret", "out/secret.txt", "broken"} {
		if got := Read(ctx, dir, name); got.State != protocol.FilesReadStateNotFound || got.Text != nil {
			t.Fatalf("read %q = %#v", name, got)
		}
	}
	if got := List(ctx, dir, "out"); got.State != protocol.FilesListStateNotFound || len(got.Entries) != 0 {
		t.Fatalf("list through an escaping link = %#v", got)
	}
	if got := Read(ctx, dir, "inside/inner.txt"); got.State != protocol.FilesReadStateReady || *got.Text != "inner\n" {
		t.Fatalf("read through an inside link = %#v", got)
	}
}

func TestReadText(t *testing.T) {
	dir := plainDir(t)
	write(t, dir, "hello.txt", "héllo\n<b>&</b>\n")
	got := Read(context.Background(), dir, "hello.txt")
	valid(t, protocol.FilesContent, got)
	if got.State != protocol.FilesReadStateReady || got.Text == nil || *got.Text != "héllo\n<b>&</b>\n" || got.Truncated ||
		got.Size == nil || *got.Size != int64(len("héllo\n<b>&</b>\n")) || got.Path != "hello.txt" {
		t.Fatalf("read = %#v", got)
	}
	write(t, dir, "empty", "")
	if empty := Read(context.Background(), dir, "empty"); empty.State != protocol.FilesReadStateReady || empty.Text == nil || *empty.Text != "" {
		t.Fatalf("empty file = %#v", empty)
	}
}

func TestReadBinary(t *testing.T) {
	dir := plainDir(t)
	write(t, dir, "image.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	got := Read(context.Background(), dir, "image.png")
	valid(t, protocol.FilesContent, got)
	if got.State != protocol.FilesReadStateBinary || got.Text != nil || got.Size == nil || *got.Size != 16 {
		t.Fatalf("read = %#v", got)
	}
	// A NUL after the first 8 KiB is not looked at, and a NUL is never sent.
	write(t, dir, "late", strings.Repeat("a", binaryProbe)+"\x00\n")
	if late := Read(context.Background(), dir, "late"); late.State != protocol.FilesReadStateReady {
		t.Fatalf("late NUL = %#v", late.State)
	} else {
		valid(t, protocol.FilesContent, late) // the validator refuses NUL in text
	}
}

func TestReadInvalidUTF8(t *testing.T) {
	dir := plainDir(t)
	write(t, dir, "latin1.txt", strings.Repeat("plain text line\n", 20)+"caf\xe9\n")
	got := Read(context.Background(), dir, "latin1.txt")
	valid(t, protocol.FilesContent, got)
	if got.State != protocol.FilesReadStateReady || !strings.HasSuffix(*got.Text, "caf\uFFFD\n") {
		t.Fatalf("a little invalid UTF-8 = %#v", got)
	}
	write(t, dir, "noise", strings.Repeat("ab\xff\xfe", 100))
	if noise := Read(context.Background(), dir, "noise"); noise.State != protocol.FilesReadStateBinary || noise.Text != nil {
		t.Fatalf("mostly invalid UTF-8 = %#v", noise)
	}
}

func TestReadCutsABigFileAtALineBreak(t *testing.T) {
	dir := plainDir(t)
	var content strings.Builder
	for line := 0; content.Len() <= protocol.MaxFileTextBytes+1000; line++ {
		fmt.Fprintf(&content, "line %d of a big file\n", line)
	}
	write(t, dir, "big.log", content.String())
	got := Read(context.Background(), dir, "big.log")
	valid(t, protocol.FilesContent, got)
	text := *got.Text
	if got.State != protocol.FilesReadStateReady || !got.Truncated || len(text) > protocol.MaxFileTextBytes ||
		len(text) < protocol.MaxFileTextBytes-100 || !strings.HasSuffix(text, "\n") || !strings.HasPrefix(content.String(), text) ||
		*got.Size != int64(content.Len()) {
		t.Fatalf("read %d bytes, truncated %v, size %d", len(text), got.Truncated, *got.Size)
	}

	// One long line without a break is cut on a rune boundary.
	write(t, dir, "minified.js", strings.Repeat("é", protocol.MaxFileTextBytes))
	minified := Read(context.Background(), dir, "minified.js")
	valid(t, protocol.FilesContent, minified)
	if !minified.Truncated || !utf8.ValidString(*minified.Text) || len(*minified.Text) != protocol.MaxFileTextBytes {
		t.Fatalf("minified: %d bytes, truncated %v", len(*minified.Text), minified.Truncated)
	}
}

func TestReadKeepsTheJSONReplyUnderTheMessageLimit(t *testing.T) {
	dir := plainDir(t)
	// Each < becomes six bytes of JSON (\u003c): 256 KiB of them would be 1.5 MiB.
	write(t, dir, "angles.html", strings.Repeat(strings.Repeat("<", 63)+"\n", protocol.MaxFileTextBytes/64))
	got := Read(context.Background(), dir, "angles.html")
	valid(t, protocol.FilesContent, got) // DecodeHost refuses a message over 1 MiB
	data, _ := json.Marshal(got)
	if got.State != protocol.FilesReadStateReady || !got.Truncated || len(data) > textBudget+1024 || !strings.HasSuffix(*got.Text, "\n") {
		t.Fatalf("reply %d bytes, truncated %v", len(data), got.Truncated)
	}
}

func TestReadRefusesAFileOver50MiB(t *testing.T) {
	dir := plainDir(t)
	file, err := os.Create(filepath.Join(dir, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxFileBytes + 1); err != nil { // sparse: no disk used
		t.Fatal(err)
	}
	_ = file.Close()
	got := Read(context.Background(), dir, "huge.bin")
	valid(t, protocol.FilesContent, got)
	if got.State != protocol.TooLarge || got.Text != nil || got.Size == nil || *got.Size != MaxFileBytes+1 {
		t.Fatalf("read = %#v", got)
	}
}

func TestSearchInARepositorySkipsIgnoredFilesAndRanksNamesFirst(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, ".gitignore", "node_modules/\nbuild/\n")
	for _, name := range []string{"src/app.go", "src/app_test.go", "docs/application.md", "app/readme.md", "main.go"} {
		write(t, repo, name, "x\n")
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	write(t, repo, "notes/app-notes.txt", "untracked\n")
	write(t, repo, "node_modules/app/index.js", "ignored\n")
	write(t, repo, "build/app.out", "ignored\n")
	write(t, repo, "nested/.git/HEAD", "ref: refs/heads/main\n")

	// A session in a subfolder searches from the repository root.
	found := Search(context.Background(), filepath.Join(repo, "src"), "APP")
	valid(t, protocol.FilesFound, found)
	want := []string{"src/app.go", "src/app_test.go", "docs/application.md", "notes/app-notes.txt", "app/readme.md"}
	if found.State != protocol.FilesSearchStateReady || found.Query != "APP" || found.Truncated || !reflect.DeepEqual(found.Paths, want) {
		t.Fatalf("found = %#v, want %q", found, want)
	}
	if none := Search(context.Background(), repo, "zzz"); none.State != protocol.FilesSearchStateReady || len(none.Paths) != 0 {
		t.Fatalf("no match = %#v", none)
	}
}

func TestSearchOutsideARepositoryWalksABoundedTree(t *testing.T) {
	dir := plainDir(t)
	for _, name := range []string{"a/app.txt", "node_modules/x/app.js", ".git/app", "b/c/APP.md", "b/readme"} {
		write(t, dir, name, "")
	}
	found := Search(context.Background(), dir, "app")
	valid(t, protocol.FilesFound, found)
	if want := []string{"a/app.txt", "b/c/APP.md"}; found.State != protocol.FilesSearchStateReady || found.Truncated || !reflect.DeepEqual(found.Paths, want) {
		t.Fatalf("found = %#v, want %q", found, want)
	}

	previous := maxWalk
	maxWalk = 3
	t.Cleanup(func() { maxWalk = previous })
	if cut := Search(context.Background(), dir, "app"); cut.State != protocol.FilesSearchStateReady || !cut.Truncated {
		t.Fatalf("a walk past its limit = %#v", cut)
	}
}

func TestSearchCapsMatches(t *testing.T) {
	dir := plainDir(t)
	for index := range protocol.MaxFoundPaths + 10 {
		write(t, dir, fmt.Sprintf("m%03d.txt", index), "")
	}
	found := Search(context.Background(), dir, "m")
	valid(t, protocol.FilesFound, found)
	if len(found.Paths) != protocol.MaxFoundPaths || !found.Truncated || found.Paths[0] != "m000.txt" {
		t.Fatalf("found %d, truncated %v", len(found.Paths), found.Truncated)
	}
}

func TestRootIsTheRepositoryTopLevel(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "src/deep/main.go", "package main\n")
	write(t, repo, "README.md", "# hi\n")
	ctx := context.Background()
	listed := List(ctx, filepath.Join(repo, "src", "deep"), "")
	if listed.Root == nil || *listed.Root != filepath.ToSlash(repo) || !reflect.DeepEqual(names(listed.Entries), []string{"src", "README.md"}) {
		t.Fatalf("listed = %#v", listed)
	}
	if got := Read(ctx, filepath.Join(repo, "src"), "README.md"); got.State != protocol.FilesReadStateReady || *got.Text != "# hi\n" {
		t.Fatalf("read from a subfolder = %#v", got)
	}
	// Outside a repository the root is the folder itself.
	plain := plainDir(t)
	if listed := List(ctx, plain, ""); listed.Root == nil || *listed.Root != filepath.ToSlash(plain) {
		t.Fatalf("plain root = %#v", listed.Root)
	}
	if gone := List(ctx, filepath.Join(plain, "gone"), ""); gone.State != protocol.FilesListStateUnavailable {
		t.Fatalf("missing folder = %#v", gone)
	}
}

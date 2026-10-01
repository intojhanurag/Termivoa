package gitchanges

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// repoWithOrigin is a writable repository named "proj" with a clone of it as
// origin, so refs/remotes/origin/* and origin/HEAD exist.
func repoWithOrigin(t *testing.T) string {
	t.Helper()
	upstream := writableRepo(t)
	git(t, upstream, "branch", "release")
	dir := filepath.Join(t.TempDir(), "proj")
	git(t, filepath.Dir(dir), "clone", "-q", upstream, dir)
	git(t, dir, "config", "user.name", "test")
	git(t, dir, "config", "user.email", "test@example.com")
	return dir
}

func branchNames(listed protocol.BranchesListedPayload) []string {
	var names []string
	for _, branch := range listed.Branches {
		names = append(names, fmt.Sprintf("%s:%v", branch.Name, branch.Remote))
	}
	return names
}

func TestBranchesListing(t *testing.T) {
	dir := repoWithOrigin(t)
	git(t, dir, "checkout", "-q", "-b", "feat/x")
	listed := Branches(context.Background(), dir)
	if listed.State != protocol.ChangesStateReady || listed.Truncated {
		t.Fatalf("listed = %+v", listed)
	}
	want := []string{"feat/x:false", "main:false", "origin/main:true", "origin/release:true"}
	if got := branchNames(listed); !slices.Equal(got, want) {
		t.Fatalf("branches = %v, want %v (origin/HEAD skipped)", got, want)
	}
	if *listed.Repo != "proj" || *listed.Current != "feat/x" || *listed.DefaultBranch != "main" {
		t.Fatalf("repo/current/default = %q %q %q", *listed.Repo, *listed.Current, *listed.DefaultBranch)
	}

	// No local branch for origin/HEAD: the remote branch is the default.
	git(t, dir, "remote", "set-head", "origin", "release")
	if got := *Branches(context.Background(), dir).DefaultBranch; got != "origin/release" {
		t.Errorf("default = %q, want origin/release", got)
	}
	// Without origin/HEAD: main, then master, then the current branch.
	git(t, dir, "remote", "set-head", "origin", "-d")
	if got := *Branches(context.Background(), dir).DefaultBranch; got != "main" {
		t.Errorf("default = %q, want main", got)
	}
	git(t, dir, "branch", "-q", "-m", "main", "master")
	if got := *Branches(context.Background(), dir).DefaultBranch; got != "master" {
		t.Errorf("default = %q, want master", got)
	}
	git(t, dir, "branch", "-q", "-D", "master")
	if got := *Branches(context.Background(), dir).DefaultBranch; got != "feat/x" {
		t.Errorf("default = %q, want the current branch", got)
	}
	git(t, dir, "checkout", "-q", "--detach")
	if listed := Branches(context.Background(), dir); listed.Current != nil {
		t.Errorf("detached current = %q", *listed.Current)
	}

	for index := range protocol.MaxBranches + 5 {
		git(t, dir, "branch", fmt.Sprintf("b%03d", index))
	}
	listed = Branches(context.Background(), dir)
	if len(listed.Branches) != protocol.MaxBranches || !listed.Truncated {
		t.Errorf("listed %d branches, truncated %v", len(listed.Branches), listed.Truncated)
	}

	if listed := Branches(context.Background(), t.TempDir()); listed.State != protocol.NotGit || listed.Repo != nil {
		t.Errorf("outside a repository: %+v", listed)
	}
}

func TestCreateAndRemoveWorktree(t *testing.T) {
	dir := repoWithOrigin(t)
	root := filepath.Join(t.TempDir(), "termivoa", "worktrees")
	ctx := context.Background()

	created, err := CreateWorktree(ctx, dir, root, "brave-otter", "origin/release")
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != filepath.Join(root, "proj", "brave-otter") || created.Repo != "proj" {
		t.Fatalf("created = %+v", created)
	}
	if got := output(t, created.Path, "symbolic-ref", "--short", "HEAD"); got != "termivoa/brave-otter" {
		t.Fatalf("worktree branch = %q", got)
	}
	if got := output(t, created.Path, "rev-parse", "HEAD"); got != output(t, dir, "rev-parse", "origin/release") {
		t.Fatalf("worktree HEAD %s is not origin/release", got)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "keep.txt")); err != nil {
		t.Fatal(err)
	}
	if name, repo, ok := WorktreeOf(root, created.Path); !ok || name != "brave-otter" || repo != "proj" {
		t.Fatalf("WorktreeOf = %q %q %v", name, repo, ok)
	}

	// From inside the linked worktree, the main repository is the same.
	listed := Branches(ctx, created.Path)
	if *listed.Repo != "proj" || *listed.Current != "termivoa/brave-otter" {
		t.Fatalf("listing in the worktree: %+v", listed)
	}
	second, err := CreateWorktree(ctx, created.Path, root, "calm-fox", "main")
	if err != nil || second.Path != filepath.Join(root, "proj", "calm-fox") {
		t.Fatalf("create from a worktree = %+v, %v", second, err)
	}

	for name, test := range map[string]struct {
		name, base string
		want       error
	}{
		"same name":      {"brave-otter", "main", ErrWorktreeExists},
		"bad name":       {"Brave_Otter", "main", ErrWorktreeName},
		"path name":      {"../x", "main", ErrWorktreeName},
		"unknown base":   {"new-one", "nope", ErrWorktreeBase},
		"tag as base":    {"new-one", "v1", ErrWorktreeBase},
		"option base":    {"new-one", "--orphan", ErrWorktreeBase},
		"head as base":   {"new-one", "origin/HEAD", ErrWorktreeBase},
		"commit as base": {"new-one", "HEAD~0", ErrWorktreeBase},
	} {
		if name == "tag as base" {
			git(t, dir, "tag", "v1")
		}
		if _, err := CreateWorktree(ctx, dir, root, test.name, test.base); !errors.Is(err, test.want) {
			t.Errorf("%s: err = %v, want %v", name, err, test.want)
		}
	}
	// A branch with the name, without a folder, is also taken.
	git(t, dir, "branch", "termivoa/taken")
	if _, err := CreateWorktree(ctx, dir, root, "taken", "main"); !errors.Is(err, ErrWorktreeExists) {
		t.Errorf("existing branch: %v", err)
	}
	if _, err := CreateWorktree(ctx, t.TempDir(), root, "x", "main"); !errors.Is(err, ErrNotRepository) {
		t.Errorf("outside a repository: %v", err)
	}

	// Dirty: refused without force, removed with force; branches stay.
	write(t, created.Path, "new.txt", "new\n")
	if got := RemoveWorktree(ctx, created.Path, false, nil); got.State != protocol.Dirty {
		t.Fatalf("dirty remove = %+v", got)
	}
	if _, err := os.Stat(created.Path); err != nil {
		t.Fatal("dirty worktree was removed")
	}
	if got := RemoveWorktree(ctx, created.Path, true, nil); got.State != protocol.Removed {
		t.Fatalf("forced remove = %+v", got)
	}
	if got := RemoveWorktree(ctx, second.Path, false, nil); got.State != protocol.Removed {
		t.Fatalf("clean remove = %+v", got)
	}
	for _, path := range []string{created.Path, second.Path} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists: %v", path, err)
		}
	}
	output(t, dir, "rev-parse", "--verify", "termivoa/brave-otter")
	output(t, dir, "rev-parse", "--verify", "termivoa/calm-fox")
	if got := RemoveWorktree(ctx, dir, true, nil); got.State != protocol.WorktreeRemoveStateFailed {
		t.Errorf("removing the main worktree = %+v", got)
	}
}

func TestWorktreeLimitAndRollBack(t *testing.T) {
	dir := repoWithOrigin(t)
	root := t.TempDir()
	ctx := context.Background()
	created, err := CreateWorktree(ctx, dir, root, "one", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := RollBack(ctx, created); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rolled back worktree still exists: %v", err)
	}
	if out := output(t, dir, "branch", "--list", "termivoa/one"); out != "" {
		t.Errorf("rolled back branch still exists: %q", out)
	}
	for index := range MaxWorktrees {
		if err := os.MkdirAll(filepath.Join(root, "proj", fmt.Sprint("w", index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateWorktree(ctx, dir, root, "one", "main"); !errors.Is(err, ErrWorktreeLimit) {
		t.Errorf("over the limit: %v", err)
	}
}

func TestWorktreeOf(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"home", "u", "termivoa", "worktrees")
	for _, test := range []struct {
		dir  string
		ok   bool
		name string
	}{
		{filepath.Join(root, "proj", "brave-otter"), true, "brave-otter"},
		{filepath.Join(root, "proj"), false, ""},
		{filepath.Join(root, "proj", "brave-otter", "sub"), false, ""},
		{filepath.Join(root, "proj", "Bad_Name"), false, ""},
		{filepath.Join(filepath.Dir(root), "other", "brave-otter"), false, ""},
		{root, false, ""},
	} {
		name, _, ok := WorktreeOf(root, test.dir)
		if ok != test.ok || name != test.name {
			t.Errorf("WorktreeOf(%q) = %q %v", test.dir, name, ok)
		}
	}
	if _, _, ok := WorktreeOf("", filepath.Join("proj", "x")); ok {
		t.Error("an empty root matched")
	}
}

func TestRemoveTreatsIgnoredFilesAsDirty(t *testing.T) {
	dir := repoWithOrigin(t)
	write(t, dir, ".gitignore", ".env\n")
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "ignore")
	ctx := context.Background()
	created, err := CreateWorktree(ctx, dir, t.TempDir(), "keep-env", "main")
	if err != nil {
		t.Fatal(err)
	}
	write(t, created.Path, ".env", "SECRET=1\n")
	called := false
	if got := RemoveWorktree(ctx, created.Path, false, func() { called = true }); got.State != protocol.Dirty || called {
		t.Fatalf("remove with an ignored .env = %+v (beforeRemove ran: %v)", got, called)
	}
	if _, err := os.Stat(filepath.Join(created.Path, ".env")); err != nil {
		t.Fatalf("the ignored .env was deleted: %v", err)
	}
	if got := RemoveWorktree(ctx, created.Path, true, func() { called = true }); got.State != protocol.Removed || !called {
		t.Fatalf("forced remove = %+v (beforeRemove ran: %v)", got, called)
	}
}

func TestFailedAddLeavesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell script")
	}
	dir := repoWithOrigin(t)
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lefthook: lint failed' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	_, err := CreateWorktree(context.Background(), dir, root, "hooked", "main")
	var failure *AddError
	if !errors.As(err, &failure) || !strings.Contains(failure.Output, "lefthook: lint failed") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "proj", "hooked")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder is left: %v", err)
	}
	if out := output(t, dir, "branch", "--list", "termivoa/hooked"); out != "" {
		t.Errorf("the branch is left: %q", out)
	}
	if out := output(t, dir, "worktree", "list", "--porcelain"); strings.Contains(out, "hooked") {
		t.Errorf("git still lists the worktree: %s", out)
	}
}

func TestRepositoryFolderNamesStayInsideTheRoot(t *testing.T) {
	newRepo(t) // git config isolation
	root := filepath.Join(t.TempDir(), "root")
	for _, name := range []string{"..git", "...git"} {
		bare := filepath.Join(t.TempDir(), name)
		git(t, filepath.Dir(bare), "init", "-q", "--bare", name)
		if _, err := CreateWorktree(context.Background(), bare, root, "x", "main"); !errors.Is(err, ErrWorktreeName) {
			t.Errorf("bare repository %q: err = %v", name, err)
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was made: %v", err)
	}
}

func TestRollBackKeepsAMovedBranch(t *testing.T) {
	dir := repoWithOrigin(t)
	ctx := context.Background()
	created, err := CreateWorktree(ctx, dir, t.TempDir(), "moved", "main")
	if err != nil {
		t.Fatal(err)
	}
	write(t, created.Path, "work.txt", "work\n")
	git(t, created.Path, "add", "work.txt")
	git(t, created.Path, "commit", "-q", "-m", "work")
	if err := RollBack(ctx, created); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("folder still exists: %v", err)
	}
	if got := output(t, dir, "log", "-1", "--format=%s", "termivoa/moved"); got != "work" {
		t.Errorf("moved branch = %q, want it kept at the new commit", got)
	}
}

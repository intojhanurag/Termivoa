package gitchanges

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Worktrees (WorktreeCreate in protocol/v1/broker-rpc.schema.json). A Termivoa worktree lives at
// <root>/<repo>/<name> on the branch termivoa/<name>, where repo is the base
// name of the main repository root. Branches are never deleted, except the
// one a failed create made moments before (RollBack).

// MaxWorktrees is how many worktrees one repository may have under the root.
const MaxWorktrees = 50

// BranchPrefix starts the name of every branch a worktree create makes.
const BranchPrefix = "termivoa/"

var (
	ErrNotRepository  = errors.New("gitchanges: the folder is not in a git repository")
	ErrWorktreeExists = errors.New("gitchanges: the worktree folder or branch already exists")
	ErrWorktreeBase   = errors.New("gitchanges: the base is not a branch of the repository")
	ErrWorktreeLimit  = errors.New("gitchanges: the repository has too many worktrees")
	ErrWorktreeName   = errors.New("gitchanges: invalid worktree or repository name")
)

// branchRef is one branch: its full ref name and the short name git prints.
type branchRef struct {
	full, short string
	remote      bool
}

// Branches lists the branches of the repository that holds dir: local first,
// then remote-tracking ones without */HEAD, at most protocol.MaxBranches.
func Branches(ctx context.Context, dir string) protocol.BranchesListedPayload {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	listed := protocol.BranchesListedPayload{State: protocol.ChangesStateUnavailable, Branches: []protocol.BranchRef{}}
	main, state := openMain(ctx, dir)
	if state != protocol.ChangesStateReady {
		listed.State = state
		return listed
	}
	refs, err := branchRefs(ctx, main)
	if err != nil {
		return listed
	}
	listed.State = protocol.ChangesStateReady
	for _, ref := range refs {
		if len(listed.Branches) == protocol.MaxBranches {
			listed.Truncated = true
			break
		}
		listed.Branches = append(listed.Branches, protocol.BranchRef{Name: ref.short, Remote: ref.remote})
	}
	if repo := repoName(main); protocol.ValidateWorktreeRepo(repo) == nil {
		listed.Repo = &repo
	}
	current := ""
	if out, _, err := run(ctx, dir, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		current = strings.TrimSpace(string(out))
	}
	if protocol.ValidBranchName(current) {
		listed.Current = &current
	}
	if name := defaultBranch(ctx, main, refs, current); name != "" {
		listed.DefaultBranch = &name
	}
	return listed
}

// defaultBranch is the local branch origin/HEAD names, else that remote
// branch, else main, else master, else the current branch; "" if none exists.
func defaultBranch(ctx context.Context, main string, refs []branchRef, current string) string {
	byFull := map[string]string{}
	for _, ref := range refs {
		byFull[ref.full] = ref.short
	}
	var candidates []string
	if out, _, err := run(ctx, main, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		remote := strings.TrimSpace(string(out))
		candidates = append(candidates, "refs/heads/"+strings.TrimPrefix(remote, "refs/remotes/origin/"), remote)
	}
	candidates = append(candidates, "refs/heads/main", "refs/heads/master", "refs/heads/"+current)
	for _, full := range candidates {
		if short, ok := byFull[full]; ok {
			return short
		}
	}
	return ""
}

// branchRefs lists every branch with a name the protocol can carry, sorted by
// full ref name, so refs/heads come before refs/remotes.
func branchRefs(ctx context.Context, main string) ([]branchRef, error) {
	out, _, err := run(ctx, main, "for-each-ref", "--format=%(refname)%00%(refname:short)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var refs []branchRef
	for _, line := range strings.Split(string(out), "\n") {
		full, short, ok := strings.Cut(line, "\x00")
		if !ok || strings.HasSuffix(full, "/HEAD") || !protocol.ValidBranchName(short) {
			continue
		}
		refs = append(refs, branchRef{full: full, short: short, remote: strings.HasPrefix(full, "refs/remotes/")})
	}
	return refs, nil
}

// openMain finds the root of the main repository that holds dir, also when
// dir is in a linked worktree. For a bare repository it is the repository.
func openMain(ctx context.Context, dir string) (string, protocol.ChangesState) {
	out, _, err := run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", protocol.NotGit
		}
		return "", protocol.ChangesStateUnavailable
	}
	common := filepath.Clean(filepath.FromSlash(strings.TrimSuffix(string(out), "\n")))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), protocol.ChangesStateReady
	}
	return common, protocol.ChangesStateReady
}

func repoName(main string) string {
	if name := strings.TrimSuffix(filepath.Base(main), ".git"); name != "" {
		return name
	}
	return filepath.Base(main)
}

// Created is a worktree that CreateWorktree made.
type Created struct {
	Path, Repo, Main, Commit string
}

// CreateWorktree adds the worktree <root>/<repo>/<name> on the new branch
// termivoa/<name> from base, a branch of the repository that holds dir. The
// new branch tracks nothing, so a push makes its own remote branch.
func CreateWorktree(ctx context.Context, dir, root, name, base string) (Created, error) {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if !protocol.ValidWorktreeName(name) || root == "" {
		return Created{}, ErrWorktreeName
	}
	main, state := openMain(ctx, dir)
	switch state {
	case protocol.NotGit:
		return Created{}, ErrNotRepository
	case protocol.ChangesStateUnavailable:
		return Created{}, errors.New("gitchanges: git is not available or failed to read the repository")
	}
	created := Created{Repo: repoName(main), Main: main}
	if protocol.ValidateWorktreeRepo(created.Repo) != nil {
		return Created{}, ErrWorktreeName
	}
	parent := filepath.Join(root, created.Repo)
	created.Path = filepath.Join(parent, name)
	if _, err := os.Lstat(created.Path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return Created{}, ErrWorktreeExists
	}
	if _, _, err := run(ctx, main, "rev-parse", "-q", "--verify", "refs/heads/"+BranchPrefix+name); err == nil {
		return Created{}, ErrWorktreeExists
	}
	if entries, err := os.ReadDir(parent); err == nil && len(entries) >= MaxWorktrees {
		return Created{}, ErrWorktreeLimit
	}
	refs, err := branchRefs(ctx, main)
	if err != nil {
		return Created{}, err
	}
	full := ""
	for _, ref := range refs {
		if ref.short == base {
			full = ref.full
			break
		}
	}
	if full == "" {
		return Created{}, ErrWorktreeBase
	}
	out, _, err := run(ctx, main, "rev-parse", "-q", "--verify", full+"^{commit}")
	if err != nil {
		return Created{}, ErrWorktreeBase
	}
	created.Commit = strings.TrimSpace(string(out))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Created{}, err
	}
	if output, err := runWrite(ctx, main, "worktree", "add", "--no-track", "-b", BranchPrefix+name, created.Path, created.Commit); err != nil {
		return Created{}, errors.New("gitchanges: git worktree add failed: " + failureText(ctx, output, err))
	}
	return created, nil
}

// RollBack undoes a create whose session did not start: it removes the
// worktree and deletes its branch only while that still points at the commit
// the create made it at.
func RollBack(ctx context.Context, created Created) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	output, err := runWrite(ctx, created.Main, "worktree", "remove", "--force", created.Path)
	if err != nil {
		return errors.New("gitchanges: " + failureText(ctx, output, err))
	}
	name := filepath.Base(created.Path)
	if output, err := runWrite(ctx, created.Main, "update-ref", "-d", "refs/heads/"+BranchPrefix+name, created.Commit); err != nil {
		return errors.New("gitchanges: " + failureText(ctx, output, err))
	}
	return nil
}

// WorktreeOf reports the name and repository of dir when it is a Termivoa
// worktree folder, <root>/<repo>/<name>. It only looks at the path.
func WorktreeOf(root, dir string) (name, repo string, ok bool) {
	if root == "" || dir == "" {
		return "", "", false
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 || parts[0] == ".." || !protocol.ValidWorktreeName(parts[1]) || protocol.ValidateWorktreeRepo(parts[0]) != nil {
		return "", "", false
	}
	return parts[1], parts[0], true
}

// RemoveWorktree removes the linked worktree at path with git worktree
// remove. Without force, a worktree with changes or untracked files is kept
// (dirty). The branch is kept.
func RemoveWorktree(ctx context.Context, path string, force bool) protocol.WorktreeRemovedPayload {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	main, state := openMain(ctx, path)
	if state != protocol.ChangesStateReady {
		return removeFailed("the session folder is not a git worktree or git failed to read it")
	}
	top, _, err := run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || !sameFolder(strings.TrimSuffix(string(top), "\n"), path) || sameFolder(main, path) {
		return removeFailed("the session folder is not a linked git worktree")
	}
	if !force {
		out, truncated, err := run(ctx, path, "status", "--porcelain", "--untracked-files=normal")
		if err != nil {
			return removeFailed("could not read the worktree status: " + err.Error())
		}
		if truncated || len(strings.TrimSpace(string(out))) > 0 {
			return protocol.WorktreeRemovedPayload{State: protocol.Dirty}
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	output, err := runWrite(ctx, main, append(args, path)...)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return protocol.WorktreeRemovedPayload{State: protocol.Removed}
	case !force && errors.As(err, &exit) && strings.Contains(string(output), "modified or untracked files"):
		return protocol.WorktreeRemovedPayload{State: protocol.Dirty} // Changed after the status check.
	default:
		return removeFailed(failureText(ctx, output, err))
	}
}

func sameFolder(a, b string) bool {
	first, err := os.Stat(filepath.FromSlash(a))
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

func removeFailed(output string) protocol.WorktreeRemovedPayload {
	output = tail(output).text()
	return protocol.WorktreeRemovedPayload{State: protocol.WorktreeRemoveStateFailed, Output: &output}
}

package gitchanges

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Write actions (changes.commit and changes.push). Unlike reads they take
// git's locks as usual and let the repository's hooks run, with a longer
// timeout. They never use a shell and never force.
const (
	writeTimeout = 120 * time.Second // Hooks and remotes can be slow.
	tailBytes    = 8 << 10           // Output kept from a write command.
)

// Commit commits exactly the given paths of a fresh listing with message.
// A rename commits both of its paths. Untracked paths are added first; other
// staged files stay staged and out of the commit (git commit --only).
func Commit(ctx context.Context, dir, message string, paths []string) protocol.ChangesCommittedPayload {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := protocol.ValidateCommitMessage(message); err != nil {
		return commitFailed(err.Error())
	}
	repo, refusal := openForWrite(ctx, dir)
	if refusal != "" {
		return commitFailed(refusal)
	}
	if state := inProgress(ctx, repo); state != "" {
		return commitFailed("a " + state + " is in progress in " + validUTF8(repo.root) + "; finish or abort it first")
	}
	entries, _, err := list(ctx, repo)
	if err != nil {
		return commitFailed("could not list the changes: " + err.Error())
	}
	byPath := map[string][]int{}
	for index, change := range entries {
		byPath[change.file.Path] = append(byPath[change.file.Path], index)
		if change.file.OldPath != nil {
			byPath[*change.file.OldPath] = append(byPath[*change.file.OldPath], index)
		}
	}
	var pathspecs, untracked []string
	seen := map[string]bool{}
	for _, path := range paths {
		if len(byPath[path]) == 0 {
			return commitFailed(path + " is not in the current changes; refresh and try again")
		}
		for _, index := range byPath[path] {
			change := entries[index]
			for _, raw := range []string{change.raw, change.rawOld} {
				// :(literal) instead of --literal-pathspecs, which would
				// reach the hooks' own git commands through the environment.
				if raw != "" && !seen[raw] {
					seen[raw] = true
					pathspecs = append(pathspecs, ":(literal)"+raw)
				}
			}
			// After git rm --cached a path is both deleted and untracked;
			// adding it commits it as it is in the working tree.
			if change.file.Status == protocol.Untracked && !slices.Contains(untracked, ":(literal)"+change.raw) {
				untracked = append(untracked, ":(literal)"+change.raw)
			}
		}
	}
	// commit --only takes tracked paths (also deleted ones) from the working
	// tree, but refuses paths unknown to git, so untracked ones go first.
	if len(untracked) > 0 {
		if output, err := runWrite(ctx, repo.root, append([]string{"add", "--"}, untracked...)...); err != nil {
			return commitFailed(failureText(ctx, output, err))
		}
	}
	base, err := diffBase(ctx, repo)
	if err != nil {
		return commitFailed("could not read HEAD: " + err.Error())
	}
	output, err := runWrite(ctx, repo.root, append([]string{"diff", "--quiet", "--no-ext-diff", "--no-textconv", base, "--"}, pathspecs...)...)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return protocol.ChangesCommittedPayload{State: protocol.Nothing}
	case !errors.As(err, &exit) || exit.ExitCode() != 1:
		return commitFailed(failureText(ctx, output, err))
	}
	output, err = runWrite(ctx, repo.root, append([]string{"commit", "--only", "-m", message, "--"}, pathspecs...)...)
	if err != nil {
		return commitFailed(failureText(ctx, output, err))
	}
	out, _, err := run(ctx, repo.root, "rev-parse", "--short", "HEAD")
	hash := strings.TrimSpace(string(out))
	if err != nil || len(hash) < 4 || len(hash) > 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return commitFailed("committed, but could not read the new commit: " + fmt.Sprint(err))
	}
	return protocol.ChangesCommittedPayload{State: protocol.Committed, Commit: &hash}
}

// Push pushes the current branch to its upstream, or to origin with a new
// upstream when it has none. It never forces.
func Push(ctx context.Context, dir string) protocol.ChangesPushedPayload {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	repo, refusal := openForWrite(ctx, dir)
	if refusal != "" {
		return pushFailed(refusal)
	}
	out, _, err := run(ctx, repo.root, "symbolic-ref", "-q", "--short", "HEAD")
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" {
		return pushFailed("HEAD is not on a branch; check out a branch to push")
	}
	args := []string{"push"}
	if _, _, err := run(ctx, repo.root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err != nil {
		out, _, err := run(ctx, repo.root, "remote")
		if err != nil {
			return pushFailed("could not list the remotes: " + err.Error())
		}
		remotes := strings.Fields(string(out))
		switch {
		case len(remotes) == 0:
			return protocol.ChangesPushedPayload{State: protocol.NoRemote}
		case !slices.Contains(remotes, "origin"):
			return pushFailed(validUTF8(branch) + " has no upstream and no remote is named origin; set one with git push -u <remote> " + validUTF8(branch))
		}
		args = append(args, "-u", "origin", "HEAD")
	}
	output, err := runWrite(ctx, repo.root, args...)
	if err != nil {
		return pushFailed(failureText(ctx, output, err))
	}
	pushed := protocol.ChangesPushedPayload{State: protocol.Pushed, URL: pullRequestURL(string(output))}
	if name := validUTF8(branch); utf8.RuneCountInString(name) <= 256 {
		pushed.Branch = &name
	}
	return pushed
}

// openForWrite finds the repository or says why it cannot.
func openForWrite(ctx context.Context, dir string) (repository, string) {
	repo, state := open(ctx, dir)
	switch state {
	case protocol.ChangesStateReady:
		return repo, ""
	case protocol.NotGit:
		return repo, "the session folder is not in a git repository"
	default:
		return repo, "git is not available or failed to read the repository"
	}
}

// inProgress names a merge, rebase, cherry-pick or revert that is waiting to
// be finished; committing in the middle of one would mix up the user's work.
func inProgress(ctx context.Context, repo repository) string {
	out, _, err := run(ctx, repo.root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "git operation check" // Refuse rather than guess.
	}
	gitDir := strings.TrimSuffix(string(out), "\n")
	for _, marker := range []struct{ file, state string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
	} {
		if _, err := os.Lstat(filepath.Join(gitDir, marker.file)); err == nil {
			return marker.state
		}
	}
	return ""
}

// pullRequestLink finds https links in push output; pullRequestURL keeps the
// first that opens a new pull or merge request (GitHub, GitLab, Bitbucket).
var pullRequestLink = regexp.MustCompile(`https://[^\s\p{Cc}]+`)

func pullRequestURL(output string) *string {
	for _, link := range pullRequestLink.FindAllString(validUTF8(output), -1) {
		if (strings.Contains(link, "/pull/new/") || strings.Contains(link, "/merge_requests/new") ||
			strings.Contains(link, "/pull-requests/new")) && protocol.ValidateLinkURL(link) == nil {
			return &link
		}
	}
	return nil
}

// runWrite runs a git command that may change the repository. Unlike run it
// takes locks, leaves the user's git and ssh settings alone (hooks see a
// normal environment), and returns the last tailBytes of stdout and stderr
// together. stdin is the null device, and git never prompts.
func runWrite(ctx context.Context, dir string, args ...string) (tail, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	var output tail
	cmd.Stdout, cmd.Stderr = &output, &output
	err := runWriteCommand(cmd)
	return output, err
}

// tail keeps the last tailBytes written to it. It grows to twice that before
// it drops the start, so small writes stay cheap.
type tail []byte

func (buffer *tail) Write(data []byte) (int, error) {
	*buffer = append(*buffer, data...)
	if len(*buffer) > 2*tailBytes {
		*buffer = append((*buffer)[:0], (*buffer)[len(*buffer)-tailBytes:]...)
	}
	return len(data), nil
}

// text is the last protocol.MaxGitOutput characters, valid UTF-8 and trimmed.
func (buffer tail) text() string {
	data := []byte(buffer)
	if len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:] // A rune cut by the start of the tail.
	}
	text := strings.TrimSpace(validUTF8(string(data)))
	if extra := utf8.RuneCountInString(text) - protocol.MaxGitOutput; extra > 0 {
		for index := range text {
			if extra == 0 {
				text = strings.TrimSpace(text[index:])
				break
			}
			extra--
		}
	}
	return text
}

// failureText is what git printed, or why it did not finish.
func failureText(ctx context.Context, output tail, err error) string {
	text := output.text()
	if ctx.Err() != nil {
		text = strings.TrimSpace(text + "\ngit did not finish within " + writeTimeout.String())
	} else if text == "" {
		text = "git failed: " + err.Error()
	}
	return tail(text).text()
}

func commitFailed(output string) protocol.ChangesCommittedPayload {
	output = tail(output).text()
	return protocol.ChangesCommittedPayload{State: protocol.CommitStateFailed, Output: &output}
}

func pushFailed(output string) protocol.ChangesPushedPayload {
	output = tail(output).text()
	return protocol.ChangesPushedPayload{State: protocol.PushStateFailed, Output: &output}
}

package protocol

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Worktree bounds (branches.listed, worktree.removed, and the worktree fields
// of session create and session records).
const (
	MaxBranches          = 200
	MaxWorktreeNameRunes = 40
	maxBranchNameRunes   = 256
	maxWorktreeRepoRunes = 128
	maxWorktreeRepoBytes = 384
	maxWorktreeBaseRunes = 256
)

// ValidWorktreeName reports whether name matches ^[a-z0-9]+(-[a-z0-9]+)*$
// and has 1..40 characters. It is a folder name and part of a branch name,
// so nothing else passes.
func ValidWorktreeName(name string) bool {
	if name == "" || len(name) > MaxWorktreeNameRunes || name[0] == '-' ||
		name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// ValidBranchName reports whether a branch name fits the protocol: 1..256
// characters of UTF-8 without control characters.
func ValidBranchName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= maxBranchNameRunes &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

// ValidateWorktreeRepo checks a repository name: safe display text of 1..128
// characters and at most 384 bytes.
func ValidateWorktreeRepo(repo string) error {
	if err := validateBrokerRPCText(repo, maxWorktreeRepoBytes, "repo"); err != nil {
		return err
	}
	if utf8.RuneCountInString(repo) > maxWorktreeRepoRunes {
		return errors.New("repo is outside bounds")
	}
	return nil
}

// ValidateWorktreeCreate checks the optional worktree of a session create
// request.
func ValidateWorktreeCreate(worktree *WorktreeCreate) error {
	if worktree == nil {
		return nil
	}
	if !validUUID(worktree.SourceSessionID) {
		return errors.New("worktree sourceSessionId must be a canonical UUID")
	}
	if !ValidWorktreeName(worktree.Name) {
		return errors.New("worktree name must be 1..40 of a-z, 0-9 and single inner hyphens")
	}
	base := worktree.Base
	if base == "" || !utf8.ValidString(base) || utf8.RuneCountInString(base) > maxWorktreeBaseRunes || strings.IndexByte(base, 0) >= 0 {
		return errors.New("worktree base must be 1..256 characters without NUL")
	}
	return nil
}

func validateSessionWorktree(worktree *SessionWorktree) error {
	if worktree == nil {
		return nil
	}
	if !ValidWorktreeName(worktree.Name) {
		return errors.New("session worktree name is invalid")
	}
	return ValidateWorktreeRepo(worktree.Repo)
}

func validateBranchesListed(payload BranchesListedPayload) error {
	if payload.State != ChangesStateReady && payload.State != NotGit && payload.State != ChangesStateUnavailable {
		return errors.New("invalid branches state")
	}
	ready := payload.State == ChangesStateReady
	if payload.Branches == nil || len(payload.Branches) > MaxBranches || (!ready && len(payload.Branches) > 0) {
		return errors.New("branches are outside bounds")
	}
	if !ready && (payload.Repo != nil || payload.DefaultBranch != nil || payload.Current != nil || payload.Truncated) {
		return errors.New("only a ready listing carries repo, branches and names")
	}
	if payload.Repo != nil {
		if err := ValidateWorktreeRepo(*payload.Repo); err != nil {
			return err
		}
	}
	for _, name := range []*string{payload.DefaultBranch, payload.Current} {
		if name != nil && !ValidBranchName(*name) {
			return errors.New("branch name is outside bounds")
		}
	}
	for _, branch := range payload.Branches {
		if !ValidBranchName(branch.Name) {
			return errors.New("branch name is outside bounds")
		}
	}
	return nil
}

func validateWorktreeRemoved(payload WorktreeRemovedPayload) error {
	if payload.State != Removed && payload.State != Dirty && payload.State != WorktreeRemoveStateFailed {
		return errors.New("invalid worktree remove state")
	}
	if !validGitOutput(payload.Output) {
		return errors.New("worktree remove output is outside bounds")
	}
	return nil
}

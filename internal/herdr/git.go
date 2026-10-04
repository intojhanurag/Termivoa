package herdr

import (
	"os"
	"path/filepath"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
)

const (
	gitCheckLimit = 200 * time.Millisecond // one workspace's file checks in a poll
	gitCheckRetry = 60 * time.Second       // after a check that did not answer in time
	gitMaxLevels  = 32                     // parents looked at above a pane folder
)

// gitCheck is one workspace's repository check that did not answer in time.
type gitCheck struct {
	result  chan bool
	retryAt time.Time
}

// gitWorkspaces is the file-system part of design A1 (docs/herdr-manage.md):
// the workspaces that herdr gave no worktree record and whose pane folders
// are in a git repository. herdr can make a worktree there, so their group
// gets "+". Each workspace is checked in its own goroutine, all with one
// 200 ms limit; one that does not answer (a hung network mount) counts as
// not git and is not tried again for 60 s, nor while its check still hangs.
// Poll loop only.
func (provider *Provider) gitWorkspaces(snapshot wireSnapshot, now time.Time) map[string]bool {
	folders := map[string][]string{}
	for _, pane := range records(snapshot.Panes, func(record wirePane) string { return record.ID }) {
		if pane.Cwd != "" && len(folders[pane.WorkspaceID]) < maxTargets {
			folders[pane.WorkspaceID] = append(folders[pane.WorkspaceID], pane.Cwd)
		}
	}
	checks := map[string]*gitCheck{}
	var started []string
	for _, workspace := range records(snapshot.Workspaces, func(record wireWorkspace) string { return record.ID }) {
		if tree := workspace.Worktree; tree != nil && tree.RepoKey != "" || len(folders[workspace.ID]) == 0 || len(checks) == maxGroups {
			continue
		}
		if late := provider.gitChecks[workspace.ID]; late != nil {
			if now.Before(late.retryAt) {
				checks[workspace.ID] = late
				continue
			}
			select {
			case <-late.result: // it ended at last; its answer is old: check again
			default:
				late.retryAt = now.Add(gitCheckRetry)
				checks[workspace.ID] = late
				continue
			}
		}
		check := &gitCheck{result: make(chan bool, 1)}
		checks[workspace.ID] = check
		started = append(started, workspace.ID)
		go func(find func([]string) bool, folders []string) { check.result <- find(folders) }(findRepository, folders[workspace.ID])
	}
	git := map[string]bool{}
	limit := time.NewTimer(gitCheckLimit)
	defer limit.Stop()
	expired := false
	for _, id := range started {
		if !expired {
			select {
			case found := <-checks[id].result:
				git[id] = found
				delete(checks, id)
				continue
			case <-limit.C:
				expired = true
			}
		}
		select {
		case found := <-checks[id].result:
			git[id] = found
			delete(checks, id)
		default:
			checks[id].retryAt = now.Add(gitCheckRetry)
		}
	}
	// Only the checks that did not answer stay, and only for workspaces herdr still has.
	provider.gitChecks = checks
	return git
}

// Test seams: the repository check, and its owner check.
var (
	findRepository = inRepository
	ownFolder      = func(info os.FileInfo) bool { return ipc.RequireCurrentOwner(info) == nil }
)

// inRepository reports whether one of folders, first first, is in a git
// repository: the folder, resolved, is a directory of this user, and it or a
// parent (at most 32 levels, stopping at a folder that is not this user's)
// has a .git entry. Only lstat and stat; no git process.
func inRepository(folders []string) bool {
	for _, folder := range folders {
		if !filepath.IsAbs(folder) {
			continue
		}
		folder, err := filepath.EvalSymlinks(folder)
		if err != nil {
			continue
		}
		for range gitMaxLevels {
			info, err := os.Stat(folder)
			if err != nil || !info.IsDir() || !ownFolder(info) {
				break
			}
			if _, err := os.Lstat(filepath.Join(folder, ".git")); err == nil {
				return true
			}
			parent := filepath.Dir(folder)
			if parent == folder {
				break
			}
			folder = parent
		}
	}
	return false
}

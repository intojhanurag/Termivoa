# Changes view

The phone can show the uncommitted `git` changes of a session's project: a
file list with +/− counts, and the diff of one file. The wire format is in
`protocol/v1`: `changes.list` → `changes.summary` and `changes.file` →
`changes.patch` on the live stream. The controller can also commit selected
files (`changes.commit` → `changes.committed`) and push (`changes.push` →
`changes.pushed`). The code is `internal/gitchanges` (git) and
`internal/broker/changes.go` (wiring).

## Which folder

The broker uses the session's live working folder ([live-folder.md](live-folder.md))
when it knows it, else the project folder the session started in. git then
finds the repository that holds that folder.

## What runs

For `changes.list`:

1. `git rev-parse --show-toplevel`: the repository root. "not a git
   repository" → `not_git`; git missing or any other failure → `unavailable`.
2. `git rev-parse -q --verify --short HEAD` and `git symbolic-ref -q --short HEAD`:
   the branch, or the short commit when HEAD is detached.
3. `git diff --raw --numstat -z -M <HEAD> --`: staged and unstaged changes
   against HEAD with renames, deletes, type changes and +/− counts (binary
   files have no counts). Before the first commit the base is the empty tree
   (`git hash-object -t tree --stdin`).
4. `git ls-files --others --exclude-standard -z`: untracked files that git does
   not ignore. The broker counts their lines itself (first 1 MiB of a regular
   file; a NUL in the first 8000 bytes means binary).

For `changes.file`, the broker lists again and accepts only a path (or the old
path of a rename) from that fresh listing; any other path is `missing`. It then
runs `git diff -M -U3 HEAD -- <old> <path>`, or for an untracked file
`git diff --no-index -- /dev/null <path>` (exit code 1 is normal).

## Safety

- `exec.CommandContext`, never a shell. Paths are literal pathspecs
  (`--literal-pathspecs`) after `--`.
- `-c core.fsmonitor=false -c core.untrackedCache=false -c color.ui=false`,
  and `--no-ext-diff --no-textconv --no-color` on diffs, so no configured diff
  program, text converter or fsmonitor hook runs.
- Environment: inherited, plus `GIT_OPTIONAL_LOCKS=0` (git never takes the
  index lock or writes the index), `GIT_TERMINAL_PROMPT=0`, `LC_ALL=C`.
- git runs in its own goroutine, never on the session actor. One changes
  request per viewer at a time (another gets `limit_exceeded`). The viewer
  detaching or the session closing cancels it.

Clean filters (for example Git LFS) configured by the repository still run
when git compares working files, as they do for `git diff` in a terminal.

## Bounds

| Bound | Value |
| --- | --- |
| Time per request | 5 s, then git is killed and the reply is `unavailable` |
| git output per command | 4 MiB; beyond it git is killed and the result is `truncated` |
| Files per summary | 300, sorted by path (`truncated` when more), and 128 KiB of path text |
| Lines per patch | 2000, and 96 KiB of text (`truncated`) |
| Text per line | 1000 characters; bad UTF-8 becomes U+FFFD |

A patch has no file headers: `@@` lines are `hunk`, the rest are `context`,
`add` or `remove` with their line numbers. "\ No newline at end of file" is
skipped. A binary file is `binary` with no lines.

## Who can see what

Any paired device that can view a session can read the diffs of that
session's repository: every uncommitted change, and the full text of untracked
files that git does not ignore (for example a new `.env` that is not in
`.gitignore`). It cannot read unchanged or ignored files. No controller lease
is needed, as for terminal history. Revoking a device stops it at once.

## Commit and push

Only the controller can commit or push: both messages carry the lease, and the
session actor checks it right before git starts (`not_controller` or
`stale_lease` otherwise). One write runs per session at a time, whoever asks;
another gets `limit_exceeded`. A write that started finishes even if the
viewer detaches, because a commit cut short is worse than a lost reply; only
the reply is dropped. Reads keep their own per-viewer limit.

For `changes.commit` (message: 1..2000 characters without NUL; 1..300 paths):

1. Refuse (`failed`) while a merge, rebase, cherry-pick or revert is in
   progress (`MERGE_HEAD`, `rebase-merge`, `rebase-apply`, `CHERRY_PICK_HEAD`,
   `REVERT_HEAD` in the git folder).
2. List again. Every path must be in that fresh listing (a new path or the old
   path of a rename); otherwise `failed` and git changes nothing. A rename
   commits both of its paths.
3. `git add -- :(literal)<path>…` for the untracked paths only.
   `git commit --only` takes tracked paths (also deleted ones) from the
   working tree itself, but refuses paths git does not know.
4. `git diff --quiet --no-ext-diff --no-textconv <HEAD> -- <paths>`: no
   difference → `nothing`, and no commit runs.
5. `git commit --only -m <message> -- :(literal)<path>…`. `--only` with paths
   commits exactly those paths as they are in the working tree; other staged
   files stay staged and out of the commit.
6. `git rev-parse --short HEAD` → `committed` with that hash.

For `changes.push`:

1. `git symbolic-ref -q --short HEAD`: a detached HEAD is `failed`.
2. With an upstream (`git rev-parse --abbrev-ref --symbolic-full-name
   @{upstream}`): `git push`.
3. Without one: `git remote`. No remote → `no_remote`. A remote named
   `origin` → `git push -u origin HEAD`. Other remotes only → `failed` with a
   hint.
4. `pushed` with the branch, and `url`: the first `https://` link in the
   output that opens a new pull or merge request (`/pull/new/`,
   `/merge_requests/new`, `/pull-requests/new`), when it is at most 2048
   characters without whitespace or control characters.

Safety:

- Never a shell, and never `--force` or any force variant. A rejected push
  is `failed` with git's words.
- The repository's hooks run as for any commit or push, and git takes its
  locks as usual (no `GIT_OPTIONAL_LOCKS=0`). Paths use `:(literal)` magic,
  not `--literal-pathspecs`, which would reach the hooks' own git commands.
  No `-c` overrides; the user's git and ssh settings apply.
- Environment: inherited plus `GIT_TERMINAL_PROMPT=0`; stdin is the null
  device, so git never waits for input.
- 120 s per request (hooks and remotes can be slow), then the owned process
  boundary is killed and git is reaped before the reply is `failed`.
  The one-second pipe wait remains; Windows Job shutdown also has a
  one-second active-process wait. See the cancellation boundary below.
- `failed` carries `output`: the last 2000 characters of git's stdout and
  stderr together (a hook's message, a missing `user.email`, a rejected
  push), valid UTF-8 and trimmed.
- If `git add` ran but the commit then failed (for example a hook said no),
  the untracked files stay added, as after the same commands in a terminal.


## How do write cancellation and worktree rollback own hooks? (#222)

Every `runWrite` command uses the same small process runner for commit,
push, worktree add/remove/prune and branch cleanup. It sets ownership before
user code can execute. The caller's environment, identity, signing, hooks,
git/ssh settings and null stdin are preserved; no new protocol or retry.

| Platform | Ownership and completion |
| --- | --- |
| Unix | `Setsid` creates an isolated session/group. Context cancellation sends SIGKILL to its whole group while git still runs (before `Wait` reaps it). If git fails by itself, one group kill after `Wait` ends helpers left behind (a group ID is not reused while a member lives; ESRCH means none is left). A successful git sends no signal. Orphan reaping belongs to the OS. |
| Windows | Create a KILL_ON_JOB_CLOSE Job, start suspended, assign before resuming the primary thread using the existing PTY startup sequence. Cancellation terminates the Job and also kills a still-unassigned suspended child. After a failed or canceled git, terminate leftovers, poll the Job's active count for at most one second and close its handle. After a successful git, clear KILL_ON_JOB_CLOSE and close the handle; hook work keeps running. |
| Failed start/assignment | Close all allocated handles; a started suspended child is killed and waited before returning an error. No unowned fallback. |

A failed worktree add reaches rollback only after the write runner's cleanup.
A failure after an already-completed git change does not undo that change:
inspect the branch/files before manually retrying, as with a lost reply.
Normal synchronous hooks keep running on successful writes and their output
is still reported. Background work a hook starts (for example `npm install &` in
`post-checkout`) keeps running after a successful write; it is cleaned up
only when the write is canceled, times out or fails. Unix hooks that deliberately
start another session (for example `setsid`) escape that group; hooks are
trusted user code, not a sandbox. No unrelated process group is signaled.

Tests run real `pre-commit` and `post-checkout` hooks and assert hook/child/sleep
PIDs are gone after cancellation, and that a successful `post-commit` hook's
background sleep survives; a closed-output case cannot pass merely
because WaitDelay closed pipes. A separate controlled parent/child/grandchild
fixture covers cancellation, deadline, failed/canceled start and preservation
of an unrelated process on each native supported OS. Native Windows/Linux
execution remains a release gate when only cross-compilation was performed.

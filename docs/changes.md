# Changes view

The phone can show the uncommitted `git` changes of a session's project: a
file list with +/− counts, and the diff of one file. The wire format is in
`protocol/v1`: `changes.list` → `changes.summary` and `changes.file` →
`changes.patch` on the live stream. The code is `internal/gitchanges` (git)
and `internal/broker/changes.go` (wiring).

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

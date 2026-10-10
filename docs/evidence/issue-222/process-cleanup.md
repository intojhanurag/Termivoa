# Issue #222: git write process ownership

Date: 2026-10-08. Baseline: `08f0332`. Go: `go1.27.1`.
All processes and repositories belong to disposable test fixtures. No owner
process, repository data, startup configuration, agent or credentials used.

## Does the reported failure reproduce?

```sh
go test -run '^TestWriteCancellationEndsHookDescendants$' ./internal/gitchanges
```

Before the runtime change: exit 1, 6.694 s. Both `inherited_pipes` and
`closed_pipes` failed: `hook descendant PID <fixture> survived canceled git`.
The real pre-commit hook publishes its PID, starts a shell child that also
publishes its PID and starts a recorded sleep grandchild. The test waits for
all markers, cancels the actual `runWrite` call and checks the processes after
it returns. Fixture cleanup kills only recorded fixture descendants. Closing
hook output proves this is about child lifetime, not merely inherited pipes.

After the fix, the same command passed in 0.946 s.

## What boundary is implemented?

`runWrite` owns each write before execution. Unix starts its own session/group;
context cancellation sends SIGKILL to that group, and final cleanup also ends
helpers left after git exits. Git is waited/reaped. Orphan reaping is the OS's
job. Windows starts suspended in a pre-created KILL_ON_JOB_CLOSE Job, assigns
it before resume, terminates the Job on cancellation, and additionally kills
an unassigned suspended git if cancellation races startup. After git's Wait,
remaining Job members are terminated, its active count is polled for at most
one second and the handle is closed. Unsupported platforms fail before start,
without an unowned fallback. Environment, git/ssh config, identity, signing,
hooks, output bound and no-input behavior remain.

## Which checks actually ran?

| Command | Actual result |
| --- | --- |
| Focused race regression, five runs (below) | PASS, 25.296 s |
| `go test -race ./internal/gitchanges` | PASS, 11.441 s |
| Broker Changes/controller/worktree race regressions (below) | PASS, 4.826 s |
| `go vet ./internal/gitchanges` | PASS |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/gitchanges` | PASS, compile only |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/gitchanges` | PASS, compile only |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | PASS, 29 pages, all internal links valid |
| `git diff --check` | PASS |

```sh
go test -race -count=5 -run '^Test(Write(Process|Cancellation|Successful)|WorktreeCancellation)' ./internal/gitchanges
go test -race -run '^Test(SessionStream.*(Changes|Commits)|Worktree)' ./internal/broker
```

The complete package race run includes existing commit, push, successful-hook
and worktree behavior. Final repeated focused runs also prove hook environment
and commit completion; real post-checkout cancellation ends all recorded PIDs
and removes its fixture worktree and branch. A controlled real process tree
covers cancellation and deadline, failed/canceled start, and preserves a
separate control process. The same fixture is compiled for Windows; native
Windows execution has not been claimed. Linux zombie processes in PID checks
are dead and cannot write; their final reaping belongs to init.

Initial cross-compilation with the inherited CGO setting could not use the
Mac compiler for foreign C/assembly. Repeating with CGO_ENABLED=0 succeeded;
no compiler or tool was installed. Native Linux/Windows suites, hosted CI,
whole `scripts/check` and owner phone tests remain pending for the coordinator.

## What is deliberately outside this correction?

Unix hooks may deliberately leave the session using `setsid`. They are trusted
user code, not a sandbox; such detached programs are outside group cleanup.
No unrelated process group is killed. Cleanup cannot undo a commit or remote
push that already completed, and no write is automatically retried. Git read
process ownership, GitHub clones and PTY runtime remain unchanged.

# Issue #236: bounded broker worktree admission

Date: 2026-10-08. Branch: `fix/issue-236`, base `08f0332`.
All fixtures use temporary directories, synthetic paired devices and fake
session processes. The accepted-job test runs real local Git and an isolated
post-checkout hook; it does not contact GitHub or use the owner's sessions.

## How was the defect reproduced?

The independently verified audit probe on the original gateway client
reported ten canceled calls, ten broker handlers blocked in `createSession`,
and ten registered connections. The issue contains the complete probe.
The durable session limit and the Git timeout ran only after the mutex;
`context.WithoutCancel` had already discarded cancellation before waiting.

The new permanent tests were first run against unmodified broker code:

```sh
go test ./internal/broker -run '^TestWorktreeAdmission' -count=1 -v
```

```text
FAIL TestWorktreeAdmissionDoesNotWaitBehindAcceptedCreate
  busy create queued behind accepted work
FAIL TestWorktreeAdmissionRejectsCanceledRequestBeforeAcceptance
  canceled create = broker: the worktree source session is not running
FAIL TestWorktreeAdmissionClosedCallersAndShutdownDoNotQueue
  closed caller 0 remained queued during shutdown
FAIL (2.036s)
```

## What does the fix prove?

| Regression | Evidence |
| --- | --- |
| Busy admission has no queue | Held active slot, request returns `limit_exceeded` before worktree preparation |
| Cancellation before admission | Canceled context returns `context.Canceled`; no session starts and admission is free |
| Closed callers and shutdown | Twenty fully written CLI creates close their IPC peers while the slot stays held; all handlers leave and registered connections become zero |
| Accepted durable mutation | A real Git hook marks acceptance; canceling its gateway caller returns the existing delivery uncertainty but does not abandon the job |
| Concurrent duplicate | Same operation ID gets a correlated `limit_exceeded` with no delivery uncertainty and no receipt before the accepted job completes |
| Authorization | An invalid credential is still `unauthorized` while the create slot is busy |
| Shutdown and replay | Serve waits for the accepted hook to release, finishes, and replay returns the original session; exactly two sessions and Git worktrees exist, including the source |
| Phone text | All limit messages use fixed text covering busy creation and capacity; arbitrary host/peer details are not exposed |

The fixed focused run passed all four tests. Closed-caller output:

```text
closed callers=20; remaining registered connections=0; started sessions=0
PASS
```

## Which checks passed?

| Command | Actual result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Local workspace install passed, lockfile unchanged |
| `go test ./internal/broker ./internal/gatewayclient ./internal/gitchanges` | All three packages passed (broker 79.999 s, client 1.638 s, Git 5.630 s) |
| `go test -race` (worktree and management shutdown regressions) | Passed, 3.725 s |
| `go test -race ./internal/broker -run '^TestWorktreeAdmission' -count=1 -v` | Passed all four tests after adding admission reuse after failure, 2.170 s |
| `go vet ./internal/broker ./internal/gatewayclient ./internal/gitchanges` | Passed |
| `pnpm --filter @phone-terminal/web test -- src/screens/Worktree.test.tsx` | This invocation ran the full suite: 57 files, 1,048 tests passed |
| `pnpm typecheck` | Passed |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Passed, 29 pages; all internal links valid; `.md` pages and `llms.txt` generated |
| `git diff --check` | Passed |

Node/pnpm commands use repository-required Node 24.11.0 on PATH. No protocol
schema or generated type changed. Public docs reuse the existing Worktrees
sidebar entry; no new command, stored data, access, or trust behavior needs
a CLI/security guide change. Architecture, the internal worktree design,
roadmap, README, changelog, feature guide, Next beta notes and troubleshooting
are updated together.

Exact race command:

```sh
go test -race ./internal/broker -run '^(TestWorktree|TestManagementServeCancellation)' -count=1
```

## What remains pending or deliberately unchanged?

- One simultaneous request can be admitted and others are refused rather than
  queued. Users must wait and explicitly try again; nothing retries offline.
- An accepted operation can still finish after the phone leaves. Shutdown
  waits for that one operation, retaining the existing 120-second Git create
  and separate 120-second rollback contexts and filesystem behavior; this
  is not a new total shutdown deadline or a cap on all IPC connections.
- The hook test is POSIX-only. Native Windows and real-phone checks are
  pending; the admission and closed-peer tests use portable Go transports.
- Full repository `scripts/check`, native Linux/Windows runs, the 24-hour
  soak, PR/adversarial review, push, merge and site deployment are not run
  by this helper. The coordinator owns those gates.
- No fresh database or new pairing is needed. No unrelated defect was fixed.

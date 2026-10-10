# Worktree sessions

Termivoa's broker creates a linked Git worktree, starts its session, and
records a durable create receipt. This internal design covers the existing
feature and the admission correction in #236; user instructions live in
the public Worktrees guide.

## How does a create become accepted?

CLI management RPC and authenticated gateway session-create RPC both reach
`ManagementServer.createSession`. The gateway handler validates its message
and checks credential and Origin before admission. The broker checks the
request context, attempts `worktreeCreates.TryLock`, and checks the context
again after acquiring it. A canceled context is refused before any Git or
durable mutation; a busy slot returns the existing `limit_exceeded` code.

The slot admits one operation and has **no waiting queue**. This deliberately
changes simultaneous creates from eventual execution to immediate refusal.
It does not add a reader goroutine, RPC frame buffering, a runtime process,
or any new network or authentication surface. A socket disconnect can race
admission; a caller whose mutation was fully written but whose result was
lost still receives the gateway client's existing delivery uncertainty.

Only after admission is cancellation detached with `context.WithoutCancel`.
The broker checks `Registry.ReplayCreate` before taking the slot (so a
retry of a finished create replays even while another create runs) and
again under the slot, resolves the saved
project and profile, prepares the worktree, starts the session through the
registry, and returns its receipt snapshot. Start failure rolls back the new
worktree when no session was created. `defer Unlock` releases the slot on
every result, including replay and error. Ordinary creates without a
worktree continue using their existing registry admission.

## What happens to concurrent duplicates and shutdown?

A duplicate while the first operation runs receives a correlated busy
refusal. It writes no receipt and cannot overwrite the accepted operation's
result. A replay after completion returns the original session, or an
operation conflict when its request identity differs. The original accepted
job continues after its caller closes; there is no automatic retry.

Management shutdown closes IPC connections and waits for the accepted job
to finish before returning to the broker owner. Refused/closed calls cannot
leave worktree mutex waiters. There is no new total shutdown deadline: the
existing accepted operation and filesystem cleanup semantics are retained.

## What bounds remain explicit?

| Resource | Bound |
| --- | --- |
| Active worktree creates | 1 per broker |
| Waiting worktree creates | 0; busy requests fail before worktree preparation |
| New queue allocations or goroutines | None |
| Live sessions | Existing registry limit, default 8 |
| Termivoa worktrees | Existing 50 per repository |
| Git create execution | Existing 120 s context deadline |
| Rollback | Existing separate 120 s context deadline; filesystem removal retains its existing behavior |
| Messages, Git output and receipt capacity | Existing IPC, Git and Store limits, unchanged |

The bound applies to worktree admission, not every broker connection or
operation. It does not claim that all OS filesystem calls can be canceled.

## What does the phone say?

The phone's `createProblem` maps `limit_exceeded` to fixed text that covers
busy creation and capacity: wait for another create, or remove a finished
worktree. It does not expose arbitrary peer/host error details. No control,
PTY input, offline queue, retry, pairing or database behavior changes.

Evidence and the failing baseline are in [evidence/issue-236](evidence/issue-236/verification.md).

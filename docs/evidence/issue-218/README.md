# Issue #218: keep reads available during slow operations

Date: 2026-10-08. Base: `08f03326ad22280a7076d40f77b7a9445866b6c2`.
All repositories, credentials, processes and hook control files belong to
isolated synthetic fixtures. Production runtime and configuration are untouched.

## Was the original report real?

Yes. The regression uses a real private broker IPC listener and the production
gateway client, a real temporary git repository and a post-checkout hook that
waits for an explicit fixture-owned release file. On the base, a session list
through the same client times out after 250 ms while creation holds the shared
RPC connection (`regression-before.txt`). No fixture delay is added to the list.

After isolation, list, authentication and a new bound Live-open all finish
while the same hook still waits. A bad credential is refused. The hook is then
released, the one creation completes and its session is recorded. The focused
run observed sub-millisecond exchanges; race-instrumented exchanges were a few
milliseconds. These are local fixture timings, not real-phone latency claims.

## What changes?

Known blocking operations use their own IPC connection, with at most four
active exchanges. Overload is refused before dialing and is not queued.
Session mutation acknowledgements still check operation identity. Credentials,
origin forwarding, IPC role, framing and broker authorization are preserved.
Cancellation/Close interrupts active calls and dials; no written mutation is
automatically retried. Existing herdr/GitHub dedicated exchanges share the
same admission bound. The worktree error mapper now gives correct limit advice
for transport saturation as well as session/worktree capacity.

See [internal design](../../gateway-rpc.md). This bounds gateway exchanges,
not the existing broker-owned worktree lifetime after the caller cancels.
That pre-existing queue behavior is reported separately to the coordinator.

## What passed?

| Command | Actual result |
| --- | --- |
| `go test ./internal/broker -run TestSlowWorktreeHookDoesNotBlockGatewayReadsOrLiveOpen -count=1 -v` | Passed after the fix; list/auth/Live-open available during the real blocked hook |
| `go test ./internal/gatewayclient ./internal/gateway -count=1` | Both full packages passed |
| Selected broker/client race command below | Passed; no races reported |
| `go test -race ./internal/gatewayclient -run '^TestSlowRPCDoesNotBlockAuthentication$' -count=1 -v` | All five newly isolated handlers passed with forwarded-origin assertions |
| `go vet ./internal/gatewayclient ./internal/broker` | Passed |
| `pnpm install --frozen-lockfile` (Node 24) | Passed; lockfile unchanged |
| `pnpm --dir web exec vitest run src/screens/Worktree.test.tsx` | 13 tests passed |
| `pnpm --dir web typecheck` | Passed |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && pnpm exec astro build` | Passed; 29 pages built, internal links valid |
| `git diff --check` | Passed |

The selected race command was:

```sh
go test -race ./internal/gatewayclient ./internal/broker -run 'TestSlow|TestCloseInterrupts|TestMutationOperationMismatch|TestDeadIdle|TestCancellation|TestMultiComputerMethodsForwardOrigin' -count=1 -v
```

Client tests cover saturation with authentication still available, no excess
dials or mutation retries, slot release after cancellation, shutdown of active
exchanges and pending dials, correlated refusals, operation mismatch, stale
shared-connection recovery and origin propagation.

Broader coordinated repository checks and independent adversarial review are
pending before merge. Native Windows/Linux and physical phone checks remain
owner gates; the POSIX git-hook fixture does not claim Windows execution.
No database reset or new pairing is required.

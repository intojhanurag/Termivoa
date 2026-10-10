# Issue #217: complete byte-limited History

Date: 2026-10-08. Base: `08f03326ad22280a7076d40f77b7a9445866b6c2`.
All terminal output is synthetic, generated inside fixtures. No production
runtime, terminal history, credentials, startup or Tailscale changes.

## Is the reported defect real?

Yes. The new phone regression fails on the base with `ready` instead of
`loading` after page 25 (`regression-before.txt`). The real Go terminal fixture
retains 1,632 styled rows within the normal 32 MiB history bound and requires
40 wire pages (`host-reproduction.txt`). Thus the report is reachable without
violating the host's retention limits.

## What fixes it?

The phone follows a validated contiguous cursor until `complete`, instead of
assuming 25 pages cover every retained row. It only appends the frozen live
screen after completion. Each attempt has explicit row, flattened-text,
received-data, request, restart and time budgets; failures discard partial
rows and expose History unavailable. See [design](../../terminal-history.md).
The host protocol and runtime are unchanged.

## What passed?

| Command | Actual result |
| --- | --- |
| `pnpm install --frozen-lockfile` (Node 24) | Passed; lockfile unchanged |
| `pnpm --dir web typecheck` | Passed |
| `pnpm --dir web exec vitest run src/terminal/Connection.test.ts` | 100 tests passed, including existing live/control/input/reconnect coverage |
| `go test ./internal/protocol -run TestStyledRetainedHistoryRequiresMoreThan25Pages -count=1 -v` | Passed; all 1,632 rows read in 40 pages |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && pnpm exec astro build` | Passed; 29 pages built, all internal links valid |
| `git diff --check` | Passed |

The phone tests include 40 trimmed pages, every one of 5,000 one-row pages,
request and received-byte budgets surviving restarts, invalid cursors,
flattened-text budget, one global timeout, late reply suppression and
completion/close/reopen timer cleanup.

## What remains pending?

Broader repository checks and independent adversarial review are coordinated
before merge. Real iPhone and Android selection/copy checks remain owner gates;
these fixtures do not substitute for those tests. No database reset or new
pairing is needed.

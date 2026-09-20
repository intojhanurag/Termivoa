# P3 immutable normal-history paging checkpoint

Date: 2026-09-21

Branch under review: `feat/p3-history-paging` (private local checkpoint)

## Implemented boundary

- The terminal actor owns normal-history epoch and revision counters. Every
  retained-row mutation advances the revision; an explicit normal-history
  clear advances the epoch and resets its revision.
- Screen frames expose only history token/count/byte metadata. Retained rows
  leave the actor only through `ReadHistory`, which copies an oldest-first page
  of at most 200 complete normalized rows.
- Each read presents the exact epoch/revision advertised by a screen frame. A
  mismatch returns the current token with `Changed=true` and no rows, which the
  canonical protocol converts to `history.changed` rather than combining
  positions from different snapshots.
- Protocol conversion reuses the existing v1 `history.read`, `history.page`,
  and `history.changed` schemas. It verifies exact next-offset progression and
  trims a page below the 1 MiB outbound-message budget when encoded rows make
  fewer than 200 rows necessary.
- The emulator's normal scrollback is capped at 5,000 complete rows and a
  conservative 32 MiB normalized-payload estimate. Its accounting includes
  blank cells added to the current terminal width and is recalculated on
  resize, so a wider grid cannot leave an oversized hidden buffer.
- Alternate-screen output remains separate from normal history. The alternate
  buffer retains only its bounded live grid and no hidden scrollback.
- Live `screen.patch` messages derive `historyReset` from the viewer's actual
  base epoch. A viewer that skips intermediate publications still receives the
  reset signal when its base predates the current epoch.
- Page copying occurs inside the terminal/session actor path. Protocol
  conversion and eventual transport occur after the copied result leaves the
  actor; no gateway, store, registry, or operation-receipt behavior changed in
  this checkpoint.

## Deterministic coverage

- Exact 200-row page boundary, oldest-first offsets, final short page, empty
  page, maximum offset, invalid offset/limit, and caller mutation isolation.
- Old-token read after concurrent output returns changed with no rows.
- Normal `ED 3` clear advances the epoch, clears retained rows, invalidates the
  old token, and emits a viewer-relative reset signal once.
- Alternate-screen scrolling and `ED 3` neither mutate nor reset normal
  history; the dependency test proves the alternate screen has no hidden
  scrollback allocation.
- Oldest-complete-row eviction at a small row limit and exact enforcement of
  the 5,000-row session limit.
- Small byte-budget retention followed by expansion from 20 to 400 columns;
  the emulator evicts rows whose normalized width no longer fits rather than
  hiding them from transport.
- Protocol rejection of null rows, non-advancing offsets, inconsistent
  completion markers, and changed results encoded as pages.
- Pathological maximum-width rows force page trimming while remaining within
  the outbound payload budget.
- Session generation is checked inside the broker actor before history access.

## Commands and actual results

```text
PATH=<pinned Node 24.11.0>/bin:$PATH pnpm install --frozen-lockfile
PASS: 160 packages reused from the pinned local store; lockfile unchanged

PATH=<pinned Node 24.11.0>/bin:$PATH ./scripts/check
PASS: protocol generation drift, plan synchronization, Go format/vet/tests,
patched dependency suites, TypeScript typecheck, 32 web tests, and production
Vite build

GOTOOLCHAIN=go1.27.1 go test -race ./internal/terminal ./internal/protocol ./internal/broker -count=1
PASS

GOTOOLCHAIN=go1.27.1 go test -race ./internal/terminal -run 'TestHistory|TestSnapshotsExposeHistoryMetadataWithoutRows' -count=50
GOTOOLCHAIN=go1.27.1 go test -race ./internal/protocol -run 'TestHistory' -count=50
GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -run 'TestHistoryResetSignalFollowsViewerBaseEpoch|TestSessionHistoryReadChecksGenerationInsideActor' -count=50
PASS

GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go test -c ./internal/broker
GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go test -c ./internal/terminal
GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go test -c ./internal/protocol
GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go build ./cmd/pterm
PASS
```

The first full-check attempt exposed a test-fixture mistake: the strict
`history.changed` converter correctly rejected a changed result that still
contained rows. The fixture was corrected to model the actor contract (changed
responses contain no rows), the focused protocol test passed, and the complete
unchanged check then passed. Native Windows 11 execution remains a separate
owner-dependent gate and is not claimed by the successful cross-compile.

A broad ten-repeat race sweep of all three packages hit the pre-existing
`TestViewerResyncInterruptsSnapshotAndWrongAckForcesFreshSnapshot` timeout once
while waiting for its third snapshot. The unchanged test then passed 20
isolated race repetitions, the complete affected-package race run passed, and
all history-specific race tests passed 50 repetitions. No race report was
emitted. The timing flake is recorded rather than counted as a history-paging
failure or concealed as a clean broad repetition.

## Remaining integration work

- The gateway/WebSocket adapter remains outside this checkpoint. It must map
  the actor result to the canonical page/changed message and transmit the
  already-copied immutable payload without retaining another unbounded cache.
- The P5 history/selection view must combine the frozen retained rows with the
  corresponding live screen and restart from the new token after
  `history.changed`.
- Native Windows 11 and physical-phone evidence remain pending as recorded in
  `docs/progress.md`.

# P3 bounded viewer delivery checkpoint

Update 2026-10-08 (#221): the historical coverage below predates demand-driven
publication. A stalled sink can now have zero pending screen notifications
while publication pauses. The one-slot bound, byte budget, canonical progress
and slow-viewer deadline remain. See [current evidence](../issue-221/README.md).

Date: 2026-09-20

Branch under review: `feat/p3-viewer-delivery` (private checkpoint)

## Implemented boundary

- The broker session actor atomically registers at most 16 viewers, captures
  immutable canonical screen frames, and owns each viewer's acknowledged base
  revision, in-flight revision, snapshot transaction, and resync epoch.
- PTY output schedules at most one screen capture per 60 Hz interval. Each
  viewer has a capacity-one notification, so repeated output coalesces to the
  newest immutable frame instead of building a patch backlog.
- A viewer worker converts frames to protocol v1 messages outside the session
  actor. It sends a full snapshot on attach, resize, resync, or oversized patch;
  live patches use the exact last delivered or acknowledged revision as their
  `baseRevision`.
- Snapshot patches remain locked until the client confirms the matching
  `snapshotId` and revision through `snapshot.applied`. A mismatched
  acknowledgement invalidates the base and schedules a new snapshot. An
  unacknowledged completed snapshot expires through an actor-owned deadline,
  detaches that viewer, and releases its connection slot. Expiry does not
  enqueue through the bounded public request channel.
- Snapshot row chunks and patches are measured after JSON encoding. Every
  outbound message is limited to 1 MiB. Each viewer send reserves from the
  advertised 2 MiB per-viewer budget until the downstream sink reports that
  its retained copy has drained, not merely until `Send` returns. The sink
  contract requires an error return to retain no copy. Missing drain callbacks
  hit the bounded send deadline and detach the slow viewer without blocking PTY
  parsing or other viewers.
- A partially transmitted snapshot is abandoned before `snapshot.end` when
  its single coalesced catch-up patch cannot fit in one legal message. A fresh
  snapshot supersedes it; repeated supersession disconnects the lagging viewer
  rather than creating an unbounded window.
- Live publication uses a screen-only terminal capture, avoiding a copy of up
  to 32 MiB of retained history on every render tick. The existing full
  snapshot still provides bounded history to the independent history path.
- Protocol conversion enforces a 512 KiB encoded-row ceiling. Pathological
  link/text cells are clipped deterministically while retaining the complete
  column grid and setting `contentClipped`, which guarantees a single row can
  fit in a legal snapshot message.
- Per-viewer row clipping state follows the acknowledged frame. A patch keeps
  screen-level `contentClipped` true when an unchanged row is still clipped,
  and replaces that row's clipping state only when the whole row is repainted.
- When the child exits, the actor captures one final immutable screen before
  publishing `exited`. The registry waits on a retirement barrier until every
  attached viewer has delivered/acknowledged that final revision or detached;
  only then may it close the PTY and terminal runtime.
- Worker teardown uses a dedicated internal channel bounded to the maximum 16
  viewers. Each subscription emits at most one detach event and waits for the
  actor's acknowledgement. A blocked actor and saturated public request queue
  therefore delay cleanup without dropping it or creating unbounded goroutines.
- Every viewer-specific actor request carries the exact subscription instance
  as its incarnation. Reusing a connection ID cannot let a detached handle or
  late worker completion acknowledge, resync, reserve, complete, restart, or
  query delivery state belonging to the replacement viewer.
- The broker exposes no ID-only session disconnect capability. Gateway teardown
  closes the exact `ViewerSubscription`; its actor-acknowledged detach releases
  the matching controller lease without ending the managed process.
- Control acquisition also requires the exact currently attached
  `ViewerSubscription`. The actor derives connection/device identity from that
  capability after incarnation validation; callers cannot present reusable
  string IDs to acquire control. A current viewer may still explicitly take
  control from another current viewer, preserving the single-controller rule.

The gateway remains outside this ownership boundary. Its eventual connection
adapter must implement the cancellation-aware `ViewerSink` drain callback over
one dedicated authenticated IPC connection per viewer. It must use a bounded
writer queue, invoke the callback only after the queued copy is written or
dropped, and must not retain terminal state, queue raw ANSI, or own the process
lifetime.

## Deterministic coverage

- Initial snapshot delivery blocks patches until matching `snapshot.applied`;
  the first patch advances the exact base revision, and a subsequent resize
  produces a full snapshot at the new dimensions.
- Resync cancels a partial snapshot, uses a fresh snapshot identity, and a
  wrong acknowledgement forces another full snapshot.
- Fifty PTY output chunks continue to mutate canonical state while the initial
  viewer write is stalled; the viewer retains exactly one pending screen
  notification and is disconnected at its deadline.
- A sink that accepts and retains a copied message without reporting drain is
  disconnected at the send deadline; its bytes remain charged until the test
  sink explicitly drops the copy, then return to zero.
- A completed snapshot with no `snapshot.applied` times out, reports the typed
  slow-viewer/acknowledgement error, and a new viewer can immediately reuse the
  same connection ID.
- Deterministic saturation fixtures block the actor in resize, fill all 128
  public request slots, and hold it beyond the former one-second enqueue
  window. Both acknowledgement expiry and failed-sink detach still run through
  the internal lifecycle path after the actor resumes; the former unblocks
  retirement and the latter releases its connection slot.
- A detached handle is followed by a replacement using the same connection ID.
  Both stale `snapshot.applied` and resync calls return `ErrViewerMissing`; the
  replacement remains on its acknowledged revision and receives the next
  update as an exact-base patch rather than a forced snapshot. A delayed old
  detach event and stale reserve/complete/restart/latest-frame actor requests
  are also rejected without detaching or mutating that replacement. Stale,
  unattached, and nil viewer capabilities cannot acquire control, while the
  replacement capability can acquire and retain its lease.
- Two concurrently attached current viewers explicitly take control in turn;
  the second grant invalidates the first lease and remains heartbeat-valid.
- A child that emits `FINAL` and exits inside the 60 Hz coalescing window
  delivers a patch containing `FINAL` before the session retirement barrier
  permits runtime closure.
- A snapshot whose second row is transport-clipped keeps `contentClipped=true`
  in a later patch that changes only the first row.
- Output with no viewers reaches the canonical emulator; a later attach starts
  from the current revision rather than replaying raw bytes.
- Viewer count is bounded and viewer close releases its matching controller
  lease.
- A maximum-width row containing maximally escaped cell text and links is
  clipped below the row ceiling; its complete `snapshot.rows` envelope remains
  below 1 MiB and passes host-message validation.
- Screen-only terminal captures retain current grid/revision while omitting
  retained history copies.

## Commands and actual results

```text
GOTOOLCHAIN=go1.27.1 go test ./internal/broker -count=20
PASS

GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -run 'Test(SnapshotTimeoutSurvives|FailedViewerDetachSurvives|SnapshotAcknowledgement|FinalPublishedFrame)' -count=20
PASS

GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -run 'TestDetachedViewerHandleCannotMutateReplacementWithReusedConnectionID|TestCurrentViewerCanExplicitlyTakeControl|TestManagedSessionRuntime/(structured_input|viewer_close|only_current)' -count=50
PASS

PATH=$HOME/.nvm/versions/node/v24.11.0/bin:$PATH ./scripts/check
PASS: protocol generation drift, Go format/vet/tests, patched dependency tests,
TypeScript typecheck, 32 web tests, and production Vite build

GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker ./internal/protocol ./internal/terminal -count=10
PASS

GOTOOLCHAIN=go1.27.1 go vet ./...
PASS

GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go test -c ./internal/broker -o <temporary>/broker.test.exe
PASS (compile only; not native Windows execution)

git diff --check
PASS
```

The first repository-check attempt stopped before tests because this isolated
worktree had no `node_modules` directory. `pnpm install --frozen-lockfile`
reused the pinned local store, after which the unchanged repository check
passed.

## Remaining integration work

- The local IPC listener and gateway WebSocket adapter are not part of this
  checkpoint. Gateway restart preservation still requires an end-to-end test
  after those components are joined.
- Broker-authenticated device principals and revocation socket closure arrive
  with P4 authentication. A gateway-provided display label must never become
  the authorization boundary.
- History paging/epoch ownership is separate P3 work. Live viewer publication
  deliberately sends the current zero-valued history counters until that actor
  state is integrated; this checkpoint does not claim history pagination.
- Native Windows 11 and physical-phone evidence remain owner-dependent gates
  already recorded in `docs/progress.md`.

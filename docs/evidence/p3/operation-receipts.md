# P3 durable operation-receipt checkpoint

Date: 2026-09-20

Scope: broker-owned deduplication for session create, rename, and terminate
mutations, integrated with the bounded viewer-retirement lifecycle. This is not
the complete P3 exit gate; gateway routes, executable process separation, and
gateway-restart evidence remain pending.

## Implemented behavior

- Operation IDs are canonical lowercase UUIDs with a valid version and RFC
  variant. The ID is globally unique across the three mutation kinds.
- The broker hashes a canonical JSON request shape with SHA-256. Reusing an
  operation ID for a different kind or request returns an operation conflict;
  the original session state is not changed.
- Migration `002_mutation_receipts.sql` stores the request fingerprint and a
  typed, non-secret result containing session identity, generation, accepted
  state, optional rename title, and commit time. It stores no terminal content,
  process arguments, environment, credentials, controller lease, or runtime
  handle.
- Session creation starts the broker-owned runtime, then atomically inserts its
  running metadata and successful receipt. A failed transaction closes that
  runtime. A durable replay returns the original identity without spawning a
  second process. A broker crash before commit ends the uncommitted child at the
  existing broker process boundary; a crash after commit is recovered as a
  lost session and never silently reruns the command.
- Rename updates the title and inserts its receipt in one SQLite transaction.
  A later retry returns the original committed title and time even if another
  operation renamed the session again.
- Termination moves metadata to `exiting` and inserts the acceptance receipt in
  one transaction. Only the first committed caller invokes the runtime's
  process-tree termination; concurrent and later retries return the stored
  acceptance. Runtime termination failure does not rewrite that accepted
  result, but is retained in broker lifecycle diagnostics. Final `exited` or
  `lost` state is persisted by registry cleanup.
- Terminal persistence keeps `updated_at` monotonic when a receipt timestamp is
  later than the observed process end while retaining the actual `ended_at`.
  This prevents a clock-ordering edge from leaving durable state at `exiting`.
- Receipt-backed rename and terminate acceptance also clamp a regressed wall
  clock to the stored `updated_at` instead of blocking the user action. A
  terminal transition whose clock falls before creation/start clamps the
  lifecycle event time to the established timeline while still completing the
  requested process shutdown.
- `Ended` immediately persists and publishes immutable final metadata for
  `List`, while the registry continues to count the runtime as live until every
  viewer has acknowledged the final revision or detached. Only then does
  `RetirementReady` permit runtime close and capacity release. Receipt-backed
  terminate retries remain deterministic across this interval and invoke the
  runtime termination path exactly once.
- Registry shutdown closes mutation admission before any receipt replay, waits
  for already-admitted rename and terminate operations, and then cancels
  session lifetime. Every accepted create owner and duplicate has separate
  close admission under the same mutex gate; shutdown cancellation releases
  owner lookup/start work before `Close` waits for all admitted create callers.
  Consequently `Close` cannot return while a create can still access SQLite or
  return a durable replay, while calls reaching the gate after closure are
  rejected. Closing a runtime detaches pending viewers and releases its
  retirement barrier, so shutdown cannot deadlock behind final-frame delivery.
  Process cleanup errors remain in lifecycle diagnostics after definitive
  resource completion instead of being discarded.
- The in-memory create-operation map remains a bounded same-process concurrency
  cache. SQLite is authoritative after cache eviction or broker/store reopen.

## Retention and failure boundary

The store retains at most 16,384 receipts and prefers at most 8,192 receipts
whose sessions are `exited` or `lost`. Foreign-key cascade also removes receipt
history when bounded terminal session metadata is deleted. Receipts associated
with `starting`, `running`, or `exiting` sessions are never pruned. If active
receipts alone reach the hard cap, a new unique operation returns a typed
capacity error before changing session metadata. Existing retries and conflict
checks continue to work, so the bound does not trade away active-session
correctness. Once a terminal receipt is pruned, its operation ID leaves the
documented bounded deduplication window.

## Verification

```text
GOTOOLCHAIN=go1.27.1 go test -race -timeout=180s \
  ./internal/store ./internal/broker -count=5
passed

GOTOOLCHAIN=go1.27.1 go test -race -timeout=180s ./internal/broker \
  -run 'TestRegistry(CloseWaitsFor(CreateAdmittedBeforeStart|AdmittedDurableCreateReplayAndDuplicate)|PersistsAndListsFinalStateBeforeViewerRetirement|ViewerBarrierCleanupErrorRemainsObservableOnClose|ReceiptTerminatePersistsFinalStateBeforeViewerRetirement|CloseReleasesPendingViewerRetirementBarrier|PersistsFinalStateWhenReceiptTimestampIsLaterThanActualEnd)' \
  -count=20
passed

GOTOOLCHAIN=go1.27.1 go test -race -timeout=180s ./internal/store \
  -run 'TestTerminalTransitionKeepsActualEndTime|TestReceiptBackedMutationsClampClockRollback' \
  -count=50
passed

GOTOOLCHAIN=go1.27.1 go test -race -timeout=180s ./internal/broker \
  -run 'TestRegistry(PersistsFinalStateWhenReceiptTimestampIsLaterThanActualEnd|TerminationSurvivesClockRollback)' \
  -count=50
passed

GOTOOLCHAIN=go1.27.1 go vet ./internal/store ./internal/broker
passed

GOTOOLCHAIN=go1.27.1 go test -timeout=180s ./...
passed

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
protocol generation drift, Go formatting/vet/tests, dependency patch tests,
TypeScript checking, 32 web tests, and production web build passed

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c -o /dev/null ./internal/store
passed

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c -o /dev/null ./internal/broker
passed

git diff --check
passed
```

Targeted tests cover reopen-persistent create replay, cross-kind and
same-kind fingerprint conflicts, atomic rename and termination results,
v1-to-v2 schema upgrade, receipt-capacity rejection without metadata mutation,
terminal-history pruning, two racing requests with different fingerprints, and
sixteen concurrent termination retries producing exactly one runtime
termination call. Deterministic audit regressions cover a future-dated receipt
followed by an earlier actual process end, accepted runtime termination failure,
retired-session cleanup failure, blocked rename/terminate operations racing
shutdown, rejection of new mutations after the close gate, and rejection of a
durable terminate replay after closure. Cross-feature regressions additionally
hold the final viewer barrier while asserting immediate durable final metadata,
final-state listing, retained capacity, exactly-once receipt-backed runtime
termination, delayed cleanup-error reporting, and deadlock-free registry
shutdown. Create-shutdown regressions block an owner before start admission and
block a successful durable replay with an already-admitted duplicate; both
prove that `Close` waits for the callers, rejects later new/replayed calls, and
establishes a safe boundary for closing the store. The repeated race run
exercises the SQLite and registry concurrency paths. Greptile's clock-rollback
finding is covered at both store and registry levels: rename remains accepted,
terminate still invokes the runtime once, timestamps remain monotonic, and
final cleanup persists instead of leaving the process or record stuck.

The Windows cross-compile verifies the pure-Go build boundary only. It is not
native Windows 11 execution evidence.

## Explicitly pending

- HTTP and IPC mutation routes do not exist yet; they must pass canonical
  operation IDs into these broker APIs instead of implementing gateway-local
  deduplication.
- The executable entry point does not yet construct the long-running broker,
  open SQLite, or begin a broker generation; process separation and platform
  listener integration into that executable are later P3 checkpoints.
- Native Windows 11 execution remains owner-blocked. No physical-phone,
  Tailscale, installer, signing, or release claim is made by this checkpoint.

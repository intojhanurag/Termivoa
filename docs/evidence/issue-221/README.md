# Issue #221: capture at the next usable viewer slot

Verified 2026-10-08 on macOS arm64, Apple M5, Go 1.27.1, Node 24.11.0.
Base: `08f0332`. Synthetic terminal data only; no production processes or data.

## Is the report real?

Yes. Before the runtime change, the new capture-rate regression failed: a
single 5 fps thumbnail caused 78 full copies over 1.61 s (48.4/s). An
unacknowledged snapshot caused 77 over 1.43 s. No-viewer and mixed-viewer
cases passed. See [before.txt](before.txt), transcribed from the red run.

The fixture counts **all** `ScreenSnapshot` calls, including independent
agent-state scans. It emits 300 synthetic chunks with a 4 ms sleep between
chunks, then waits for the canonical revision and final delivery. Actual
duration exceeds 1.2 s due to sleep resolution and development load. These
are capture/delivery checks, not an isolated CPU comparison or busy soak.

## What changed?

The actor retains the first dirty output's coalescing deadline. One reusable
timer selects the earliest usable viewer slot. A thumbnail does not request a
copy before its next delivery slot; in-flight sends, queued/in-flight notices
and unacknowledged snapshots pause its demand. Dirty output stays pending.
A ready Live viewer keeps its cadence. Actor requests recalculate demand after
attach/detach, delivery or acknowledgement changes.

Attach, resize and exit still capture directly. Valid snapshot catch-up checks
capture dirty output once on demand; stale requests cannot trigger a copy.
The delivery cap/trailing wake remain because other viewers and special
captures can publish earlier. Independent agent scans and presence previews
remain. No schema, input/controller, byte budget or history ownership change.

## What passed?

| Command | Result |
| --- | --- |
| `go test ./internal/broker -run 'TestViewerCapture\|TestViewerSnapshotCatchup\|TestThumbnail\|TestViewerSnapshotBoundary\|TestFinalPublished\|TestStalledViewer\|TestNoViewer\|TestViewerResync' -count=1 -v` | Passed; [after.txt](after.txt) contains the final run. |
| `go test ./internal/broker -count=1` | Passed, 92.530 s, including controller/input/lifecycle regressions. |
| `go test -race ./internal/broker -run 'TestViewer\|TestThumbnail\|TestFinalPublished\|TestNoViewer\|TestStalledViewer\|TestSnapshot\|TestAcceptedViewer' -count=1` | Passed, 17.370 s. |
| `go vet ./internal/broker ./internal/terminal` | Passed. |
| `pnpm install --frozen-lockfile`; `pnpm protocol:check` | Passed; generated Go/TypeScript unchanged. |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Passed: 29 pages, valid links, llms/Markdown emitted. |
| `go test ./internal/broker -run '^$' -bench '^BenchmarkViewerPublicationCapture$' -benchmem -benchtime=200ms -count=1` | Real terminal-core grid-copy costs in [capture-benchmark.txt](capture-benchmark.txt). |

New regressions cover no-viewer/thumbnail/mixed demand, queued and in-flight
notices, blocked patch sends, all viewers awaiting acknowledgement, release
after output stops, stale/current catch-up checks, detach/reattach and final
screen/revision chains. Existing trailing timer, slow-viewer deadline/budget,
resize snapshot, resync, final retirement, control and input checks pass.
The stalled sink's exact-one pending wake assertion now permits zero because
publication pauses; timeout, budget and canonical progress assertions remain.

The benchmark uses the real core and blank grids: 157,917 B/capture at 80×24,
985,181 B at 200×60, 6,559,095 B at 400×200. These are per-copy costs, not
allocations for a complete running broker. Nanosecond results were under
shared development load, including broker tests; no wall-clock speedup claim.

## Documentation and pending gates

Architecture and multi-computer design explain demand/special captures. README,
Unreleased changelog (host/phone), Wall guide and Next beta notes describe the
outcome; the existing sidebar entry remains correct. No access/data changes,
new command, new failure mode or major feature: security/CLI/troubleshooting/
landing pages need no change.

Pending: parent integration/adversarial PR review, integrated busy soak with
separate #220/#232/#224 work, iPhone/Android Wall + Live checks and Windows
evidence. No push, PR, merge or deployment by the implementing helper.

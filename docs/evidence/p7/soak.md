# P7 soak, busy sessions and stalled-viewer evidence

Date: 2026-09-27. Branch `test/v0-soak`, base `f139b49`, test commit
`362d172`. Product code was not changed.

## Environment

| Item | Value |
| --- | --- |
| Hardware | Apple M5, 10 cores, 24 GiB (`sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize`) |
| OS | macOS 26.5.2 (25F84) (`sw_vers`) |
| Go | go1.27.1 darwin/arm64 (`go version`) |
| Profile | Development profile, temp data and runtime directories under `/tmp/pterm-soak-*` |
| Shell | None. The child is a synthetic test-binary fixture, not zsh. |
| Dimensions | 80 x 24 (broker default). No resize during the runs. |
| Network | None. Viewers use the real gateway client over local IPC. No WebSocket, no phone, no Tailscale. |

## What runs

Package `tests/soak` (build tags `soak && darwin`). A normal `go test ./...`
skips the package, so CI time does not change. Runner: `scripts/soak.sh`.

- **Broker.** The test binary runs `host.RunBroker` in a **separate process**
  (the same entry point that `pterm __broker --dev` uses). It also writes
  `runtime/metrics` samples (heap object bytes, total Go-mapped bytes,
  goroutines) one time each second. The test reads RSS and CPU time with
  `ps -o rss=,time= -p <broker pid>` and counts the children with `pgrep -P`.
  The RSS includes the Go testing framework (the idle broker is 16 MiB).
- **Child.** The test binary in raw mode on the broker's PTY. Row 1 is a status
  line outside the scroll region: `S pid=<pid> seq=<last line> echo=<last input
  token> t=<wall-clock ns>`. The status line changes every 10 ms. Rows 2–24
  scroll 72-byte lines `L<seq> ...` at `PTERM_SOAK_RATE` bytes/s.
- **Viewer.** It uses the same path as a phone behind the gateway: pairing
  (local grant, redeem, local approval, poll), then `gatewayclient.OpenLive`,
  `hello`, `session.attach`, a strict decode of every host message, a
  snapshot/patch screen model, `snapshot.applied`, `control.acquire`, a 5 s
  heartbeat, and `input.text` plus `input.key Enter`.
- **Output-to-frame latency.** The time between the child writing its status
  line (`t=`) and the viewer receiving the patch that contains it. Both
  timestamps come from the same host clock.
- **Input round trip.** The time from sending `input.text` to receiving the
  frame that shows the child's echo of that token. Four probes each second on
  each session. This value includes the broker input path, the child, the
  parser, the 60 Hz publish, IPC and the client decode.

### Documented bounds (constants in `tests/soak/soak_test.go`)

| Bound | Value | Reason |
| --- | --- | --- |
| Broker RSS, three busy sessions | ≤ 300 MiB | Plan §13 combined budget. Here it applies to the broker alone. |
| RSS growth after 20 s warm-up | ≤ 32 MiB | Growth must stop after the history fills. |
| RSS growth while one viewer is stalled | ≤ 16 MiB | 2 MiB per-client queue budget plus slack for the allocator. |
| Goroutine growth after warm-up | ≤ 16 | No leak from reconnects. |
| Input round trip p95, busy | < 250 ms | This bound is for a busy host. It is **not** the plan's 30 ms key-to-frame target, which needs a controlled benchmark with ≥ 1,000 events. |
| Output-to-frame p95, busy | < 250 ms | Same as above. |
| Patches per viewer | ≥ 20/s | The publish rate is at most 60 Hz. |

## Commands

```sh
./scripts/soak.sh                                        # default: 2 min, 1 MiB/s/session, 20 s stall
PTERM_SOAK_RATE=0     PTERM_SOAK_DURATION=40s ./scripts/soak.sh -run ThreeBusy
PTERM_SOAK_RATE=65536 PTERM_SOAK_DURATION=40s ./scripts/soak.sh -run ThreeBusy
PTERM_SOAK_CPUPROFILE=/tmp/pterm-broker.cpu PTERM_SOAK_RATE=65536 PTERM_SOAK_DURATION=30s ./scripts/soak.sh -run ThreeBusy
GOTOOLCHAIN=go1.27.1 go test -count=1 -v -run 'TestGatewayRestartPreservesBrokerSessionAndCanonicalOutput|TestPtermBrokerAndGatewayExecutableModesRestartIndependently|TestBrokerStartupMarksPriorLiveMetadataLostWithoutRerun' ./tests/integration/
```

The default run went from 2026-09-26T20:38:04Z to 20:40:41Z UTC
(2026-09-27 local). Result: `ok github.com/intojhanurag/phone-terminal/tests/soak 156.606s`.

## Results

### 1. Three busy sessions: PASS

Configuration: three sessions, each at 1 MiB/s (measured child output: 0.99
MiB/s each), one controlling viewer for each session, and a run of 120 s. Every
20 s, one viewer is closed and a new one opens through a **new**
`gatewayclient.Client`. This models a gateway restart, because the broker sees
all of that gateway's connections close. There were 5 reconnects in the run.

| Session | Patches/s | Viewer wire | Input RTT p50 / p95 / p99 / max (n=401) | Output-to-frame p50 / p95 / p99 / max (n≈4,590) |
| --- | --- | --- | --- | --- |
| 0 | 44.4 | 5.77 MiB/s | 58.6 / 71.8 / 83.2 / 105.9 ms | 48.8 / 58.6 / 63.2 / 109.3 ms |
| 1 | 44.4 | 5.76 MiB/s | 60.8 / 70.8 / 81.8 / 98.5 ms | 48.8 / 58.5 / 62.8 / 103.6 ms |
| 2 | 44.5 | 5.77 MiB/s | 61.2 / 72.1 / 80.8 / 126.9 ms | 48.7 / 58.4 / 62.4 / 93.6 ms |

Broker resources, sampled every ~5 s:

| Point | RSS | Go heap objects | Go mapped | Goroutines | Children |
| --- | --- | --- | --- | --- | --- |
| Idle (no sessions) | 16.2 MiB | 2.3 MiB | 16.8 MiB | 22 | 0 |
| 5.9 s | 129.7 MiB | 96.1 MiB | 124.9 MiB | 66 | 3 |
| Warm (21.4 s) | 134.3 MiB | 67.3 MiB | 128.9 MiB | 66 | 3 |
| Final (120.2 s) | 135.2 MiB | 83.9 MiB | 129.4 MiB | 66 | 3 |

- Maximum RSS: 135.2 MiB (bound 300). RSS growth after warm-up: +0.9 MiB
  (bound 32). The heap moves between 52 and 97 MiB with GC cycles and has no
  upward trend.
- Goroutines: 66 at warm-up, 66 at the end, 66 maximum, after 5 reconnects.
- Broker CPU: 200% of one core, averaged over the run (240.8 s CPU in 120.2 s).
- The child pid did not change across the 5 viewer/gateway-client
  replacements. At the end, all three sessions have state `running` with the
  same generation, and every child process is alive.
- Every patch chained from the revision on the screen. There were 0
  `baseRevision` mismatches.
- Resync after stress: each child was paused. Then a fresh observer's snapshot
  for rows 2–24 matched the long-running controller's patched screen exactly,
  at flood lines `L001730304`, `L001731024` and `L001731024`.

Comparison runs (40 s each, same harness):

| Child rate per session | Patches/s | Viewer wire | Input RTT p95 | Output-to-frame p50 / p95 | Broker CPU | Max RSS |
| --- | --- | --- | --- | --- | --- | --- |
| 0 (status line only, no scrolling) | 49.9 | 0.31 MiB/s | 8.0–15.6 ms (n=121) | 8.1 / 10.2 ms | 19% | 36.6 MiB |
| 64 KiB/s | 48.8 | 6.33 MiB/s | 62.8–63.9 ms (n=121) | 39.8 / 54.0 ms | 129% | 129.8 MiB |
| 1 MiB/s (default run) | 44.4 | 5.77 MiB/s | 70.8–72.1 ms (n=401) | 48.8 / 58.5 ms | 200% | 135.2 MiB |

### 2. Stalled viewer: PASS

One session at 1 MiB/s, one healthy controlling viewer, and one viewer that
sends `hello` and `session.attach` and then never reads. The stall window is
20 s.

- The child continued to run. Its pid stayed the same, and it wrote 292,320
  more lines during the stall.
- The healthy viewer was not affected: 50.8 patches/s during the stall and no
  errors.
- Broker memory: RSS was 61.9 MiB before the stall and 65.1 MiB at its peak
  (+3.2 MiB, bound 16). The heap was 19.9 MiB before and 31.5 MiB after, and
  it stays inside the heap range of a single busy session. Goroutines went
  from 38 to 42 while the stalled viewer's writer was blocked, and back to 38
  after the broker dropped the viewer.
- The broker disconnected the stalled viewer. When the test started to read,
  only 4 messages (1,593 bytes) were buffered, and then the stream ended:
  `ipc: read frame body: unexpected EOF`. The broker closed the connection in
  the middle of a frame after its 5 s write deadline, as the poisoned-stream
  rule in `session_writer.go` says. This is far below the 2 MiB budget.
- A fresh viewer attached and received a complete snapshot (24 rows,
  `rowCount` checked). After the child was paused, rows 2–24 of this snapshot
  matched the healthy viewer's patched screen exactly, at `L000365040`, with
  the same pid.

### 3. Gateway restart and broker restart: PASS (existing tests plus one new probe)

The existing tests cover these items. They were run again on this machine and
all passed:

- `TestGatewayRestartPreservesBrokerSessionAndCanonicalOutput`: the gateway
  stops and starts, the child pid and generation stay the same, and the output
  keeps going.
- `TestPtermBrokerAndGatewayExecutableModesRestartIndependently`: the real
  `pterm __broker` and `pterm __gateway` executables. The gateway is killed
  with SIGKILL, and the session stays `running` with the same id and
  generation.
- `TestBrokerStartupMarksPriorLiveMetadataLostWithoutRerun`: the metadata of a
  seeded prior live session becomes `lost`, and nothing runs again.

The soak run added two items:

- Gateway-client replacement under load, 5 times, with the child pid
  unchanged (see section 1).
- A real broker **crash** (`TestSoakSessionLimitAndBrokerKill`). SIGKILL to a
  broker that has 8 live busy sessions. All 8 children exited **11 ms** later
  (PTY hangup), with no orphans. A new broker on the same data directory lists
  all 8 prior sessions as `lost` and starts 0 children. The paired credential
  continued to work after the restart.

### 4. Eight-session limit: PASS

Eight sessions created through the gateway client all succeeded. The ninth
returned `limit_exceeded: broker resource limit reached` (typed
`gatewayapi.Error`). After the rejection, the list shows 8 sessions and there
are 8 children. After a terminate, a new create succeeded, so the slot was
released correctly.

Resource data point: **8 busy sessions (1 MiB/s each, no viewers) used 278.7
MiB RSS** (heap 193.7 MiB, 104 goroutines, 166% CPU) after 10 s.

## Defects and findings

No acceptance bound in this document failed. No product defect was found that
violates a written requirement, so this work adds no failing test. The items
below are measured risks. The P7 review must accept each one or send it to
the owner of the product code.

- **P7-SOAK-1: bandwidth for each viewer when the screen scrolls.** When the
  screen scrolls on every frame, each patch carries all 24 rows. Each cell is a
  JSON object with `contentClipped`, `continuation`, `text` and `width`. As a
  result, a viewer receives about **5.8–6.3 MiB/s** for any output that keeps
  the screen scrolling, even at only 64 KiB/s of child output. With a static
  screen and only a status line, it is 0.31 MiB/s. On a slow cellular link, one
  full-screen patch (tens of KiB to more than 100 KiB) can take longer than the
  broker's 5 s `DefaultViewerTimeout`. The broker would then disconnect the
  phone as slow, again and again. This was not measured on a real link. See
  "Owner verification needed".
- **P7-SOAK-2: broker CPU for each viewer.** A CPU profile at 64 KiB/s ×3
  (`PTERM_SOAK_CPUPROFILE`) shows the following:
  - About 34% of broker CPU is `writev` of patch frames.
  - About **28% is `protocol.DecodeHost`, which strictly decodes the broker's
    own outbound message again** (`viewer.go` `hostMessageForRequest`, line
    487).
  - About 10% is GC.
  - Broker CPU grows with viewers × frame rate × screen size, not with child
    bytes: 19% for a static screen, 129% at 64 KiB/s, 200% at 1 MiB/s, and
    166% for 8 busy sessions **without** viewers.
- **P7-SOAK-3: latency under scrolling load.** Output-to-frame goes from p50 8
  ms (static) to p50 40–49 ms (scrolling). The input round trip goes from p95
  8–16 ms to p95 63–72 ms. Both stay well inside the busy bound. The plan's
  "p95 < 30 ms app-added key-to-frame" target was **not** tested here. That
  target needs the controlled 1,000-event benchmark, and the idle samples here
  are only n=121 per session.
- **P7-SOAK-4: memory headroom at the session limit.** A session whose
  history is full (5,000 rows × 80 columns) costs about 33 MiB of broker RSS.
  The `MaxHistoryBytes` estimate is 32 MiB, but the real Go heap for each row
  is far above the normalized text size. 8 busy sessions use 278.7 MiB RSS for
  the broker alone. This is below the 300 MiB target, which applies only to
  the three-session test. But wider terminals (up to 400 columns) or a gateway
  would go above 300 MiB. This was not measured at 400 columns.
- **Unrelated flake.** `tests/integration` `TestPtermDesktopAttachNativeOuterTTY`
  failed one time (`desktop_attach_darwin_test.go:102: attach executable did
  not exit: context deadline exceeded`) during one plain `go test ./tests/...`.
  It then passed 4 times in a row. It was not investigated because it is out of
  scope.

## Not tested: owner verification needed

- **Windows 11 soak.** The package is `darwin` only (it uses `ps`, `pgrep`, and
  Unix PTY hangup to prove there are no orphans). ConPTY and Job Object
  cleanup under load are not covered.
- **Real phone over cellular.** No WebSocket, browser rendering, Tailscale, or
  cellular link was in the path. Before release, the bandwidth for each viewer
  (P7-SOAK-1) must be checked against a real link.
- **Multi-hour soak.** The plan asks for a 24-hour three-session run with
  repeated phone and gateway reconnects. This work ran 2 minutes. The harness
  supports a longer run with, for example,
  `PTERM_SOAK_DURATION=24h ./scripts/soak.sh -run ThreeBusy`
  (`-timeout` is 60m in the script; raise it for longer runs). The plan's
  10-minute 1 MiB/s stress item is also not claimed.
- **Real `pterm __gateway` process and WebSocket relay under load.** The soak
  drives the broker through `gatewayclient`, which is the gateway's own broker
  client. The gateway's HTTP/WebSocket layer and the gateway's RSS and CPU
  were not measured.
- **Controlled key-to-frame benchmark** (≥ 1,000 events, p95 < 30 ms) and
  foreground reconnect p95 at 120x40 were not run.

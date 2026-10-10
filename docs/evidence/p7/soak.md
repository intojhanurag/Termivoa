# P7 soak, busy sessions and stalled-viewer evidence

Harness update (2026-10-08, issue #232): the `children` metric now counts
every terminal fixture from one PID/parent/argument snapshot, excluding
scanner helpers while retaining unexpected duplicate fixtures. The original
September measurements below used the earlier `pgrep` census. See
[issue #232 evidence](../issue-232/README.md) for regressions and the separate
30-second correctness run.

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

- **P7-SOAK-1: bandwidth for each viewer when the screen scrolls. FIXED
  2026-09-27, see "P7-SOAK-1 fix" below.** When the
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

## P7-SOAK-1 fix: output adapts to the phone link (2026-09-27)

Branch `feat/hu-bandwidth`. Same machine and Go as above.

### Cause

The broker already keeps at most one pending update for each viewer: a
viewer's worker takes the newest published frame only after its previous
write has drained, and it diffs from the last delivered revision (a full
snapshot if the patch cannot chain or is too large). But the gateway read the
broker stream as fast as local IPC allowed and queued everything for the
phone. The broker never saw a slow phone, so it never coalesced, and the
gateway dropped the phone when its 2 MiB outbox budget filled. The phone
reconnected, got a fresh snapshot, and the loop repeated.

### Change

- **Backpressure** (`internal/gateway/live.go`). The relay reads the next
  broker message only while the phone outbox, counting the message on the
  wire, holds at most 64 KiB. A slow phone therefore stops the broker stream,
  the broker's IPC write blocks, and the viewer worker coalesces to its newest
  frame. The child and the canonical state never wait: the worker is outside
  the session actor (existing isolation, proved again by the soak's
  `childLinesAdvanced`).
- **Liveness.** A pause that does not end within 15 s, or one message write
  that takes more than 15 s, disconnects the phone (1013 "client too slow").
  The broker's `DefaultViewerTimeout` rose from 5 s to 20 s so that the
  gateway, not the broker's IPC write deadline, reports the stalled phone. The
  2 MiB outbox budget stays as a hard bound.
- **Kernel buffer.** The phone socket's send buffer is 16 KiB. The gateway
  listens on loopback only, so this costs no throughput, and stale frames wait
  in the broker (which replaces them) instead of in an auto-sized kernel
  buffer the relay cannot see.
- **Compression.** The phone WebSocket negotiates permessage-deflate with
  **no context takeover** in both directions (`CompressionNoContextTakeover`;
  browsers support it natively). Each message is compressed alone. A
  CRIME-style attack needs a secret and attacker-influenced bytes in the same
  compression window, so that the compressed length leaks whether a guess
  matches the secret. Terminal output is attacker-influenced (any program can
  print), and `control.changed` carries the holder-only lease. Without context
  takeover, the lease message never shares a window with terminal output, and
  each message only reveals its own compressed length. The 256 KiB inbound and
  1 MiB outbound limits apply to decompressed sizes (coder/websocket applies
  the read limit after inflate; the relay checks the broker message before
  deflate). Messages below 512 bytes are sent uncompressed.
- **Close codes.** When the broker stream ends, the relay flushes the outbox
  and closes with a code based on the last broker message: 1008 only after
  `access.revoked` (revocation, logout, expiry), 1000 after `session.state`
  `exited` or `lost`, and 1013 for any other end.

Broker `DecodeHost` of its own output (P7-SOAK-2) is unchanged.

### Soak harness

`TestSoakThrottledPhones` (`tests/soak/throttle_test.go`) starts the real
gateway (`gateway.Run`) in the test process in front of the separate broker
process, one session that scrolls at `PTERM_SOAK_RATE`, and three phones on
real WebSockets: unthrottled (controller), 1 MiB/s and 100 KiB/s. A throttled
phone reads at most its rate from a small receive buffer, so TCP flow control
reaches the gateway as from a slow link. A disconnected phone reconnects at
once, as the web client does, and the test counts it. After 5 s of warm-up
the test measures for `PTERM_SOAK_THROTTLE_DURATION` (default 60 s): frames/s,
decoded JSON bytes/s, bytes/s read from the socket ("wire"), output-to-frame
staleness, disconnects, patch chaining, and broker RSS. Then it pauses the
child and requires every phone's screen to equal a fresh snapshot.
`PTERM_SOAK_DEFLATE=0` makes the phones not offer compression.

The shared viewer now decodes payloads with plain `json.Unmarshal` after the
strict `DecodeHost`, which already validates the payload. The second strict
pass made the harness CPU, not the link, the bottleneck once compression
allowed ~40 frames/s to a throttled phone.

Commands:

```sh
PTERM_SOAK_RATE=1048576 ./scripts/soak.sh -run ThrottledPhones
PTERM_SOAK_RATE=1048576 PTERM_SOAK_DEFLATE=0 ./scripts/soak.sh -run ThrottledPhones
PTERM_SOAK_RATE=0 ./scripts/soak.sh -run ThrottledPhones
PTERM_SOAK_RATE=0 PTERM_SOAK_DEFLATE=0 ./scripts/soak.sh -run ThrottledPhones
```

"Before" is the same test file run against base commit `12b8dc7` (the old
gateway does not negotiate compression).

### Results, scrolling at 1 MiB/s (60 s each)

| Build | Phone | Disconnects | Frames/s | JSON MiB/s | Wire MiB/s | Staleness p50 / p95 |
| --- | --- | --- | --- | --- | --- | --- |
| Before | fast | 0 | 49.7 | 6.44 | 6.44 | 21 / 46 ms |
| Before | 1 MiB/s | **38** | 5.1 | 0.75 | 0.80 | 474 / 916 ms |
| Before | 100 KiB/s | **9** | 0.5 | 0.09 | 0.10 | 2.65 / 5.27 s |
| Backpressure, no deflate | fast | 0 | 50.0 | 6.49 | 6.49 | 21 / 24 ms |
| Backpressure, no deflate | 1 MiB/s | 0 | 7.7 | 1.00 | 1.00 | 293 / 309 ms |
| Backpressure, no deflate | 100 KiB/s | 0 | 0.8 | 0.10 | 0.10 | 6.95 / 8.19 s |
| Backpressure + deflate | fast | 0 | 49.5 | 6.42 | **0.125** | 25 / 43 ms |
| Backpressure + deflate | 1 MiB/s | 0 | 49.5 | 6.42 | 0.125 | 26 / 44 ms |
| Backpressure + deflate | 100 KiB/s | 0 | 38.6 | 5.01 | 0.097 | 3.37 / 3.68 s |

Every "after" run ended with each phone on the canonical screen (the
100 KiB/s phone converged 3.3 s after the child paused with deflate, 6.2 s
without), and 0 patches failed to chain. Before, the 1 MiB/s phone was still
reconnecting when the check ran and did not converge in 30 s.

Broker RSS with the throttled phones: before 66.6 → 73.4 MiB peak;
backpressure 71.9 → 75.4 MiB; with deflate 76.7 → 82.3 MiB (bound: +16 MiB).
Goroutines stayed at 46.

### Results, static screen (status line only, 60 s each)

| Build | Phone | Disconnects | Frames/s | Wire MiB/s | Staleness p50 |
| --- | --- | --- | --- | --- | --- |
| Before | 100 KiB/s | **3** | 15.2 | 0.097 | 7.23 s |
| Backpressure, no deflate | 100 KiB/s | 0 | 16.4 | 0.098 | 4.73 s |
| Backpressure + deflate | fast / 1 MiB/s / 100 KiB/s | 0 | 50.0 | **0.031** (from 0.299) | 8.7 ms |

Compression reduces the wire bytes ~51x when the screen scrolls and ~10x for
a static screen with a changing status line.

### Full default suite after the fix

`./scripts/soak.sh` (all four tests): `ok ... tests/soak 225.409s`.

- Three busy sessions: 46.9 patches/s, output-to-frame p95 59.8–60.4 ms,
  max RSS 155.3 MiB, +11.0 MiB after warm-up, 66 goroutines throughout, 5
  reconnects, 0 base mismatches, post-stress resync exact.
- Stalled viewer: the healthy viewer kept 51.3 patches/s, RSS +3.4 MiB, and
  the broker still dropped the stalled viewer (now after its 20 s delivery
  timeout instead of 5 s); a fresh snapshot matched exactly.
- Session limit and broker kill: unchanged, PASS.
- Throttled phones (deflate): fast 49.8 frames/s at 0.126 MiB/s wire,
  1 MiB/s 49.9 frames/s, 100 KiB/s 38.8 frames/s at 0.098 MiB/s, 0
  disconnects, all final screens exact.

Unit tests added: `TestLiveRelayPausesBrokerReadsWhilePhoneIsBehind`,
`TestLiveStalledPhoneIsDisconnected`, `TestLiveBrokerEndCloseCodeFollowsLastMessage`,
`TestLiveCompressesWithoutContextTakeover` (a 134,014-byte frame used 655
wire bytes; a compressed inbound message above 256 KiB decompressed closes
with 1009 and never reaches the broker), and
`TestSlowViewerReceivesCoalescedPatchesWithExactRevisions` (200 changes: fast
viewer ~57 patches, 100 ms-per-message viewer 12, every patch chains from the
previous revision and both end at the final revision).

### Interpretation and limits

- The throttled phones stay connected and get fewer, newer frames. Without
  compression, a 100 KiB/s phone gets one full-screen patch about every 1.3 s;
  with compression, it gets ~39 frames/s.
- The fast phone keeps ~50 frames/s (the publish rate is at most 60 Hz).
- The remaining staleness of the 100 KiB/s phone is mostly buffering outside
  the gateway: the test phone's macOS loopback receive buffer holds ~300 KiB
  even with `SO_RCVBUF` 32 KiB (measured separately: 326–382 KiB accepted by a
  loopback socket that is not read), which is ~3 s at 100 KiB/s. A real link
  adds its own buffers. The gateway itself holds at most 64 KiB plus one
  message and 16 KiB of socket buffer; the broker holds at most one frame.
- In production, Tailscale Serve terminates the phone's connection and
  proxies it to the loopback gateway. Buffers in tailscaled are outside this
  fix; a real cellular test is still needed (see "Owner verification needed").
- Compression costs gateway CPU. It did not appear in the test-process
  profile's top entries; the gateway's strict `DecodeHost` of each broker
  message (in `gatewayclient`) costs more. The gateway's RSS and CPU were not
  measured separately.
- A phone that is slow by CPU rather than link now receives frames as fast as
  it decodes them, and compressed frames are small, so kernel buffers hold more
  of them. The harness showed this before the double validation was removed:
  a CPU-bound test phone reached seconds of staleness with no link limit.

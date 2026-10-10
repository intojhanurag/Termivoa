# #220: validated typed terminal encoding

Date: 2026-10-08. Baseline: `08f0332`.
Environment: Apple M5, macOS arm64, Go 1.27.1; docs Node 24.11.0.
Only synthetic isolated sessions; no production settings or terminal data.

## Reproduction and change

The broker previously marshalled a generated payload and envelope, then called
the full strict `DecodeHost` on its own bytes. A 30-second profile reproduces
the audit: `DecodeHost` consumes 21.23 CPU sample seconds, 55.10% cumulative,
mostly repeated JSON decoding/field inspection and allocation.

Generated snapshots, patches and History pages now use shared envelope and
semantic validators before normal JSON serialization. Exact generated value
types guarantee field names and unique keys. Required arrays, Unicode text,
link bounds and all terminal semantics stay checked. Malformed Go text/links
retain the strict round trip after JSON replacement; actual OSC8 regression
coverage proves both wire acceptance/output and byte-cap expansion rejection.
Low-rate notices retain
the strict wire round trip. All receiving trust boundaries still decode
strictly. See [terminal-delivery.md](../../terminal-delivery.md).

## Controlled initial optimization comparison

Three changing sessions, 64 KiB/s per session, 30 seconds, one viewer per
session and one reconnect. Heavy checks on this machine were paused during
the sequential measurements of the initial optimization. This full comparison
precedes the final UTF-8 eligibility scan added to preserve malformed Go string
normalization. Final-head measurements and its blocked soak gate are below. The same diagnostic harness overlay was
applied to both builds; details and the original harness failure are below.

| Metric | Baseline | Changed |
| --- | --- | --- |
| Broker CPU from process samples | 140% (1.40 cores) | 30% (0.30 cores) |
| Maximum sampled RSS | 142.2 MiB | 133.5 MiB |
| Input round trip p95, three sessions | 32.39–33.98 ms | 22.49–25.26 ms |
| Output-to-frame p95, three sessions | 25.11–25.21 ms | 16.43–16.90 ms |
| Profile CPU samples over about 30.5 seconds | 38.53 CPU seconds | 7.86 CPU seconds |
| Message construction cumulative samples | 22.15 CPU seconds | 1.16 CPU seconds |

Every final screen matched the patched screen after stress, and session
identity survived the reconnect. CPU sample percentages are cumulative and
overlap. Process CPU and sampled CPU are different measurements. These
results describe this synthetic workload, not idle-shell or real-phone use.
Output-to-frame estimates use the existing timestamp probe; its separate
partial-timestamp bug (#224) is not changed in this PR.

The standalone benchmark encodes a 24-row plain-cell screen patch with a fixed
request UUID. The generated payload is prepared outside the timed loop.

| Columns | Before time/message | After time/message | Before allocated/message | After allocated/message |
| --- | --- | --- | --- | --- |
| 80 | 6.54–6.68 ms | 0.298–0.302 ms | 6.42–6.44 MB | 0.289–0.290 MB |
| 200 | 13.39–13.74 ms | 0.699–0.704 ms | 14.30–14.32 MB | 0.701–0.711 MB |
| 400 | 26.70–27.05 ms | 1.372–1.375 ms | 28.49 MB | 1.437–1.460 MB |

Allocation counts fall from approximately 89,600/213,600/423,500 to 11–13.
These are temporary allocations, not retained heap.

Logs: [benchmark before](bench-before-final.txt),
[final benchmark after Unicode eligibility guard](bench-final-normalization.txt),
[profile before](profile-before-measured.txt),
[profile after](profile-after-measured.txt),
[diagnostic soak before](soak-before-measured-quiet.txt),
[diagnostic soak after](soak-after-measured-quiet.txt).

## Final-head results and pending gate

The final Unicode eligibility guard retains the old strict wire round trip for
malformed Go text/links. The table above uses its repeated final benchmark;
these final repetitions ran after other package checks were allowed to resume.
The large allocation reduction is stable, while exact timing is workload- and
machine-dependent.

A quiet final-head profile with the same diagnostic filter ran only 16 seconds
before the separate scanner census race (#232) aborted it. The partial profile
contains 3.90 CPU sample seconds over 16.04 seconds,
and message construction remains a small fraction of the initial strict path.
See [final-head partial profile](profile-final-head.txt) and
[final-head failed diagnostic soak](soak-final-head.txt). Neither the partial
run nor the earlier optimization comparison passes the **final full soak gate**.
The coordinator will run it after fixing the separate scanner census issue.

## Original harness failure and diagnostic overlay

The unchanged soak harness failed before this fix with "broker children = 4,
want 3" and again on the changed build. The fourth child was a confirmed
`lsof` scanner; all three session children remained. This is filed separately
as #232. See [original baseline failure](soak-before-first-attempt.txt) and
[original changed-build failure](soak-after-original-harness.txt). An original
baseline retry also passed; the unchanged gate is still treated as flaky.

For the final before/after measurement only, an uncommitted Go overlay replaced
this one line in `tests/soak/harness_test.go`:

```go
s.children = len(childPIDs(h.broker.Process.Pid))
```

with this measurement-only replacement:

```go
s.children = 0
for _, childPID := range childPIDs(h.broker.Process.Pid) {
    command, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(childPID)).Output()
    if err == nil && (strings.TrimSpace(string(command)) == "lsof" ||
        strings.TrimSpace(string(command)) == "/usr/sbin/lsof" ||
        strings.TrimSpace(string(command)) == "(lsof)") {
        continue
    }
    s.children++
}
```

It counts every returned child except confirmed scanner command names. Failed name lookups and all other children,
including duplicate fixture children, still count. A first bare-name-only
diagnostic filter still failed because macOS also reports the absolute path.
A later final-head run also failed when the scanner census still raced despite
the name filter; unknown or missing-name children were intentionally counted.
No product code or other harness assertion was changed by the overlay. It is
not committed or used by normal checks; its passing measurements are **not a
claim that the original soak gate passed**.

## Commands and correctness checks

```sh
PTERM_SOAK_RATE=65536 PTERM_SOAK_DURATION=30s PTERM_SOAK_CPUPROFILE=<local-profile> scripts/soak.sh -run TestSoakThreeBusySessions
# Final comparison repeated both builds with the identical diagnostic -overlay.
go tool pprof -top -cum <local-profile>
go test ./internal/broker -run '^$' -bench '^BenchmarkHostTerminalMessage$' -benchmem -benchtime=1s -count=2
pnpm install --frozen-lockfile
pnpm protocol:check
go test ./internal/protocol ./internal/broker ./internal/gatewayclient -count=1
go vet ./internal/protocol ./internal/broker ./internal/gatewayclient
go test -race ./internal/protocol ./internal/broker ./internal/gatewayclient -count=1
cd site
pnpm install --frozen-lockfile --ignore-workspace
npx astro build
```

The complete affected-package unit/race runs passed on the initial optimization.
After the final Unicode handling adjustment, the full protocol/terminal suites,
focused encoder/snapshot/viewer/history race tests, affected-package vet and
repeated benchmark passed again. Protocol generation drift and the 29-page
docs build passed. Logs include both earlier broad checks and final focused
checks. Full integration checks are still a coordinator gate.

Protocol tests compare exact serialized bytes with the old serializer, then
validate them with the strict decoder. They cover valid wide/clipped cells,
escaped text and URLs, invalid shape/metadata/row/style/link cases, malformed
UTF-8 and raw surrogate bytes (same normalization/acceptance as the original
wire path), valid astral Unicode and literal backslash-u text,
cell byte limits and outbound byte-budget errors. Existing malformed-input,
snapshot splitting, slow-reader, reconnect and frame tests remain enabled.

Full repository checks and independent adversarial review are coordinator
gates. Physical-phone, native Windows/Linux runtime and 24-hour soak checks
remain pending. No schema, revision, access, database, pairing or CLI changes.

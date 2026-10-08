# Issue 224: complete soak status records and valid latency samples

Date: 2026-10-08. Platform: macOS arm64. Go: 1.27.1.
Base: `08f0332`. This is a harness correction; no shipped runtime changes.

## Reproduction

New regression tests were run against the unchanged parser:

```sh
go test -tags soak -count=1 -run '^TestObserveStatus' ./tests/soak
```

Exit 1. The tests directly supplied synthetic row text and also split the
child's printed control sequences through the real terminal emulator:

| Input | Before-fix result |
| --- | --- |
| `S pid=123 seq=45 echo=P0x1 t=179 !` | Accepted `497597h15m0.014999821s` as latency |
| Same complete timestamp observed at 15 ms and 1 s | Two samples: `[15ms 1s]` |
| Printed status split at byte 42, before the timestamp finished | Accepted `497597h15m0.014999999s` |
| Timestamp 1 s in the future, observed at 15 ms | Accepted `-985ms` |

The baseline split tests supplied a proposed `!` marker that the old parser
ignored. The marker is now in the child writer's shared format string.
These rows and timestamps are synthetic; no real terminal output or secrets
are stored here.

## What changed?

The printed row is `S pid=<pid> seq=<line> echo=<token> t=<Unix ns> !`.
The viewer accepts the complete, ordered record with canonical decimal
integers. Its timestamp must be between session creation and reception;
its PID must remain the same and its flood sequence cannot go backwards.
Each timestamp is sampled only once per viewer, even after measurements
are collected. Fresh paused/static-child timestamps are accepted with an
unchanged flood sequence. There is no upper latency cutoff: a valid slow
link's one-hour delay remains a one-hour sample.

PID, sequence and echoed-input state are also updated only from a complete
valid record, so a partial row cannot satisfy an input/echo waiter early.
Busy-session and throttled-phone checks fail if no complete samples arrive.

The metric remains a same-host wall-clock interval from the child's status
formatting, immediately before buffered output is flushed, to first complete
snapshot/patch reception. It covers observed records, not every child write:
normal frame coalescing may skip writes. Clock changes can invalidate a record;
the harness rejects future or regressing times rather than producing a
negative sample. Reconnecting creates a new viewer, which may initially sample
the last complete status from a snapshot.

## Verification

| Command | Result |
| --- | --- |
| `go test -tags soak -race -count=20 -run '^TestObserveStatus' ./tests/soak` | PASS, 3.486 s |
| `go vet -tags soak ./tests/soak` | PASS |
| `git diff --check` | PASS |

Regressions cover every prefix of a status row, every byte split of the
child writer's format through the emulator, duplicate observations and
collection, paused/static records, valid delayed records, malformed fields,
overflow, missing markers, stale/future timestamps and inconsistent identity.
After the fix, the valid 15 ms synthetic record is measured once; timestamp
prefixes add no sample and do not alter PID, sequence or echo state.

The coordinator also executed:

| Command | Actual result |
| --- | --- |
| `PTERM_SOAK_DURATION=30s go test -tags soak ./tests/soak -run '^TestSoakThreeBusySessions$' -count=1 -v` | Pass; three child identities, one reconnect, all three final screens match; 998–1,000 valid output samples per session |
| `PTERM_SOAK_THROTTLE_DURATION=20s go test -tags soak ./tests/soak -run '^TestSoakThrottledPhones$' -count=1 -v` | Pass; fast, 1 MiB/s and 100 KiB/s viewers have nonempty samples and all final screens match |

These runs overlapped other repository checks, so their CPU/latency numbers
are not isolated performance comparisons. The 100 KiB/s viewer retained a
valid roughly 3.7-second tail, rather than hiding it with a latency cutoff.
The separate intermittent scanner-child assertion defect (#232) did not
occur in this busy run; this PR does not fix it. Native Windows/Linux,
real-phone and 24-hour soak gates remain pending.

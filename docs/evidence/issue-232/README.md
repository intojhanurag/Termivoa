# Issue #232: terminal fixture census during scanner activity

Date: 2026-10-08. Branch: `fix/issue-232`. Base: `08f0332` from
`origin/main`. Environment: macOS / Apple Silicon, Go 1.27.1.

## What was reproduced?

The original census used `pgrep -P <broker pid>` and treated every direct
broker child as a terminal session. During the earlier #220 investigation,
the command below intermittently failed with `broker children = 4, want 3`.
Process inspection showed three `TestSoakChild` fixtures plus an `lsof`
scanner helper. All three fixture identities were still present. A temporary
per-PID helper filter also raced helper exit and fork-before-exec identity;
that measurement-only filter is not part of this fix.

```sh
PTERM_SOAK_RATE=65536 PTERM_SOAK_DURATION=30s go test -tags soak ./tests/soak -run '^TestSoakThreeBusySessions$' -count=1 -v
```

This is an intermittent harness defect, not evidence of a fourth terminal.
The earlier failure is recorded in issue #232; this branch did not rerun the
old intermittent soak solely to collect another failure.

## What changed?

`fixtureChildPIDs` reads PID, parent PID and full arguments from one
`ps -ww -axo pid=,ppid=,args=` invocation with untruncated arguments. It
counts **every** direct child with the terminal fixture's exact
`-- pterm-soak-child <rate>` argument marker.
It does not take an expected-PID allowlist. Unknown duplicate fixture PIDs,
including fixtures with unexpected rate or trailing arguments, are counted.
Marker substrings in executable names do not qualify.

Non-fixture scanner helpers and fork-before-exec helper names do not count
as sessions. There are no later per-PID command lookups that can race helper
exit. A failed process-table command fails the test instead of silently
returning an empty census. Process-table arguments are never logged or saved.

The `children` metric now means terminal fixtures. The busy-session checks,
eight-session limit / broker-kill census and restart census use the same
helper. Existing session generations, displayed child PID identity,
`processAlive` checks, and waiting for every captured fixture PID to exit
after broker death remain intact. Shipped runtime code is unchanged.

## Which regressions passed?

| Regression | Expected behavior |
| --- | --- |
| Scanner overlaps a three-fixture snapshot | Three fixtures; scanner excluded |
| Fork-before-exec helper name overlaps snapshot | Three fixtures; helper excluded |
| Unexpected duplicate fixture PID | Four fixtures; no known-PID filtering |
| Duplicate has unexpected arguments | Still counted |
| Scanner has vanished before snapshot | Fixture count unchanged |
| One terminal fixture is missing | Fixture count falls |
| Other parent or marker substring | Excluded |
| Executable path contains spaces | Exact marker still recognized |
| Live repeating `lsof` alongside an actual PTY fixture | One fixture |
| Second actual PTY fixture starts; scanner then exits | Two fixtures throughout helper exit |
| First actual PTY fixture exits | One remaining fixture |

The live test verifies `lsof` has executed before the overlapping census.
It drains only synthetic fixture output, discards helper output and kills /
waits for its own helper and PTY fixtures during cleanup.

```sh
go test -race -tags soak ./tests/soak -run '^TestSoakFixtureChildCensus' -count=1 -v
go vet -tags soak ./tests/soak
```

Both passed. Race output: [census-race.txt](census-race.txt). Vet exited 0.

## What did the busy run prove?

The reproduction command above passed after this fix. Full output:
[busy-30s.txt](busy-30s.txt). The run used 65,536 B/s/session for 30 seconds
with three sessions and one viewer/gateway-client replacement. Every busy
sample and the final census reported three terminal fixtures. Existing
identity/liveness, latency, memory and final-screen assertions passed.

This is a correctness run under concurrent machine load, **not** an isolated
performance comparison or the default 1 MiB/s/session gate. No #220 runtime
optimization or #224 harness change was copied onto this branch. Parent
integration checks and the final combined soak remain pending. The
eight-session broker-kill test was updated to use the same census but was
not rerun here; the full tagged suite, longer stress/24-hour soak and native
Windows checks remain pending.

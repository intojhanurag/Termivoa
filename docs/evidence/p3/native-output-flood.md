# Native finite-output probe

Date: 2026-09-21. Source base: `7691302`; fixture is included with this evidence
checkpoint. Environment: Apple Silicon, macOS 26.5.2 (25F84), Go 1.27.1.
Only synthetic child output and disposable test directories are used.

## What this establishes

An actual broker-owned PTY child emits 32,768 fixed 80-byte rows (2,621,440
bytes), then issues a cursor-position query and verifies its expected response.
Run once with no viewer and once with a viewer whose sink consumes nothing.
The stalled sink starts before output is released, honors cancellation, and is
disconnected with `ErrViewerSlow`.

After completion the same running child PID appears in the canonical screen.
Normal history remains within both retention bounds and supplies a consistent
200-row page. A fresh observer then receives and validates the actual chunked
snapshot; every normalized row, metadata and revision equals canonical state.
No raw replay, private shell, user terminal or provider account is involved.

The test uses a `stressprobe` build tag to keep this bulk-output fixture separate
from ordinary unit tests:

```sh
go test -race -tags=stressprobe ./tests/integration \
  -run '^TestNativeOutputFloodPreservesCanonicalStateWithoutConsumingViewer$' \
  -count=3 -timeout=120s -v
go vet -tags=stressprobe ./tests/integration
```

Actual result: **pass**, three repetitions / six cases, package time 34.575s.
Each case completed in 5.130–5.761s under the race detector. Retained history was
1,632 rows / 33,550,656 normalized bytes, below 5,000 rows / 33,554,432 bytes.
An earlier corrected single race run also passed (12.344s package time).
Vet and `git diff --check` passed. Windows amd64 `CGO_ENABLED=0` test compilation
with this tag passed; the resulting PE test executable was not run on Windows.
Independent source review found no fixture-ownership or sink-accounting defect.

## Failed runs and separate cleanup defect

The first ordinary probe, before adding the fresh-attachment/history-page checks,
completed both modes in approximately 0.4s. The first three-repeat race run failed
all six cases because the new bulk-output test reused an existing interactive
receipt helper with a fixed five-second deadline. A diagnostic repeat failed the
same way. At five seconds the child was still running, canonical output/history
was advancing, and the completion/query markers had not been reached.

The fixture now polls against its original declared 30-second bulk-test context,
instead of using that unrelated keystroke helper. The runtime, interactive helper,
and process cleanup deadlines were not increased. Measured race completion above
explains this fixture deadline mismatch; it is not a product latency result.

Those interrupted runs also consistently exposed a **separate runtime defect**:
`Session.Close` during active output exceeded its three-second termination limit
with `context deadline exceeded` and `operation not permitted`. Independent native
reproduction captured the session actor waiting synchronously in termination,
the PTY reader waiting on its bounded output queue, and the reaper waiting for the
child. The child completed teardown only after the actor resumed draining output.
The finite-completion probe passing does **not** resolve this shutdown/drain cycle.
A dedicated lifecycle regression and async termination fix now address that
cycle locally; see [active-output shutdown](active-output-shutdown.md) for the
failing baseline, native reruns, worker/input/reply ordering, and limitations.
It remains a P3 reliability blocker on branches that have not integrated the fix.

## Explicit limitations

This is a finite correctness probe, not the required sustained 1 MiB/s ten-minute
three-session stress test, 24-hour soak, latency percentile, or RSS/CPU benchmark.
It does not measure two other interactive sessions or any physical phone. It
checks the expected query reply but does not independently reject extra duplicate
replies; dedicated query-uniqueness fixtures own that requirement. Windows native
execution remains pending. No release acceptance gate is marked complete by this
probe alone.

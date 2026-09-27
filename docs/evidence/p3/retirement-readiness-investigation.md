# Real-session retirement timeout investigation

Date: 2026-09-21. Base: `5c39c430c5c8f822a2f7bda009de7531b1b0c539`.
Environment: macOS 26.5.2 (25F84), Apple Silicon, Go 1.27.1.

## Report and failure-only diagnostics

A concurrent full race/full repository check reported
`TestRegistryRetiresRealExitedSession` failing after its unchanged eight-second
deadline with `real session was not retired: <nil>`. A focused ten-repeat rerun
had already passed before this investigation. That is not enough to classify
the original failure or erase it.

The test builds a fresh disposable query fixture, starts it under the real
session registry, and expects both terminal-query replies and natural process
exit before retirement. The registry separately waits for `Ended`, final-status
publication, viewer retirement, and complete runtime cleanup. The original
failure does not identify which of these stages was delayed.

Temporary diagnostics were added only at the existing failure deadline, before
cleanup. They would record the session's `Ended`, `RetirementReady`, `Done`, and
shutdown signals, immutable final status, queue sizes, and test-process
goroutines. On Mac they would then query and sample only the exact synthetic
child PID with a four-second diagnostic bound. No other process discovery,
terminal transcript, environment, or production project was involved.

The failure did not recur, so none of those diagnostics ran. No child sample,
actor stall, or terminal-query failure was captured. The temporary probe was
removed; this checkpoint changes documentation only.

## Actual validation

- Focused race test, one invocation, overlapped a full ordinary Go suite:
  PASS, test duration 5.14 s including fixture build, package 7.765 s.
- `go test ./... -count=1 -timeout=120s`: PASS for all packages; broker
  package 13.021 s. A full race suite started while this suite was active.
- `go test -race ./... -count=1 -timeout=120s`: PASS for all packages;
  broker package 17.439 s.
- `go test -race ./internal/broker -run
  '^TestRegistryRetiresRealExitedSession$' -count=10 -timeout=90s -v`:
  PASS, package 8.062 s; individual test/build durations 0.39–1.88 s.
  This overlapped a second complete ordinary Go suite.
- Second `go test ./... -count=1 -timeout=120s`: PASS for all packages;
  broker package 7.714 s.

These are Go-suite overlaps, not an exact recreation of every Node/frontend
step from the original concurrent repository check. Test durations include
fixture builds and do not establish runtime latency acceptance.

## Unresolved classification

The previously captured `_dyld_start` stall affected a different PTY fixture
failure. It is a plausible hypothesis for this separately built query fixture,
but no sample establishes it here. No activation probe, retry, skip, longer
retirement deadline, or runtime repair is justified by these passing reruns.

Keep the original eight-second observation unresolved. On recurrence, capture
the exact child and lifecycle barriers before cleanup to distinguish pre-main
activation, missing query replies, process wait, and registry cleanup. Native
Windows, real phones, and release readiness remain separate pending gates.

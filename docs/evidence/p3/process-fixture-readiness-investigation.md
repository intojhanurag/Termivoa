# Executable broker readiness investigation

Date: 2026-09-21. Base: `091acaa0951323339af88cdf39720971cb783f20`.
Environment: macOS 26.5.2 (25F84), Apple Silicon, Go 1.27.1.

## Report and bounded investigation

A parent-agent run reported that
`TestPtermBrokerAndGatewayExecutableModesRestartIndependently` failed while
waiting five seconds for the newly built `pterm` broker's IPC endpoint. That run
overlapped the full repository check with a broad focused race suite. The full
check passed; the separate race run reported the missing endpoint.

This investigation kept the executable-mode fixture and its five-second
readiness deadline unchanged. Temporary failure-only instrumentation was added
to cleanup before terminating a fixture child. It would have printed only that
child's captured stdout/stderr, queried its PID with `ps`, and sampled that same
PID for one second under a three-second diagnostic deadline. It neither scanned
other processes nor sampled a user shell.

The failure did not recur, so the diagnostic branch never executed and no
startup stack was captured. The temporary instrumentation was removed. No
runtime, test timing, skip, retry, warm-up, or assertion change is included.

## Actual runs

| Run | Result |
| --- | --- |
| Isolated `go test -race ./tests/integration -run '^TestPtermBrokerAndGatewayExecutableModesRestartIndependently$' -count=1 -timeout=60s -v` | PASS; test 2.01 s, package 3.664 s |
| `go test -race ./... -count=1 -timeout=120s` concurrently with focused race test `-count=3 -timeout=90s -v` | Both PASS. Full-suite integration package 8.316 s; focused package 10.598 s, individual tests 2.68/1.04/0.99 s |
| `go test ./... -count=1 -timeout=120s` concurrently with focused race test `-count=10 -timeout=90s -v` | Both PASS. Full-suite integration package 6.486 s; focused package 16.857 s, individual tests 0.88–2.49 s |

Thus 14 focused invocations and both complete Go suites passed in this checkout.
The concurrent invocations overlapped at startup; not every later iteration of
the repeated focused test necessarily overlapped the full suite. These are Go
test loads, not a recreation of every frontend step in `scripts/check`.

## Conclusion and remaining uncertainty

The earlier PTY fixture investigation captured a child stalled before Go main
at `_dyld_start`. That is a plausible hypothesis for a newly linked executable,
but it is **not established for this broker-start report**. Without a failure
sample or child error, copying the activation workaround here would be an
unverified change. No fixture fix is justified by the current reproduction.

The original failure remains an intermittent, unclassified test observation;
passing reruns do not erase it. On recurrence, capture the exact created
broker's output and bounded process sample before cleanup, then distinguish
pre-main activation, startup rejection, and a broker initialization stall.
Do not increase the endpoint deadline or weaken a runtime gate based on these
passing runs. Native Windows and phone release gates remain separate.

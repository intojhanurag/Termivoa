# Live-client shutdown output errors

Recorded 2026-09-21 on macOS 26.5.2 (25F84), Apple Silicon, Go 1.27.1.
Base: integrated desktop checkpoint `5c39c430c5c8f822a2f7bda009de7531b1b0c539`.

## Reproduction and fix

Independent review reproduced a lost error when an owned output operation
finished cancellation with a substantive cleanup failure. A synthetic output
waited for its context to end, then returned the cancellation joined with a
descriptor-restoration failure. After painting began, `Client.Close()` returned
nil: its first `stop(nil)` invocation had already consumed the shutdown guard,
so the later output error was ignored. The regression failed on the base with
`Close discarded output cleanup failure: <nil>`.

The output-failure callback now retains a late substantive error independently
of first-wins connection shutdown. `Close` still joins the painter before
returning, making the error available to attachment cleanup before restoring
the outer TTY. Pure cancellation-only error chains remain quiet after an
ordinary close; a joined cancellation and independent failure is not mistaken
for harmless cancellation. Existing network close and detach handling are
unchanged. Neither failure nor close terminates a managed session.

Tests cover explicit close, parent-context cancellation, repeated close,
joined/wrapped cancellation, and joined substantive errors. They verify that
output cleanup has stopped before close returns and that `Err` retains the
same failure. Ordinary close of blocked output remains successful.

## Validation

- `go test -race ./internal/liveclient -run
  'Test(CloseRetainsLateOutputCleanupFailure|CloseIgnoresPureOutputCancellationAfterShutdown|OnlyContextCancellation)'
  -count=10 -timeout=30s`: passed.
- `go test -race ./internal/liveclient ./internal/desktop ./internal/cli
  -count=1 -timeout=60s`: passed.
- `go vet ./internal/liveclient ./internal/desktop ./internal/cli`: passed.
- `git diff --check`: passed.
- Pinned Gitleaks 8.30.1 staged scan: passed, no leaks found.

This is deterministic synthetic failure injection plus native Mac package
tests, not a claim that an actual OS restoration failure was induced. No
native Windows, physical-phone, full repository, or release gate was rerun for
this narrow fix. The original caller handles and host terminal were not changed
by the synthetic reproduction.

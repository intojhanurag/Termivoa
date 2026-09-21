# Canonical desktop live IPC client

Date: 2026-09-21. Base: `9d212651efe331f35fdbb66038905e705125072f`.
Local environment: macOS 26.5.2 (25F84), Apple Silicon, Go 1.27.1.

`internal/liveclient` implements `desktop.Transport` over the restricted local
CLI IPC handshake and canonical live-session envelopes. It attaches as an
observer, assembles complete snapshots using `desktop.FrameClient`, renders
through the constrained desktop renderer, and acknowledges only after painting.
Invalid patch bases request a new snapshot; changed generations close the
connection. Neither terminal output bytes nor outer-terminal query responses
are passed through this transport.

The API is `Dial(ctx, Config)`, `WaitReady`, `AcquireControl`, `Send`, `Resize`,
`ReleaseControl`, `Detach`, `State`, `Changed`, `Done`, `Err`, and `Close`.
Config supplies a context-aware `Output.WriteContext` implementation. Painting
has a five-second deadline; cancellation joins the painter before Close returns,
so an attachment can safely restore its outer TTY afterwards. There is no
background fallback around an uninterruptible writer. The native terminal
output adapter and command integration remain separate work.

Control requires an explicit request and a matching grant request ID with an
advancing epoch. Public controller state cannot grant a lease. Input has one
outstanding user operation, monotonically increasing decimal-string sequences,
and no automatic retry. Concurrent calls return busy immediately. Rejection
does not consume a sequence; accepted acknowledgement advances it only for the
same current lease. Takeover while an input acknowledgement is in flight cannot
restore the old lease. A missing or invalid input acknowledgement closes the
connection and reports delivery uncertainty. Cancellation before a write attempt
discards the unsent action without claiming uncertain delivery.

One host reader processes control changes independently of the painter. The
validated frame queue is bounded to 2 MiB and 64 messages including the active
paint; overflow closes the connection and releases queued data. Controller
heartbeats are scheduled every five seconds. Resize requires local control,
release disables sending immediately, and detach closes only the viewer. On an
orderly exited-session EOF, already queued complete patches finish before Done,
preserving the final screen without treating broker retirement as a failure.

## Evidence

- `go test -race ./internal/liveclient ./internal/desktop -count=1 -timeout=60s`
  passed after the orderly-EOF adjustment: liveclient 2.374 seconds,
  desktop 1.340 seconds.
- After moving input-attempt registration before write-cancellation handling,
  `go test -race ./internal/liveclient -run 'TestLostInputAck|TestRequestCancellation|TestWriteCancellation' -count=1 -timeout=30s`
  passed in 1.624 seconds. This prevents cancellation notification from racing
  ahead of uncertainty bookkeeping at the first write attempt.
- `go vet ./internal/liveclient`: passed.
- `git diff --check`: passed.
- `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --staged --redact --no-banner`:
  passed, no leaks found in the staged checkpoint (approximately 61 KiB).
- Synthetic `net.Pipe` hosts perform the real IPC and canonical hello handshake.
  Tests cover partial snapshot invisibility, paint-before-ack, large decimal
  revisions, bad-base resync, controller-only resize, no automatic acquire,
  accepted/rejected input sequences, no concurrent input queue, stale and
  unsolicited grants, wrong request acknowledgement, takeover before ack,
  missing-ack uncertainty, validation before sending, generation changes,
  heartbeat content, release, detach, frame count/byte bounds, renderer failure,
  blocked output cancellation/join, cancellation while waiting for the writer,
  partial-frame write cancellation, and final-patch drain before retirement.
- Heartbeat payload/state behavior is exercised directly; the fixed five-second
  ticker is source-inspected rather than represented as a wall-clock measurement.

These are synthetic local client and renderer tests. Broker live-stream
integration, real interactive TTY restoration, native Windows execution,
physical phones, performance acceptance, and release readiness are not claimed.
No account-backed coding CLI, startup installation, Tailscale configuration,
signing, or publication was performed.

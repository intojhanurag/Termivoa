# P3 local live-session dispatch

Date: 2026-09-21. Environment: local macOS Apple Silicon; Go 1.27.1.
This is local transport evidence, not a phone, browser, native Windows, or
release-installation claim.

## Implemented boundary

After restricted local IPC negotiation, the first request selects management
RPC or canonical desktop live-stream negotiation. Live desktop access is
CLI-only; gateway/browser authorization remains pending P4. A live connection
attaches one observer, acknowledges snapshots, explicitly acquires control,
sends structured input, requests resize/history/resync, and detaches without
owning the child lifetime. All responses use the canonical JSON schemas.

Actor-ordered holder-only grants and public control/lifecycle notices are
covered separately by `viewer-session-events.md`. Input scalar/enum boundary
regressions are in `session-protocol.md`. An independent source review of the
actor-notice commit found no blocker. The independent dispatcher audit found
the split connection-budget issue below; its follow-up review is clean.

## Executed focused checks

- `go test ./internal/broker -run 'TestSessionStream|TestSessionWriter' -timeout=30s`
  passed after integration, including structured Ctrl+C, application cursor
  key, bracketed paste, and mouse encoding through the authoritative emulator.
- `go test -race ./internal/broker -run 'TestSessionStream|TestSessionWriter|TestManagement' -timeout=90s`
  passed for the integrated notice/grant path. Final combined protocol/IPC/broker
  race validation also passed (12.125 seconds for the broker package).
- Stream tests use length-prefixed connections, real canonical emulator state,
  and a synthetic process seam. They prove duplicate input is not reenqueued,
  sequence gaps and stale controllers are rejected, reconnect starts read-only,
  and viewer closure preserves the same child identity/generation.
- Query fixtures capture exactly one cursor reply with zero, one, and two
  viewers. Clients receive normalized snapshots, not query sequences.
- Resize, consistent/stale history pages, and explicit resync are exercised over
  the stream. Control is rejected before a snapshot acknowledgement. Gateway
  identity cannot enter the desktop path.
- Concurrent writer fixtures assert complete framing, cancellation closes a
  partial stream, and over-budget connections release their reservations.

One existing management-cancellation test had a scheduling race: it canceled
the connection before setting a read deadline, and setting the deadline could
itself fail on the already-closed pipe. The deadline is now established before
cancellation; the closure assertion remains unchanged. Both isolated baseline
and changed reruns passed; this was a test ordering repair, not a claim that
the original intermittent failure never occurred.

## Review fixes and final validation

The full race run initially reproduced a resize snapshot timeout. The snapshot
chunk builder re-encoded and revalidated every growing row prefix, doing
quadratic work in the screen height. It now measures each row and encodes each
final chunk once. The same resize/history/resync fixture passed three race
repetitions after the fix (5.439 seconds total). A JSON-expansion fixture proves
contiguous multi-chunk rows stay within the 1 MiB envelope limit.

Independent review found that queued actor notices and direct dispatcher
responses had separate 2 MiB budgets. They now share one connection reservation
boundary, including framing bytes, without counting a viewer message twice.
The regression models queued notices plus a stalled history response and rejects
a frame that previously fit each separate budget while exceeding the total.
The reviewer independently reran stream/writer race tests (3.896 seconds) and
confirmed balanced drain, failure, and detach paths.

Final commands/results:

- Node 24.11.0 `./scripts/check`: passed, including Go checks, patched dependency
  tests, protocol drift, TypeScript, 47 web tests, and production web build.
- `go test -race ./internal/protocol ./internal/ipc ./internal/broker -count=1 -timeout=90s`:
  passed on the final implementation.
- Windows amd64 `CGO_ENABLED=0 go test -c ./internal/broker` and `go build ./cmd/pterm`:
  passed; both outputs verified PE32+. This is not native Windows execution.
- Pinned `govulncheck@v1.8.0`: no vulnerabilities found. The initial scanner
  built itself with Go 1.26 and could not load this Go 1.27 module; rebuilding the
  same pinned scanner with `GOTOOLCHAIN=go1.27.1` corrected the tooling mismatch.
- `pnpm audit`: no known vulnerabilities found.
- Pinned Gitleaks `v8.30.1` scanned 76 reachable commits with no leaks found.
  `gitleaks git --pre-commit --staged --redact --no-banner` also passed on the
  checkpoint (45.41 KiB of staged content scanned before the evidence update).

The first full check hit the existing two-second native PTY fixture READY
timeout. An isolated rerun passed in 0.20 seconds, and subsequent full checks
passed. The timeout was not removed or increased. Its intermittent cause remains
unproven and must be investigated if it recurs; it is not labelled a product
performance pass.

GitHub DNS resolution was unavailable during final preparation, so push/PR
review remain pending network recovery. No hosted CI pass is claimed.
No installation, Tailscale, startup registration, signing, or publication has
been performed by this checkpoint.

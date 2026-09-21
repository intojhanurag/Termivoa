# Bounded controlled desktop resize

Date: 2026-09-21. Environment: Go 1.27.1, macOS 26.5.2 (25F84), arm64.
This is local synthetic evidence, not native Windows or real-phone validation.

## Reproduction

At the public attach checkpoint, a controlled 30x6 canonical screen followed by a
physical 20x5 viewport and one queued old-sized patch closed the viewer. The new
`TestControllerShrinkRequestsFittingSnapshotWithoutClosing` failed before the fix:
`controller shrink closed the viewer instead of requesting a fitting snapshot`.
The existing viewport guard correctly prevented unsafe drawing, but the controller
needed a bounded transition instead of disconnecting on every such shrink.

## Behavior and bounds

An explicit controller resize, or a typed local zero-byte viewport rejection after
the initial applied snapshot, starts one resize transition. The client disables
input immediately, sends structured `terminal.resize`, then one `screen.resync`
for that target. Resync abandons an old unacknowledged snapshot; an oversized or
skipped screen is never marked applied. Patches during the transition are not
painted. Only a fitting complete canonical snapshot can resume readiness.

The normal 2 MiB frame queue and message/dimension bounds remain unchanged.
Targets coalesce, a transition has one fixed five-second deadline, and no more
than eight different targets can be requested before it must converge. Stale
frames and changed targets do not extend the deadline. A host that does not
converge causes an actionable re-attach error, not a resnapshot loop. Ordinary
output writes retain their independent five-second cancellation limit.

The CLI shows a local ASCII Syncing placeholder when the previous canonical grid
no longer fits. This local placeholder is never a canonical frame/acknowledgement.
Keystrokes during syncing are discarded and never replayed. The fitting snapshot
restores canonical cursor state. Loss of control while awaiting a snapshot can
resume as an observer; it never reacquires control or sends another resize.
Initial oversized attachments and observer-only shrink remain non-mutating errors
with enlarge/re-attach guidance. A viewport below 20x5 is unsupported and fails
clearly rather than clamping a larger canonical grid into it.

A resize can race the physical check immediately before actual output. Only the
exact local `ViewportResizeRequired` error with zero bytes written can enter the
recovery path. Partial writes and joined cleanup failures still close the viewer,
and substantive cleanup errors remain observable. `FrameClient.ApplyPatch` now
preserves both its resync sentinel and the concrete renderer error so this narrow
case is identifiable without weakening ordinary viewport/protocol errors.

## Verification

- `go test -race ./internal/cli ./internal/liveclient ./internal/desktop -count=1 -timeout=60s`: passed (4.463s / 2.895s / 2.331s).
- Ten race-enabled repetitions of shrink, fixed-deadline/stale snapshots,
  observer-only rejection, zero-byte versus partial/joined errors, takeover,
  coalesced/bounded target changes, CLI Syncing/no-replay, and concrete renderer
  error identity: passed (CLI 1.403s, liveclient 6.517s, desktop 2.008s).
- `go vet ./internal/cli ./internal/liveclient ./internal/desktop`: passed.
- Windows x64 CGO-disabled CLI/liveclient test compilation: passed, not executed.
- `git diff --check`: passed.

The timeout test shortens only its immutable test-instance deadline after the real
request has been sent. Production retains five seconds. No production timeout was
increased and no retry was added to user input.

Independent native outer-PTY executable tests, interactive host applications,
real phone use and native Windows console execution remain separate gates. This
checkpoint does not claim those tests passed. Public command files and the new
session-creation workflow are owned by the parallel integration branch.

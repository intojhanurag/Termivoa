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

The CLI compares the desired size with the applied canonical grid, not a second
polling cache. If the paint worker completed recovery before the next size poll,
that poll does not cause a second same-size resize/resync. Growing back to a
previous size still requests a new canonical grid.

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

## Independent review follow-up

Review reproduced an old snapshot acknowledgement crossing a newly published
resize/resync boundary. Snapshot validation, paint and acknowledgement now share
the resize-publication gate; no client state mutex is held across output. The
regression admits a resize before a queued old snapshot completes, then proves
only the replacement snapshot is acknowledged. Five independent race repetitions
of this regression and related resize/output-error cases passed.

The native outer-PTY reviewer reproduced shrink to 60x17 followed by a stalled
grow back to 120x39 three times. The obsolete size cache caused the grow request
to be skipped even though the canonical grid was still small. Comparing canonical
metadata fixes this. The independent executable test passed once against this
working-tree candidate (7.85s; package 9.458s), including complete cell/cursor
comparisons after shrink/grow, Ctrl+C/D bytes, takeover, same-process reconnect,
and terminal restoration. Committed repeated executable evidence is tracked by
the separate integration branch, not implied by this working-tree run.

Initial control acquisition previously displayed Control on the old initial
screen before the requested-size snapshot completed. Setup now retains Syncing
until that target is painted and acknowledged. An initial attempt to discard
queued input before writing Control was insufficient: independent review proved
input typed during a blocked status write remained queued for later execution.

The final setup lifecycle uses an independent bounded decoder while Connect,
initial resize, and status output run. It drains/discards shell actions and still
handles local Ctrl+]. Setup cancellation joins both the connector and decoder
before restoring the terminal. Successful setup cancels/joins that decoder,
discards its incomplete parser state, then performs one native raw-input discard
before starting a fresh live decoder. No startup event queue is replayed. This
handoff is the input-acceptance boundary; input during the brief transition after
Control is written but before handoff can be discarded, never deferred. Native
discard failure closes the attachment with an error and leaves the child running.

The discard is performed only on the owned duplicate, after raw-terminal entry
and between joined decoder phases. macOS uses TIOCFLUSH with the BSD FREAD pointer;
Linux uses TCFLSH/TCIFLUSH. Windows uses FlushConsoleInputBuffer, which requires
GENERIC_WRITE and discards all pending console input records. The API is used
only at this explicit boundary, never as a recurring live-input flush. See the
[Microsoft contract](https://learn.microsoft.com/en-us/windows/console/flushconsoleinputbuffer).
Windows console access/behavior remains unexecuted, not inferred from compilation.

Additional verification on the candidate:

- `go test -race ./internal/cli ./internal/desktop ./internal/liveclient -count=1 -timeout=60s`: passed (3.877s / 2.381s / 3.260s).
- `go vet ./internal/cli ./internal/desktop ./internal/liveclient`: passed.
- Ten focused race repetitions of startup handoff, local startup detach,
  substantive cleanup-error retention, initial readiness, canonical grow-back,
  and resize/snapshot publication ordering: passed (3.049s / 1.469s / 2.797s).
- Actual disposable Mac raw-PTY tests block Control output while delivering text,
  an incomplete bracketed paste, and an incomplete CSI. Only a fresh post-handoff
  key reaches the transport; Ctrl+] detaches and original terminal modes return.
- Windows x64 CGO-disabled CLI and desktop test binaries, and Linux x64 CLI test
  binary: compilation passed. These binaries were not executed on this Mac.
- A separate agent's read-only source review found no remaining demonstrated
  startup/resize defect; its ten focused race repetitions passed (desktop 1.544s,
  CLI 3.138s, liveclient 2.906s). This is an agent review, not a second human approval.
- Staged Gitleaks v8.30.1 redacted scan and `git diff --cached --check`: passed.

Complete live keys during later Syncing are covered by the existing discard
tests. Incomplete input events spanning a later live readiness transition are a
separate unverified case; the startup parser reset is not evidence for that case.

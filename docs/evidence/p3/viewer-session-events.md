# Actor-ordered viewer session events

Date: 2026-09-21. Base: `f74f7620746ba1646e7a056886558f23dbbc795a`, with
the live-session decoder audit commits applied.

## Implemented boundary

The existing viewer worker now serializes normalized frame messages and
actor-produced `control.changed` / `session.state` notices through the same
sink. Attach starts with public controller and lifecycle state. Takeover,
release, expiry, revocation, and viewer failure update observers without a
polling loop. A per-actor deadline timer expires an idle controller lease.

`Session.AcquireControlForRequest(ctx, viewer, requestID)` queues the holder's
secret grant with the original request UUID inside the same actor operation
that broadcasts non-secret state to other viewers. A dispatcher must not send
another successful grant reply. This prevents a separately written old grant
from overtaking a newer takeover notice. The existing local `AcquireControl`
API returns its grant directly and emits only public notices.

There are at most 64 queued notices plus one notice in flight per viewer.
Their encoded bytes share the existing 2 MiB outgoing budget with frame
writes. Overflow or sink timeout disconnects the slow viewer; critical state
is not silently discarded while leaving that viewer connected. Notices can
drain while a frame waits for `snapshot.applied`, but cannot interrupt a
snapshot transaction already being sent. Retirement waits for both the final
frame boundary and all queued/in-flight lifecycle notices, or viewer detach.

All detach paths now release that connection's controller and pending accepted
input, including snapshot-ack timeout and sink failure. Previously only the
explicit viewer detach event did so, allowing timeout removal to retain a lease.

## Snapshot-end acknowledgement race

A fast client can acknowledge `snapshot.end` before the viewer worker commits
its successful write to the actor. The actor now registers the in-flight
snapshot identity before sending its end, can stage that matching early
acknowledgement, and unlocks patches only after the end write has drained and
delivery completion commits. Interrupted or mismatched snapshots still reset
the applied base. No acknowledgement is invented or sent on the client's behalf.

## Verification

- `go test -race ./internal/broker -run 'TestViewer|TestSnapshotTimeout|TestFinalPublishedFrame|TestCurrentViewer|TestManagedSessionRuntime|TestFailedViewerDetach' -count=1 -timeout=30s`:
  passed (9.020 seconds).
- `go test -race ./internal/broker -count=1 -timeout=90s`: passed
  (11.476 seconds).
- `go vet ./internal/broker`: passed.
- New deterministic tests cover observer-only initial state, holder-only secret
  grants and request correlation, monotonic takeover ordering, explicit release,
  idle expiry without status polling/output/input, bounded notice overflow,
  final-state drain before retirement, immediate timeout controller release,
  and acknowledgement before snapshot-end write completion without an early patch.
- Existing resync gate fixture now selects `snapshot.rows` rather than assuming
  that the second wire message is a screen row. Native fixture connections use
  canonical UUIDs because their controller IDs now enter the wire protocol.
- Initial broad attempts did not pass: the saturation helper assumed an empty
  request queue, failed when a notice-completion request already occupied a slot,
  and cleanup blocked on its intentionally paused mock Resize. A timeout stack
  confirmed that fixture path; the helper now saturates remaining capacity and
  mock release is deferred. The affected tests passed after the correction.

This is actor, normalized-message, and local fixture evidence. Remote dispatch,
browser authentication, native Windows 11, physical phones, and release gates
remain separate. A network dispatcher must additionally enforce its observer
attach/snapshot readiness and authenticated connection identity before actions.

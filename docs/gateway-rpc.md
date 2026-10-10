# Gateway RPC availability

Updated 2026-10-08, issue #218. The gateway remains an IPC transport; the
broker owns authorization, SQLite, processes, emulators and PTY input.
This corrects scheduling of existing requests, without changing the network,
trust surface or public protocol.

## Which requests share a connection?

| Class | Requests | Connection lifetime |
| --- | --- | --- |
| Fast RPC | Authentication, pairing, cached settings/provider/GitHub status, the cached per-request preview check, session/computer lists and ordinary metadata changes | One serialized reusable connection |
| Blocking RPC | Session create/terminate, notification test, herdr open | Fresh connection per exchange, at most four simultaneously |
| Integration RPC | herdr create/manage, GitHub action/repository list | Fresh connection per exchange, at most four simultaneously, separate slots |
| Live | Authenticated live stream after `gateway.live.open` | Existing owner-controlled stream lifetime |
| Upload | Upload begin/chunks/commit | Existing dedicated upload connection and HTTP upload bound |

A worktree checkout or hook may take up to 120 seconds in the broker. Giving
it the same serialized connection as every session read previously caused
unrelated reads and pre-Live authentication to wait. Blocking RPC isolation
also covers existing network/process waits that could cause the same symptom.
Fast calls do not share the blocking admission slots.

## What bounds the added concurrency?

One gateway client has four blocking slots and four integration slots, so
GitHub and herdr calls cannot take the slots session create/terminate need.
The GitHub state read (`GET /api/v1/github`) stays on the fast connection.
The preview check runs on every proxied preview request and is cached for
2 s by the broker, so it stays fast. A fifth blocking exchange receives a
`limit_exceeded` error before any broker dial or write. There is no added
waiting queue. Each accepted exchange inherits the caller's cancellation and
is registered before dialing. Client shutdown cancels pending dials and closes
active sockets; completion or failure unregisters it and releases its slot.

This bounds active gateway exchanges, not the duration of existing
broker-owned jobs after their caller goes away. Worktree create still uses
its operation receipt and existing broker lifetime: a canceled caller may
have created a worktree/session. The gateway reports delivery uncertain and
never automatically resends a fully written mutation. No new broker
background job, input queue, shell owner or mutation retry is introduced.

## What remains checked on every exchange?

Requests keep their restricted IPC gateway role, framing limits, raw
credential and forwarded origin. The broker reauthorizes them. Responses
must match the request ID and result type. Session mutations additionally
match the operation ID before success is exposed. Invalid framing,
correlation, payload or operation identity closes the isolated connection
and marks delivered mutations uncertain. Correlated broker refusals remain
ordinary errors.

The existing fast connection still redials after a reused connection's
incomplete write; it never retries a fully written request. Isolated
exchanges dial once and never retry. Authentication results are not cached.

## How is availability verified?

The broker integration fixture uses a real temporary git repository and a
post-checkout hook that waits for an explicit fixture-owned release file.
While worktree creation stays blocked, the same production gateway client
lists sessions, reauthenticates and opens a new bound Live stream over the
real private IPC listener. An invalid credential is still refused. Releasing
the hook finishes the one creation and produces the expected session.

Client regressions exercise every newly isolated slow handler, slot saturation
with fast authentication still available, cancellation/slot reuse, pending
dial cancellation, shutdown, operation correlation and no mutation retries.

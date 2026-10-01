# Local connection modes

Protocol v1 uses four-byte big-endian length-prefixed JSON for **all** local
messages. This is not a second terminal-byte protocol. Socket/pipe current-user
peer checks precede `ipc.hello` / `ipc.welcome`; message sizes are 256 KiB
inbound and 1 MiB outbound, including JSON encoding.

After the IPC handshake, the first request selects one connection mode:

- From a `cli` peer, a management request from `broker-rpc.schema.json` or a
  local administration request from `admin-rpc.schema.json` selects
  sequential, correlated RPC. The connection cannot later switch to live mode.
- From a `gateway` peer, only `gateway-rpc.schema.json` is accepted. A
  successful credential-bound `gateway.live.open` switches that connection to
  a browser live stream, and `gateway.upload.begin` dedicates it to one
  chunked image upload. See `authentication-rpc.md`.
- Canonical `hello` from `envelope.schema.json` selects a live terminal stream.
  It negotiates the same capability profile, envelopes, and structured inputs
  used by the phone protocol. No original child ANSI or raw client bytes cross
  this boundary.

Live mode accepts an IPC `cli` peer declaring the canonical `desktop` client
kind, or a `gateway` peer declaring `browser` only after `gateway.live.open`
bound the connection to a broker-validated browser credential. A gateway cannot
declare `desktop`, a CLI cannot declare `browser`, and the gateway role alone
is never an implicit trust grant.

## Live attachment

The broker creates unpredictable connection/device UUIDs internally. A client
cannot choose another viewer's identity. Each connection attaches to one
session generation as an observer. The initial control/lifecycle notices are
followed by a normalized snapshot transaction. Control acquisition is rejected
until `snapshot.applied` succeeds. Reconnection always creates a new observer;
neither leases nor user input are replayed automatically.

`control.acquire` returns its correlated holder-only lease through the actor's
ordered notice stream. The dispatcher must not send a second grant response.
All other observers receive the public controller/epoch without the lease.
Takeover, release, expiry, and disconnect keep this ordering, including while
a snapshot waits for acknowledgement.

Accepted input returns `input.accepted` with the same request ID and sequence.
Duplicate accepted sequences are acknowledged without another child write;
gaps, stale leases, and changed generations are rejected. An acknowledgement
proves receipt at the broker, not successful shell execution. Transport loss
after sending leaves delivery uncertain and must never trigger automatic
retransmission.

`session.state` also carries `agent` while the session is running: the
broker's `working`/`waiting`/`idle` state, `stateSince`, and the parsed
`question` while waiting. The broker sends a new `session.state` whenever the
agent state or question changes, so a live screen needs no polling.
It also carries `previews` and `cwd` (the live working folder, display text
only) while running, and a new `session.state` follows when either changes.

`input.answer` answers the current question. It carries the controller lease,
the next input sequence, `questionId` and `optionIndex` (0-based). The broker
authorizes the lease first, then requires that `questionId` is still the
detected question on the current screen; otherwise it replies
`input.rejected` with `question_changed` (or `invalid_request` for an option
that is not offered). Neither rejection consumes the sequence, so the client
may keep its lease, refresh, and answer again with the same sequence. On
acceptance the broker writes the keys for that option exactly once through
the single input writer. The client never sends the keys; the broker derives
them from the screen. Just before writing, the broker checks the question
again and drops the answer if it changed in between; it then sends a fresh
`session.state` at once so the client can show the new question. Answers are
never sent automatically.

`screen.resync` schedules a new snapshot. `terminal.resize` requires the current
lease and causes a full snapshot. History requests read immutable bounded actor
pages and return either `history.page` or `history.changed`. `changes.list`
returns `changes.summary` and `changes.file` returns `changes.patch` with the
same request ID; they need no lease, run git off the session actor, allow one
request in flight per viewer (another gets `limit_exceeded`), and are
described in `docs/changes.md`. `changes.commit` and `changes.push` require
the current lease, checked right before git runs (`not_controller` or
`stale_lease` otherwise), and return `changes.committed` and `changes.pushed`
with the same request ID. One of them runs per session at a time (another gets
`limit_exceeded`); a started one finishes even if the viewer detaches, and
only its reply is dropped. `tasks.list` returns `tasks.listed` with the same
request ID: the commands the project folder's files define, read without
running anything. It needs no lease, runs off the session actor, allows one
request in flight per viewer (another gets `limit_exceeded`), and is described
in `docs/project-tasks.md`. `branches.list` returns `branches.listed` with
the same request ID: the branches of the repository that holds the session's
folder (the same folder as `changes.list`), to pick the base of a new
worktree. It needs no lease, runs git off the session actor and allows one
request in flight per viewer. `worktree.remove` requires the current lease and
is valid only for a session that started in a Termivoa worktree
(`<home>/termivoa/worktrees/<repo>/<name>`); it shares the one-write-per-session
slot with `changes.commit` and `changes.push`, runs `git worktree remove`
(with `--force` only when asked), replies `worktree.removed`, and on `removed`
then terminates the session as the management operation does. Branches are
never deleted. `session.detach`
closes this viewing connection without ending the child; session switching
uses a new connection. Termination is only the explicit management operation.

One context-aware writer serializes complete frames and bounds all retained
outgoing messages to 2 MiB per connection. A partial write, timeout, or budget
overflow closes the connection. Viewer workers never block canonical output
parsing indefinitely. Connection closure releases only the subscription and
its controller lease, not the broker, store, or managed process.

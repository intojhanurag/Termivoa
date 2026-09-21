# Local connection modes

Protocol v1 uses four-byte big-endian length-prefixed JSON for **all** local
messages. This is not a second terminal-byte protocol. Socket/pipe current-user
peer checks precede `ipc.hello` / `ipc.welcome`; message sizes are 256 KiB
inbound and 1 MiB outbound, including JSON encoding.

After the IPC handshake, the first request selects one connection mode:

- A management request from `broker-rpc.schema.json` selects sequential,
  correlated management RPC. The connection cannot later switch to live mode.
- Canonical `hello` from `envelope.schema.json` selects a live terminal stream.
  It negotiates the same capability profile, envelopes, and structured inputs
  used by the phone protocol. No original child ANSI or raw client bytes cross
  this boundary.

The currently implemented live mode accepts only an IPC `cli` peer declaring
the canonical `desktop` client kind. A gateway/browser cannot obtain this local
desktop capability. Broker-authorized browser credentials and gateway binding
are a separate P4 deliverable, not an implicit trust grant to the gateway.

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

`screen.resync` schedules a new snapshot. `terminal.resize` requires the current
lease and causes a full snapshot. History requests read immutable bounded actor
pages and return either `history.page` or `history.changed`. `session.detach`
closes this viewing connection without ending the child; session switching
uses a new connection. Termination is only the explicit management operation.

One context-aware writer serializes complete frames and bounds all retained
outgoing messages to 2 MiB per connection. A partial write, timeout, or budget
overflow closes the connection. Viewer workers never block canonical output
parsing indefinitely. Connection closure releases only the subscription and
its controller lease, not the broker, store, or managed process.

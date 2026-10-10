# Terminal message construction

Status: #220 reduces broker message construction overhead in the next beta.

## What is the typed encoding boundary?

The broker already owns immutable canonical frames and constructs generated
Go protocol payloads. `ViewerSubscription.hostMessageForRequest` now calls
`protocol.EncodeHost`. It shares envelope checks with the wire decoder:
version, direction, canonical UUIDs, session identity and identity pairing.

The hot terminal payloads use exact generated value types: snapshot begin,
rows and end, screen patches and History pages. Their validators are shared
with the receiving decoder. They check counters, snapshot IDs, dimensions,
cursor bounds, row/run/cell structure, widths and wide-cell continuations,
styles, safe text, URLs, History offsets and completion. Required arrays must
not be nil. Generated rows containing malformed Go text or link bytes use the
original strict wire round trip, so validation sees JSON's U+FFFD replacement
and any expansion past byte bounds. A malformed OSC 8 query keeps the same
wire acceptance and output as before; it is not rejected in row normalization.
Enum, counter and identity fields already require their exact ASCII forms.

Those generated structs serialize exact field names, all required scalar
fields and unique keys by construction. Raw JSON, maps, pointers to payloads,
other named types and custom marshalers cannot enter the terminal fast path.
Valid strings retain normal `encoding/json` escaping, including HTML and
Unicode escapes. No schema, revision or wire layout changes.

Other, lower-rate message types retain the strict encode/decode round trip.
This keeps the optimization small and limited to the measured hot path.
`DecodeHost` and `DecodeClient` remain mandatory for untrusted received bytes,
including the gateway, desktop and browser boundaries. Unknown/duplicate keys,
required fields, malformed UTF-8 and lone JSON surrogates are still checked
there; typed encoding is not a received-message validator.

## How are byte and queue limits preserved?

The encoder checks the final envelope against the same 1 MiB message budget.
The broker maps an oversized message back to its existing splitting error.
Snapshots still split at whole rows; snapshots, patches and notices still use
the same per-viewer byte reservation, slow-reader drop and drained callback.
Revision bases, snapshot acknowledgements, coalescing, controller leases and
PTY input/query ownership do not change.

## What evidence is available?

Tests compare bytes with the original serializer and revalidate output with
the strict decoder. Invalid payload, identity, Unicode, row/style/link and
message-size cases remain rejected. Existing viewer/snapshot and soak fixtures
cover patch correctness, reconnects and slow readers. Local measurement and
remaining gates are in [issue-220 evidence](evidence/issue-220/README.md).

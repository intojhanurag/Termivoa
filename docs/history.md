# History page construction

Status: implemented; #219 improves host page construction in the next beta.

## Ownership and limits

The broker's terminal actor retains normal-screen history, at most 5,000 rows
and 32 MiB per session. A read copies an immutable page of at most 200 rows
with its epoch, revision and offset. Protocol conversion happens after the
copy leaves the actor. Alternate-screen history stays separate. Contents are
not persisted. The original behaviour is specified in
[implementation-plan.md](implementation-plan.md), section 7.

## Exact encoded-size accounting

`terminalRowWithSize` converts cells to styled runs, clips only pathological
rows under the existing per-row cap and validates the canonical row. It
returns the exact JSON byte length already measured during row bounding.
`HistoryPageFrom` sums those lengths and one comma between rows. For each
candidate it marshals a copy of the generated payload with an empty rows
array. That tiny metadata encoding accounts for decimal counter widths,
offset digit changes, `complete` and optional `nextOffset` without copying
the schema into a handwritten byte formula.

The first candidate exceeding `MaxOutboundMessageBytes - 1024` is removed.
The previous rows remain incomplete with the exact advancing next offset.
The last actor row uses complete metadata when appropriate before checking
the budget. Every accepted payload still passes `validateHistoryPage`; the
transport encodes the final payload normally. No schema, revision, row
content, page boundary or trust rule changes.

Construction is linear in candidate row encoding and normalization, rather
than encoding every preceding row again. Temporary allocations still include
canonical rows and per-row bounding; this is not zero-allocation encoding.
The independent phone pagination limit is #217, outside this change.

## Verification

Tests compare admitted rows against actual growing-payload JSON encoding for
plain rows, escapes, links, mixed styles, clipped pathological rows, final
completion and maximum counter metadata. Empty pages and invalid cells retain
their validation behaviour. The allocation benchmark covers 80, 200 and 400
columns. Measurements and commands are in
[issue-219 evidence](evidence/issue-219/README.md).

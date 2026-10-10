# Terminal history

Updated 2026-10-08. Implements the frozen History/Selection view specified in
[implementation-plan.md](implementation-plan.md), with
server-authoritative replication from [ADR 0002](decisions/0002-server-authoritative-terminal.md).

## How is history frozen?

`Connection.openHistory` captures the screen text and its history epoch/revision.
Normal-screen history reads carry that token and request at most 200 rows.
The broker may return fewer rows because every message must fit 1 MiB.
The client accepts only the expected offset and matching token, validates that
an incomplete page's cursor advances by exactly its returned row count, and
continues until `complete`. Only then does it append the captured screen.
Alternate-screen applications use their current screen only.

`history.changed` drops the previous token's partial rows, freezes the current
screen again and starts at zero with the new token. A maximum of five restarts
prevents continuous output from creating an endless loop. New output after a
completed freeze sets `newOutput`; it never edits the selectable text.

## What bounds a loading attempt?

| Bound | Value | Lifetime |
| --- | --- | --- |
| Retained rows | 5,000 | Current token |
| Flattened history text | 32 MiB, conservatively counted as two bytes per JavaScript code unit | Current token |
| Received page data | 128 MiB, same UTF-16 accounting, including style/link overhead | Entire attempt, across token restarts |
| History requests | 5,000, enough even for one row per page | Entire attempt, across token restarts |
| Loading deadline | 60 seconds | Entire attempt, across token restarts |
| Token restarts | Five | Entire attempt |

A timed-out, failed-send, disconnected, invalid-cursor or over-budget attempt
clears its partial rows and screen and exposes the existing `unavailable`
view. It must not look ready or splice the screen after missing rows. A
completed, closed or stopped read cancels the deadline. Opening History again
starts a fresh attempt; input and the live display are independent.

The host still retains at most 5,000 rows and 32 MiB of normalized history,
kept in memory only. No protocol, database, pairing or auth change is needed.

## How is the styled-page defect verified?

Issue #217 is reproduced by a real 80-column/24-row terminal containing
2,000 synthetic lines of alternating red and green cells. Its 32 MiB bound
retains 1,632 rows, which require 40 byte-limited pages. A phone regression
feeds that page shape and verifies that all retained rows precede the screen.
Additional regressions exercise 5,000 one-row pages, malformed cursors,
request/data budgets across restarts, timeout, completion and reopening.

## How does the host build a history page?

Issue #219 (next beta) made host page construction linear.


The broker's terminal actor retains normal-screen history, at most 5,000 rows
and 32 MiB per session. A read copies an immutable page of at most 200 rows
with its epoch, revision and offset. Protocol conversion happens after the
copy leaves the actor. Alternate-screen history stays separate. Contents are
not persisted. The original behaviour is specified in
[implementation-plan.md](implementation-plan.md), section 7.

### Exact encoded-size accounting

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

### How is page construction verified?

Tests compare admitted rows against actual growing-payload JSON encoding for
plain rows, escapes, links, mixed styles, clipped pathological rows, final
completion and maximum counter metadata. Empty pages and invalid cells retain
their validation behaviour. The allocation benchmark covers 80, 200 and 400
columns. Measurements and commands are in
[issue-219 evidence](evidence/issue-219/README.md).

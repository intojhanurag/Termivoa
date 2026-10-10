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

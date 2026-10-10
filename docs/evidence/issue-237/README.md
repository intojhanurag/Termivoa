# Issue #237: overflow-safe snapshot row bounds

Date: 2026-10-08. Branch: `fix/issue-237`. Base: `08f0332` from
`origin/main`. Go 1.27.1, Node 24.11.0, pnpm 10.33.0; macOS / Apple Silicon.

## What was reproduced?

The existing strict `DecodeHost` validator accepted a `snapshot.rows`
payload with `startRow=9223372036854775807` and one row, despite the schema's
existing maximum of 199. Adding the row count overflowed signed `int64`
before the upper-bound comparison. `git blame` dates the original condition
to `0e7b870c` (2026-09-20); this is not introduced by the #220 optimization.

A temporary package-local test using the existing synthetic fixtures ran:

```go
payload := SnapshotRowsPayload{
    SnapshotID: testSnapshotID, Revision: "1", StartRow: math.MaxInt64,
    Rows: []TerminalRow{testRow(20)},
}
_, err := DecodeHost(encodeTestMessage(t, SnapshotRows, payload, true))
if err != nil { t.Fatal(err) }
t.Logf("strict DecodeHost accepted startRow=%d with one row; schema maximum is 199", payload.StartRow)
```

```sh
go test ./internal/protocol -run '^TestAuditSnapshotRowOverflow$' -count=1 -v
```

[reproduction.txt](reproduction.txt) records the accepted invalid coordinate.
That temporary probe was removed. The permanent regression was then run
against the unchanged validator:

```sh
go test ./internal/protocol -run '^TestSnapshotRowsCoordinateBounds$' -count=1 -v
```

[regression-before.txt](regression-before.txt) fails for three distinct
overflow cases: one row at maximum `int64`, two rows at maximum minus one,
and a 200-row chunk whose sum wraps. Legal boundaries already passed.

## What changed?

Go now explicitly requires `0 <= startRow < MaxRows` before evaluating
`rowCount <= MaxRows - startRow`. Row counts remain 1–200. Short-circuit
evaluation establishes both subtraction operands' bounds first, so neither
addition nor subtraction can overflow. A chunk starting at 199 with one row
still ends exactly at 200 and is accepted; two rows there are rejected.

Only the existing host-message validation changes. No fields, generated
types, protocol revision, authentication, stored data or valid wire behavior
change. Normal broker-generated frames already use bounded coordinates.
The browser already rejects these large coordinates. This is a contract
validation defect, not a demonstrated remote exploit.

## Which adjacent coordinates were audited?

| Coordinate | Arithmetic / validation |
| --- | --- |
| Snapshot start and chunk length | Fixed explicit schema range plus bounded subtraction |
| History page offset and row count | Offset already checked against 0–4999 before addition; count <=200 and end <=5000 |
| History request offset and limit | Existing offset / limit bounds reject extreme integers |
| Patch row index | Compared directly against previously validated screen dimensions |
| Cursor row / column | Compared directly against bounded screen dimensions |
| Normalized run columns | Must match a locally accumulated position; at most 400 runs of 400 cells, no untrusted coordinate addition |
| Browser snapshot assembler | Numbers do not wrap like signed Go integers; existing schema rejects extreme offsets first |

Permanent neighboring tests cover history pages ending exactly at 5000,
pages crossing that limit, extreme history offsets and patch indexes.
A direct desktop snapshot API addition issue was separately reproduced and
filed as #247 by the coordinator under rule 21; no desktop code or probe is
bundled here. Normal transport decoding rejects this input after this fix.

## Which checks passed?

| Command | Result |
| --- | --- |
| `go test ./internal/protocol -run '^(TestSnapshotRowsCoordinateBounds\|TestAdjacentTerminalRowCoordinateBounds)$' -count=1 -v` | Pass; [regression-after.txt](regression-after.txt) |
| `go test ./internal/protocol ./internal/desktop ./internal/terminal` | All three packages passed |
| `go vet ./internal/protocol ./internal/desktop ./internal/terminal` | Exit 0 |
| `go test -race ./internal/protocol ./internal/desktop ./internal/terminal` | All three packages passed |
| `pnpm --filter @phone-terminal/web exec vitest run src/protocol/validate.test.ts src/terminal/SnapshotAssembler.test.ts` | 22 tests passed |
| `pnpm --filter @phone-terminal/web typecheck` | Exit 0 |
| `pnpm protocol:check` | Exit 0; generated Go / TypeScript unchanged |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Exit 0; all internal links valid, 29 pages built |

Node commands used the existing Node 24.11.0 executable on `PATH`. The first
new browser fixture omitted the required `contentClipped` cell field; that
fixture was corrected before the passing browser run. Browser tests confirm
the existing 0 / 199 acceptance and negative / 200 / extreme-int rejection,
including literal integer JSON tokens that JavaScript rounds when parsing.

## What documentation and gates remain?

Architecture, the private terminal capability design, roadmap, progress,
Unreleased changelog (phone and host scopes), README, Live terminal guide and
Next beta release notes are updated. The existing Live terminal sidebar
entry remains valid. There are no new commands, data/access changes, failure
modes or major feature, so CLI/security/troubleshooting and landing-page
changes are not applicable.

The site build still prints existing empty-i18n / absent-docs-404-entry
warnings, but generates the 404 route and passes link validation. No site
tooling fix is bundled. Parent full integration / PR checks, real-phone
verification and native Windows gates remain pending. No push, PR, merge or
deployment was performed by this task.

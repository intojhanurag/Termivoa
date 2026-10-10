# #219: linear History page construction

Date: 2026-10-08. Starting commit: `08f0332`.
Environment: Apple M5, macOS arm64, Go 1.27.1; docs build Node 24.11.0.
Synthetic cells only; no user terminal output or credentials.

## Reproduction and result

The original conversion appended each normalized row and marshalled the entire
growing page to check its length. The benchmark prepares 200 identical plain
`x` rows with opaque foreground/background and a valid incomplete boundary.
Two runs before and after use the same source and row/message limits.

| Columns | Before time/page | After time/page | Before bytes allocated/page | After bytes allocated/page |
| --- | --- | --- | --- | --- |
| 80 | 131.5–132.9 ms | 2.37–2.39 ms | 133.3–135.2 MB | 5.77 MB |
| 200 | 56.29–56.37 ms | 2.22–2.23 ms | 60.45–64.44 MB | 5.17–5.18 MB |
| 400 | 29.97–35.01 ms | 2.20–2.21 ms | 41.82–41.92 MB | 5.32 MB |

These are transient allocations and local conversion timings, not retained
heap or end-to-end phone latency. Full output: [before.txt](before.txt) and
[after.txt](after.txt). Benchmark variation includes runtime pooling/GC.

## Implementation and boundaries

Reuse the row JSON length already measured while bounding normalized rows.
Add those lengths and row commas; marshal only the tiny empty-row metadata
for each candidate. This accounts exactly for optional next offsets, decimal
digit transitions and final completion. The transport still encodes the final
payload normally, and row/page validators remain in place.

- Compare admitted rows and pagination metadata against actual whole-payload
  JSON encoding with plain rows, styles, links, JSON/HTML/Unicode escapes,
  pathological row clipping and maximum uint64 epoch/revision counters.
- Admit an exactly `MaxOutboundMessageBytes - 1024` payload; reject the final
  row after a one-byte increase, preserving the correct advancing next offset.
- Preserve empty complete pages and invalid-cell rejection.
- Existing envelope-budget and strict `DecodeHost` tests remain unchanged.

No schema, protocol revision, database, pairing, data access or CLI change.
The phone pagination issue #217 is independent and is not fixed by this PR.

## Executed checks

```sh
go test ./internal/protocol -run '^$' -bench '^BenchmarkHistoryPageFrom$' -benchmem -benchtime=1s -count=2
go test ./internal/protocol -count=1
go vet ./internal/protocol ./internal/terminal ./internal/broker
go test -race ./internal/protocol ./internal/terminal ./internal/broker -count=1
go test -race ./internal/protocol -run '^TestHistory' -count=1
cd site
pnpm install --frozen-lockfile --ignore-workspace
npx astro build
```

All commands used Go 1.27.1 / Node 24.11.0 where relevant. Logs accompany this
file (the personal workspace path in the site log is redacted; trailing whitespace is stripped). Full repository checks, independent adversarial review and merge are
coordinator gates. Physical-phone tests, native Windows/Linux runtime checks
and the 24-hour soak remain pending.

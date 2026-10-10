# Chat view M1a: benchmarks and fuzzing

Synthetic transcripts only (`internal/transcript/transcripttest`); no real
transcript was read. Machine: Apple M5, darwin/arm64, Go 1.27.1.

| Measure | Budget (design section 7) | Result | Command |
| --- | --- | --- | --- |
| Parse throughput | ≥ 50 MB/s per core | 281 MB/s (8.6 MB file of typical turns) | `go test ./internal/transcript -run XXX -bench BenchmarkClaudeParse` |
| First page, 200 MB file | ≤ 300 ms, ≤ 8 MiB read | 6.7 ms, 1.7 MiB read | `go test ./internal/transcript -run XXX -bench BenchmarkBackfill` |
| Page read budget | ≤ 8 MiB | `TestBackfillReadsAtMostTheBudget` (18 MiB file) | `go test ./internal/transcript` |
| Tails per computer | ≤ 8 | `TestChatTailsAreSharedCappedAndLinger` | `go test ./internal/broker -run Chat` |

## Fuzzing

| Target | Time | Executions | Result |
| --- | --- | --- | --- |
| `FuzzClaudeLine` (every block passes the wire validator) | 90 s | 21.4 million | pass |
| `FuzzTail` (any file, read by poll and pages, gives valid pages) | 90 s | 184 thousand (file I/O per run) | pass |

Command: `go test ./internal/transcript -run XXX -fuzz <target> -fuzztime 90s`.

# Issue 216: synchronize the fake GitHub clone assertion

Date: 2026-10-08. Platform: macOS arm64. Go: 1.27.1.
Base: `08f0332` (`main` when the worktree was created).

## Reproduction

```sh
go test -race -count=3 -run '^TestGitCloneNeedsALiveToken$' ./scripts/fakegithub
```

Exit 1 before the fix. The race detector reported the test reading
`server.gitAuths` at `scripts/fakegithub/main_test.go:173`, concurrent with
the append in `server.serveGit` at `scripts/fakegithub/main.go:516`:

```text
WARNING: DATA RACE
Read: TestGitCloneNeedsALiveToken, main_test.go:173
Previous write: (*server).serveGit, main.go:516
--- FAIL: TestGitCloneNeedsALiveToken
    testing.go:1865: race detected during execution of test
FAIL
```

These are summarized stack locations, with local absolute paths removed.
The existing clone test exercises real local git against synthetic fixture
repositories; it uses no production credentials or repositories.

## Fix and review

Copy the final authentication record while holding the same mutex used by
the HTTP handler. Check the record after unlocking. If no record exists,
fail the assertion explicitly instead of indexing an empty slice.

The adjacent fixture-state reads in `main_test.go` initialize repositories
and the git handler before `httptest.NewServer` starts. The HTTP control
route that copies `gitAuths` already holds the same mutex. No other direct
post-start reads of mutable fixture state were found in the tests.

## Verification

| Command | Result |
| --- | --- |
| `go test -race -count=20 -run '^TestGitCloneNeedsALiveToken$' ./scripts/fakegithub` | PASS, 7.099 s |
| `go test -race -count=10 ./scripts/fakegithub` | PASS, 4.321 s |
| `go vet ./scripts/fakegithub` | PASS |

The same test now passes repeatedly with the race detector enabled. The
fixture still rejects an invalid token and accepts the live token.
The shipped host, phone app and public protocol are unchanged.
The repository-wide race suite is left to the coordinating merge gate.

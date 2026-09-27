# PTY fixture cold activation and readiness

Date: 2026-09-21. Base: `09de65c99df6af8354d1a6c39b6789fb5242fad0`.
Environment: Apple Silicon, macOS 26.5.2 (25F84), Go 1.27.1.

## Reproduction and diagnosis

The unchanged `go test ./... -count=1 -timeout=120s` failed
`TestStartReadWriteResizeAndWait` after its two-second wait for `READY`.
The same test alone passed in 0.21 seconds. A second reproduction under
`go test -race ./... -count=1 -timeout=120s` used temporary, synthetic-child-only
diagnostics at the original deadline, before closing its PTY:

- `ps` showed the fixture alive after two seconds with 0.00 seconds CPU time.
- A one-second `sample` capture showed a 96 KiB physical footprint and all
  93 samples at `_dyld_start + 0`; binary images were not yet available.
- No Go runtime, fixture `main`, terminal read, or `READY` write appeared in
  that captured child stack. The failure was before fixture activation, not
  evidence that a PTY reader lost a `READY` already written by the child.

This establishes the sampled cold-launch failure stage. It does not identify
which macOS loader or security subsystem caused the wait; a narrowly filtered
system-policy log query returned no matching evidence. No OS security setting
was changed. The diagnostic code was removed from the final change, and raw
process samples and local paths are not committed.

## Correction and unchanged gates

After building the exact fixture, `TestMain` now performs one explicit native
activation probe with its own ten-second deadline. It executes only the existing
`args` fixture mode, with a fixed marker verified in the JSON response. This
uses ordinary pipes, no PTY, no project, and no descendant process. The duration
and bound are reported in test output. Missing executable, wrong response,
cancellation, or deadline failure fails setup; there is no retry or skip.

The existing two-second `READY` and `ECHO_OK` PTY deadlines and all lifecycle,
query, resize, argument, and descendant-cleanup assertions are unchanged. Cold
native activation and PTY I/O are now separate, bounded test concerns. These
are not product startup or latency acceptance measurements.

`TestMain` also now calls `os.Exit` only after its setup/test function returns,
so the fixture directory's deferred removal actually runs. Previously the
direct `os.Exit(m.Run())` bypassed that defer on every invocation. Existing
leftover directories from earlier runs were not deleted.

## Verification

- `go test -race -json ./... -count=1 -timeout=120s`: all packages passed.
  The activation probe took 1.11 seconds; the first PTY echo/read/resize/wait
  test then completed in 0.00 seconds at Go test's displayed precision.
  `internal/ptyhost` completed in 3.515 seconds including setup and race exit.
- `go test -race ./internal/ptyhost -count=20 -timeout=90s`: passed in
  5.885 seconds with no race report.
- Added regressions reject an absent executable, incorrect activation marker,
  and pre-canceled context. A synthetic `sleep` child is deadline-canceled
  and reaped rather than accepted as a successful probe or retried.
- `go test -race ./internal/ptyhost -run '^TestFixtureActivation' -count=20
  -timeout=30s`: passed in 4.594 seconds after ensuring the reap assertion
  also permits cancellation before any child was started.
- `go vet ./internal/ptyhost` and `git diff --check`: passed.
- Pinned Gitleaks `v8.30.1` staged scan: passed with no leaks found.
- Running the activation tests with `TMPDIR` set to a new isolated verification
  directory passed in 0.998 seconds (activation 207 ms). A subsequent search
  found no `pterm-ptyhost-test-*` directories there, verifying cleanup after
  `TestMain` returned.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c ... ./internal/ptyhost`:
  compiled a Windows x64 PE test executable. Native Windows execution remains
  pending; cross-compilation is not a Windows pass.
- An earlier post-fix ordinary full-suite run passed PTY tests but failed the
  independent gateway incomplete-request shutdown fixture. That separate
  accepted-connection synchronization defect was reported to its owner; it was
  not altered or concealed by this change.

No host runtime implementation, dependency, protocol, release artifact,
startup registration, Tailscale setting, credential, or publication changed.

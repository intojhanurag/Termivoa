# Issue #256: remove only the saved-login filename

2026-10-08; branch `fix/issue-256`, base `08f0332`; macOS arm64, Go 1.27.1.
Design-only commit `f5c685e` opened as private draft PR #261 before runtime
implementation. Final local verification: 2026-10-09.
**DO NOT MERGE without owner review.**

Confirmed with a temporary Go overlay: create 50 bytes of synthetic token JSON
in a temporary outside file, `os.Link` it to `github.json`, call existing
`loadAccount`/`deleteAccount`, then compare the outside file's bytes. The probe
fails: `outside target unchanged=false bytes=50`. See [baseline.txt](baseline.txt).
No owner data, token, network request or startup operation was used.

Actual command from this worktree:

```sh
go test -overlay <temporary-overlay.json> ./internal/github \
  -run '^TestAudit213HardlinkDeleteDoesNotMutateOtherName$' -count=1 -v
```

The coordinator retains the synthetic probe/overlay in the repository's Git
scratch area under `issue-sweep-2026-10-08/213-security-review/`; it maps a new
test-only package file, without changing shipped code. The historical test
name reflects where the inherited defect was discovered; #256 owns its fix.

## What changed and what passed?

`deleteAccount` now removes only the fixed stored-login name with `os.Remove`.
No content open, overwrite, truncation, sync or size-based buffer remains.
Missing entries succeed; other errors propagate. Unused overwrite-only flags
were removed. Remote revocation, caller serialization and cancellation/error
semantics are unchanged; no schema, protocol or migration. No secure-erasure
claim and no guarantee against arbitrary same-account parent replacement or
recreating the name after removal.

| Command/gate | Actual result |
| --- | --- |
| `go test ./internal/github -run '^TestDeleteAccount\|^TestDisconnectReportsLocalRemovalFailure$' -count=1 -v` before runtime fix | Hardlink and replaced-hardlink cases fail by comparing outside bytes; normal/missing/invalid and caller-error cases pass. [regression-before.txt](regression-before.txt). |
| `go test ./internal/github -run '^TestDeleteAccount\|^TestDisconnectReportsLocalRemovalFailure$\|^TestDeleteDoesNotFollowASymlink$\|^TestDisconnectRevokesAndDeletes$' -count=1 -v` | Passed, 0.998 s; [regression-after.txt](regression-after.txt). |
| `go test -race ./internal/github -count=1 -timeout=120s` | Final full package passed, 15.351 s. Includes connect/refresh/disconnect/network and local removal tests. |
| `go vet ./internal/github` | Passed. |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/github -o <private-scratch-output>` | Passed. Compilation only; no Windows execution claim. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/github -o <private-scratch-output>` | Passed. Compilation only; no Linux execution claim. |
| `pnpm install --frozen-lockfile`; `pnpm protocol:check` | Passed; generated Go/TypeScript unchanged. |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Passed: 29 pages, valid links, llms and Markdown copies emitted. |

Permanent fixtures create real hard links/symlinks on one temporary volume,
including replacements of an initially saved account. They verify the app
entry is gone and the outside bytes are identical, plus normal/missing/damaged
files, empty/nonempty directories, repeat removal and Disconnect surfacing a
local removal error. No replacement race is invented or assumed fully solved.
macOS symlink cases executed without skips. On Windows, a missing symlink
privilege is reported as a skipped native gate, not success.

The first task-owned common test file referenced the macOS/Linux-only fake
Service and failed Windows compilation. It was split: caller regression uses
the Service's existing build tags, while file-removal tests stay cross-platform.
Final compilation and package tests above pass. No unrelated runtime change.

## Documentation and pending gates

ADR/design, architecture, GitHub integration design and threat model now state
unlink-only removal and remove obsolete overwrite/erasure claims. README,
Unreleased host/phone changelog, GitHub guide, Security and Next beta release
notes describe removal/copy limits. Existing GitHub sidebar entry remains
correct. No new command/failure mode/major feature: CLI, troubleshooting and
landing pages require no change for this task.

## Independent reviews (2026-10-09)

Both reviews pinned implementation `0da5258750bf09d6915f6c4cfd35530c90480b6b`.

| Review | Actual evidence and verdict |
| --- | --- |
| Coordinator security | `GOTOOLCHAIN=go1.27.1 go test -race ./internal/github -run '^TestDeleteAccount\|^TestDisconnectReportsLocalRemovalFailure$' -count=1 -timeout=60s` passed, 1.689 s. No blocker: fixed application filename, no token-content write/size allocation, only absence normalized, outside bytes and caller failure tested; remote/context/caller semantics unchanged. |
| History-agent feasibility/alternatives | `go test ./internal/github -run '^TestDeleteAccount\|^TestDisconnectReportsLocalRemovalFailure$\|^TestDeleteDoesNotFollowASymlink$\|^TestDisconnectRevokesAndDeletes$' -count=1 -v` passed on macOS arm64, 0.664 s, no skips. No blocker: normal/missing/invalid entries and outside bytes correct; constant application memory; link-count/verified-overwrite/quarantine retain unnecessary shared-inode write risks. |
| Platform source review | Feasibility reviewer checked Go 1.27.1 Unix unlink/rmdir and Windows DeleteFile/RemoveDirectory plus `os.Remove` documentation. Windows implementation may inspect attributes/clear read-only internally: the application does no inspection, and removal does not overwrite contents. Native Windows/Linux execution is not claimed. |

Replacement fixtures replace entries before deletion; they do not prove
arbitrary concurrent replacement protection. Existing caller behavior and
normal Disconnect error handling remain unchanged.

Pending: explicit owner approval, parent integration (including separate
#213/#255), broader required checks,
native Linux/Windows and real-phone evidence. CI remains denied; no hosted
check/merge/deployment claim. Only local commits by the implementing helper;
the coordinator owns draft PR #261 and has pushed the implementation. This
follow-up changes internal review documentation only; `git diff --check`
passes, with no runtime/test/public-doc change or repeated build claim.

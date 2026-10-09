# Issue #255: private-file repair evidence

Current owner-review status: [approved on 2026-10-09, subject to required checks](owner-approval.md). Owner-pending statements below record earlier evidence; required hosted and native gates remain pending.

Date: 2026-10-08. Base: `origin/main` at `08f0332`. Toolchain:
`go version go1.27.1 darwin/arm64`.

## Independent baseline reproduction

Temporary Unix-only Go tests called production `loadAccount` and
`ipc.EnsurePrivateFile` with isolated synthetic files. Fixtures contained only
`synthetic non-secret fixture`; no real login, provider or service was used.
Each assertion required refusal without changing the outside file's 0644 mode.
The temporary reproduction test files were removed after execution; the
architecture-first commit `3b0bdff` contains no runtime or test-code changes.

Command:

```sh
go test -count=1 -v -run '^TestIssue255' ./internal/github ./internal/ipc
```

Observed:

```text
TestIssue255TokenSymlinkLeavesOutsideMode
load rejected=true; outside mode=0600
FAIL: token symlink must be rejected without changing its target
TestIssue255HardLinkLeavesOutsideMode
hard link rejected=false; outside mode=0600
FAIL: hard link must be rejected without changing its shared inode
```

The token test creates an outside file, an isolated data directory and a
`github.json` symlink to that outside file. The hard-link test creates a second
name for an outside inode and passes that name to the shared guard. In both,
`os.Chmod(outside, 0644)` establishes the mode before the operation, avoiding
umask ambiguity. The failing result is the reproduction, not a passed gate.

## Code inspection

- Unix `securePrivateFile` performs pathname chmod before `validatePrivateFile`
  checks `Lstat`, type, mode and owner.
- Windows `securePrivateFile` performs pathname ACL repair before regular-file
  validation. Its current recovery exception permits Administrators-owned
  objects and rejects other foreign owners; the fix must preserve this policy.
- A check before pathname repair still leaves a replacement race. A descriptor
  repair without single-link validation still modifies shared hard links.

## Pending gates

At the initial design milestone, implementation and all implementation gates
were pending. Parent opened draft PR #264 at `3b0bdff` before local code work.
Hosted CI remains explicitly denied. No push, PR creation, approval, merge,
deployment or owner-file changes were performed by this agent.

## Local implementation verification (2026-10-09)

| Command | Observed result |
| --- | --- |
| `go test -race -count=10 -run '^Test(PrivateFile|TokenFileUnsafe)' ./internal/ipc ./internal/github` | PASS; IPC 1.548 s, GitHub 1.955 s |
| `go test -race ./internal/ipc ./internal/github ./internal/host` | PASS; 2.285 s / 15.970 s / 2.474 s |
| `go test -race -run '^Test(SaveUpload\|GatewayUpload\|UploadCommit)' ./internal/broker` | PASS; 1.889 s |
| `go vet ./internal/ipc ./internal/github ./internal/host ./internal/broker` | PASS |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/ipc` | PASS; compile only, including final Windows tests |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/ipc` | PASS; compile only |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | PASS; Node 24.11.0, pnpm 10.33.0; 29 pages and all internal links valid |

Retained tests prove actual token loading leaves outside symlink/hard-link
targets at 0644, valid repair keeps the same inode, unsafe directories/FIFOs
remain unchanged, descriptor metadata rejects a synthetic foreign UID, and a
deterministic open/rename/replace interleaving cannot redirect chmod. A link
added after open but before validation is refused without mutation. The foreign
UID test is metadata validation, not privileged chown of another user's file.

Windows fixtures cover final symlink/hard-link refusal with unchanged owner/DACL,
non-file refusal, explicit loose-DACL repair on the same object and a rename
blocked while the no-delete-sharing handle is open. Symlink privilege absence
is an explicit skip; other errors fail. The owner-recovery advice fixture proves
message/cause preservation only. All Windows fixtures were compiled, not run.

Root independent security source review found no code blocker, with native
Windows owner recovery still pending. Feasibility/alternatives review, owner
approval, native Windows/Linux execution, whole integration and phone checks
remain pending. Existing GitHub/security sidebar entries are retained; no new
command, protocol, database or landing-page feature was introduced.

## Windows denied-open advice follow-up (2026-10-09)

Independent feasibility source review found a gap at `ac2e9b5`: the initial
repair open requested WRITE_DAC and could fail before ownership inspection,
losing the existing recovery advice for a safe Administrators-owned object
that still permitted READ_CONTROL. The formatter-only test did not cover it.

The follow-up performs bounded, nonmutating inspection only after the initial
ACCESS_DENIED. A no-reparse, no-delete-sharing handle requests READ_CONTROL and
file attributes; regular/nonreparse/single-link metadata and Administrators
ownership are checked on that handle. It only wraps the original cause with
the existing advice. Failed inspection, unsafe objects or other owners keep
the original error; no ACL mutation or privilege enabling occurs. The same
classification applies to a denied Windows broker-lock open.

The new native fixture uses an isolated file with Administrators ownership and
an OWNER_RIGHTS ACE, explicitly asserts security/attribute inspection succeeds
and repair access is denied, then calls production EnsurePrivateFile. It
requires the advice and original access-denied cause while owner/DACL remain
unchanged. A second state denies inspection and requires the original error
without ownership inference. A pre-held setup handle restores the synthetic
owner/DACL at cleanup. Only controlled setup ACCESS_DENIED, PRIVILEGE_NOT_HELD
or INVALID_OWNER limitations can skip; production assertions never skip.
This fixture is compiled only; no native Windows result is claimed.

| Follow-up command | Observed result |
| --- | --- |
| `go test -race -count=10 -run '^Test(PrivateFile\|TokenFileUnsafe)' ./internal/ipc ./internal/github` | PASS; 1.695 s / 2.137 s on macOS; Windows fixture excluded by platform |
| `go vet ./internal/ipc ./internal/github ./internal/host ./internal/broker` | PASS |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/ipc` | PASS; includes final native fixture, compile only |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/ipc` | PASS; cross-platform static check only |

Frozen docs-site install/build passed again (29 pages, all internal links).
Independent feasibility follow-up source review found no new source blocker;
final pinned confirmation, native Windows/Linux evidence, owner approval and
hosted CI remain pending/blocked as recorded above.

## Final pinned independent review and combined local gate (2026-10-09)

The independent feasibility reviewer confirmed runtime head
`4a09144f25b5e3a4932a74233aa0a73f9b166f90`: source/local-evidence GO,
with the denied-open advice finding resolved. The reviewer independently
passed Windows IPC test cross-compilation, Windows IPC cross-vet and diff
checking on that pinned head. These are compilation/static evidence, not a
native Windows run. Native Windows/Linux runtime and owner approval remain
pending; draft PR #264 stays DO NOT MERGE and CI is explicitly denied.

The coordinator also reported a combined local gate containing the #255, #256,
#260 and #265 branch changes: `go test -race ./...` passed (broker 108.5 s),
Go vet/protocol generation drift/web typecheck passed, 1,080 web tests and the
web build passed, govulncheck found zero vulnerabilities, and macOS/Windows/Linux
amd64/arm64 static builds passed. This is coordinator-reported aggregate
evidence, not a fresh execution on this isolated branch or merged-main
evidence. No runtime changes follow the checked #255 head; the final follow-up
only consolidates the Unreleased Fixed heading and records review/gates.
No issue closure, native execution, hosted CI, owner approval or deployment
is implied by this aggregate result.

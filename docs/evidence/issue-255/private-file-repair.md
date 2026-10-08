# Issue #255: private-file repair evidence

Date: 2026-10-08. Base: `origin/main` at `08f0332`. Toolchain:
`go version go1.27.1 darwin/arm64`.

## Independent baseline reproduction

Temporary Unix-only Go tests called production `loadAccount` and
`ipc.EnsurePrivateFile` with isolated synthetic files. Fixtures contained only
`synthetic non-secret fixture`; no real login, provider or service was used.
Each assertion required refusal without changing the outside file's 0644 mode.
The temporary reproduction test files were removed after execution; this
architecture-first commit contains no runtime or test-code changes.

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

Implementation, retained regression tests, focused race/vet, adjacent-caller
tests, Windows/Linux runtime validation, docs-site build, both independent
adversarial reviews and owner approval are pending. Hosted CI is explicitly
denied. No push, PR creation, approval, merge, deployment or owner-file changes
were performed by this agent.

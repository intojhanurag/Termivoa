# Private-file permission repair

Status: **proposed — DO NOT MERGE without owner review**, 2026-10-08.
This is the implementation design for #255 and
[ADR 0012](decisions/0012-private-file-permission-repair.md). The current main
guard still performs pathname-based permission repair. Architecture-first
draft PR #264 was opened at `3b0bdff` before local implementation began;
the local implementation is not merged or owner-approved. Stored secrets and authentication state make this a critical
change under AGENTS.md rule 24.

## What does the guard guarantee?

For an existing, clean absolute pathname, Termivoa must reject a final link,
non-regular file, disallowed owner or multiply linked object before changing
any permissions. It may repair a permitted, single-linked regular file to the
existing private boundary and verify that boundary using its open object.
It must not change a replacement target that appears under the same pathname.

| Platform | Existing private boundary to preserve | Proposed operation |
| --- | --- | --- |
| macOS/Linux | Effective user's UID; regular file; mode 0600 | No-follow, nonblocking open; file metadata checks; descriptor chmod and verification |
| Windows | Current user's owner and protected current-user-only allow DACL; existing Administrators-owned recovery exception | Open final reparse object; reject reparse/directory/multiple-link attributes; handle owner/DACL operations and verification |

Windows recovery may change an Administrators-owned object's owner to the
current user only under the existing exception. Request the rights needed for
the specific operation; a bounded reopen for extra recovery rights must still
prove the same object and revalidate it before mutation. When a safe object is
known to be Administrators-owned, failure must preserve the existing advice to
repair ownership from an elevated prompt. On an initial repair-open denial, a
separate nonmutating no-reparse handle requests security-read/attribute rights
and checks type, link count and owner solely to select that advice. If this
inspection is denied or cannot prove the safe Administrators-owned object, keep
the original access error without inferring ownership. Other foreign owners
remain refused even if this process could change their permissions.

## Which callers share this boundary?

| Caller | Object | Scope of this change |
| --- | --- | --- |
| `internal/host/broker.go` | Main SQLite file | Preserve original file identity; no copy/replace |
| `internal/github/token.go` | Stored GitHub account | Reject unsafe token pathname without changing its target |
| `internal/ipc/listener.go` | Temporary endpoint descriptor before publication | Use the same guard; preserve atomic publication |
| `internal/ipc/transport_windows.go` | Broker lock file | Preserve the opened lock object's identity and existing lock behavior |
| `internal/broker/upload.go` | New uploaded file | Keep current private-file repair through the shared helper |

Read-only descriptor validation is separate from repair. Its existing owner,
type and permission guarantees must remain; regression tests must demonstrate
that the new repair guard does not weaken descriptor admission.

## How does Unix repair work?

1. Open without final-symlink following, with close-on-exec and nonblocking
   flags. Nonblocking avoids a malicious FIFO hanging before the type check.
2. Read metadata from the descriptor. Require regular type, effective-user
   UID and link count one. No `Chmod(path)` is allowed on this path.
3. Apply mode 0600 using the descriptor and verify its metadata again.
4. Compare no-follow pathname metadata to the descriptor identity. Refuse a
   missing, replaced or newly linked path; release the descriptor on every exit.

A permitted original object can receive the intended 0600 repair even if a
concurrent rename makes the final identity check fail. The replacement itself
must remain unchanged. This is not a transaction that restores the old mode.
Unreadable/unopenable objects are refused rather than using pathname chmod as
a fallback. The guard does not create files or consume their contents.

## How does Windows repair work?

Use `CreateFile` with `OPEN_EXISTING`, `FILE_FLAG_OPEN_REPARSE_POINT` and
appropriate security/attribute rights. Inspect the handle's file information
and security descriptor before mutation. Reject final reparse points and
non-regular objects, disallowed owners and link counts other than one.
Use `GetSecurityInfo`/`SetSecurityInfo` on that handle, with the same one-user
ACE and protected-DACL policy as today. Validate owner and DACL on the handle
after repair. Keep delete sharing disabled during repair where compatible
with current callers; prove pathname identity and do not fall back to
`SetNamedSecurityInfo` for private files.

The existing Windows lock path already opens a file before taking its lock.
Ensure that the object whose ACL is validated is also the one locked, rather
than making a separate pathname success authorize a different open object.
Native Windows tests must cover this integration; a cross-build proves only
compilation. Runtime directory ACL work is outside this design.

## What races and limitations remain?

| Situation | Required result or explicit limit |
| --- | --- |
| Final symlink/reparse point present at open | Refuse before modifying its target |
| Hard link already present | Refuse before modifying the shared object |
| Path replaced after open | Permission operation remains bound to validated original; replacement unchanged; final identity check refuses |
| Wrong owner or non-regular object | Refuse before mutation, with no blocking FIFO read |
| Valid file needs repair | Same identity retained; private permissions verified |
| Same-user process creates another link after validation | No atomic link-count/permission transaction is available; recheck and refuse when detected, without claiming same-user sandboxing |
| Caller closes guard then reopens by pathname | Outside this API's object-lifetime guarantee; later reads are not bound to the guard's handle |
| Directory/path-ancestor replacement | No new general ancestor sandbox; preserve existing containing-directory/canonicalization rules |

No credentials, account content, terminal data or environment are included in
errors or evidence. Tests use isolated synthetic files and no GitHub requests.

## How will verification work?

| Gate | Required evidence | Current state |
| --- | --- | --- |
| Baseline reproduction | Actual `loadAccount` symlink and shared-helper hard-link mode assertions | Reproduced on current main; see evidence |
| Unix regression | Symlink, hard link, directory, FIFO, foreign-owner metadata check, valid repair, descriptor validation, deterministic replacement barrier | Local regressions pass on macOS; native Linux pending |
| Windows regression | Reparse/hard-link refusal, same-handle DACL repair, owner exception/failure, broker lock identity; safe platform/privilege skips | Native tests added and cross-compiled; native execution pending |
| Adjacent callers | IPC/GitHub/host/upload relevant tests, race and vet | Local macOS race/vet pass; see evidence |
| Documentation | Full rules 22/25 docs and frozen docs-site build before implementation review | Updated; frozen install and 29-page build pass |
| Adversarial review | Independent security plus feasibility/alternatives findings and resolutions | Root security source review found no code blocker; feasibility review pending |
| Owner review | Explicit approval before merge | Pending; DO NOT MERGE |
| Hosted CI | Existing required policy | Explicitly denied; no enabling or bypass |

## Review findings and resolutions

| Review | Finding | Resolution |
| --- | --- | --- |
| Baseline analysis | Descriptor repair alone still changes a hard-linked outside name | Require link count one before mutation and recheck afterward |
| Root independent security source review, 2026-10-09 | No code blocker in Unix descriptor repair, Windows same-handle ACL/recovery or original lock-handle integration | Source-reviewed; local tests pass. Native Windows recovery execution is not claimed |
| Root review of Windows test scope | Owner-recovery advice test proves message/cause only, not a real Administrators-owner recovery state | Keep native recovery-state gate explicitly pending; native normal repair/link fixtures added and cross-compiled only |
| Independent feasibility review of `ac2e9b5` | Initial repair open required WRITE_DAC before ownership inspection, so a read-control-accessible Administrators-owned object lost recovery advice when that open was denied | Add denial-only nonmutating, no-reparse handle inspection; preserve original cause and give advice only for the verified safe Administrators-owned object. Native OWNER_RIGHTS fixture explicitly proves denied repair / allowed inspection preconditions; compiled only |
| Independent feasibility follow-up source review, 2026-10-09 | No new source blocker in denial-only inspection, preserved cause, no mutation fallback or fixture setup/production assertions | Native execution remains pending; reviewer will confirm final pinned head |

Windows access-right distinctions and the fixture’s OWNER_RIGHTS SID name were
checked against Microsoft’s [standard access rights](https://learn.microsoft.com/en-us/windows/win32/secauthz/standard-access-rights),
[file security](https://learn.microsoft.com/en-us/windows/win32/fileio/file-security-and-access-rights)
and the [SID strings](https://learn.microsoft.com/en-us/windows/win32/secauthz/sid-strings).
The follow-up does not enable privileges or mutate an inspection handle.

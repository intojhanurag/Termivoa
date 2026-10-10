# How should Termivoa remove its saved GitHub login safely?

Status: merged in #261 (next beta), fixing #256;
[ADR 0013](decisions/0013-github-token-file-unlink.md). Independent security
and feasibility reviews found no blocker; the owner approved it on 2026-10-09.

## What was reproduced?

A synthetic regular token file outside the data directory was hard-linked to
`github.json`. `loadAccount` accepted it; `deleteAccount` zeroed its shared
inode, changing the other filename's 50 bytes. The temporary overlay probe
fails on the unmodified runtime (`08f0332`); [evidence](evidence/issue-256/README.md).
This is a local file-integrity defect, not a claimed remote exploit.

## What is the proposed deletion boundary?

| Step | Behavior |
| --- | --- |
| Caller ownership | Existing broker serialization/private canonical directory; separate #213 offline cleanup holds its exclusive data lock. |
| Local removal | Application code calls `os.Remove(filepath.Join(dataDir, tokenFileName))` once, without token-file inspection/open, content write, truncation or size-based allocation. Go's platform implementation may inspect file attributes while removing the entry. |
| Missing entry | Success, including absence at the removal boundary. |
| Other failure | Return the existing local error; do not silently report successful local removal. |
| Symlink or hard link | Remove only Termivoa's entry; do not follow a final symlink or modify another name's contents. |
| Replaced entry | Remove the current entry without opening or overwriting its inode. Preserve replacement target contents, whether regular, linked or a symlink. |
| Invalid entry | Remove a file, symlink or empty directory; reject a nonempty directory without recursive deletion. |

The helper's scope is the fixed stored-login name, not the whole directory.
Normal Disconnect retains its current revocation request and token-state
serialization. Other callers keep their existing cancellation/error behavior;
this task adds no new context interface or best-effort fallback. A parent
cancellation check in the separate #213 cleanup remains before local removal.
There is no application inspection/open/content-overwrite window because
removal never overwrites token contents. Go's Windows implementation may
inspect attributes and clear a read-only attribute when retrying removal;
this is not a claim that the OS removal implementation changes no metadata.
This is not a guarantee against every replacement race: a same-account process
can recreate the filename after removal, and removal acts on the current entry
rather than proving it is an earlier inspected object. Existing caller
serialization and stable directory ownership remain required. Tests exercise
replacement at the fixed name and preservation of other names, not an invented
guarantee that arbitrary concurrent owner-file manipulation is prevented.
No schema/setting change, migration, new pairing or new credential access.

## What does removal not prove?

Unlinking Termivoa's name is not secure erasure. Copies, other hard links,
filesystem snapshots and backups may retain bytes, and removing local data
does not itself end GitHub access. Remote revocation and manual revocation
remain separate. The private data-directory boundary is unchanged; a malicious
process controlling the same OS account can still manipulate owner files.

## How will this be verified?

| Gate | Required evidence |
| --- | --- |
| Baseline | Failing real hard-link preservation fixture; outside-file contents checked, no network. |
| Normal/missing | Existing local removal and saved account becomes absent; repeated removal succeeds. |
| Hard link | Outside name retains exact bytes after Termivoa's name is gone. |
| Symlink/replaced entry | Target remains unchanged; replacement fixture has no inode overwrite. |
| Invalid entry | Empty entry removal allowed; nonempty directory returns error and retains contents. |
| Caller regression | GitHub connect/disconnect/token tests and race/vet; separate cancellation gate where applicable. |
| Platforms | Cross-platform tests, supported Windows/Linux compilation; native platform evidence explicitly pending if unavailable. |
| Docs | Remove stale zero-overwrite/secure-erasure claims from internal GitHub design/threat model; public GitHub/security guide, Next beta notes, changelog/README; map/roadmap/progress. |
| Approval | Draft first; security and feasibility reviews recorded; explicit owner approval before merge; no CI enablement. |

## Which review findings are resolved?

The coordinator reviewed unlink-only alternatives before the draft opened.
Permanent tests reproduce normal and replaced hard-link corruption before the
fix and pass after it; other-name bytes are compared directly. Normal/missing,
damaged file, empty/nonempty directory, symlink/replaced-symlink and caller error
regressions pass. A first Windows compile caught a task-owned test referencing
Unix-only service fixtures; the caller test now has the service's build tags,
while file-removal tests remain platform-neutral. #213/#255 are not bundled;
no claim that this draft is merged or released.

| Independent review, 2026-10-09 | Finding and resolution |
| --- | --- |
| Coordinator security review, `0da5258` | No blocker: fixed-name removal avoids content writes/size allocation and the application preopen race; only absence is normalized; other-name preservation and caller errors are directly tested. Focused race tests passed, 1.689 s. |
| History agent feasibility/alternatives review, `0da5258` | No blocker: constant application memory, existing missing/error/directory handling and unchanged Disconnect behavior; unlink is simpler and safer than overwrite/link-count/quarantine alternatives. Independent focused tests passed on macOS arm64, 0.664 s, with no skips. |
| Feasibility wording precision | Distinguish application code from Go's platform implementation: Windows removal may inspect/adjust attributes internally without overwriting contents. Qualified above and in ADR 0013. |
| Remaining gates | Owner approved design and implementation on 2026-10-09, subject to required checks. Combined local verification passed in the coordinator checkout, not this isolated branch or main. Native Linux/Windows, real-phone and required hosted checks remain pending; CI is denied. |

## Owner decision (2026-10-09)

Owner approved the design and implementation on 2026-10-09 in this conversation: “Approve all three, subject to required checks”. Required checks remain mandatory; CI stays disabled as explicitly requested. This records the owner’s conversation decision, not a GitHub review submitted on their behalf. Nothing is merged or released.

See [approval evidence](evidence/issue-256/owner-approval.md).

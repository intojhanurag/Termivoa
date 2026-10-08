# How should Termivoa remove its saved GitHub login safely?

Proposed correction for #256, [ADR 0013](decisions/0013-github-token-file-unlink.md).
**DO NOT MERGE without owner review.** This is the design-only checkpoint;
runtime implementation, final independent reviews and owner approval are pending.

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
| Local removal | Call `os.Remove(filepath.Join(dataDir, tokenFileName))` once. No token-file open, metadata inspection, byte write, truncation or size-based allocation. |
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
There is no metadata/write window because removal never writes any inode.
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

None yet. ADR 0013 records the alternatives; independent review and owner
approval remain pending. The design-only checkpoint does not claim the defect
fixed or any implementation gate passed. #213 uninstall cleanup and #255
private-file validation are separate tasks and will not be bundled here.

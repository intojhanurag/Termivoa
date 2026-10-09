# ADR 0013: remove the saved GitHub login without overwriting an inode

- Status: accepted by the owner on 2026-10-09, subject to required checks; not merged.
- Date: 2026-10-08
- Issue: #256
- Design: [github-token-removal.md](../github-token-removal.md)

## What problem does this solve?

`deleteAccount` overwrites a regular `github.json` before removing its name.
`O_NOFOLLOW` protects against a final symlink, but a hard link is also a regular
file. Its inode can be shared with an unrelated name, so the overwrite changes
that other file. A replacement between inspection and opening has the same
problem. A synthetic 50-byte fixture reproduces the hard-link corruption on
`08f0332`; no real account or remote request was used.

## What decision is proposed?

Remove only the stored-login directory entry with Go's `os.Remove`. Treat
absence as success; propagate other errors through the existing caller's
handling. Application code does not inspect/open/write/sync the token inode
or allocate a buffer from its size. Go's removal implementation may inspect
attributes; on Windows it may clear a read-only attribute when retrying.
Neither operation overwrites token contents. A symlink is unlinked, a hard
link loses only Termivoa's name,
and a replaced entry's content is never overwritten. A nonempty directory
remains an error; this is not recursive cleanup.

The broker's private canonical data directory and serialization remain the
ownership boundary. This change does not promise protection against a process
that already controls the same OS account replacing the parent directory.
The separate #213 offline cleanup must retain its exclusive data ownership.

Keep Disconnect's remote-revocation behavior and existing callers unchanged.
Deleting a local filename does not securely erase filesystem blocks, backups,
hard-linked names, copied credentials or a remote login. Documentation must
say removal, not erasure. No network endpoint, token scope, schema, migration
or protocol change. #255 token-file permission handling remains separate.

## Which alternatives were considered?

| Alternative | Decision |
| --- | --- |
| Unlink the name without writing | Proposed: smallest cross-platform operation; preserves other names/content and removes the inspection/open/write race. |
| Reject link counts above one, then overwrite | Rejected: metadata/open races remain, platform-specific checks add complexity, and COW storage/backups still prevent a secure-erasure guarantee. |
| Open no-follow, verify inode/link count, overwrite | Rejected: a new hard link can be created after verification; writing has no required benefit and still alters shared storage. |
| Rename to quarantine, inspect/overwrite there | Rejected: rename does not isolate a shared inode; more names/state without removing the risk. |
| Add encrypted token storage or an OS vault | Separate architecture/secret-management project; unnecessary to stop this directory-entry deletion from corrupting another file. |

## What review is required?

Independent security review must verify linked/replaced-file preservation,
ownership and honest deletion claims. Independent feasibility review must
check Go's supported OS behavior, error/absence handling and alternatives.
Record findings in the design and obtain explicit owner approval before
merging either design or implementation (AGENTS.md rule 24). CI remains denied;
do not enable it or claim hosted checks passed.

On 2026-10-09, independent coordinator security and history-agent feasibility
reviews of `0da5258` found no blocker, with focused race/regression tests
passing. The feasibility review confirmed Go's Unix unlink/rmdir and Windows
DeleteFile/RemoveDirectory implementation and qualified application-versus-OS
metadata claims above. See the design/evidence for commands and limits.
Owner approved design and implementation on 2026-10-09, subject to required
checks. Native Linux/Windows execution and required checks remain pending;
this ADR is accepted, but neither design nor implementation is merged.

## Owner decision (2026-10-09)

Owner approved the design and implementation on 2026-10-09 in this conversation: “Approve all three, subject to required checks”. Required checks remain mandatory; CI stays disabled as explicitly requested. This records the owner’s conversation decision, not a GitHub review submitted on their behalf. Nothing is merged or released.

See [approval evidence](../evidence/issue-256/owner-approval.md).

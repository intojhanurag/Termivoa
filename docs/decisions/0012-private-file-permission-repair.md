# ADR 0012: Repair private-file permissions through the validated open object

- Status: **proposed — DO NOT MERGE without owner review**. Architecture-first
  draft for #255 under AGENTS.md rule 24; neither implementation nor approval
  is implied by this document.
- Date: 2026-10-08
- Design: [Private-file permission repair](../private-file-permissions.md)

## Context

The shared IPC private-file guard protects the database, GitHub token file,
endpoint descriptors, uploaded files and Windows broker lock files. On Unix,
it changes permissions by pathname before rejecting symlinks, wrong types or
wrong owners. On Windows, its pathname ACL operation precedes the regular-file
check. Rejection can therefore already have changed another file. Moving
`Lstat` ahead of the pathname operation leaves a check/use replacement race.
Hard links share an inode and require their own check even with a descriptor.

Synthetic tests on current main reproduce both a rejected GitHub symlink
changing its outside target from 0644 to 0600 and an accepted hard link doing
the same. Evidence: [#255 baseline](../evidence/issue-255/private-file-repair.md).

## Decision proposed for review

1. Keep one shared Go guard and its current clean-absolute-path API. Do not
   expand authentication, protocol, network access or caller privileges.
2. Open the existing final object without following a final symlink/reparse
   point. Before any mutation, verify regular-file type, permitted owner and
   exactly one hard link using that open object.
3. Repair and verify permissions on that same open object: `File.Chmod(0600)`
   on Unix; handle security APIs with a protected current-user-only DACL on
   Windows. Preserve the existing Windows Administrators-owned recovery
   exception and its actionable failure message; do not accept other owners.
4. Verify pathname identity against the opened object before reporting
   success. Never retry a pathname mutation after a replacement or failed
   validation. Bound handles, calls and time; do not read file contents.
5. Reject multiply linked private files before repair. Existing legitimate
   owned, single-linked regular files remain repairable. No database migration,
   fallback or new pairing is needed for normal files. Intentionally linked
   private files are refused and require owner inspection.

## Alternatives and consequences

| Alternative | Reason not selected |
| --- | --- |
| `Lstat`, then pathname `Chmod`/ACL change | Replacement can redirect the mutation after validation |
| Descriptor repair without hard-link validation | A second name still receives the same permission change |
| Refuse every file whose permissions need repair | Loses the existing repair behavior for legitimate owned files |
| Copy or replace the object | Can split database/lock identity, loses metadata and expands the task |
| General filesystem sandbox or caller API rewrite | A separate design; not needed to stop this permission mutation |

The guard binds its own mutation to one object, not all later pathname reads
by callers. A same-user hostile process can change paths or create links after
a check; it already has access to this user's files. Do not claim a sandbox
against that process or protection for a future caller reopen. Directory
permission repair is outside #255. Report other defects separately.

## Approval and verification gates

Independent security and feasibility/alternatives reviews must challenge
Windows owner recovery, handle sharing, hard-link behavior, replacement races
and caller assumptions. Record each finding and its resolution in the design.
Owner approval is required before merging either design or implementation.
Hosted CI is explicitly denied; it must not be enabled, bypassed or treated as
passed. Native Windows/Linux evidence remains pending until actually run.

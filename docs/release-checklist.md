# Release and private-to-public checklist

This checklist prepares a reviewable v0.1.0 candidate. It does not authorize publication, a GitHub visibility change, signing with owner identities, or distribution. The repository owner performs the private-to-public visibility change manually.

## Candidate identity

- [ ] Source commit SHA is recorded and all intended changes are committed.
- [ ] Candidate uses a documented prerelease identifier while private.
- [ ] Exact reproducible build commands and build environments are recorded.
- [ ] macOS and Windows artifacts, checksums, and SBOMs identify the same source commit.
- [ ] Unsigned/test and signed/release artifacts are unambiguously labeled.
- [ ] Proposed public version is `v0.1.0`, not a claim of 1.0 stability.

## Technical acceptance

- [ ] P0–P7 exit criteria in the authoritative implementation plan are complete.
- [ ] Go runtime works on tested Apple Silicon macOS and Windows 11 x64 versions.
- [ ] Native zsh, Windows PowerShell, and available PowerShell 7 evidence is recorded; WSL is separate.
- [ ] Physical iPhone Safari/Home Screen and Android Chrome checklists are complete, with executor and exact versions.
- [ ] Private cellular access through approved Tailscale HTTPS is demonstrated without Funnel.
- [ ] Multiple sessions, desktop handoff, reconnect, normalized terminal display, structured input, bounded history, pairing, and revocation meet their fixtures.
- [ ] Gateway restart preserves child identity; broker restart reports sessions lost without replay.
- [ ] Terminal-query, Unicode width, alternate-buffer, flood/stalled-viewer, controller lease, input sequence, and revocation fixtures pass.
- [ ] Required security, rate-limit, malformed-input, Origin/Host/CSRF, and token-leak tests pass.
- [ ] Performance, stress, and 24-hour soak evidence names hardware, software, dimensions, rates, network conditions, and exact build.
- [ ] Known defects are fixed or explicitly block release; no requirement was silently removed.

## Installation, signing, and update

- [ ] Fresh macOS installation works without Go, Node.js, or compiler tooling.
- [ ] macOS `.app` and DMG are signed with the approved Developer ID and notarized/stapled; identities are referenced by name only.
- [ ] Fresh per-user Windows installation works without Go, Node.js, or compiler tooling.
- [ ] Windows executable and Inno Setup installer are signed with the owner-approved trusted identity.
- [ ] No root daemon, SYSTEM service, Defender/SmartScreen bypass, or disabled certificate validation is introduced.
- [ ] Startup registration changes are described before consent and tested per user.
- [ ] Active sessions block broker replacement/restart during manual update.
- [ ] Previous signed version and tested database backup/rollback path are available.
- [ ] Clean uninstall removes registered startup/install files and offers data deletion separately.

## Documentation and legal review

- [ ] README support matrix contains only actually tested combinations and accurately describes limitations.
- [ ] Setup, doctor, install, update, rollback, uninstall, and host-awake/broker-lifetime behavior are documented.
- [ ] CHANGELOG has accurate release date and user-visible changes.
- [ ] Apache-2.0 license is present for original code.
- [ ] Exact direct/transitive dependency inventory, upstream licenses, required notices, and distribution copies are reviewed.
- [ ] SBOM and checksums are included with artifacts.
- [x] Owner-approved Code of Conduct enforcement contact is present (aojharaj2004@gmail.com).
- [x] Owner-approved private vulnerability-reporting route is present (`SECURITY.md`, aojharaj2004@gmail.com).
- [x] Final product name: **Termivoa** (owner decision, 2026-09-27; repository `intojhanurag/Termivoa`). Package identifiers (`dev.phone-terminal.*`), data folders, startup entries and the Go module path `github.com/intojhanurag/phone-terminal` keep the old working name for compatibility (`docs/naming.md`).
- [ ] Maintainer/publisher identity for signing and screenshots from synthetic data are approved.
- [ ] Contribution workflow and contribution licensing are accurate; no fabricated sign-offs or CLA service exists.

## Secret and privacy review

- [ ] Current tracked files are reviewed for credentials, tokens, signing material, private identifiers, employer/customer code, personal paths, terminal transcripts, and private test data.
- [ ] Full reachable Git history, including all branches and tags, receives the same review.
- [ ] PRs, issues, discussions, release drafts, screenshots, Actions logs, caches, and artifacts are reviewed because they may become visible.
- [ ] Gitleaks scans staged content and repository history at the pinned version; findings are resolved or documented with owner-visible expiry.
- [ ] Any exposed real credential is revoked/rotated before cleanup; deleting a file alone is not treated as remediation.
- [ ] Any destructive history/log/artifact cleanup has explicit owner authorization and has been verified before publication.
- [ ] No source or artifact was uploaded to a public registry, gist, preview site, analysis service, or container registry during private development.

## GitHub and workflow review

- [ ] Effective `main` protection/ruleset requires pull requests, actual stable CI check names, resolved conversations, and protection from force pushes/deletion.
- [ ] Account-plan limitations are documented rather than bypassed by making the repository public.
- [ ] Workflow permissions default to read-only contents and jobs use minimum additional permissions.
- [ ] External actions are pinned to immutable commit SHAs.
- [ ] Untrusted fork code cannot execute through `pull_request_target` with secrets.
- [ ] Normal PR jobs have no signing, provider, deployment, or personal-machine credentials.
- [ ] Dependabot covers Go modules, npm/pnpm, and Actions without unattended automatic merge.
- [ ] External apps, bots, webhooks, deploy hooks, and inherited settings are reviewed for public behavior.

## Owner handoff before visibility change

- [ ] Present the private repository name and verify it remains private.
- [ ] Present candidate source commit, proposed tag, exact artifacts/checksums, and reproducible build instructions.
- [ ] Present tested platform/device matrix, known limitations, unresolved blockers, and credential locations by name only.
- [ ] State clearly whether evidence supports readiness for the owner's manual visibility change.
- [ ] Owner explicitly confirms final review; the agent does **not** run or offer to run a visibility-changing command.
- [ ] Installer/package/release publication remains unpublished unless separately authorized.

## After the owner reports a manual public transition

- [ ] Verify public visibility with a read-only check.
- [ ] Reinspect effective rulesets/branch protection and workflow permissions after the transition.
- [ ] Verify which repository material and Actions history became visible.
- [ ] Perform only separately authorized release/package/installer publication actions.
- [ ] Verify each published artifact, checksum, signature, and source link.
- [ ] Report exactly what is public and what remains unpublished.

Making the repository private again cannot retract copies or forks. Keep the private-development history unless the owner approves a specific cleanup.

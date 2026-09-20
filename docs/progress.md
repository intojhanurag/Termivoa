# Implementation progress

Last updated: 2026-09-20

## Current status

P0 — Private repository, preflight, and scaffold: **in progress**.

No application implementation, benchmark, platform integration, physical-phone test, installer, signing operation, or release has been completed. Later milestones remain pending.

## Verified preflight facts

| Item | Evidence observed on 2026-09-20 | Status / milestone impact |
| --- | --- | --- |
| Workspace | Isolated `phone-terminal` checkout; new Git repository on `main`, with no commit at inspection time | Approved workspace is available for P0. The machine-specific absolute path is intentionally not recorded. |
| GitHub identity | Authenticated `gh` session reports account `intojhanurag` | Intended owner identity verified without printing credentials. |
| GitHub destination | GitHub API reports `intojhanurag/phone-terminal`, visibility `PRIVATE`, and no default branch before the first push | Private-first target verified. Visibility must remain private. |
| Git author | User name and email are configured locally; values were not copied into this document | Bootstrap commits can preserve the configured identity. Commit-signing availability still needs verification before commit. |
| Development host | Apple Silicon (`arm64`) macOS 26.5.2 | Local P0 work and later macOS testing are possible; a complete P1 claim still requires native test evidence. |
| Go toolchain | System Go is 1.21.13; Go 1.27.1 was fetched and verified through `GOTOOLCHAIN=go1.27.1` without replacing the system installation | Locked P0/P1 toolchain is available per repository command; global Go remains unchanged. |
| Web toolchain | Default PATH has Node.js 25.8.1; nvm already contains Node.js 24.11.0, selected with pnpm 10.33.0 for repository checks | Locked P0 web toolchain is available without changing the global default. |
| Tailscale | CLI 1.102.4 is installed; backend reported `Stopped` | No configuration was changed. P4 production phone access requires owner authorization/login and conflict-safe Serve setup. |

Commands used for this read-only preflight included `gh api user`, `gh repo view`, `git config --get`, `uname -m`, `sw_vers`, tool version commands, and a minimal `tailscale status` query. No token, raw credential, private hostname, or Tailscale Serve configuration was recorded here.

## P0 deliverables

- [x] Verify authenticated GitHub owner and exact destination.
- [x] Verify the destination is private before first push.
- [x] Create truthful open-source preparation and repository-policy documents.
- [x] Record architecture and fixed decisions without claiming implementation.
- [x] Add the authoritative plan at `plan.md` and `docs/implementation-plan.md`.
- [x] Add Apache-2.0 `LICENSE` text.
- [x] Add editor, ignore, and attributes policy files.
- [x] Resolve and pin exact Go/web/tool dependencies and commit lockfiles.
- [x] Add repository-owned Unix and Windows check tasks.
- [x] Add pinned GitHub Actions workflows with required stable check names.
- [x] Run staged-file and secret review.
- [x] Create and push the private bootstrap commit; SHA `fbaeaeb382662a83f9498715896e722acf742694`.
- [x] Verify supported branch/ruleset controls and configure required PR/check/conversation/force-push/deletion rules.
- [ ] Record final bootstrap CI results after the release-smoke repair is merged.

P1 must not begin until the P0 exit criterion is met: private repository verified, bootstrap commit pushed, permissions/CI recorded, and access blockers identified.

## Owner-dependent and external blockers

These questions must be resolved before their dependent gates; they do not block independent documentation work.

| Need | Current state | Blocked gate |
| --- | --- | --- |
| Required toolchains | Go 1.27.1 is available through Go toolchain selection; Node 24.11.0 is available through nvm | No current P0 toolchain blocker; developers must explicitly select Node 24. |
| Windows 11 x64 access | Not established; physical machine versus VM is unknown | Native P1 lifecycle and P6 installer validation |
| Physical iPhone and Android access | Test arrangement not established | P4 private cellular connection and P5 physical-device acceptance |
| Tailscale login/configuration | Installed but stopped; no authorization to log in or alter Serve configuration | P4 private HTTPS evidence |
| Host installation consent | Not requested; no startup/PATH/install changes made | P6 installation and startup evidence |
| Coding CLI and disposable test repository | Not selected or authorized | Real-application smoke test in P7 |
| Apple signing/notarization | Membership, identities, and approved credential location not established | Signed macOS P6/P8 artifacts |
| Windows code signing | Owner-approved trusted identity and key access not established | Signed Windows P6/P8 artifacts |
| Public identity | Final product name, package identifiers, publisher identity, and publication permission not confirmed | Public-release P8 handoff |
| Public contacts | Owner-approved Code of Conduct and vulnerability-reporting routes not provided | Public-readiness documents |
| Branch enforcement | Supported and configured on private `main`: required PR, strict named checks, resolved conversations, linear history, admin enforcement, no force push, no deletion | No current blocker. Squash is the only enabled merge method. |

## Milestones

| Milestone | Status | Evidence |
| --- | --- | --- |
| P0 — Private repository, preflight and scaffold | In progress | This document; bootstrap commit/CI evidence pending |
| P1 — Go terminal core | Not started | None |
| P2 — Frame and input protocol | Not started | None |
| P3 — Persistent broker | Not started | None |
| P4 — Authentication and private access | Not started | None |
| P5 — Phone experience | Not started | None |
| P6 — Install and update | Not started | None |
| P7 — Reliability and review | Not started | None |
| P8 — Release preparation | Not started | None |

## Repository and CI evidence

The repository API reports private visibility, full owner admin/push/pull permissions,
`main` as the default branch, and squash-only merging. Branch protection requires
the nine stable CI contexts defined in `.github/workflows/ci.yml`.

The first bootstrap CI run (`35508287671`) passed protocol generation, Go quality,
Go race, web quality, macOS/Windows scaffold integration, secret scanning, and
vulnerability scanning. Both release-smoke matrix entries failed because the
unanchored `pterm` ignore pattern excluded `cmd/pterm/main.go` from the bootstrap
commit. Branch `fix/p0-bootstrap-ci` anchors the binary ignore rules at repository
root and adds the previously excluded scaffold command. P0 remains in progress
until its PR CI passes and the repair is merged.

## Known defects

The initial release-smoke failure above is under repair. No functional application
exists, so runtime defects have not otherwise been evaluated. Missing implementation
and unexecuted gates are not counted as passing behavior.

# Implementation progress

Last updated: 2026-09-20

## Current status

P0 — Private repository, preflight, and scaffold: **complete**.

P1 — Go terminal core: **in progress**, tracked by private issue #7. The owner
approved the documented ConPTY query-response and macOS process-group
boundaries on 2026-09-20; native Windows 11 validation remains pending.

P2 — Frame and input protocol: **implementation started** on
`feat/p2-frame-protocol` at the owner's direction while the owner-dependent
Windows 11 P1 evidence remains pending. The canonical protocol, normalized
frame/patch model, browser renderer/input foundation, and minimal desktop
client are implemented as a reviewable checkpoint. P1 is not represented as
complete, and P2 remains in progress until its live connection acceptance
tests are integrated.

The P1 terminal-core checkpoint is implemented and merged. No benchmark,
physical-phone test, installer, signing operation, or release has been
completed. Unexecuted later-milestone gates remain pending.

## Verified preflight facts

| Item | Evidence observed on 2026-09-20 | Status / milestone impact |
| --- | --- | --- |
| Workspace | Isolated `phone-terminal` checkout; new Git repository on `main`, with no commit at inspection time | Approved workspace is available for P0. The machine-specific absolute path is intentionally not recorded. |
| GitHub identity | Authenticated `gh` session reports account `intojhanurag` | Intended owner identity verified without printing credentials. |
| GitHub destination | GitHub API reports `intojhanurag/phone-terminal`, visibility `PRIVATE`, and no default branch before the first push | Private-first target verified. Visibility must remain private. |
| Git author | User name and email are configured locally; values were not copied into this document. No commit-signing requirement is configured. | Bootstrap commits used the configured identity; no signing setting was disabled. |
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
- [x] Record final bootstrap CI results after the release-smoke repair is merged.

The P0 exit criterion is met: the private repository, bootstrap push, repository
controls, final CI run, and access blockers are recorded above.

## Owner-dependent and external blockers

These questions must be resolved before their dependent gates; they do not block independent documentation work.

| Need | Current state | Blocked gate |
| --- | --- | --- |
| Required toolchains | Go 1.27.1 is available through Go toolchain selection; Node 24.11.0 is available through nvm | No current P0 toolchain blocker; developers must explicitly select Node 24. |
| Windows 11 x64 access | Owner has a physical Windows laptop but cannot make it available during the current implementation session | Native P1 lifecycle and P6 installer validation remain pending; native GitHub Windows CI continues as supplemental evidence |
| Physical iPhone and Android access | Test arrangement not established | P4 private cellular connection and P5 physical-device acceptance |
| Tailscale login/configuration | Installed but stopped; no authorization to log in or alter Serve configuration | P4 private HTTPS evidence |
| Host installation consent | Not requested; no startup/PATH/install changes made | P6 installation and startup evidence |
| Coding CLI and disposable test repository | Not selected or authorized | Real-application smoke test in P7 |
| Apple signing/notarization | Membership, identities, and approved credential location not established | Signed macOS P6/P8 artifacts |
| Windows code signing | Owner-approved trusted identity and key access not established | Signed Windows P6/P8 artifacts |
| Public identity | Final product name, package identifiers, publisher identity, and publication permission not confirmed | Public-release P8 handoff |
| Public contacts | Owner-approved Code of Conduct and vulnerability-reporting routes not provided | Public-readiness documents |
| Branch enforcement | Supported and configured on private `main`: required PR, strict named checks, resolved conversations, linear history, admin enforcement, no force push, no deletion | No current blocker. Squash is the only enabled merge method. |
| Existing GitHub App | `Greptile Review` runs automatically on private PRs; the agent did not install it | Owner confirmed on 2026-09-20 that Greptile is intentionally authorized to review this project. It is not a required branch-protection context and its confidence threshold is configured not to block merging. |

## Milestones

| Milestone | Status | Evidence |
| --- | --- | --- |
| P0 — Private repository, preflight and scaffold | Complete | PR #5 squash-merged as `6ece2af8ea46fbf5bc7221dfba1b677ca7ea02e8`; final-head CI run `35509425646` passed all ten required checks |
| P1 — Go terminal core | In progress | PR #8 merged as `0342a0058e088b4a4970eb23ec2e150aac6f0232`; architecture boundaries approved; native Windows 11 execution remains pending |
| P2 — Frame and input protocol | In progress | Private issue #9 and branch `feat/p2-frame-protocol`; checkpoint evidence in `docs/evidence/p2/frame-protocol-validation.md`; live broker/gateway connection and physical-device evidence remain pending |
| P3 — Persistent broker | Not started | None |
| P4 — Authentication and private access | Not started | None |
| P5 — Phone experience | Not started | None |
| P6 — Install and update | Not started | None |
| P7 — Reliability and review | Not started | None |
| P8 — Release preparation | Not started | None |

## Repository and CI evidence

The repository API reports private visibility, full owner admin/push/pull permissions,
`main` as the default branch, and squash-only merging. Branch protection requires
the ten stable CI contexts defined in `.github/workflows/ci.yml`.

The first bootstrap CI run (`35508287671`) passed protocol generation, Go quality,
Go race, web quality, macOS/Windows scaffold integration, secret scanning, and
vulnerability scanning. Both release-smoke matrix entries failed because the
unanchored `pterm` ignore pattern excluded `cmd/pterm/main.go` from the bootstrap
commit. Branch `fix/p0-bootstrap-ci` anchors the binary ignore rules at repository
root and adds the previously excluded scaffold command.

At repair head `b6cffa1c42d12be48229acc78c6b8900dc6714f5`, all ten
repository-owned CI checks passed in run `35508613947` (runner/check naming was
also pinned and corrected). An independent self-review found no missing Section
19 files, direct collaborators, webhooks, releases, tags, or deployments. P0 is
tracked in private issue #6 and linked from PR #5. The review also identified
the pre-existing `greptile-apps` check on PR #5. On 2026-09-20, the owner
confirmed that Greptile is intentionally authorized for this project. GitHub
branch protection requires only the ten repository-owned contexts, so Greptile
does not gate merging. Its concrete review finding was fixed before resolution.

PR #5 was squash-merged on 2026-09-20 as
`6ece2af8ea46fbf5bc7221dfba1b677ca7ea02e8`, automatically closing issue #6.
Final-head Actions run `35509425646` passed all ten required checks. The
repository remained private after merge, and `main` retained its protection.

## Known defects

The initial release-smoke failure above was fixed and merged. P1 found two
defects in pinned dependencies and carries narrow reviewed source patches:
arbitrary `vt.Write` boundaries split grapheme clusters and xpty leaked the
original Windows process handle after reopening it for `exec.Cmd`. The patch
record and removal criteria are in `docs/dependency-patches.md`.

Native Windows 11 lifecycle execution is still blocked on machine/VM access;
cross-compilation is not a platform pass. Missing implementation and unexecuted
gates are not counted as passing behavior. Native Windows Server 2025 CI also
demonstrated that ConPTY consumes child DSR/CPR queries before they reach the Go
emulator and supplies the response itself. On 2026-09-20 the owner approved
treating broker-owned ConPTY as the Windows platform responder for queries it
consumes, while retaining the exactly-one-response rule and prohibiting viewer
responses. The owner also approved the macOS PTY process group as the managed
termination boundary; a descendant that deliberately creates a new session is
outside that boundary. These boundaries are resolved design decisions, not
claims that native Windows 11 validation has passed.

P2 exposed a width mismatch in the unmodified pinned
`@xterm/addon-unicode-graphemes` provider for required Extended Pictographic
ZWJ fixtures. The implemented server-cell adapter retains that provider,
repairs its GB11 classification, and applies each validated canonical cell's
explicit width while painting. Automated xterm-buffer fixtures pass; physical
Safari and Chrome font/layout agreement is still pending and is not inferred
from these tests.

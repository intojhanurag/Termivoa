# Implementation progress

Last updated: 2026-09-27

## Current status

### Summary (2026-09-27)

The v0 flow is complete on `main` and verified on the macOS development
profile (Apple Silicon, macOS 26.5.2): desktop attach, phone pairing and
revocation, the authenticated gateway, the phone web app (UI v2), the 3-step
`pterm setup` first run, per-user startup registration and unsigned
packaging. It landed as #27 (v0, containing #23–#26), #28 (first run and
daily use) and #30 (phone UI v2). All three were merged without hosted CI,
because GitHub Actions jobs do not start (account billing). The full local
`./scripts/check` and `go test -race ./internal/... ./cmd/...` passed on the
merged heads. The GitHub repository is now `intojhanurag/Termivoa`. The owner
confirmed the product name **Termivoa** (2026-09-27); user-facing text uses it,
and data folders, startup entries and other stored names keep "Phone Terminal"
for compatibility (`docs/naming.md`).

Not yet verified (owner checklist in
[owner-verification.md](owner-verification.md)):

- Hosted CI on `main`, including native Windows tests and the packaging
  workflow.
- Physical iPhone Safari and Android Chrome, and private Tailscale Serve HTTPS
  over cellular.
- Native Windows 11: broker, ConPTY, desktop attach, installer, Scheduled
  Tasks.
- Startup after a real macOS logout/login, and the unsigned DMG.
- Signing and notarization (needs owner credentials).
- Windows soak, 24-hour soak, 10-minute stress run and the key-to-frame
  latency benchmark.
- Owner decisions: the Unix termination boundary, the final public name and
  identifiers.

The dated entries below are the history log; older entries describe the state
on their own date.

### Linux host (2026-09-27, branch `feat/linux-host`)

Linux (Ubuntu 22.04/24.04, x86_64 and arm64, including servers and WSL) is a
supported host: `linux` shell-profile platform (store migration 7), XDG paths,
systemd user units with a separate linger question, a no-systemd fallback,
WSL notes, `/proc`-based preview ports, Tailscale operator detection,
`scripts/install-linux.sh` and `packaging/linux/build.sh`. Docs:
`docs/install.md#linux-desktop-server-and-wsl`.

Evidence (commands run by the agent on the Mac and in Docker 29.2.1, arm64):

- `gofmt`, `go vet ./...` for darwin, `CGO_ENABLED=0 GOOS=linux` amd64 and
  arm64, and `GOOS=windows`; `go test ./...` on macOS; `./scripts/check`.
- `go test ./...` as uid 1000 in `golang:1.27`: all packages pass. Before the
  two fixes below, `internal/broker` (upload slot race) and
  `internal/gateway` (Linux send buffer) failed there on `main` too.
- Privileged `ubuntu:24.04` container with systemd as PID 1: setup, doctor,
  a bash session, stop/launch, broker crash restart, shutdown, user-manager
  restart with linger, uninstall with data deletion (details in
  `owner-verification.md` section 3a).

Pending owner: real VPS `t3-vps` and an Ubuntu desktop, x86_64, polkit linger,
Tailscale operator and Serve, a phone over Tailscale, zsh and fish.

### Phone UI v2 (2026-09-27)

The owner approved the Paper design "termivoa" (session tabs, agent keys,
keycap dock, snippets, launch presets with run first, redesigned Sessions,
Pair, Composer, History and Settings). It is implemented in `web/**`, with
two optional broker session-list fields (`lastOutputAt`, `preview`) for the
new-output dot and the list preview. An adversarial agent review fixed four
defects (overlapping polls, pairing code not read by screen readers, duplicate
snippet ids, hidden SGR-8 text in previews). The full local check (155 web
tests) and Go race tests pass; a real dev-stack browser run at 390×844 passed
pairing, run first, agent keys (a real `⌃C` interrupt), snippets, tab switching
with the new-output dot, and the session list preview. Real-phone checks are
listed in `docs/owner-verification.md`.

### First-run polish merge without CI (2026-09-27)

The owner asked to merge PR #28 (3-step setup, origin fix, bandwidth pacing
and compression, terminal/environment/desktop fixes, web polish) the same way:
the Actions minutes were still used up, and every CI job was blocked before it
started. "Include administrators" was turned off only for the squash merge and
restored right after. The full local `./scripts/check` (117 web tests) and
`go test -race ./internal/... ./cmd/...` passed on the merged head.

### v0 merge without CI (2026-09-27)

The owner asked to merge the v0 stack without hosted CI: the account's 3,000
included GitHub Actions minutes for September were used up, and they reset in
early October. PR #27 contains all of #23–#26 and was squash-merged alone into
`main` with administrator bypass; "include administrators" was turned off only
for that merge and turned back on right after. #23–#26 were closed as included
in #27. Before the merge, an adversarial review of all five PRs found 25 real
defects; all were fixed with failing-first tests except desktop mouse
forwarding, which is documented as out of scope for v0. The full local
`./scripts/check` and `go test -race ./internal/... ./cmd/...` passed on the
merged head (macOS arm64). Native Windows tests, the Linux `/proc` path and the
packaging workflow have not run in CI yet: run CI on `main` when minutes reset
and fix any failure in a follow-up PR.

### v0 stack (2026-09-27)

The P3–P6 work is pushed as stacked private PRs: #23 (P3 desktop attach and
live client) → #24 (P4 pairing, broker auth and gateway) → #25 (P5 phone
website) → #26 (P6 unsigned packaging and lifecycle commands) → the v0 polish
PR. (Superseded the same day: #27 replaced #23–#26 and was merged; see
"v0 merge without CI" above.) None is merged yet: GitHub Actions did not start any job because of the
account billing/spending limit, and branch protection requires the ten CI
checks. Full local `./scripts/check` passed at the head of each PR. The
configured Greptile review did not respond on these PRs; same-account agent
review passes are recorded in each evidence file instead.

A real end-to-end run on this Mac (`pterm up --dev`, desktop Chromium at
390×844) passed: pair → approve → session list → new zsh session → Take
control → typed command → real shell output; `pterm attach` observed the same
session read-only and detached with Ctrl+]; a broker restart marked old
sessions `lost` and the browser credential stayed valid. Everything that needs
a real phone, native Windows, Tailscale HTTPS, a real login, or signing
credentials is listed in `docs/owner-verification.md`.

### Earlier local integration checkpoint (2026-09-21)

The reviewed P3 process, management CLI, live IPC, and desktop-client components
are combined on `feat/p3-desktop-integration`. Main remains at the privately merged
PR #22 checkpoint `9d212651efe331f35fdbb66038905e705125072f`; local integration is
not described as merged. GitHub API DNS lookup is currently timing out, so remote
visibility/review cannot be refreshed and these checkpoints have not been pushed.
No authentication, account, or network settings were changed to work around it.

Implemented locally:

- Independent broker/gateway process modes and broker-owned session persistence.
- Real `list` and explicitly confirmed `terminate` commands over restricted IPC.
- Canonical observer/control/input/snapshot/history live IPC and a client tested
  against a real Mac broker and PTY, including same-child reconnect.
- Bounded desktop input decoding, cancellable owned terminal I/O, join-before-TTY
  restoration, and reversible Windows console setup (native execution pending).
- Exact session selection and public `new`/`attach` commands, including one-shot
  creation, read-only attachment, explicit control, detach and TTY restoration.
- Actual executable attachment in disposable Mac outer PTYs, plus isolated real
  zsh, less and Vim compatibility fixtures. No owner shell configuration is used.

The full `./scripts/check` at integrated `d227802` passed, including 47 web tests.
The full Go race suite passed at `2c1bf3c`; later command checkpoints have focused
race evidence, not a claim that this older full run covers them. The real outer-PTY
executable test additionally passed twice at `df6d8d2` (16.144s). A concurrent
earlier broad race run failed a real-session retirement test and a viewer
resnapshot test. The viewer cancellation/epoch races were reproduced and fixed;
the retirement timeout remains unclassified despite passing focused reruns.
A separate earlier
executable-broker readiness timeout remains unclassified, documented in
`docs/evidence/p3/process-fixture-readiness-investigation.md`. These failures are
not erased by green reruns. Native Windows 11, real-phone, authentication, signed
installer, performance/soak, and release gates remain pending.

Desktop startup input is now admitted only for the current connected, ready
controller epoch, and an early Ctrl+] at the startup handoff detaches instead of
being flushed (2026-09-27, `docs/evidence/p3/desktop-input-admission.md`).
Native shrink-then-grow resizing is covered by the outer-PTY executable test
(`docs/evidence/p3/desktop-native-resize.md`). The active-output termination
drain fix is integrated (`docs/evidence/p3/active-output-shutdown.md`).
Native Windows execution of these paths remains pending.

### Milestone record

The finite-output probe reproduced a broker shutdown wait/drain cycle during
active PTY output. The local lifecycle fix keeps the actor draining canonical
output while one bounded termination worker runs; cleanup joins that worker,
process Wait and PTY EOF. Deterministic tests cover Close, explicit termination,
lost state, repeated/canceled admitted requests, error identity, queued input
revocation, authoritative replies racing Done and continuously queued Status
traffic that must not starve output drain. Native continuous-output Mac
Close/Terminate regressions fail on the old runtime and pass with the fix.
Independent source review, affected race tests, vet and Windows compile-only
checks pass. Native Windows and sustained multi-session stress/soak remain
pending. See `docs/evidence/p3/active-output-shutdown.md`; this is a local,
unpushed checkpoint, not a complete P3 or release gate.

P0 — Private repository, preflight, and scaffold: **complete**.

P1 — Go terminal core: **in progress**, tracked by private issue #7. The owner
approved the documented ConPTY query-response and macOS process-group
boundaries on 2026-09-20; native Windows 11 validation remains pending.

P2 — Frame and input protocol: **in progress**. The first checkpoint was
merged from private PR #10 as `0e7b870c5ad1be1c63c99a080437cd04b8ef16ad`.
The canonical protocol, normalized frame/patch model, browser renderer/input
foundation, and minimal desktop client are implemented. P1 is not represented
as complete, and P2 remains in progress until its live connection acceptance
tests are integrated.

P3 — Persistent broker: **in progress**, tracked by private issue #11. The
controller/input-ingress checkpoint was merged from private PR #12 as
`1ac199dfea8a2e5aa6d86bcab70cce80183679e9`. The bounded local handshake
checkpoint merged from private PR #13 as
`a92d1ca1836df29c4f3a72bc772bed8b6d354b54` and the
broker-owned live-session runtime merged from private PR #14 as `d3ff4fd`. The
session registry merged from private PR #15 as
`400f48460ea7546cb815f631439b1f1933a2c645`, followed by SQLite persistence
from private PR #16 as `bc361141995f1bbf1d5a273ac38835fa9ee7cc3d`, and secure
platform IPC merged from private PR #17 as `9ab4a3e99e7f15d8634315e2e1e22eda059354d0`.
Viewer delivery merged from private PR #18 as
`1ee75ea4717562905ccb79175729b999df072346`. Durable operation receipts merged
from private PR #19 as `fe968ec303e31cd9dd34cf56540733686f1df974`, adding
broker-integrated deduplication for session create, rename, and terminate,
shutdown admission barriers, clock-rollback-safe monotonic persistence, and
retained runtime/cleanup diagnostics.
Broker-management RPC merged from private PR #20 as
`e14cc5722066f714db2c3a71a4d5442b11df4089`.
Immutable history paging merged from private PR #21 as
`f74f7620746ba1646e7a056886558f23dbbc795a`.
Broker-management dispatch merged from private PR #22 as
`9d212651efe331f35fdbb66038905e705125072f`. A local management-client
checkpoint is now implemented on `feat/p3-management-cli`: it authenticates as
the local CLI, enforces bounded and exactly correlated management RPC, exposes
only saved configuration IDs for create, and distinguishes a typed rejection
from uncertain mutation delivery without retrying. The user-facing command
layer implements bounded `list` and exact-ID, explicitly confirmed `terminate`
orchestration, including safe rendering of legacy control characters and
honest already-exiting/exited/lost output. Thin `cmd/pterm` routing for `list`
and `terminate` is integrated locally; `new` and `attach` now use the canonical
live-session stream. Focused race, full repository, and Windows
cross-build evidence is recorded in
`docs/evidence/p3/management-cli.md`.
Independent review additionally moved operation-ID validation inside the
serialized client exchange so invalid acknowledgements close the connection
before queued mutations can write. Confirmation now requires a context-aware
input reader; cancellation and late-answer regressions pass without a reader
goroutine. An owned OS-descriptor/handle input adapter is now implemented;
actual macOS pipe and pseudo-terminal cancellation, descriptor ownership,
TTY/flag restoration, and Close interruption tests pass under the race
detector. Windows tests compile to x86-64 PE, including an isolated native
console fixture, but native Windows execution remains pending. Public desktop
command routing is still an explicit integration gate.
The same owned-handle infrastructure now supports cancellable terminal
output. Actual macOS stalled-pipe and aliased input/output PTY tests pass;
`Close` joins blocked writes while preserving caller-owned stdout. Desktop
command integration and native Windows execution remain pending, documented
in `docs/evidence/p3/terminal-output.md`.

The live local-stream checkpoint is implemented on `feat/p3-session-ipc`:
canonical CLI-only desktop negotiation, observer attachment, correlated
actor-ordered control grants, structured input acknowledgements, history,
resize/resync, and a bounded cancellable writer. It also rejects malformed
Unicode and invalid mouse buttons. Full repository/race/cross-compile checks and
independent integration review pass; evidence and fixed review findings are in
`docs/evidence/p3/session-ipc.md`. This does not grant gateway/browser access,
complete desktop CLI attachment, or claim physical-device support.

Independent integrated-client review reproduced a late output-cleanup error
being hidden after `Close` initiated cancellation. The live client now retains
substantive output errors before the joined painter exits, without turning
ordinary cancellation or normal network closure into a failure. Focused race
regressions and desktop/liveclient/CLI package tests pass; synthetic failure
injection and platform limitations are recorded in
`docs/evidence/p3/live-client-cleanup.md`.

The viewer checkpoint provides broker-owned
bounded viewer subscriptions, exact snapshot/patch revision boundaries,
slow-viewer isolation, actor-owned acknowledgement expiry, reliable bounded
detach delivery, screen-only live capture, and a conservative encoded-row
transport limit. Focused race/vet and the complete repository check pass;
evidence is in `docs/evidence/p3/viewer-delivery.md`.

The broker-management RPC checkpoint defines and validates bounded local IPC
requests/results for session list, create, rename, terminate, and status.
Mutation operation IDs are distinct from request correlation IDs, and create
requests can reference only saved project and shell-profile IDs. Request
dispatch merged from private PR #22 as
`9d212651efe331f35fdbb66038905e705125072f`: accepted current-user
connections complete the handshake, execute sequential correlated RPC, resolve
process configuration inside the broker, return typed bounded errors, and
survive gateway reconnects without starting a second process. Validation
evidence is recorded in
`docs/evidence/p3/broker-management-rpc.md` and
`docs/evidence/p3/broker-management-dispatch.md`.
The protocol audit then tightened exact case-sensitive JSON field matching,
lowercase UUID canonicalization, schema-level session identity invariants, and
consistent UTF-8 title semantics while preserving stored legacy titles. The
dispatcher now enforces response correlation. Independent broker/gateway
process modes are implemented on `feat/p3-process-modes`: the broker acquires
canonical data-directory and runtime IPC locks before opening SQLite, retains
data ownership through Registry/Store cleanup, owns generation recovery and
the Registry, while the gateway owns only an exact-Host loopback health
bootstrap and restricted IPC probes. macOS runner and executable-process tests
prove a gateway restart preserves the same child/session generation and
advancing canonical output; startup recovery marks prior live metadata lost
without rerunning it. Review regressions also prove that a same-data broker with
a distinct runtime cannot overlap live ownership or shutdown cleanup, bare and
partial development modes remain isolated from canonical production paths, and
gateway timeout cleanup force-closes incomplete HTTP connections without
suppressing the timeout error. Evidence is in `docs/evidence/p3/process-modes.md`.
Authenticated HTTP/WebSocket-to-live-session dispatch and native Windows 11
execution remain pending.

The history-paging checkpoint provides actor-owned normal-history
epochs/revisions, immutable pages of at most 200 rows, stale-token
`history.changed` results, viewer-relative history-reset signaling, and strict
5,000-row/32 MiB normalized retention applied to the emulator scrollback
itself. Alternate-screen state has no hidden scrollback. Focused race tests
pass; full repository and Windows cross-compile evidence is recorded in
`docs/evidence/p3/history-paging.md`.

The follow-up boundary audit found and fixed a retained-wide-cell resize
blocker: shrinking a history row with a width-2 grapheme in the new final
column previously produced an invalid row without a continuation. The adapter
now transports a clipped single-column replacement at that width and restores
the original grapheme if a later width can represent it. Repeated resize,
alternate-screen, retention, race, and transport fixtures cover the boundary.

The final review audit also found that internal screen patches omitted the
frame's history row/byte/token metadata. Patch diff/apply now round-trips all
four fields, canonical wire patches read the token from that patch, and the
reset signal remains derived from each viewer's applied base epoch. Actor-backed
append/reset regression tests, the full Node 24 check, race tests, and Windows
amd64 compile-only checks pass.

An independent dispatch audit found that initial mutation responses were being
rebuilt from mutable saved configuration or current session metadata. The
checkpoint now persists the exact bounded response snapshot with every receipt,
checks create receipts before resolving saved configuration, and returns
rename/terminate receipt snapshots without rereading current state. Regression
tests cover disabled create configuration, rename A/B/replay A, and terminal
state change before terminate replay. Migration 003 rewrites old create
fingerprints to the stable project/profile-ID identity. A later PR review found
that v2 create receipts could not prove an original title after a later rename;
the migration now marks unprovable legacy create/terminate results unavailable
instead of manufacturing a hybrid response, and retries fail deterministically
without rerunning the operation. Store, Registry, and RPC regressions cover the
boundary. Final validation and the follow-up independent audit are recorded in
`docs/evidence/p3/broker-management-dispatch.md`.

The P1 terminal-core checkpoint is implemented and merged. No benchmark,
physical-phone test, installer, signing operation, or release has been
completed. Unexecuted later-milestone gates remain pending.

The desktop decoder-boundary checkpoint reproduces upstream Ultraviolet paste
and incomplete-sequence accumulation beyond the product limit. A documented
local patch now applies opt-in early limits, typed errors, failed-reader
invalidation, and cancellation that joins the input pump. The desktop adapter
selects 64 KiB limits. Retained upstream and adapter race suites pass on macOS;
Windows test binaries cross-compile only. Actual command wiring and native
Windows console validation remain pending. See
`docs/evidence/p3/desktop-decoder-bounds.md`.

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

Note (2026-09-27): the repository was later renamed to `intojhanurag/Termivoa`;
the old name redirects. It is still private.

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
| Coding CLI and disposable test repository | Claude Code and Codex CLI ran in a disposable empty project on the Mac development profile (`docs/evidence/p7/coding-agents.md`) | A real-phone agent run remains in `owner-verification.md` |
| Apple signing/notarization | Membership, identities, and approved credential location not established | Signed macOS P6/P8 artifacts |
| Windows code signing | Owner-approved trusted identity and key access not established | Signed Windows P6/P8 artifacts |
| Public identity | Name decided: Termivoa (2026-09-27; repository `intojhanurag/Termivoa`; kept compatibility names in `docs/naming.md`). Publisher identity for signing and publication permission not confirmed | Public-release P8 handoff |
| Hosted CI | GitHub Actions jobs do not start because of account billing; `main` was merged without hosted CI (2026-09-27) | Native Windows CI, packaging workflow, required checks on later PRs |
| Public contacts | Done (2026-09-27): owner chose aojharaj2004@gmail.com for security reports (`SECURITY.md`) and Code of Conduct reports | No current blocker |
| Branch enforcement | Supported and configured on private `main`: required PR, strict named checks, resolved conversations, linear history, admin enforcement, no force push, no deletion | No current blocker. Squash is the only enabled merge method. |
| Existing GitHub App | `Greptile Review` runs automatically on private PRs; the agent did not install it | Owner confirmed on 2026-09-20 that Greptile is intentionally authorized to review this project. It is not a required branch-protection context and its confidence threshold is configured not to block merging. |

## Milestones

| Milestone | Status | Evidence |
| --- | --- | --- |
| P0 — Private repository, preflight and scaffold | Complete | PR #5 squash-merged as `6ece2af8ea46fbf5bc7221dfba1b677ca7ea02e8`; final-head CI run `35509425646` passed all ten required checks |
| P1 — Go terminal core | Implemented on macOS; native Windows 11 pending | PR #8 merged as `0342a0058e088b4a4970eb23ec2e150aac6f0232`; architecture boundaries approved; native Windows 11 execution remains pending |
| P2 — Frame and input protocol | Implemented; physical-device evidence pending | Private issue #9; PR #10 merged as `0e7b870c5ad1be1c63c99a080437cd04b8ef16ad`; checkpoint evidence in `docs/evidence/p2/frame-protocol-validation.md`; live broker/gateway connection and physical-device evidence remain pending |
| P3 — Persistent broker | Merged in #27 (2026-09-27); native Windows pending | Private issue #11; controller/input-ingress and local IPC merged through PR #13; broker-owned runtime and bounded registry merged through PR #15; SQLite persistence merged through PR #16; platform IPC merged through PR #17; viewer delivery merged through PR #18; durable operation receipts merged through PR #19; broker-management RPC merged through PR #20; immutable history paging merged through PR #21; broker-management dispatch merged through PR #22; process modes, live-session IPC and desktop attach landed through PR #23, merged as part of #27; evidence under `docs/evidence/p3/` |
| P4 — Authentication and private access | Merged in #27 (2026-09-27) | `docs/evidence/p4/`; real phone over cellular and Tailscale Serve pending owner |
| P5 — Phone experience | Merged in #27; UI v2 in #30 (2026-09-27) | `docs/evidence/p5/phone-web.md`; physical iPhone/Android pending owner |
| P6 — Install and update | Merged in #27; first run in #28 (2026-09-27); unsigned | `docs/evidence/p6/`; Windows installer run, real startup registration and signing pending owner |
| P7 — Reliability and review | In progress | `docs/evidence/p7/` (Mac soak, coding agents, terminal fixes); Windows soak, 24-hour soak and stress run pending |
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

Termination boundary decision (2026-09-27, pending owner approval): on Unix,
terminate and shutdown now signal every process in the managed PTY session, not
only the session shell's process group that the owner approved on 2026-09-20
(plan section 6). This ends interactive-shell background jobs. Session members
get the 500 ms grace period before SIGKILL, also when the shell exits on its own.
Processes that call `setsid` stay outside. See
`docs/evidence/p6/cli-lifecycle.md` and `docs/owner-verification.md`.

The public `new` checkpoint validates an interactive 80x24 initial viewport before
creating from saved project/shell IDs, then attaches and explicitly takes control
after the first snapshot. Native Mac command/PTY tests prove detach/cancellation
preserve the new session and restore caller terminals. Errors after creation retain
recovery guidance; no retries or destructive rollback occur. Local project
registration and release/device gates remain pending; see
`docs/evidence/p3/new-command.md`.

The controlled-resize follow-up replaces the demonstrated shrink disconnect with
a bounded Syncing/resnapshot transition. Focused race tests cover stale frames,
fixed timeout, observer/takeover boundaries, no input replay and zero-byte versus
partial/joined output failures. CLI/liveclient Windows test compilation passes;
native execution and the independent outer-PTY executable shrink fixture remain
pending. See `docs/evidence/p3/desktop-controlled-resize.md`.

Independent review of the resize candidate reproduced and fixed an old-snapshot
acknowledgement overtaking a resize publication, stale cached dimensions blocking
grow-back, and queued startup input during blocked Control status output. Setup
now drains a separate bounded decoder, joins it, discards remaining owned raw
input, then starts a fresh live decoder. Initial control waits for the requested
canonical grid. Focused race/vet and actual Mac raw-PTY startup regressions pass;
an independent working-tree executable shrink/grow run also passed. Repeated
committed executable evidence and native Windows remain separate gates. Partial
input events spanning later live readiness changes are not yet established by
these startup tests. Details and exact commands are in the same evidence record.

The public desktop attach checkpoint connects exact session generations through
restricted IPC, defaults to read-only, and accepts explicit `--control` only after
the initial snapshot is applied. Focused Mac race and disposable-PTY restoration
tests pass; Windows compile-only evidence is not a native pass. Exact-fit grids
are preserved; smooth window shrink and initial resize readiness are being fixed
in separate reviewed checkpoints. The independent executable/broker integration
suite passes on Mac, including read-only input discard, takeover, Unicode paste,
literal Ctrl+], Ctrl+] detach, SIGTERM restoration and same-child reconnect. See
`docs/evidence/p3/desktop-attach-command.md` and
`docs/evidence/p3/desktop-outer-pty.md`.

An explicit Mac `nativecompat` suite now exercises installed zsh 5.9, less 668,
and Vim 9.1 through broker-owned PTYs and structured input. Editing, Ctrl+C,
Ctrl+D, held multiline paste, Unicode text, pager navigation, actual editor
save, and alternate-to-normal buffer transitions pass on the available Mac.
The headless normalized-frame consumer does not establish outer-terminal or
phone glyph layout. Native Windows and owner login profiles remain separate
gates; evidence and the explicit command are
in `docs/evidence/p3/native-mac-applications.md`.

The CLI selection checkpoint centralizes exact session-generation resolution and
adds one-shot creation from saved project/shell IDs. It preserves uncertain-delivery
warnings and never retries or restarts a lost session. Focused Mac race tests and
vet pass; public `new`/`attach` wiring is now integrated locally. See
`docs/evidence/p3/cli-session-selection.md`.

The live-client viewport checkpoint provides a pre-paint check for the actual
desktop grid, including snapshot transfer and observer-resize changes. Focused
race tests prove rejected drawings are not written or acknowledged and exact-fit
grids remain valid. Public desktop command integration and native resize behavior
are pending; see `docs/evidence/p3/live-client-viewport.md`.

The real-session registry retirement fixture once exceeded its eight-second
deadline under concurrent full race/repository checks. Eleven further focused
race invocations, two complete ordinary Go suites, and one full race suite
passed with overlapping runs. Failure-only synthetic-child/lifecycle probes
never fired, so the cause remains unclassified; no deadline, retry, warmup, or
runtime change was made. Evidence is in
`docs/evidence/p3/retirement-readiness-investigation.md`.

The command integration checkpoint wires `pterm list` and confirmed
`pterm terminate` through the real restricted IPC client. Native Mac tests use a
disposable PTY child and verify decline, confirmed exit, and idempotent reporting
of an already-exited session. Focused race/vet, Windows cross-build, and frontend
checks pass. Its initial full repository check failed the existing PTY fixture
READY timeout, which remains under investigation rather than being counted as
green. See `docs/evidence/p3/management-command-integration.md`. Desktop attach,
phone access, and release readiness are not claimed by this checkpoint.

The Windows desktop console checkpoint adds the missing reversible UTF-8 code
pages and output VT-processing setup around the outer terminal. Injected policy
tests pass on Mac, and Windows cross-compilation is supplemental only. Native
Windows console/Unicode execution and complete desktop command wiring remain
pending. See `docs/evidence/p3/windows-console-modes.md`.

The desktop shutdown checkpoint reproduced restoration of the enclosing terminal
before the input decoder had stopped. The coordinator now connects after terminal
entry, joins input and display workers before restoration, and observes connection
loss. Focused race tests and Windows cross-compilation pass; native terminal/client
integration remains a separate pending gate. Evidence is recorded in
`docs/evidence/p3/desktop-shutdown.md`.

Independent command review reproduced blocked stdout preventing cancellation
of listing and termination confirmation. Management output now uses the
owned, context-aware pipe/terminal adapter and joins its cleanup. Actual Mac
command regressions verify cancellation with saturated pipes, preserved caller
descriptors/flags, no consumed confirmation, no termination, and no duplicate
leak; both regressions fail with the previous command implementation. Regular
file/custom-writer output retains explicitly documented synchronous limits.
Focused race/vet and Windows cross-compilation pass after the fix; native
Windows and the earlier full-check PTY fixture issue remain pending.

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

The P3 viewer-delivery design audit found that a maximally escaped normalized
row can exceed the 1 MiB outbound-message limit even though snapshot chunks
cannot split a row. The viewer checkpoint now applies a
conservative encoded-row budget, clipping marker, persistent per-row clipping
state, and boundary fixtures. The gateway adapter is still unimplemented and
must honor the viewer sink's 2 MiB drain-accounting contract before this can be
claimed end to end.

The P3 session-registry checkpoint retains a bounded in-memory create-operation
cache as a concurrency optimization. SQLite now owns the durable result for
session create, rename, and terminate mutations, including fingerprint conflict
detection across broker/store reopen. Active-session receipts are never pruned;
terminal history is bounded, and new unique mutations are rejected if active
receipts alone fill the hard cap. SQLite also records project, shell-profile,
and lifecycle metadata and marks prior live rows lost when a new broker
generation begins. It does not restore dead processes or terminal contents.

The history-paging checkpoint's broad ten-repeat race sweep observed one
timeout in the existing viewer-resync test while waiting for a third snapshot.
The test passed 20 isolated race repetitions afterward; the full affected
race run and 50 history-specific repetitions also passed without a race
report. This remains recorded as a non-reproduced timing flake in
`docs/evidence/p3/history-paging.md`, not as native-platform evidence.

The recurring full-suite PTY fixture readiness failure was reproduced with the
fresh child still at macOS `_dyld_start`, before the Go fixture could emit
`READY`. Test setup now separately validates and times one bounded native
activation probe; the original two-second PTY I/O deadlines remain unchanged.
The fixture directory cleanup also now runs before `TestMain` exits. Full-suite
race tests and twenty focused PTY race repetitions passed; native Windows is
still pending. See `docs/evidence/p3/pty-fixture-activation.md` for the sampled
failure, exact checks, and distinct cold-launch versus PTY validation scopes.

## P4 pairing and authentication (broker side)

On 2026-09-27 branch `feat/p4-pairing-broker` implemented the broker half of
P4: gateway and local administration RPC schemas, migration 004 with hashed
secrets, the pairing/credential/revocation service, credential-bound browser
live streams with access rechecks in the session actor, shell-profile
discovery, `internal/gatewayclient`, and the `pair`, `devices`, `revoke`,
`project`, `profile` and `config origin` commands. A gateway IPC peer can no
longer reach unauthenticated session management. Race tests, Windows
cross-vet, web checks and the real-broker pairing integration test pass on
macOS. `TestPtermDesktopAttachNativeOuterTTY` fails identically on the base
commit and remains open. Native Windows, the HTTP gateway/web pairing path and
a real phone remain pending. See `docs/evidence/p4/pairing-broker.md`.

## Private beta and post-beta features (2026-09-28 to 2026-09-29)

- **Private beta `v0.1.0-beta.1`** (2026-09-28): GitHub prerelease in the
  private repository with unsigned macOS, Linux and Windows binaries and
  `SHA256SUMS` (#52). Downloaded with `gh` and checksum-verified; the macOS
  binary carries no quarantine attribute.
- **Website and docs** (#53–#57): landing page and Astro Starlight docs at
  <https://termivoa.vercel.app> and `/docs`, sources in `site/`.
- **Swipe to scroll full-screen apps** (#58): Claude Code enables the alternate
  screen and SGR mouse tracking (captured in a pty), so a swipe now sends
  wheel steps. Unit tests; owner confirmation on a real iPhone.
- **Reconnect** (#59, #61): after a long hide the socket is checked with a
  screen resync instead of replaced; 5 s attach timeout; no retries while
  hidden; one `gateway.log` line per phone connection. The log showed
  connections of 1–5 s in bursts before #61. The owner confirmed on the
  iPhone after #61.
- **Changes view** (#60): `changes.list/file` with bounded read-only git in
  `internal/gitchanges`. End-to-end check on an isolated `pterm up --dev`
  stack with a paired browser at 390×844.
- **Recap and commit & push** (#62): `agentStatus.workedSeconds`,
  controller-only `changes.commit/push`. End-to-end check on an isolated dev
  stack driven through Chrome DevTools: a commit of 2 of 3 changed files
  (`git show` confirmed the third stayed uncommitted) and a push to a local
  bare remote. The recap was checked by unit tests only.
- **Agent wall** (web only, `feat/agent-wall`): `/wall` shows each running
  session as a tile with its own read-only `Connection` and `TerminalView`,
  scaled with CSS `transform: scale()` to the tile's width and moved up to keep
  the cursor row in view (fitting the whole grid made a phone-shaped grid a
  narrow column with unreadable text; fixed in review); waiting first, then
  working. Tiles never take control, send input or resize, and detach on
  leave. Checked with `npx tsc --noEmit -p .`, `npx vitest run` (ordering,
  scale, tiles, empty state, tap to open) and `./scripts/check`. On an
  isolated `pterm up --dev` stack at 390×844 with 4 shells: 2 columns, readable
  tiles after the fix (checked again with 3 sessions printing output), tap opens the session, browser back returns to `/wall`, and
  each session's `viewerCount` fell from 1 to 0 on leaving; 4 columns at 1024 px.

- **Private beta `v0.1.0-beta.2`** (2026-09-29, #65): GitHub prerelease at
  `4421f2c` with unsigned macOS, Linux (amd64, arm64) and Windows binaries,
  `install-linux.sh` and `SHA256SUMS`. Each binary carries the build ID and no
  personal paths; the macOS and Linux arm64 binaries start. Downloaded with
  `gh`: all checksums match and the macOS file has no quarantine attribute.

Still pending: real Linux, Windows and Android devices; CI after the Actions
minutes reset; the 24-hour soak; the history secret scan and licence audit
(see `docs/pending-work.md`). Features from #60 and #62 need a broker restart
on the owner's Mac, which ends running sessions.

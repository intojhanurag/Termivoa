# Milestone evidence

Each milestone receives a subdirectory containing sanitized commands,
timestamps, environment details, expected and actual results, and defect links.
Raw terminal history and credentials must never be stored here.

These files are historical records. Each one describes the state on its own
date; the current status is in [`../progress.md`](../progress.md) and the
items still waiting for the owner are in
[`../owner-verification.md`](../owner-verification.md). Milestones follow the
plan in [`../implementation-plan.md`](../implementation-plan.md).

## P0 — Private repository, preflight and scaffold

Verify the GitHub target, create the private repository, pin dependencies and
set up CI.

- [bootstrap-validation.md](p0/bootstrap-validation.md) — private repository, bootstrap commit and first CI runs.

## P1 — Go terminal core

Wrap the PTY and the Go virtual terminal; canonical state and one response
writer; native process lifecycle.

- [core-validation.md](p1/core-validation.md) — PTY host, emulator, query replies and process lifecycle fixtures.

## P2 — Frame and input protocol

Canonical schemas, normalized snapshots and patches, structured input, the
xterm display adapter and a minimal desktop client.

- [frame-protocol-validation.md](p2/frame-protocol-validation.md) — protocol schemas, frame/patch model and browser renderer checkpoint.

## P3 — Persistent broker

Separate broker and gateway processes, user IPC, session registry, metadata,
controller leases, history, and the desktop CLI.

- [active-output-shutdown.md](p3/active-output-shutdown.md) — broker shutdown drains and joins during active PTY output.
- [broker-management-dispatch.md](p3/broker-management-dispatch.md) — management RPC and operation receipts over local IPC.
- [broker-management-rpc.md](p3/broker-management-rpc.md) — canonical management schema, generated types and strict validation.
- [cli-session-selection.md](p3/cli-session-selection.md) — exact session selection and creation in the CLI.
- [controller-input-ingress.md](p3/controller-input-ingress.md) — first broker slice: controller leases and input ingress.
- [desktop-attach-command.md](p3/desktop-attach-command.md) — public `pterm attach` command.
- [desktop-controlled-resize.md](p3/desktop-controlled-resize.md) — bounded resize from a controlling desktop client.
- [desktop-decoder-bounds.md](p3/desktop-decoder-bounds.md) — bounded desktop input decoding (Ultraviolet patch).
- [desktop-input-admission.md](p3/desktop-input-admission.md) — desktop input admitted only for the ready controller epoch.
- [desktop-native-resize.md](p3/desktop-native-resize.md) — native shrink-then-grow resize regression.
- [desktop-outer-pty.md](p3/desktop-outer-pty.md) — real `pterm attach` executable in a disposable outer PTY.
- [desktop-polish.md](p3/desktop-polish.md) — desktop exit message and row-run painting fixes.
- [desktop-shutdown.md](p3/desktop-shutdown.md) — desktop attachment shutdown ordering.
- [gateway-shutdown-fixture.md](p3/gateway-shutdown-fixture.md) — gateway shutdown test synchronization.
- [history-paging.md](p3/history-paging.md) — immutable normal-screen history paging.
- [live-client-cleanup.md](p3/live-client-cleanup.md) — live-client shutdown output errors.
- [live-client-viewport.md](p3/live-client-viewport.md) — desktop viewport validation before paint.
- [live-client.md](p3/live-client.md) — canonical desktop live IPC client.
- [live-ipc-integration.md](p3/live-ipc-integration.md) — desktop client and real broker integration.
- [local-ipc-framing.md](p3/local-ipc-framing.md) — local IPC framing and handshake.
- [management-cli.md](p3/management-cli.md) — restricted management client, `pterm list` and `pterm terminate`.
- [management-command-integration.md](p3/management-command-integration.md) — public management commands wired to the broker.
- [native-mac-applications.md](p3/native-mac-applications.md) — native zsh, pager and editor compatibility on macOS.
- [native-output-flood.md](p3/native-output-flood.md) — finite-output flood probe.
- [new-command.md](p3/new-command.md) — `pterm new` create-and-attach command.
- [operation-receipts.md](p3/operation-receipts.md) — durable operation receipts for create, rename and terminate.
- [platform-ipc-listeners.md](p3/platform-ipc-listeners.md) — Unix socket and Windows named-pipe broker endpoints.
- [process-fixture-readiness-investigation.md](p3/process-fixture-readiness-investigation.md) — executable broker readiness timeout investigation.
- [process-modes.md](p3/process-modes.md) — broker and gateway as independent process modes.
- [pty-fixture-activation.md](p3/pty-fixture-activation.md) — PTY fixture cold activation and readiness.
- [retirement-readiness-investigation.md](p3/retirement-readiness-investigation.md) — real-session retirement timeout investigation.
- [session-ipc.md](p3/session-ipc.md) — local live-session dispatch.
- [session-protocol.md](p3/session-protocol.md) — live-session decoder boundary audit.
- [session-registry.md](p3/session-registry.md) — broker session registry and resource accounting.
- [session-runtime.md](p3/session-runtime.md) — broker-owned live-session runtime.
- [sqlite-persistence.md](p3/sqlite-persistence.md) — broker-owned SQLite metadata storage.
- [terminal-output.md](p3/terminal-output.md) — cancellable local terminal output.
- [viewer-delivery.md](p3/viewer-delivery.md) — bounded viewer delivery.
- [viewer-resync-cancellation.md](p3/viewer-resync-cancellation.md) — actor-ordered viewer resync cancellation.
- [viewer-session-events.md](p3/viewer-session-events.md) — actor-ordered viewer session events.
- [windows-console-modes.md](p3/windows-console-modes.md) — reversible Windows console configuration (native run pending).

## P4 — Authentication and private access

Pairing and revocation, cookie and Origin rules, local project registration,
and Tailscale setup/status.

- [auth-primitives.md](p4/auth-primitives.md) — pairing secret and lifetime primitives.
- [gateway-http.md](p4/gateway-http.md) — gateway HTTP and WebSocket boundary.
- [pairing-broker.md](p4/pairing-broker.md) — broker-side pairing, authentication and revocation.
- [session-activity.md](p4/session-activity.md) — `lastOutputAt` and `preview` session-list fields.

## P5 — Phone experience

The phone screens, mobile input, shortcut row, composer, history, connection
UX and accessibility.

- [phone-web.md](p5/phone-web.md) — phone web app, including UI v2.

## P6 — Install and update

Packaging, per-user startup, doctor, uninstall, staged manual update and
signing scripts.

- [cli-lifecycle.md](p6/cli-lifecycle.md) — pairing QR code, `pterm stop` and `pterm shutdown`.
- [first-run.md](p6/first-run.md) — 3-step `pterm setup` first run on the development profile.
- [packaging.md](p6/packaging.md) — startup registration, lifecycle commands and unsigned packaging.

## P7 — Reliability and review

Test matrix, security cases, stress and soak, and source review.

- [coding-agents.md](p7/coding-agents.md) — Claude Code and Codex CLI in a managed session.
- [soak.md](p7/soak.md) — Mac soak, busy sessions and stalled viewers.
- [terminal-fixes.md](p7/terminal-fixes.md) — OSC title leak and child environment leak fixes.

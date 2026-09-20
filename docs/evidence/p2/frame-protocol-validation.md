# P2 frame/protocol checkpoint evidence

Date: 2026-09-20

Environment: Apple Silicon macOS 26.5.2, Go 1.27.1, Node.js 24.11.0,
pnpm 10.33.0

## Implemented checkpoint

- Canonical JSON Schema Draft 2020-12 union for all 27 v1 WebSocket message
  types, generated Go/TypeScript models, and strict direction-aware Go
  validation.
- Immutable canonical full frames and exact-base, whole-row patches with
  decimal-string wire revisions.
- Browser validation with Ajv before display-state mutation; interrupted
  snapshots remain hidden until every row is received.
- Display-only xterm painting through a constrained sequence allowlist, with
  local scrollback and input disabled.
- Server-cell Unicode width adapter over the pinned grapheme addon. Actual
  xterm buffer cells were verified for ASCII, CJK, combining accents,
  variation selectors, rainbow flag, woman-technologist, and family ZWJ
  fixtures.
- Structured browser input sequencing that does not queue offline input or
  retransmit after disconnect.
- Minimal Go desktop renderer and Ultraviolet input decoder. Outer-terminal
  replies are ignored; Ctrl+] detaches and Ctrl+V then Ctrl+] sends the literal
  chord.

## Commands and actual results

```text
GOTOOLCHAIN=go1.27.1 go test ./internal/protocol ./internal/terminal ./internal/desktop
PASS

GOTOOLCHAIN=go1.27.1 go test -race ./internal/protocol ./internal/terminal ./internal/desktop
PASS

GOTOOLCHAIN=go1.27.1 go vet ./internal/protocol ./internal/terminal ./internal/desktop
PASS

pnpm protocol:check
PASS after the generated protocol commit

pnpm typecheck
PASS

pnpm test:web
PASS: 5 files, 28 tests

pnpm build:web
PASS
```

The protocol schema was also compiled in Ajv 2020 strict mode and exercised
with valid and invalid examples by the repository tests.

## Honest limitations and pending gate work

- This is a reviewable P2 checkpoint, not the P2 exit gate. Broker/gateway IPC,
  bounded per-viewer queues, live WebSocket reconnect, and controller leases
  belong to the P3 integration and are not claimed here.
- Automated browser evidence uses xterm's real buffer model but not a physical
  Safari or Chrome rendering surface. Required iPhone and Android width,
  rotation, keyboard, and WebGL-loss checks remain pending.
- The pinned Unicode grapheme provider is experimental. Its demonstrated
  mismatch is handled by the documented server-cell adapter, but physical-font
  agreement still requires device evidence.
- Native Windows 11 execution remains unavailable in this session. Windows CI
  is supplemental and is not represented as the required physical-platform
  pass.

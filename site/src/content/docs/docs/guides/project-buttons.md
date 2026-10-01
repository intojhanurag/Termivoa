---
title: Project buttons
description: Termivoa reads your project's package.json, Makefile, Cargo.toml or go.mod and shows its test, dev, build and lint commands as one-tap buttons.
lastUpdated: 2026-10-01
---

Termivoa finds your project's own commands, such as tests or the dev server, and shows them as one-tap buttons in the Snips sheet.

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`.
:::

<!-- image: the This project part of the Snips sheet -->

## Which commands does Termivoa find?

Termivoa reads these project files:

- **package.json** scripts, with npm, pnpm, yarn or bun picked from the lockfile (`pnpm-lock.yaml` gives `pnpm test`).
- **Makefile** targets, for example `make test`.
- **Cargo.toml** (Rust): `cargo test`, `cargo run`, `cargo build` and `cargo clippy`.
- **go.mod** (Go): `go test ./...` and `go build ./...`.

It only reads them; nothing runs until you tap an action.

## Run a project command

1. In the session, take control.
2. Tap **snips**. **This project** is at the top.
3. Find the command in its group: **test**, **dev**, **build**, **lint** or **other** (tap **More** for more).
4. Tap the command, then an action:
   - **Run in new tab** starts a new session in the same project and shell, types the command once (only if no one else has control), and opens it.
   - **Type here** types it in this session and presses Enter.

## Why is Type here off?

**Type here** needs control and a normal shell prompt. It is off while a full-screen app like Claude Code is open, or something runs, because the command would go into that program. Use **Run in new tab**.

## When no commands show

- **No project commands found in** a folder: it has none of the files above.
- **Could not read the project**: tap **Retry**.

Saved snippets stay below the project commands; see [Save and send snippets](/docs/guides/live-terminal/#save-and-send-snippets).

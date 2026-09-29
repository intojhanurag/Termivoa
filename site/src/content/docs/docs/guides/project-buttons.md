---
title: Project buttons
description: Run your project's own test, dev, build and lint commands from the Snips sheet, in a new tab or in the current session.
---

This page helps you run your project's own commands, such as tests or the dev server, with one tap.

## What Termivoa finds

Termivoa reads the files in the session's project folder on your computer. It finds:

- **package.json** scripts. The command uses npm, pnpm, yarn or bun. Termivoa picks the tool from the lockfile in the folder, for example `pnpm-lock.yaml` gives `pnpm test`.
- **Makefile** targets, for example `make test`.
- **Cargo.toml** (Rust): `cargo test`, `cargo run`, `cargo build` and `cargo clippy`.
- **go.mod** (Go): `go test ./...` and `go build ./...`.

Termivoa only reads these files. Nothing runs until you tap an action.

## Run a project command

1. Open the session and take control.
2. Tap **snips** in the dock. The sheet shows **This project** at the top.
3. Find the command. Commands are in groups: **test**, **dev**, **build**, **lint**, then **other**. Each button shows the name and the exact command. When there are many other commands, tap **More**.
4. Tap the command. Two actions show.
5. Tap an action:
   - **Run in new tab** starts a new session in the same project and shell, types the command there, and opens it. The command is typed once, and only if no one else has control. Your current session does not change.
   - **Type here** types the command in this session and presses Enter.

**Type here** works only at a normal shell prompt. It is off while a full-screen app such as Claude Code or Codex is open, or while something is running, because the command would go into that program. Use **Run in new tab** then. You also need control to use **Type here**.

## When no commands show

- **No project commands found in** a folder: the folder has none of the files above.
- **Could not read the project**: tap **Retry**.

Your saved snippets are still below the project commands. See [Snippets](/docs/guides/live-terminal/#snippets).

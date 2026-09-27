---
title: CLI reference
description: Every pterm command with a one-line description.
---

This page lists every `pterm` command you run on your computer.

Run `pterm help` to see the same list in your terminal.
On Windows, type `.\pterm.exe` in place of `pterm` unless `pterm` is on your PATH.

## Setup and health

| Command | What it does |
| --- | --- |
| `pterm setup` | First run: checks, one confirmation, startup at login, phone address, Tailscale Serve (asks first), pairing. |
| `pterm setup --apply --yes` | Installer mode: registers startup and asks nothing. It does not change Tailscale or pair a phone. |
| `pterm doctor` | Read-only checks. Never prints secrets. |
| `pterm config origin [<url>]` | Shows or sets the private HTTPS address that the phone uses. |
| `pterm launch [--no-open]` | Starts the broker and gateway if needed, and opens the local page. |
| `pterm stop` | Stops the gateway only. Sessions keep running, but phones cannot connect. |
| `pterm shutdown` | Asks first, ends all sessions, and stops the broker and gateway. |
| `pterm uninstall [--dry-run] [--yes] [--delete-data]` | Removes startup at login. Deletes data only with `--delete-data`. |

## Phones

| Command | What it does |
| --- | --- |
| `pterm pair [--origin <url>] [--no-qr] [--no-wait]` | Prints a two-minute, single-use pairing link and QR code, then approves the phone after you compare codes. |
| `pterm pair pending` | Shows phones that wait for approval, and their codes. |
| `pterm pair approve <pending-id>` | Approves a phone after you compare its code. |
| `pterm devices` | Shows paired phones, their status and last use. |
| `pterm devices rename <device-id> <label>` | Renames a paired phone. |
| `pterm revoke <device-id>` | Asks first, then removes a phone's access at once. |

## Sessions

| Command | What it does |
| --- | --- |
| `pterm list` | Shows sessions and their state. |
| `pterm new --project <id> --shell <id>` | Starts a session and takes control of it. |
| `pterm attach [--control] <session-id>` | Opens a session in this terminal. Read-only unless you add `--control`. Ctrl+] leaves. |
| `pterm terminate <session-id>` | Asks first, then ends one session and everything it started. |

In `pterm attach`, Ctrl+] never ends the session. With control, press Ctrl+V and then Ctrl+] to send a real Ctrl+].

## Projects and shells

| Command | What it does |
| --- | --- |
| `pterm project add [--name <name>] <path>` | Saves a project folder. New sessions start in a project. |
| `pterm project list` | Shows saved projects and their IDs. |
| `pterm profile list` | Shows saved shell profiles and their IDs. |
| `pterm profile detect` | Finds the shells on this computer and saves them as profiles. |

## Other

| Command | What it does |
| --- | --- |
| `pterm help` | Shows the command list. |
| `pterm up --dev [--listen <addr>]` | Development only: runs an isolated test copy. It does not touch your real setup. |

`--dev` and `--runtime-dir <folder>` are for development tests only. You do not need them for normal use.

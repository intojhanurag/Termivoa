---
title: CLI reference
description: Every command and flag of pterm, the Termivoa host program, for setup, startup, pairing phones, sessions, projects, shells and dev tests.
lastUpdated: 2026-10-01
---

`pterm` is the Termivoa host program. `pterm help` (or `--help`, `-h`) prints this command list.
On Windows, use `.\pterm.exe` unless `pterm` is on your PATH.

Background processes: the broker owns sessions; the gateway serves the phone page on `127.0.0.1:8787`.

## Set up, start and stop

| Command | What it does |
| --- | --- |
| `pterm setup` | First run: checks, one confirmation, startup at login, phone address, Tailscale Serve (asks first), pairing. |
| `pterm setup --apply --yes` | Installer mode: registers startup, asks nothing, sets the phone address only if unset, never changes Tailscale or pairs a phone. |
| `pterm doctor` | Read-only checks. Never prints secrets. |
| `pterm config origin [<url>]` | Shows or sets the phone's private HTTPS address. |
| `pterm launch [--no-open]` | Starts the broker and gateway if needed, and opens the local page. |
| `pterm stop` | Stops only the gateway. Sessions keep running, but phones cannot connect. |
| `pterm shutdown` | Asks first, ends all sessions, and stops the broker and gateway. |
| `pterm uninstall [--dry-run] [--yes] [--delete-data]` | Removes startup at login. Deletes data only with `--delete-data`. |
| `pterm up --dev [--listen <addr>]` | Development only: an isolated test copy that leaves your real setup alone. |

`--dev` and `--runtime-dir <folder>` are for development tests only.

## Pair and manage phones

| Command | What it does |
| --- | --- |
| `pterm pair [--origin <url>] [--no-qr] [--no-wait]` | Prints a two-minute, single-use pairing link and QR code, then approves the phone after you compare codes. |
| `pterm pair pending` | Shows phones waiting for approval, with their codes. |
| `pterm pair approve <pending-id>` | Approves a phone after you compare its code. |
| `pterm devices` | Shows paired phones, status and last use. |
| `pterm devices rename <device-id> <label>` | Renames a paired phone. |
| `pterm revoke <device-id>` | Asks first, then removes a phone's access at once. |

The pairing link and QR code are secret. Do not share them.

## Work with sessions

| Command | What it does |
| --- | --- |
| `pterm list` | Shows sessions and their state. |
| `pterm new --project <id> --shell <id>` | Starts a session with control. |
| `pterm attach [--control] <session-id>` | Opens a session in this terminal, read-only unless `--control`. |
| `pterm terminate <session-id>` | Asks first, then ends the session and all it started. |

In `pterm attach`, Ctrl+] leaves without ending the session. With control, Ctrl+V then Ctrl+] sends a real Ctrl+].

## Save projects and shells

| Command | What it does |
| --- | --- |
| `pterm project add [--name <name>] <path>` | Saves a project folder where new sessions start. |
| `pterm project list` | Shows saved projects with IDs. |
| `pterm profile list` | Shows saved shell profiles with IDs. |
| `pterm profile detect` | Finds this computer's shells and saves them as profiles. |

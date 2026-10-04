---
title: CLI reference
description: Every command and flag of pterm, the Termivoa host program, for setup, startup, pairing phones, sessions, projects, shells and dev tests.
lastUpdated: 2026-10-04
---

`pterm` is the Termivoa host program. `pterm help` (or `--help`, `-h`) prints this command list.
On Windows, use `.\pterm.exe` unless `pterm` is on your PATH.

Background processes: the broker owns sessions; the gateway serves the phone page on `127.0.0.1:8787`.

## Set up, start and stop

| Command | What it does |
| --- | --- |
| `pterm setup` | First run: checks, one confirmation, startup at login, phone address, Tailscale Serve (asks first), pairing. It refuses before any change while other Tailscale Serve handlers share this computer's Termivoa address (port 443); see [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers). It then exits with code 3; other errors exit with 1. |
| `pterm setup --verbose` | `pterm setup` prints a short plan, one line for each change, and any warning of the checks under **Warnings:**. `--verbose` also prints the checks, the startup files and every command. |
| `pterm setup --apply --yes` | Installer mode: registers startup, asks nothing, sets the phone address only if unset, never changes Tailscale or pairs a phone. It refuses in the same way. |
| `pterm doctor` | Read-only checks. Never prints secrets. It also shows the protocol revision and multi-computer checks. |
| `pterm config origin [<url>]` | Shows or sets the phone's private HTTPS address. |
| `pterm launch [--no-open]` | Starts the broker and gateway if needed, and opens the local page. While Tailscale Funnel is on for port 443 or 8443, it first prints a warning with the command that turns Funnel off. |
| `pterm stop` | Stops only the gateway. Sessions keep running, but phones cannot connect. |
| `pterm shutdown` | Asks first, ends all sessions, and stops the broker and gateway. |
| `pterm uninstall [--dry-run] [--yes] [--delete-data]` | Removes startup at login. Deletes data only with `--delete-data`. |
| `pterm up --dev [--data-dir <dir>] [--runtime-dir <dir>] [--listen <addr>] [--preview-listen <addr>\|off]` | Development only: an isolated test copy that leaves your real setup alone. |

`--dev` and `--runtime-dir <folder>` are for development tests only.

## Pair and manage phones

| Command | What it does |
| --- | --- |
| `pterm pair [--origin <url>] [--no-qr] [--no-wait]` | Prints a two-minute, single-use pairing link and QR code, then approves the phone after you compare codes. Every `pterm pair` that pairs (also `--via` and `approve`) refuses while Tailscale Funnel is on for port 443 or 8443; see [troubleshooting](/docs/reference/troubleshooting/#pterm-pair-refuses-because-tailscale-funnel-is-on). |
| `pterm pair pending` | Shows phones waiting for approval, with their codes. |
| `pterm pair approve <pending-id>` | Approves a phone after you compare its code. |
| `pterm devices` | Shows paired phones, status and last use. A **VIA** column shows the home address a phone uses this computer from (`-` for this computer's own page). |
| `pterm devices rename <device-id> <label>` | Renames a paired phone. |
| `pterm revoke <device-id>` | Asks first, then removes a phone's access at once. |

The pairing link and QR code are secret. Do not share them.

## Use several computers

See [Use several computers](/docs/guides/computers/).

| Command | Where | What it does |
| --- | --- | --- |
| `pterm pair --via <name\|url>` | New computer | Prints a two-minute, single-use add-computer link for the Termivoa app at home and, on a terminal wide enough for it, a QR code of that link, then approves the phone after you compare codes. `<name>` is home's machine name, such as `mac`; `<url>` is its full address. It tells you to tap the link in a Termivoa session and confirm "Add this computer?", or, in **Settings → + Add a computer**, to scan the QR code (**Scan QR**) or paste the link. Scan it in the app, not with the phone's Camera app: that opens the browser. In a terminal it also shows this computer in the app at home under **Settings → Computers → Found on your network**, with **Add**, and waits up to 10 minutes (it prints "Sent to mac (https://…)."). Then it shows the QR code and the link only when you press Enter ("No Add button? Press Enter to show a QR code and a link."); when nothing reached home, it prints them at once. The prompt starts `Phone "iPhone" wants to add this computer (vps) to your Termivoa app on mac.`, and success says `Done. vps is in your Termivoa app on mac. Open the app and tap it.` When the link expires and nothing reached home, it ends as before. With piped input or `--no-wait` it sends nothing to home. `pterm setup` does the same when you answer `2` to **Is Termivoa already on your phone?**; its list shows each computer's full address and system. See [How do I add a computer?](/docs/guides/computers/#how-do-i-add-a-computer) |
| `pterm pair --via <name\|url> --allow-tagged` | New computer | The same, when home or this computer is a tagged Tailscale node. Check the tags first. **Add** does not work for a tagged computer: use the QR code or the link. |
| `pterm pair pending` | New computer | Also shows a **VIA** column: the home app a waiting phone comes from. |
| `pterm computers` | Home | Shows each phone's other computers: device ID, label, address and when added. |
| `pterm computers remove <url>` | Home | Removes a computer from every phone's list. It does not sign the phones out there; run `pterm revoke` on that computer. |
| `pterm revoke --via <url>` | Peer | Asks first, then revokes every phone that uses this computer from the Termivoa app at `<url>`, and deletes that app's pairing links and pending pairings. It does this also when no phone uses it now. Use it if home was hacked. |
| `pterm revoke <device-id>` | Home | After it revokes the phone, also lists the other computers that phone used, so you can revoke it there too. |

`pterm pair --via` also takes `--no-qr`, `--no-wait` and `--origin`. `--allow-tagged` needs `--via`.

`pterm revoke --via` asks one of these:

```text
Revoke browser access for 2 device(s) that use this computer from the Termivoa app at https://mac.tailx.ts.net? Their live connections close now. [y/N]
No device uses this computer from the Termivoa app at https://mac.tailx.ts.net now, but its pending pairings and pairing links will be removed. Continue? [y/N]
```

`pterm doctor` adds these lines:

| Line | What it means |
| --- | --- |
| `protocol revision 1 (oldest supported 1)` | This computer's protocol version, for [updating computers one at a time](/docs/guides/computers/#how-do-i-update-the-computers). |
| `multi-computer: CORS list (bound app origins): …` | Home addresses that phones use this computer from. `no bound app origins (CORS off)` means none. |
| `multi-computer: pairing CORS list (pending pairings): …` | Home addresses of pairings that wait for approval. |
| `! multi-computer: <url>: owner mismatch …`, `node ID changed (reused name?)`, `pinned node … is gone`, `different tailnet; not supported` | A home that phones use this computer from fails the Tailscale check. Owners are compared by Tailscale user ID, not by login name. |
| `! multi-computer: <url>: tags changed since pairing; phones bound to it are refused until paired again` | Home's Tailscale tags were added, removed or replaced after pairing. Pair again. |
| `! multi-computer: this computer's tailnet is unknown (is Tailscale logged out?); bound app origins cannot be checked` | This computer is not logged in to Tailscale. Log in. |
| `! tailscale serve: other handlers share this computer's Termivoa origin https://…: …` | A problem: Tailscale Serve shares more than Termivoa on this address (port 443). Move each handler to another computer or to its own Tailscale Service hostname (another port does not help); the line prints the commands that remove them. `pterm setup` refuses until you do. |
| `! tailscale serve (warning): other ports of this computer's host receive the Termivoa cookie, …` | A warning, not a problem: handlers on other ports of this computer get the Termivoa cookie when the phone opens them. See [the troubleshooting entry](/docs/reference/troubleshooting/#pterm-doctor-warns-that-other-ports-receive-the-termivoa-cookie). |
| `herdr: <path> <version> (protocol <n>), server running at <socket>, can make worktrees` | **Next beta.** herdr is installed and its server runs. The line says "cannot make worktrees" when herdr cannot. `herdr: <path>, server not running (<socket>)` means the server is off. These lines show only when herdr is installed and are never a problem. See [Termivoa + herdr](/docs/guides/herdr/). |
| `! herdr: <path>, Termivoa will not use the server socket <socket>: group or others can write <folder>; run: chmod go-w <folder>` | **Next beta.** herdr runs, but another user could reach its socket, so Termivoa does not use it and the app says "herdr is not running". Run the command the line prints. See [the troubleshooting entry](/docs/reference/troubleshooting/#pterm-doctor-says-termivoa-will-not-use-the-herdr-socket). |
| `herdr agents the phone can start: claude, codex` | **Next beta.** The agents that already run in herdr, which **New worktree** offers. |
| `! herdr: <path> <version> (protocol <n>) is too old or unreadable; update herdr (0.8.2 or later)` | **Next beta.** Update herdr. See [Update herdr on mac](/docs/reference/troubleshooting/#update-herdr-on-mac). |
| `! herdr: config.toml sets a prefix key other than ctrl+b; …` | **Next beta.** Termivoa doubles only `ctrl+b`, so a phone can send that other prefix key to a herdr terminal it opened. At worst it detaches the phone. Set herdr's prefix key back to `ctrl+b`. |
| `! herdr at <path>: <reason>; Termivoa will not run it` | **Next beta.** It shows before the herdr line, or alone when no other herdr is found. Termivoa refused a herdr program, for example one that belongs to another user or sits in a folder that others can write. |
| `! tailscale at <path>: …; Termivoa will not run it` | Another user could replace that Tailscale CLI, so Termivoa does not use it. See [the troubleshooting entry](/docs/reference/troubleshooting/#pterm-doctor-says-termivoa-will-not-run-tailscale). |

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

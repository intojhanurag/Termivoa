---
title: Troubleshooting
description: Fixes for common Termivoa problems, such as an unreachable computer, pairing errors, READ-ONLY sessions, missing notifications, worktrees and failed git pushes.
lastUpdated: 2026-10-01
---

Most Termivoa problems start with one check on the computer. It never prints secrets.

```sh
pterm doctor
```

## "Can't reach your computer"

1. Turn Tailscale on, on both phone and computer.
2. Check that the computer is awake and logged in.
3. Run `pterm doctor` on the computer.
4. Tap **Retry now**, or wait for the automatic retry.

Your sessions keep running. Any network works while Tailscale is on.

## It stops working when the computer sleeps

Keep the computer on power and stop sleep:

- macOS: **System Settings → Battery → Options → Prevent automatic sleeping on power adapter when the display is off**.
- Ubuntu desktop: turn off **Settings → Power → Automatic Suspend**.
- Windows: check **Settings → System → Power**.

Sessions continue when it wakes.

## It reconnects often

- Keep the computer awake.
- In the phone's Tailscale app, keep **VPN On Demand** on.
- Open Termivoa from the Home Screen icon.

After you switch apps or lock the screen, a 1–2 second reconnect (short amber dot) is normal. Sessions never stop.

Still dropping? `gateway.log`, in the log folder that `pterm doctor` shows, logs each phone connection, its length and why it ended (for example `phone missed a ping`).

## "This pairing link expired or was already used"

Run `pterm pair` again and scan the new QR code. A pairing link works once, for two minutes.

## "Too many pairing attempts"

Wait a minute, then run `pterm pair` again.

## The phone shows the pairing screen again

Run `pterm pair` and [pair your phone](/docs/pair-your-phone/) again. The phone was revoked (`pterm revoke`), logged out, or expired (30 days, or 7 days unused).

## "READ-ONLY" and you cannot type

Tap **Take control**. Another device has control, or the phone reconnected and now only watches.

## "Delivery uncertain"

Check the terminal before you send again. The connection broke while you typed; Termivoa never resends input itself.

## "This session is gone"

Start a new session; a lost one cannot come back. Causes: the computer restarted or logged out, the broker (the background process that owns sessions) crashed, or the program closed the terminal but kept running.

A program that ends normally shows its exit code. On a busy computer, `v0.1.0-beta.2` can still show it as lost; the next beta fixes this.

## "A new version is ready"

Tap **Reload**; sessions keep running. `pterm` on the computer changed. The **update** pill on the Sessions list means the same.

## Notifications do not arrive

1. On iPhone (iOS 16.4 or later), open Termivoa from the Home Screen icon, not a Safari tab.
2. Turn on the switch in **Settings → Notifications**.
3. Allow Termivoa notifications in the phone's settings.
4. Tap **Send a test notification** in Settings.
5. Keep the computer awake; it sends them.

A "done" notification needs at least 20 seconds of agent work. More in [Notifications](/docs/guides/notifications/).

## macOS blocks pterm (Gatekeeper)

Download again with `gh release download`, as in [Install on macOS](/docs/install/macos/). macOS blocks the unsigned beta only when a browser downloads it.

## Windows shows a SmartScreen or Defender warning

Download again with `gh release download`, as in [Install on Windows](/docs/install/windows/). Defender may still warn, because the beta is not signed.

## `pterm: command not found`

Its folder is not on your PATH. Type the full path:

- macOS and Linux: `~/.local/bin/pterm`
- Windows: go to `%LOCALAPPDATA%\Programs\Termivoa` and type `.\pterm.exe`

## Setup does not ask the Tailscale questions (Linux)

Make yourself the Tailscale operator, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

## Setup says port 443 serves something else

Fix the conflict, then run `pterm setup` again. Tailscale Serve already shares another service on HTTPS port 443, and setup leaves it alone.

## No "Preview ready" chip

- Answer `y` to the preview question in setup. `pterm doctor` shows if previews are set up.
- Only servers that the session started are offered, not ones from another terminal window.

More in [Dev-server preview](/docs/guides/preview/).

## Sessions end when you log out of a server

Run `pterm setup` again and answer `y` to the linger question. More in [Keep sessions running on a server or VPS](/docs/install/linux/#keep-sessions-running-on-a-server-or-vps).

## Changes says "not a git repository"

`cd` into your project in the terminal, then tap **Refresh**. The session's folder is not in a git repository.

## New worktree says "not in a git repository" (Next beta)

`cd` into your repository, open the session menu (**⋯**) and tap **New worktree…** again. The session's folder is not in a git repository.

## New worktree says the name "already exists" (Next beta)

Tap 🎲 or type another name. The folder `~/termivoa/worktrees/<repository>/<name>` or branch `termivoa/<name>` exists. A removed worktree keeps its branch (and name) until you run `git branch -d termivoa/<name>`.

## New worktree says there are too many worktrees (Next beta)

Remove finished worktrees with **Remove worktree…** in their session menu (**⋯**), or run `git worktree remove <folder>`. The limit is 50 per repository in `~/termivoa/worktrees`.

## New worktree says "git worktree add failed" (Next beta)

Fix the cause in git's message, then reuse the same name; Termivoa cleans up what it made. Often a repository hook (for example husky or lefthook) fails on checkout.

## Remove worktree asks about uncommitted changes or ignored files (Next beta)

Tap **Remove anyway** only if you do not need those files, else **Keep it**. Uncommitted changes and ignored files (such as `node_modules`, `.env`) are deleted with the folder; the branch is kept.

## Remove worktree fails (Next beta)

Fix the cause in git's message, or run `git worktree remove <folder>` on the computer. On Windows, first close any program that uses the folder.

On Windows, "Connection lost" in place of "Worktree removed" can be normal, because the session ends first. If the session ended and the folder is gone, it worked.

## Commit fails

The phone shows git's message. Usual causes:

- **No name or email.** Run `git config --global user.name "Your Name"` and `git config --global user.email you@example.com` on the computer.
- **A commit hook failed.** Fix what the hook (for example a linter) says.
- **A merge or rebase is in progress.** Finish it in the terminal.

## Push fails or waits for a long time

- **No remote.** Add one, for example `git remote add origin <url>`.
- **The ssh key has a passphrase.** Add the key to your ssh agent (macOS: `ssh-add --apple-use-keychain ~/.ssh/id_ed25519`). Termivoa cannot type a passphrase; the push stops after 2 minutes.
- **The remote rejected it.** Read git's message. Termivoa never force-pushes.

## Where are the logs?

| System | Logs |
| --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal/logs/` |
| Linux | `~/.local/state/phone-terminal/logs/` |
| Windows | `%LOCALAPPDATA%\Phone Terminal\logs\` |

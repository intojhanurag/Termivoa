---
title: Troubleshooting
description: Fixes for common Termivoa problems, such as an unreachable computer, pairing errors, read-only sessions, missing notifications, worktrees and failed git pushes.
lastUpdated: 2026-10-10
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

## "Read-only" and you cannot type

Tap **Take control**. Another device has control, or the phone reconnected and now only watches.

## "Delivery uncertain"

Check the terminal before you send again. The connection broke while you typed; Termivoa never resends input itself.

## "This session is gone"

Start a new session; a lost one cannot come back. Causes: the computer restarted or logged out, the broker (the background process that owns sessions) crashed, or the program closed the terminal but kept running.

A program that ends normally shows its exit code, also on a busy computer. Before `v0.1.0-beta.3`, a busy computer could show it as lost.

## "A new version is ready"

Tap **Reload**; sessions keep running. `pterm` on the computer changed. The **update** pill on the Sessions list means the same.

## Notifications do not arrive

1. On iPhone (iOS 16.4 or later), open Termivoa from the Home Screen icon, not a Safari tab.
2. Turn on the switch in **Settings → Notifications**.
3. Allow Termivoa notifications in the phone's settings.
4. Tap **Send a test notification** in Settings.
5. Keep the computer awake; it sends them.

A "done" notification needs at least 20 seconds of agent work. More in [Notifications](/docs/guides/notifications/).

**Next beta:** a temporary push-service timeout keeps your subscription for the next notification. Many stalled subscriptions no longer use up the entire send window before another phone gets an attempt. **Send a test notification** checks whether the service accepts a message now; it cannot make an unavailable service deliver it.

## Next is not there after a notification tap

**Next beta.** Reload Termivoa once after an update, then tap a new notification. The **Next** button of [Needs you](/docs/guides/needs-you/) needs the updated app to be active; right after an update the old one can still handle the tap.

| You opened the session with | Next shows |
| --- | --- |
| A tap on a notification | Yes, when another item waits. With nothing else waiting, there is no row. |
| A tap on a row in the **Needs you** tab | Yes |
| A link, a typed address or a reload | No. This is as designed. |

## I hear a sound but see no notification

If the computer runs herdr, the sound is most likely herdr's own: herdr plays a sound when an agent in a background workspace changes state. Termivoa makes no sound on the computer.

| What you notice | Where it comes from | What to do |
| --- | --- | --- |
| A sound on the computer when an agent in a background workspace changes state | herdr. `ui.sound.enabled` is `true` by default. | To stop it, set it to `false`. |
| No popup on the computer | herdr. `ui.toast.delivery` is `"off"` by default. | Set it to `"herdr"`, `"terminal"` or `"system"`. |
| No notification on the phone | Termivoa notifies a phone only after notifications are on for that phone. | See [Turn on notifications](/docs/guides/notifications/#turn-on-notifications). |

The herdr settings are in `~/.config/herdr/config.toml` on the computer (macOS and Linux). Make the file if it is not there:

```toml
[ui.sound]
enabled = false

[ui.toast]
delivery = "herdr"
```

`delivery` can be `"off"`, `"herdr"`, `"terminal"` or `"system"`. Then run `herdr server reload-config`; if nothing changes, restart herdr.

**Next beta:** on a computer's page in **Settings**, the **herdr** part with a buzz switch on says "This phone gets no buzz yet." when Termivoa can tell that this phone has no notifications from that computer. The buzz switches are for every phone, so they can be on before this phone has notifications.

## macOS blocks pterm (Gatekeeper)

Download again with `gh release download`, as in [Install on macOS](/docs/install/macos/). macOS blocks the unsigned beta only when a browser downloads it.

## Windows shows a SmartScreen or Defender warning

Download again with `gh release download`, as in [Install on Windows](/docs/install/windows/). Defender may still warn, because the beta is not signed.

## `pterm: command not found`

Its folder is not on your PATH. Type the full path:

- macOS and Linux: `~/.local/bin/pterm`
- Windows: go to `%LOCALAPPDATA%\Programs\Termivoa` and type `.\pterm.exe`

## Setup says a folder "is writable by another user" (Linux)

A folder above Termivoa's data, usually `~/.local`, lets your group or everyone write to it. Ubuntu makes folders like that for a new user. Remove that write permission, then run setup again:

```sh
chmod go-w ~/.local ~/.local/bin
~/.local/bin/pterm setup
```

The error names the folder. Termivoa refuses such a folder because another user could replace its files.

## Setup does not ask the Tailscale questions (Linux)

Make yourself the Tailscale operator, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

## Setup says port 443 serves something else

Fix the conflict, then run `pterm setup` again. Tailscale Serve already shares another service on HTTPS port 443, and setup leaves it alone. If the message says "to free it, stop the `tailscale serve` or `tailscale funnel` command that runs in the foreground (Ctrl+C)", a `tailscale serve` without `--bg` still runs in a terminal: press Ctrl+C there.

## Setup asks to share the page again after a rename

Say yes. When you rename a computer in Tailscale, its Tailscale Serve settings stay under the old name, and the phone cannot reach the new name. `pterm setup` counts only the computer's current name, so it asks to share Termivoa there, and `pterm doctor` says `! tailscale serve: not configured for Termivoa` until you do. The settings under the old name do no harm.

## Setup refuses other Tailscale Serve handlers

Move the other handlers off this computer's Termivoa address, then run `pterm setup` again. Setup stops before it changes anything:

```text
refusing to set up: other Tailscale Serve handlers share this computer's Termivoa origin (/grafana); move them as shown above, then run `pterm setup` again
```

Any other handler on `https://<this-computer>` (port 443) is the same origin as the Termivoa app, so it could control this computer and, if it is home, every computer added to it. This also applies with one computer: if you run `pterm setup` again after the update, it refuses a setup that it accepted before. `pterm doctor` reports the same handlers as a problem.

The checks above the message print the commands. For each handler:

1. Serve it from another computer, or from its own [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) hostname, or stop serving it. Another port on this computer does not help: browsers send the Termivoa cookie to every port of a computer.
2. Remove it from port 443: `tailscale serve --https=443 --set-path=/grafana off`. For a TCP forward, run `tailscale serve --tcp=443 off`.

The Windows installer runs the same check. When it says that other Tailscale Serve handlers share this computer's Termivoa address (`pterm setup` exit code 3), run `pterm setup --apply` to see the handlers.

## Setup refuses because Tailscale Funnel is on

Turn Funnel off, then run `pterm setup` again. Funnel puts your terminal on the public internet, so setup stops before it changes anything while Funnel is on for port 443 or the preview port 8443:

```text
refusing to set up: Tailscale Funnel is on for mac.example.ts.net:443, which puts Termivoa on the public internet; turn it off with `tailscale funnel --https=443 off`, then run `pterm setup` again
```

Run the command it names (for port 8443: `tailscale funnel --https=8443 off`). Termivoa never runs it for you. The command also stops sharing that port in your tailnet, so `pterm setup` asks again to share Termivoa there.

If Funnel runs in the foreground (you ran `tailscale funnel` without `--bg`, and it still runs in a terminal), the message says "stop the `tailscale serve` or `tailscale funnel` command that runs in the foreground (Ctrl+C)". Go to that terminal and press Ctrl+C.

`pterm doctor` reports the same thing as a problem:

```text
! tailscale serve: Tailscale Funnel is on for mac.example.ts.net:443, which makes it public on the internet; Termivoa must never be public. Turn it off: `tailscale funnel --https=443 off`, then run `pterm setup` again.
```

## `pterm pair` refuses because Tailscale Funnel is on

Turn Funnel off with the command it names, then run the same `pterm pair` command again. Every `pterm pair` that pairs a phone or adds a computer (`pterm pair`, `pterm pair --via <home>`, `pterm pair approve <pending-id>`) stops before it makes a link while Funnel is on for port 443 or 8443:

```text
refusing to pair: Tailscale Funnel is on for vps.example.ts.net:443, which puts Termivoa on the public internet; turn it off with `tailscale funnel --https=443 off`, then run `pterm pair --via mac` again
```

`pterm launch` still starts Termivoa on this computer, but first prints a warning with the same command:

```text
Warning: Tailscale Funnel is on for vps.example.ts.net:443, which puts Termivoa on the public internet; turn it off with `tailscale funnel --https=443 off`. Pairing a phone is refused until it is off.
```

Termivoa never runs the command for you. When Termivoa cannot read the Tailscale Serve settings (no Tailscale, or Tailscale does not answer), `pterm pair` and `pterm launch` do not check.

## `pterm doctor` warns that other ports receive the Termivoa cookie

Decide if you trust what runs there as much as Termivoa. If not, move it to another computer or to its own Tailscale Service hostname, or remove it. The warning is:

```text
! tailscale serve (warning): other ports of this computer's host receive the Termivoa cookie, because browsers send it to every port of a host: HTTPS 8080 /. …
```

A handler on another port is a different origin, so it cannot use the Termivoa app. But when the phone opens it, the browser sends it the Termivoa cookie, and it could act as that phone. This is a warning, not a problem: setup does not refuse it, because many people serve their own tools on other ports. Termivoa's own preview port 8443 is not listed; Termivoa removes the cookie there.

## `pterm doctor` says Termivoa will not run tailscale

Fix the owner or permissions that the line names, or install Tailscale from [tailscale.com/download](https://tailscale.com/download). An example line is:

```text
! tailscale at /opt/homebrew/bin/tailscale: /opt/homebrew/bin/tailscale is owned by another user; Termivoa will not run it
```

Termivoa runs the Tailscale CLI only when no other user can replace it: the file, each symlink to it and each folder up to `/` must belong to root or to you, and no folder may be writable by others (unless it has the sticky bit, as `/tmp` does). On macOS a folder writable by the `admin` group, such as `/Applications`, is accepted. Termivoa checks this again before each run.

## The app cannot load after the update (iPhone)

Update the iPhone or iPad to iOS 16.4 or later. The computer accepts a request from the app only when the browser says where it comes from (the `Origin` or `Sec-Fetch-Site` header). Safari before iOS 16.4 sends neither on reads, so the computer refuses them. This is true for every Termivoa app, also with one computer. Android Chrome is not affected.

## The app cannot read settings after the update

Close the app fully and open it again, or tap **update** on the Sessions list. An app that was open while you updated the computer can fail to read settings until it reloads.

## No "Preview ready" chip

- Answer `y` to the preview question in setup. `pterm doctor` shows if previews are set up.
- Only servers that the session started are offered, not ones from another terminal window.

More in [Dev-server preview](/docs/guides/preview/).

## No "Preview ready" chip on a session of another computer

1. Update Termivoa on that computer. A computer on an older Termivoa shows no **Preview ready** chip.
2. Start the dev server in that session, for example `npm run dev`. Only servers that the session started are offered.
3. Check that the computer has no **can't reach**, **pair again** or **Update Termivoa on …** row on the Sessions list.

## "Can't open the preview on vps"

The full text is "Can't open the preview on vps. If vps does not share dev-server previews yet, run pterm setup on vps and answer yes to the preview question." Nothing answered at that computer's preview address within 5 seconds. The app cannot see why, so check in this order:

1. On that computer, run `pterm setup` and answer `y` to the preview question. Each computer answers it for itself.
2. On that computer, run `pterm doctor`. It checks the preview setup.
3. Check that your Tailscale access rules (ACLs) let the phone reach port 8443 of that computer.
4. Close the app fully and open it again.
5. Open the preview again and tap **↻**.

If you added the computer after you opened the app, the app reloads itself once when you first open a preview on it. This is normal; you do nothing.

## The preview shows a short text and no page

Tap **↻**. If the text stays, do what the table says. The computer that runs the session writes this text, not the app.

| Text in the frame | What to do |
| --- | --- |
| "Origin not allowed." | Tap **↻**. In Safari this can show after the browser's Back to the app. |
| "Open the preview from the Termivoa app." | Tap **↻**. If it stays, close the app fully and open it again, so it updates. |
| "Open the preview from your paired phone." | Tap **↻**. |
| "This phone is no longer paired. Open Termivoa to pair again." | Pair again. For another computer: run `pterm pair --via <home>` on it and add it again. For home: run `pterm pair`. |
| "This app may not use this computer." | Run `pterm pair --via <home>` on that computer and add it again. |
| "The computer is not available." | Check that Tailscale runs and is logged in on that computer (`pterm doctor`), then tap **↻**. |
| "The dev server did not answer.", "No dev server of this session listens on that port." or "The session is not running." | Start the dev server in that session again, then tap **↻**. |

A text that stays after **↻** on another computer: pair that computer again through home (`pterm pair --via <home>` on it, then add it in the app).

## Sessions end when you log out of a server

Run `pterm setup` again and answer `y` to the linger question. More in [Keep sessions running on a server or VPS](/docs/install/linux/#keep-sessions-running-on-a-server-or-vps).

## Why did a commit, push or worktree creation time out?

Termivoa allows about two minutes for a git write. A slow hook or remote can
exceed that. **Next beta:** git and its ordinary hook children are stopped
before failure is reported or a failed worktree is cleaned up. Check the
branch and files before retrying: a completed commit or push is not undone.
Fix the hook or connection on the computer. Termivoa does not automatically
send the write again. A program that a hook deliberately detached on macOS
or Linux is outside this cleanup; manage it on the computer.

## Changes says "not a git repository"

`cd` into your project in the terminal, then tap **Refresh**. The session's folder is not in a git repository.

## New worktree says "not in a git repository"

`cd` into your repository, open the session menu (**⋯**) and tap **New worktree…** again. The session's folder is not in a git repository.

## New worktree says the name "already exists"

Tap 🎲 or type another name. The folder `~/termivoa/worktrees/<repository>/<name>` or branch `termivoa/<name>` exists. A removed worktree keeps its branch (and name) until you run `git branch -d termivoa/<name>`.

## New worktree says there are too many worktrees

Remove finished worktrees with **Remove worktree…** in their session menu (**⋯**), or run `git worktree remove <folder>`. The limit is 50 per repository in `~/termivoa/worktrees`.

## New worktree says "Termivoa reached a limit on this computer"

**Next beta.** Termivoa creates one worktree at a time on each computer.
Wait for any earlier create to finish, then try **Create** again. If no
create is running, remove a finished worktree or end a finished session to
make room. Extra requests are refused immediately and do not run later.

If the app closed while a create was already running, check Sessions for
its worktree before making another. The computer can still finish that
create after the phone leaves.

## New worktree says "git worktree add failed"

Fix the cause in git's message, then reuse the same name; Termivoa cleans up what it made. Often a repository hook (for example husky or lefthook) fails on checkout.

## Remove worktree asks about uncommitted changes or ignored files

Tap **Remove anyway** only if you do not need those files, else **Keep it**. Uncommitted changes and ignored files (such as `node_modules`, `.env`) are deleted with the folder; the branch is kept.

## Remove worktree fails

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

## Several computers

These problems are about [using several computers](/docs/guides/computers/).

### "vps · can't reach"

The line under it is "Last seen 3 h ago. Is it on, and in your tailnet?" when nothing answered from vps. The app cannot see why, so check that vps is on, awake and in your tailnet, then tap **Retry**. The other computers keep working. Without the question, vps answered with an error: run `pterm doctor` on vps.

### "This app page is out of date. Close the app and open it again."

It shows under "vps · can't reach". The app page on your phone does not allow vps yet, so the phone sends nothing to vps. This happens when the page is older than the computer, for example when the phone opened its saved copy because home answered slowly. The app reloads once by itself; this line means the reload did not help. Close the app fully (swipe it away) and open it again. If it stays, check that home is reachable, because a fresh page comes from home.

### "Connecting to vps…"

The full text is "Connecting to vps…" and "Just added. Its sessions show in a moment." You added vps a moment ago and it does not answer this phone yet. The app tries again by itself for about 5 seconds. If vps still does not answer, the row says "vps · can't reach".

### "vps · pair again"

The full line is "vps · pair again · Run pterm pair --via mac on vps". vps no longer accepts this phone: it was revoked or logged out, it expired (30 days, or 7 days unused), or home's Tailscale node changed (for example after you reinstalled Tailscale on home). Run `pterm pair --via <home>` on vps and add it again.

### "Update Termivoa on vps"

Update Termivoa on that computer. Computers can be updated one at a time; the app refuses only one that is too old, or one so old that it does not tell its version. The line uses the name you gave the computer on this phone.

### A computer is gone from the list

Add it again: run `pterm pair --via <home>` on that computer. Home pins each computer's Tailscale machine (its node ID). If that changes, for example after you reinstall Tailscale on the computer or give its name to another machine, home drops the computer from the list. When you add it again, home pins the new machine; this also works when the list is full, because dropped computers give up their place. If the name moves to another machine while you add it, the app says "this name now belongs to another Tailscale machine; add the computer again". A computer you removed from your tailnet stays in the list as "can't reach"; remove it on its page in **Settings** (tap its card, then **Remove**).

### Log out says "Could not sign out of vps"

Run `pterm devices`, then `pterm revoke <device-id>` on vps. **Log out** signs the phone out of every computer it can reach; it lists the ones it could not reach.

### `pterm pair --via` says "no computer with that name is in this tailnet"

Use home's machine name as Tailscale shows it (the first part of its address), or its full address, for example `pterm pair --via https://mac.tailx.ts.net`. Both computers must be in the same tailnet.

### `pterm pair --via` says "more than one computer has that name"

Use home's full address: `pterm pair --via https://mac.tailx.ts.net`.

### `pterm pair --via` says "that computer belongs to another Tailscale user"

Home and the new computer must belong to the same Tailscale user. Computers of other users cannot be added.

### `pterm pair --via` says "that computer is a tagged Tailscale node"

Check the tags of home and this computer, then run `pterm pair --via <home> --allow-tagged`.

### `pterm pair --via` says "this computer cannot read its Tailscale status"

Check that Tailscale runs and is logged in on this computer (`pterm doctor`), then try again. Under WSL, Tailscale usually runs on Windows, so a WSL computer cannot be added.

### `pterm pair --via` says the link "needs this computer's tailnet https address"

Run `pterm setup` on this computer so it has a Tailscale address, or pass it with `--origin https://<this-computer>.<tailnet>.ts.net`.

### The pasted link is refused

| Message | What to do |
| --- | --- |
| "This is not an add-computer link. Copy the whole link that pterm pair --via printed." | Copy the full link, from `https://` to its end. |
| "That computer is not in the same tailnet as this one." | Only computers in one tailnet can be added. |
| "That link is for this computer. Run pterm pair --via on the other computer." | Run the command on the computer you want to add, not on home. |
| "That link was made for another Termivoa app. Run pterm pair --via with this computer's name." | Run `pterm pair --via` again with the name of the home that serves your app. |
| "The link is damaged or incomplete. Copy it again." | Copy it again; long links can wrap or get cut. |
| "This add-computer link expired or was already used." | Run `pterm pair --via` again. A link works once, for two minutes. |

### The code expired, or the computer refused it

The app says "The code expired or vps refused it. Run pterm pair --via on vps again, then tap Add again, or scan or paste the new link." Run `pterm pair --via <home>` on that computer again. Then tap **Add** next to it in **Found on your network**, or scan or paste the new link. Answer `y` only when the prompt names that computer and the codes match.

### No Add button for a new computer

**Found on your network** in **Settings** shows a computer only while it waits in `pterm setup` or `pterm pair --via` in a terminal.

| You see | What to do |
| --- | --- |
| No **Found on your network** block at all | Home runs an older Termivoa. Update Termivoa on home, then reload the app. Until then, use **+ Add a computer → Scan QR** or **Paste link**. |
| "To add another computer, run pterm setup on it, or pterm pair --via mac if it is already set up. It shows here while it waits." | Nothing waits now. Start the wait on the new computer and keep its terminal open. Check what that terminal says (see the next entries). |
| "mac cannot read its Tailscale status. Check Tailscale on mac." | Check that Tailscale runs and is logged in on home (`pterm doctor` there). |
| The computer is a tagged Tailscale node, or home is | **Add** does not work with tagged computers. Use the QR code or the link. |
| The computer is already in your list and answers | It is not shown again. |

The QR code and the link always work as the other way: **Settings → + Add a computer → Scan QR** or **Paste link**.

### "vps stopped waiting"

The full text is "vps stopped waiting. On vps run pterm pair --via mac, then tap Add again." The new computer no longer waits (it was stopped, or its 10 minutes ended), or the invite in the list was no longer valid. Run `pterm pair --via mac` on vps, then tap **Add** again.

### `pterm pair --via` says "mac did not answer" or "cannot receive this yet"

These lines show on the new computer when the app at home cannot list it. The QR code and the link below them still work.

| The new computer says | What to do |
| --- | --- |
| "mac did not answer. Use the QR code or the link below." | Check that home is on and in your tailnet, and that `pterm doctor` on home is clean. Or use the QR code or the link. |
| "mac is busy right now. Use the QR code or the link below." | Home got too many offers in a short time. Wait a little; the computer tries again every 30 seconds. Or use the QR code or the link. |
| "Termivoa on mac cannot receive this yet. Update it, or use the QR code or the link below." | Update Termivoa on home, or use the QR code or the link. |
| "This computer cannot read its Tailscale status. Use the QR code or the link below." | Check that Tailscale runs and is logged in on the new computer (`pterm doctor`). |
| "This computer could not offer itself to mac. Use the QR code or the link below." | Termivoa on this computer could not send the offer. Use the QR code or the link, and run `pterm doctor` on this computer. |
| The command ends with "… Restart Termivoa on this computer (pterm shutdown, then pterm launch), then try again" | An older Termivoa still runs on this computer after an update. Run `pterm shutdown`, then `pterm launch`, then the command again. |
| "Add needs this computer's address: run pterm setup, or pterm config origin https://<this computer>. Use the QR code or the link below." | This computer has no saved Tailscale address. Run `pterm setup` (or `pterm config origin https://<this computer>.<tailnet>.ts.net`). |
| "mac is now a different Tailscale machine. Run pterm pair --via mac again." | Home's Tailscale machine changed while you waited (for example a reinstall). Run the command again. |
| "Nobody added this computer in 10 minutes. Run pterm pair --via mac again." | Run it again, and tap **Add** in the app while it waits. |
| "the link expired before a phone opened it; run `pterm pair --via mac` again" | Nothing reached home, and the link expired after 2 minutes. Run it again. |
| `pterm setup` ends with "Setup is done, but your phone is not connected yet. To connect it, run `pterm pair --via mac`." | Setup worked, but nobody added the computer in 10 minutes. Run `pterm pair --via mac`, then tap **Add** in the app. |

### `pterm pair --via` says "Add does not work with tagged computers"

The full line is "Add does not work with tagged computers: mac is tagged. Use the QR code or the link below." or "…: this computer is tagged. …". Home or this computer is a tagged Tailscale node, so home cannot show it in **Found on your network**. Scan the QR code in the app (**Settings → + Add a computer → Scan QR**) or paste the link.

### The link opens a page that says "This page cannot add the computer"

You scanned the QR code with the phone's Camera app, or opened the link in Safari. Open the Termivoa app from the Home Screen and go to **Settings → + Add a computer → Scan QR**, then scan the same QR code there. Or tap **Copy link** on that page and use **Paste link** in the app.

### Scan QR does not open the camera

| The app says | Do this |
| --- | --- |
| "The camera is blocked. Allow the camera for this app in your phone or browser settings, then tap Scan QR. Or paste the link." | Allow the camera for the Termivoa address in your phone's or browser's settings, then tap **Scan QR** again. |
| "No camera found. Paste the link." | Tap **Paste link**. |
| "The camera did not start. Close other apps that use it, then tap Scan QR. Or paste the link." | Close other camera apps and try again, or tap **Paste link**. On iPhone this also shows when a call takes the camera for 2 seconds. |

**Paste link** always works without the camera.

### `pterm doctor` says the tailnet is unknown

Log in to Tailscale on this computer, then run `pterm doctor` again. The line is `this computer's tailnet is unknown (is Tailscale logged out?); bound app origins cannot be checked`. A short Tailscale restart or logout does not end the sessions of phones that use this computer through home: the computer keeps its last good Tailscale status for up to 10 minutes.

### `pterm doctor` says "tags changed since pairing"

Pair the phone again: run `pterm pair --via <home>` on this computer. Home's Tailscale tags were added, removed or replaced after pairing, so the rules for it changed. This computer refuses phones from that home until you pair again.

### Settings shows "No notifications from vps."

That computer could not be set up for notifications; home and the other computers still notify. Check that the computer is awake and reachable (no **can't reach** or **pair again** on the Sessions list), then open **Settings** again. If it stays, turn the switch in **Settings → Notifications** off and on.

## herdr

:::note[Next beta]
These entries are not in `v0.1.0-beta.3`. See [Termivoa + herdr](/docs/guides/herdr/).
:::

### The herdr tab is missing

Check each cause, then run `pterm doctor` on the computer; it has a `herdr:` line when it finds herdr.

- herdr is not installed. Install herdr 0.8.2 or later.
- Termivoa cannot find it. It looks in the Homebrew folder (macOS), `/usr/local/bin`, `/usr/bin` (Linux), `~/.local/bin` and `~/.cargo/bin`, and then on the `PATH` that Termivoa runs with. It checks again about every 30 seconds.
- **On** is off on that computer's page in **Settings**. Turn it on.
- Termivoa on that computer is older than the next beta. Update it.

### The herdr card says "Not installed"

herdr is not on that computer. Tap **Install herdr**: Termivoa opens a session there with herdr's install command typed in. Press Enter to run it, wait until it ends, then tap **Check again** on the card. The card then says **Not running** and has **Start herdr**.

If the **herdr** part says "Run `pterm doctor` on mac." and has no **Install herdr**, a herdr program is there but Termivoa will not use it. `pterm doctor` names the reason and the fix.

### "herdr did not start"

Termivoa started herdr's server, but it did not answer in time. Run `herdr server` in a terminal on that computer to see why. On a peer computer the app says only "That computer could not do this."; the fix is the same.

### "codex is not running in herdr now."

Termivoa offers an agent only while one of that kind runs in herdr on that computer. The agent you picked in **New worktree** or **New tab** left herdr before you tapped the button. The sheet goes to no agent; it never starts another agent in its place. Pick an agent that is listed, or start that agent in herdr first.

### "The tab is ready, but the agent did not start."

herdr made the tab, but no agent showed in it within about 15 seconds (the time that herdr gives an agent to start). Open the tab and look at its terminal. If it says "command not found", that agent is not installed on the computer. Else start the agent there. The **New worktree** sheet says "The worktree is ready, but the agent did not start." for the same cause.

If the app says "The agent started, but your first prompt was not sent.", the agent was not ready for input in time (about 20 seconds). Type the prompt in the terminal.

### "The list changed. Look again, then try again."

The workspace was renamed, closed, or herdr restarted after the phone read the list. Nothing was closed or removed. Look at the row again and repeat the action.

### "silver-meadow has changes that are not committed"

Termivoa never removes a worktree that has modified or new files. Open its terminal, commit or discard the changes, then tap **Remove worktree** again.

### "Close its worktrees first."

A project's main checkout cannot be closed while one of its worktrees is open in herdr. Close or remove each worktree, then close the main one.

### "herdr closes the workspaces at this project's main folder only together."

herdr lists two workspaces under one project, and both are at that project's main folder (herdr has its worktree record of the same repository for each). herdr does not close one of them alone: herdr 0.9.3 refuses, and herdr 0.8.2 closes both. Termivoa never closes two workspaces in one tap, so **Close workspace** is off for both. Close them in herdr on that computer. A second workspace in the same folder that herdr lists as its own project is not part of this: it closes alone (tested on herdr 0.8.2 and herdr 0.9.3).

### "herdr is not running on mac"

Start herdr's server on that computer, for example open herdr there. Its projects then show on the phone. If herdr already runs there, run `pterm doctor` on that computer. A line that starts with `! herdr:` says why Termivoa does not use it.

### `pterm doctor` says Termivoa will not use the herdr socket

The line looks like this:

```text
! herdr: /usr/local/bin/herdr, Termivoa will not use the server socket /home/me/.config/herdr/herdr.sock: group or others can write /home/me/.config/herdr and /home/me/.config; run: chmod go-w /home/me/.config/herdr /home/me/.config
```

Another user could reach herdr's socket through a folder that is not private to you, so Termivoa does not use it. The phone then says "herdr is not running on mac". Run the command that the line prints, for example:

```sh
chmod go-w ~/.config/herdr ~/.config
```

This is common on Ubuntu: its default file mode lets your own group write the folders that you make, such as `~/.config`. Termivoa refuses these folders even when the group is your own private group, because it cannot prove that nobody else is in that group. Termivoa uses the same rule for its own folders. Within about 10 seconds after the fix, herdr shows on the phone.

### "Update herdr on mac"

herdr is older than 0.8.2. Update herdr on that computer.

### "herdr on vps · can't reach"

Tap **Retry**. If it stays, check that the computer is on and Termivoa runs there.

### "That terminal is gone."

The herdr pane closed, or herdr restarted. Pick the row again in the **herdr** tab.

### The terminal is small on the laptop

This is on purpose. herdr gives the pane the phone's size while it is open on the phone. Leave Live, or lock the phone; about 10 seconds later the pane has its own size again.

### "mac cannot open more herdr terminals just now"

There are two limits. At most 4 herdr terminals can be open at once for each computer, and each phone can open 20 a minute. Leave some, wait about 10 seconds, or wait a minute.

### "herdr is off on mac"

**On** is off on that computer's page in **Settings**. Turn it on.

### "Still working on mac… It shows in the list when it is ready."

A new worktree took longer than the phone waits (45 seconds). The computer may still finish it. Close the sheet and look for the new worktree in the **herdr** tab. Do not create it again with the same name.

### A tab chip is off: "Update herdr to open this tab"

herdr is too old to open a tab that has no agent. Update herdr.

### "herdr on mac did not open this terminal. Another program may hold it, or it just closed."

Someone attached to that terminal directly in herdr, or its pane closed at that moment. Termivoa never takes a terminal over. Close that attach in herdr, then tap **Try again** or open the row again.

## GitHub

:::note[Next beta]
These entries are not in `v0.1.0-beta.3`. See [Set up a GitHub repository](/docs/guides/github/).
:::

### There is no GitHub part on a computer's page in Settings

Check each cause. `pterm github status` on the computer says "GitHub: not available in this build" when the build has no GitHub.

- Termivoa on that computer is older than the next beta. Update it.
- The computer runs Windows. GitHub is for macOS and Linux only.
- The Termivoa build has no GitHub App. Use a release build of the next beta.

### "git is not on mac. GitHub needs it."

Tap **Install git** on the row. It opens a terminal on that computer with an install command typed in: Apple's developer tools on macOS, `sudo apt-get install -y git` on Linux. Press Enter there, then tap **Check again**. Termivoa needs git 2.31 or later; on another Linux, use its package manager.

### "The code ran out."

You did not enter the code on GitHub within 15 minutes. Tap **Get a new code**, then **Copy code and open GitHub** again.

### "GitHub did not connect."

You cancelled on GitHub's page, or GitHub refused. Tap **Try again**.

### "Termivoa sees no repository yet."

Termivoa can see only the repositories that you chose for it on GitHub. Tap **Choose on GitHub** and pick the repositories. A repository that is still not in the list: tap **Choose repositories on GitHub** under the list.

For a repository of an organization where you are not an owner, GitHub sends a request to the organization's owners. The repository shows after an owner approves it.

### "Another program used this login. Revoke Termivoa on GitHub, then connect again."

Something other than this computer's Termivoa used its GitHub login, for example a copy of the login file. Someone may have the login. Tap **Revoke Termivoa on GitHub** and revoke **Termivoa** under **Authorized GitHub Apps**, then tap **Connect** on each computer. Connecting again without the revoke does not stop the other program. See [What does GitHub change?](/docs/reference/security/#what-does-github-change)

### "GitHub is not connected on mac now."

The login ended: you revoked Termivoa on GitHub, or the computer did not use it for 6 months. Tap **Connect**.

### "mac cannot reach GitHub."

The computer has no internet, or GitHub does not answer. Wait, then tap **Retry**.

### A repository says "name taken"

A folder of that name is already in the home folder of that computer (`~/<name>`), and it is not this repository. Termivoa never deletes or overwrites it. Rename or move that folder on the computer, then open the list again. Two repositories with the same name from two owners cannot both be set up on one computer.

### "mac has too little free space for this repository."

The computer needs free space of 3 times the repository's size, plus 1 GB. Free some space, then tap **Set up** again.

### "The clone stopped. Nothing was left on mac."

The clone failed, or you tapped **Stop**. Termivoa removed the partial clone. Tap **Try again**. If it stops again, check the computer's internet.

### "The clone took more than 30 minutes and stopped."

A clone can take at most 30 minutes. For a very large repository, clone it in a session on the computer, then add it with `pterm project add`.

### "That repository is not on GitHub under this name now."

The repository was renamed, moved or deleted after the list was read. The list reloads. Pick the new name.

### "Large files were not downloaded. Run `git lfs pull` in a session."

The repository uses Git LFS, and Termivoa does not download large files. Open a session in the repository and run `git lfs pull`. This needs Git LFS on the computer.

### A push that changes a workflow file is refused

GitHub refuses a push from Termivoa's login when it changes a file in `.github/workflows`. Termivoa's login does not have that permission. Push that change from a computer or a folder that uses your own git login.

### git fails with "Termivoa is not connected to GitHub on this computer."

You disconnected GitHub on that computer, and the folder was cloned by Termivoa, so git uses Termivoa's login there. Connect again: in **Settings**, tap the computer's card, then **Connect**. Or, to use your own git login in that folder, remove Termivoa's lines from the folder's git settings:

```sh
git config --unset-all credential.https://github.com/<owner>/<name>.git.helper
```

### git fails with "Termivoa is not running on this computer"

The folder was cloned by Termivoa, and git asks Termivoa for the GitHub login, but Termivoa is stopped. Run `pterm launch` on that computer, then try again.

## Where are the logs?

| System | Logs |
| --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal/logs/` |
| Linux | `~/.local/state/phone-terminal/logs/` |
| Windows | `%LOCALAPPDATA%\Phone Terminal\logs\` |

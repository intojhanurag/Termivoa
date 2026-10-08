---
title: Release notes
description: Termivoa private beta release history, with the new features, fixes and known limits of each version, and what is coming in the next beta.
lastUpdated: 2026-10-08
---

The latest Termivoa release is the private beta `v0.1.0-beta.3` (2026-10-04).

## Next beta (not released yet)

New features on `main` show here until the next beta ships.

- **Terminal screen bounds.** The computer rejects malformed row positions
  in terminal screen updates; the phone already applies that limit. See
  [What limits apply to the terminal screen?](/docs/guides/live-terminal/#what-limits-apply-to-the-terminal-screen).

- **GitHub from the phone.** **Settings → Integrations** has a row **GitHub on mac** for each computer. Tap **Connect**, enter the code on GitHub, and choose your repositories one time. Then **Set up a repository** lists them, with search, and one tap clones a repository into `~/<name>` on that computer and adds it as a project, with progress, **Stop** and **New session**. **From GitHub** is also next to **Add a folder** in herdr's **New workspace** sheet. No `gh` is needed, only git. Termivoa never deletes or overwrites a folder: a folder of that name is **name taken**. Pull and push work in the cloned folder. `pterm github status` and `pterm github disconnect` are new. See [Set up a GitHub repository](/docs/guides/github/) and [What does GitHub change?](/docs/reference/security/#what-does-github-change)
- **Needs you.** While an agent waits, the Sessions list has a **Needs you** tab with a count, right after **All**. It lists every session that waits and every herdr worktree that is blocked, on all your computers, the longest wait first; **Finished** work is below. Tap one to open it, answer, then tap **Next · 2 left ›** to go to the next one. A tap on a notification opens the item with **Next** too. See [Needs you](/docs/guides/needs-you/).
- **More herdr from the phone.** **New tab** can start an agent with a first prompt. A worktree whose workspace you closed shows as **CLOSED** with **Open**. A herdr terminal has the **⋯** menu of its workspace, and **‹ Sessions** goes back to the **herdr** tab.
- **herdr fixes.** The first prompt of a new agent now reaches it: Termivoa waits until herdr says the agent is ready. "The agent did not start" also shows when the agent is not installed on the computer. A herdr terminal outside git no longer shows "this herdr terminal's folder cannot be read" by itself. **New worktree** and **New tab** offer the agents that run in herdr now, and never start another agent than the one you picked. A slow herdr check no longer turns off tabs without an agent. Two workspaces at the main folder of one repository cannot be closed from the phone (herdr closes them only together), and the app says so.
- **Fixed: buzz switches that look on while this phone gets no buzz.** A herdr card in **Settings → Integrations** now says "This phone gets no buzz yet." with a link to **Settings → Notifications**. See [I hear a sound but see no notification](/docs/reference/troubleshooting/#i-hear-a-sound-but-see-no-notification).
- **Fixed: test notifications could stop real ones.** A phone that sent a test every 5 seconds could use up the limit of 12 notifications per minute. **Send a test notification** now has its own limit (6 per minute for all phones together). Also, up to 3 phones whose push service does not answer no longer hold back the notifications of the other phones, and with two computers of the same name, only the right herdr card says "This phone gets no buzz yet." See [When do notifications come?](/docs/guides/notifications/#when-do-notifications-come)
- **Fixed: a lost notification.** When three or more sessions needed you at the same time and the push service was slow, one notification could be lost. It is no longer lost this way.
- **Manage herdr from the phone.** In the **herdr** tab, the **⋯** menu on a row has **New tab**, **Close tab**, **Close workspace** and **Remove worktree** (each asks first; a worktree with changes that are not committed is never removed), and **New workspace** opens one of your Termivoa projects in herdr. **+** now shows on every git project. **Settings → Integrations** has **Start herdr** when herdr does not run, and **Install herdr** on a computer without it (it opens a session with herdr's install command typed in; you press Enter). See [Termivoa + herdr](/docs/guides/herdr/#how-do-i-manage-workspaces-and-worktrees).
- **herdr on your phone.** If a computer runs [herdr](https://herdr.dev) 0.8.2 or later, the Sessions list gets a **herdr** tab with its projects and worktrees, and state words (**BLOCKED**, **DONE**, **WORKING**, **IDLE**). Tap a worktree to open its herdr terminal in Live, read-only until you tap **Take control**. The phone buzzes when a herdr agent is blocked or finished. **Settings → Integrations** turns herdr and its buzzes on or off for each computer, and **+** on a git project makes a new worktree and can start an agent in it. There is no setting to turn on: Termivoa finds herdr by itself. See [Termivoa + herdr](/docs/guides/herdr/).

## v0.1.0-beta.3 (2026-10-04) — private beta

The third private beta. The binaries are not signed.

:::caution[Updating from beta.2]
This update ends running sessions, and some changes break old setups:

- **Start with a fresh database.** This beta does not read data from older betas. After you replace `pterm`, run `pterm uninstall --delete-data`, then `pterm setup`, and pair your phone again. Without this, `pterm` stops with "delete the database file to start fresh".
- **iPhone needs iOS 16.4 or later**, also with one computer. See [The app cannot load after the update](/docs/reference/troubleshooting/#the-app-cannot-load-after-the-update-iphone).
- **`pterm setup` refuses other Tailscale Serve handlers on port 443** of this computer, and prints the commands to move them. See [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers).
- **Close the app and open it again** after the update. An app that stayed open can fail to read settings until it reloads.

See [Update and uninstall](/docs/reference/update-and-uninstall/).
:::

### What is new

- **All your computers in one app.** Use a Mac, a Linux server and a VPS from one Home Screen app: one Sessions list with a tab for each computer, one Wall, and **❯ new session on…** any computer. Each computer approves the phone itself. **Settings → Computers** shows each computer's version, when it last answered, and **Check again**. Read [Choose home](/docs/guides/computers/#how-do-i-choose-home) first. See [Use several computers](/docs/guides/computers/).
- **Found on your network.** On the new computer, run `pterm setup` and answer `2`, or run `pterm pair --via <home>`. In the app, tap **Add** next to it under **Settings → Computers → Found on your network**, check the code, and type `y` on the new computer. See [How do I add a computer?](/docs/guides/computers/#how-do-i-add-a-computer)
- **Scan QR.** In **Settings → + Add a computer**, scan the QR code that `pterm pair --via` prints, inside the app. **Paste link** still works. See [How do I scan or paste the link?](/docs/guides/computers/#how-do-i-scan-or-paste-the-link)
- **Preview from every computer.** Open the dev-server preview of a session on any of your computers. Each computer needs its own yes to the preview question in `pterm setup`. A page from another computer is marked **untrusted page**. See [Can I preview a session on another computer?](/docs/guides/preview/#can-i-preview-a-session-on-another-computer)
- **Notifications from every computer.** One switch in **Settings → Notifications** controls all of them. A notification from another computer starts with its name, such as `vps · …`. See [Do notifications come from every computer?](/docs/guides/notifications/#do-notifications-come-from-every-computer)
- **A new look: Bone & ink.** Flat and quiet: an ink background, serif titles, one bone button for each screen, and rows between thin lines. Cyan means live or connected, amber means a warning or your turn, and red means an error.
- **A calmer setup and add screen.** `pterm setup` prints a short plan and asks once; `pterm setup --verbose` shows every step. When it sees your other computers, it asks **Is Termivoa already on your phone?** While you add a computer, the terminal shows a short wait screen; the QR code and the link show when you press Enter. See the [CLI reference](/docs/reference/cli/).
- **Funnel checks.** Tailscale Funnel puts your terminal on the public internet. `pterm setup` and every `pterm pair` now stop while Funnel is on for port 443 or 8443, and print the command that turns it off. `pterm launch` warns. See [Setup refuses because Tailscale Funnel is on](/docs/reference/troubleshooting/#setup-refuses-because-tailscale-funnel-is-on).
- **Worktrees.** Start a task in its own folder and branch from the session menu (**⋯ → New worktree…**). See [Worktrees](/docs/guides/worktrees/).
- **Files.** Browse, search and read the session's project, read-only. Tap a path such as `src/app.ts:42` in the terminal to open that line. See [Files](/docs/guides/files/).
- **Project buttons.** Your project's own commands from `package.json`, a `Makefile`, `Cargo.toml` or `go.mod`, in the Snippets sheet. See [Project buttons](/docs/guides/project-buttons/).
- **Ask about specific lines.** In a diff, tap a line or a range and ask the agent about it. See [Ask about specific lines](/docs/guides/changes/#ask-about-specific-lines).
- **A better Wall.** Hold and drag tiles to arrange them. At most 4 tiles are live at once; a paused tile that was never live shows a small picture. See [Wall](/docs/guides/wall/).
- **Down next to Up** in the key bar while you type, and a **faster first open**.

### Fixes

- **No dead taps.** Buttons that wait for a computer show it, for example "Saving…" or "Starting…", and a double tap starts only one session.
- **Screens fit a narrow phone.** Long commands wrap inside their box at 375 and 390 px.
- **Setup and Tailscale.** Setup after you rename a computer asks to share the page at the new name. On Ubuntu, setup no longer stops on a group-writable `~/.local`. On a Mac, adding a computer no longer fails with "service unavailable".
- **`pterm launch` on a busy computer** waits up to 30 seconds for a service, as setup does.
- **Notifications** work again after **Log out** or `pterm revoke` and a new pairing.
- **Sessions.** A finished session shows its exit code, not lost. Ctrl+] right after `pterm attach` detaches cleanly. The open tab is always in view, and the folder name stays in the status line.

### Known limits

- Several computers, Found on your network, Scan QR and previews from other computers are not fully tested on a real iPhone or Android phone yet.
- The binaries are not signed. Download them with `gh`, not with a browser.

## v0.1.0-beta.2 (2026-09-29) — private beta

The second private beta. The binaries are not signed.

:::caution
Updating from beta.1 ends running sessions. Finish your work first. See [Update and uninstall](/docs/reference/update-and-uninstall/).
:::

### What is new

- **Wall.** Watch all running sessions as small live screens. Tiles only watch; tap one to open it. See [Wall](/docs/guides/wall/).
- **Changes view.** See what the agent changed: changed files with +/− counts, and each file's diff in red and green. See [Changes](/docs/guides/changes/).
- **Commit and push from the phone.** Pick files, edit the suggested message, commit, then push. It never force-pushes and needs control.
- **"While you were away" card.** When you open a session, see how long the agent worked, what changed, and whether it waits for you. See [Recap](/docs/guides/recap/).
- **Swipe to scroll full-screen apps.** Swipe on the terminal to scroll Claude Code, Codex, `vim` or `less`.
- **Much faster reconnect.** After you unlock the phone, it reconnects in seconds. A working connection is kept; a stuck attempt is retried after 5 seconds.
- **Connection log.** `gateway.log` logs each phone connection, its length and why it ended.

## v0.1.0-beta.1 (2026-09-28) — private beta

The first private beta, for invited testers only. The binaries are not signed.
Install it with [Quick start](/docs/quick-start/).

### What you can do

- **Run sessions from your phone.** Start shells and coding agents on your computer; they keep running when the phone is away.
- **Many sessions at once.** Switch with tabs. A dot shows new output.
- **Status badges.** See which sessions are **Working**, **Waiting** or **Your turn**.
- **Answer cards.** Answer Claude Code and Codex questions with one tap. A changed question is never answered by mistake.
- **Notifications.** Get a buzz when an agent waits for you or finishes. No terminal text leaves your computer.
- **Dev-server preview.** Open a page from `npm run dev` inside the app.
- **Photo and voice.** Send a photo to the agent without location data, or dictate a prompt.
- **Agent keys, snippets and presets.** One-tap keys for agent prompts, saved prompts, and one-tap session starts.
- **Live folder name.** A session's name shows its current folder, unless you renamed it.
- **Desktop attach.** Open the same session in a terminal on your computer with `pterm attach`.
- **Safe pairing.** Scan a QR code, compare a code, approve on the computer. Remove a phone at any time.
- **Three platforms.** macOS (Apple Silicon), Linux (Ubuntu desktop, server and WSL) and Windows 11.
- **Fast reconnect.** The phone reconnects within seconds after a lock or a network change.

### Known limits

- Real-device testing so far covers a Mac and an iPhone. Linux was tested in containers only. Windows and Android are not yet tested on real devices.
- The binaries are not signed. Download them with `gh`, not with a browser.
- There is no automatic updater. See [Update and uninstall](/docs/reference/update-and-uninstall/).
- The computer must stay awake and logged in. A restart, a logout or a broker crash ends all sessions. They are marked lost and never re-run.
- Termivoa shares only sessions it starts, not terminals started outside it.
- Menus without numbers, such as Claude Code's folder-trust dialog, get no answer card.
- The Sessions list does not show which device has control.
- There is no independent security audit yet.

---
title: Release notes
description: Termivoa private beta release history, with the new features, fixes and known limits of each version, and what is coming in the next beta.
lastUpdated: 2026-10-02
---

The latest Termivoa release is the private beta `v0.1.0-beta.2` (2026-09-29).

## Next beta (not released yet)

- **Breaking: iPhone needs iOS 16.4 or later.** This is true for every user, also with one computer. The computer now accepts an app request only when the browser says where it comes from, and Safari before iOS 16.4 does not. Update the iPhone first. See [The app cannot load after the update](/docs/reference/troubleshooting/#the-app-cannot-load-after-the-update-iphone-next-beta).
- **Breaking: `pterm setup` refuses other Tailscale Serve handlers on port 443.** This is true for every user, also with one computer. If another service shares this computer's Termivoa address, `pterm setup` (and the Windows installer's startup step) stops before any change and prints the commands to move it. Move it to another computer or to its own Tailscale Service hostname; another port on this computer does not help. `pterm doctor` reports it as a problem, and warns about services on other ports of this computer. See [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers-next-beta).
- **Breaking: start with a fresh database.** The next beta does not upgrade data from older betas. If `pterm` stops with "delete the database file to start fresh", delete Termivoa's data folder (`pterm uninstall --delete-data`), run `pterm setup` and pair your phone again.
- **Reload the app after you update the host.** An app that was open during the update can fail to read settings until it reloads.
- **Several computers in one app.** Use a Mac, a Linux server and a VPS from one Home Screen app: one Sessions list with a chip and a colour per computer, one Wall, and **❯ new session on…** any computer. Add a computer with `pterm pair --via <home>` on it, then tap the link in a Termivoa session and confirm **Add this computer?**, or tap **+ Add a computer** in **Settings → Computers** and paste it. Check the address, tap **Continue**, and check the code. Each computer approves and revokes the phone itself; `pterm revoke --via` cuts off a hacked home. **Log out** signs the phone out of every computer it can reach. If home is offline, the app still opens and reaches the other computers. While a computer is being added, a banner says **Adding vps… approve on vps**; it keeps running when you go back to the terminal to type `y`. **Log out** shows **Signing out…** and waits at most 6 seconds for each other computer. If you reinstall Tailscale on a computer, add it again; if you reinstall it on home, the other computers show **pair again**. Notifications and previews come from home only for now. **Not yet tested on a real iPhone or Android phone.** Read [Choose home](/docs/guides/computers/#how-do-i-choose-home) first. See [Use several computers](/docs/guides/computers/).
- **Files.** The Changes screen has a **Files** tab: browse the session's project, search file names and read any text file, read-only and without control. Tap a path such as `src/app.ts:42` in the terminal to open that line. See [Files](/docs/guides/files/).
- **Fix: project buttons cannot get stuck.** A special file (a named pipe) with the name of a project file could make the list of project commands wait forever. Termivoa now reads only normal files.
- **Fix: `pterm uninstall` names the right file.** On macOS and Windows it told you to remove an app that the beta does not install. It now names the `pterm` file to delete.
- **Worktrees.** In a session inside a git repository, open the session menu (**⋯**) and tap **New worktree…** to start a task in its own folder and branch (`~/termivoa/worktrees/<repository>/<name>` on `termivoa/<name>`), from the default branch or one you pick. Claude or Codex can start in it at once. **Remove worktree…** deletes the folder and keeps the branch. See [Worktrees](/docs/guides/worktrees/).
- **Down next to Up while you type.** With the keyboard open, the key bar shows `↓` after `↑`, so you need not hide the keyboard to press Down. See [Use the agent keys and key bar](/docs/guides/live-terminal/#use-the-agent-keys-and-key-bar).
- **Pictures on paused Wall tiles.** A tile over the live limit that was never live now shows a small picture of its screen, marked **Paused**. See [What does a tile show?](/docs/guides/wall/#what-does-a-tile-show)
- **Faster first open.** The Changes, Files and Settings screens load only when you open them, so Termivoa downloads less before your first terminal appears.
- **A lighter Wall.** At most 4 tiles are live at once, each up to 5 updates per second; the others show **Paused**. See [What does a tile show?](/docs/guides/wall/#what-does-a-tile-show)
- **Arrange the Wall.** Hold a tile and drag it. The order is saved on your phone; **Reset order** restores the default. See [Arrange the tiles](/docs/guides/wall/#arrange-the-tiles).
- **Project buttons.** The Snippets sheet shows your project's commands from `package.json`, a `Makefile`, `Cargo.toml` or `go.mod`. **Run in new tab** runs one in a new session; **Type here** types it into the current one. Nothing runs until you tap. See [Project buttons](/docs/guides/project-buttons/).
- **Ask about specific lines.** In a file's diff, tap one line or a range of up to 20 lines and ask the agent about them. See [Ask about specific lines](/docs/guides/changes/#ask-about-specific-lines).
- **Fix: Ctrl+] right after `pterm attach` connects.** It was sometimes ignored or ended with a false "connection closed" error. It now detaches cleanly.
- **Fix: finished sessions shown as lost.** On a busy computer, a finished session now shows its exit code, not lost.
- **Fix: background jobs get their full cleanup time.** When a session ends, background programs get half a second to clean up. On a very busy computer, most of it could pass before they were told to stop; now it starts when they are told.
- **Fix: first key after `pterm attach --control` shows Control.** A key typed just as the footer changed to "Control" could be lost; it is now sent. A key read while the footer says "Syncing" is still dropped.
- **Fix: the open tab is always in view.** With many sessions, opening one whose tab was off to the right left its tab hidden. The tab strip now scrolls to show the open tab.
- **Fix: the folder name stays in the status line.** A long folder path was cut at its end, so the folder name was lost. Now the start of the path is cut with `…`, so the folder name and the shell stay in view.

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

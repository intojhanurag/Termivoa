---
title: Release notes
description: What is in each Termivoa release, and its known limits.
---

This page lists what is new in each release.

## Next beta (not released yet)

- **Project buttons.** The Snips sheet shows your project's own commands from `package.json`, a `Makefile`, `Cargo.toml` or `go.mod`. Tap one to run it in a new tab or type it here. Nothing runs until you tap. See [Project buttons](/docs/guides/project-buttons/).
- **Ask about specific lines.** In a file's diff, tap one line or a range of up to 20 lines, then ask the agent about exactly those lines. See [Changes](/docs/guides/changes/#ask-about-specific-lines).
- **Fix: Ctrl+] right after `pterm attach` connects.** Pressing Ctrl+] at the moment the desktop attach finished connecting was sometimes ignored, or ended with a false "connection closed" error. It now detaches reliably and cleanly.
- **Fix: finished sessions shown as lost.** A finished session is no longer shown as lost when the computer is busy. It shows its exit code.
- **Fix: background jobs get their full cleanup time.** When a session ends, programs you started in the background get half a second to clean up. On a very busy computer most of that time could be used up before they were told to stop. The time now starts only after they are told.
- **Fix: first key after `pterm attach --control` shows Control.** A key typed at the moment the footer changed to "Control" could be lost. It is now sent. A key that the host reads while the footer still says "Syncing" is dropped.

## v0.1.0-beta.2 (2026-09-29) — private beta

The second private beta. The binaries are not signed.

:::caution
Updating from beta.1 restarts Termivoa on the computer and ends running sessions. Finish your work first. See [Update and uninstall](/docs/reference/update-and-uninstall/).
:::

### What is new

- **Wall.** Watch all running sessions at once as small live screens. Tiles only watch; tap one to open the session. See [Wall](/docs/guides/wall/).
- **Changes view.** See what the agent changed: a list of changed files with +/− counts, and each file's diff in red and green. See [Changes](/docs/guides/changes/).
- **Commit and push from the phone.** Pick files, edit the suggested message, commit, then push. It never forces, and it needs control.
- **"While you were away" card.** When you open a session, see how long the agent worked, what changed, and whether it waits for you. See [Recap](/docs/guides/recap/).
- **Swipe to scroll full-screen apps.** Swipe on the terminal to scroll Claude Code, Codex, `vim` or `less`.
- **Much faster reconnect.** After you unlock the phone, it reconnects in seconds. A connection that still works is kept, and a stuck attempt is retried after 5 seconds.
- **Connection log.** `gateway.log` gets one line for each phone connection, with its length and why it ended.

## v0.1.0-beta.1 (2026-09-28) — private beta

The first private beta, for invited testers only. The binaries are not signed.
See [Quick start](/docs/quick-start/) to install it.

### What you can do

- **Run sessions from your phone.** Start shells and coding agents on your computer. They keep running when the phone is away.
- **Many sessions at once.** Switch with tabs. A dot shows new output.
- **Status badges.** See which sessions are **Working**, **Waiting** or **Your turn**.
- **Answer cards.** Answer Claude Code and Codex questions with one tap. A changed question is never answered by mistake.
- **Notifications.** Get a buzz when an agent waits for you or finishes. No terminal text leaves your computer.
- **Dev-server preview.** Open a page from `npm run dev` inside the app.
- **Photo and voice.** Send a photo to the agent without location data, or dictate a prompt.
- **Agent keys, snippets and presets.** One-tap keys for agent prompts, saved prompts, and one-tap session starts.
- **Live folder name.** Tabs show the folder the session is in now.
- **Desktop attach.** Open the same session in a terminal on your computer with `pterm attach`.
- **Safe pairing.** Scan a QR code, compare a code, approve on the computer. Remove a phone at any time.
- **Three platforms.** macOS (Apple Silicon), Linux (Ubuntu desktop, server and WSL) and Windows 11.
- **Fast reconnect.** The phone reconnects within seconds after a lock or a network change.

### Known limits

- Real-device testing so far covers a Mac and an iPhone. Linux was tested in containers only. Windows and Android are not yet tested on real devices.
- The binaries are not signed. Download them with `gh`, not with a browser.
- There is no automatic updater. See [Update and uninstall](/docs/reference/update-and-uninstall/).
- The computer must stay awake and logged in. A restart, a logout or a broker crash ends all sessions. They are marked lost and never re-run.
- Termivoa cannot take over terminals that you started outside it. Only sessions it starts can be shared.
- Menus without numbers, such as Claude Code's folder-trust dialog, get no answer card.
- The Sessions list does not show which device has control.
- There is no independent security audit yet.

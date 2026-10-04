---
title: Release notes
description: Termivoa private beta release history, with the new features, fixes and known limits of each version, and what is coming in the next beta.
lastUpdated: 2026-10-03
---

The latest Termivoa release is the private beta `v0.1.0-beta.2` (2026-09-29).

## Next beta (not released yet)

- **A new look: Bone & ink.** Flat and quiet: an ink background, serif titles, one bone button per screen, rows between hairlines, and status words in small capitals. Cyan only means live or connected; amber says warning or your turn; red is errors. Notices say what happened and what to do: amber "vps · can't reach" with **Retry**, red "vps · pair again" with the command to run, grey "Update Termivoa on vps". The computer filter on **Sessions** is a row of tabs.
- **Breaking: iPhone needs iOS 16.4 or later.** This is true for every user, also with one computer. The computer now accepts an app request only when the browser says where it comes from, and Safari before iOS 16.4 does not. Update the iPhone first. See [The app cannot load after the update](/docs/reference/troubleshooting/#the-app-cannot-load-after-the-update-iphone-next-beta).
- **Breaking: `pterm setup` refuses other Tailscale Serve handlers on port 443.** This is true for every user, also with one computer. If another service shares this computer's Termivoa address, `pterm setup` (and the Windows installer's startup step) stops before any change and prints the commands to move it. Move it to another computer or to its own Tailscale Service hostname; another port on this computer does not help. `pterm doctor` reports it as a problem, and warns about services on other ports of this computer. See [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers-next-beta).
- **A shorter `pterm setup` for each new computer.** Setup now prints a short plan, one line for each change, and asks once; `pterm setup --verbose` shows the checks, the startup files and every command. Warnings of the checks still show, under **Warnings:**. If your phone cannot reach the computer yet (no Tailscale, the Tailscale operator missing on Linux, or you said no to sharing), setup says how to fix it and pairs nothing. When Tailscale shows your other computers, setup asks **Is Termivoa already on your phone?**: answer `1` to pair this computer, or `2` to add it to your app (the same as `pterm pair --via <name>`, with a QR code to scan in **Settings → + Add a computer → Scan QR**). The project setup adds is now **Home folder**. On Linux, the install script and setup refuse root with the commands that make a normal user, and setup ends with the `export PATH=…` command when this terminal does not find `pterm` yet. See [Install on Linux](/docs/install/linux/) and the [CLI reference](/docs/reference/cli/).
- **Breaking: start with a fresh database.** The next beta does not upgrade data from older betas. If `pterm` stops with "delete the database file to start fresh", delete Termivoa's data folder (`pterm uninstall --delete-data`), run `pterm setup` and pair your phone again.
- **Reload the app after you update the host.** An app that was open during the update can fail to read settings until it reloads. A preview shows "Open the preview from the Termivoa app." until the app reloads.
- **Several computers in one app.** Use a Mac, a Linux server and a VPS from one Home Screen app: one Sessions list with a tab and a colour per computer, one Wall, and **❯ new session on…** any computer. Add a computer with `pterm pair --via <home>` on it, then tap the link in a Termivoa session and confirm **Add this computer?**, or tap **+ Add a computer** in **Settings → Computers** and paste it. Check the address, tap **Continue**, and check the code. Each computer approves and revokes the phone itself; `pterm revoke --via` cuts off a hacked home. **Log out** signs the phone out of every computer it can reach. If home is offline, the app still opens and reaches the other computers. While a computer is being added, a banner says **Adding vps… approve on vps**; it keeps running when you go back to the terminal to type `y`. **Log out** shows **Signing out…** and waits at most 6 seconds for each other computer. If you reinstall Tailscale on a computer, add it again; if you reinstall it on home, the other computers show **pair again**. **Not yet tested on a real iPhone or Android phone.** Read [Choose home](/docs/guides/computers/#how-do-i-choose-home) first. See [Use several computers](/docs/guides/computers/).
- **Add a computer with one tap: Found on your network.** On the new computer, run `pterm setup` and answer `2`, or `pterm pair --via <home>` if it is set up. It waits up to 10 minutes. In the app, open **Settings → Computers**, and under **Found on your network** tap **Add** next to it. Check the sheet, tap **Continue**, and type `y` on the new computer only when its prompt names this computer and shows the same code as the phone. There, the QR code and the link show when you press Enter. The QR code and the link still work, and tagged computers use them. Every phone paired with home can see the computers that wait. Not tested on real computers or a real phone yet. See [How do I add a computer?](/docs/guides/computers/#how-do-i-add-a-computer) and [What does Found on your network share?](/docs/reference/security/#what-does-found-on-your-network-share)
- **Preview a dev server that runs on another computer.** Open a session on that computer, wait for **Preview ready**, and tap **Open ↗**. Each computer needs its own yes to the preview question in `pterm setup`. Update Termivoa on every computer first. Over a page from another computer, the bar always shows the computer's name and **untrusted page**: that computer chooses the page, so do not type pairing links or passwords into it. **↻** loads the preview again. Not tested on a real phone yet. See [Can I preview a session on another computer?](/docs/guides/preview/#can-i-preview-a-session-on-another-computer)
- **The preview says when it cannot open.** The app asks the preview address before it shows the page. If nothing answers in 5 seconds, it shows "Can't open the preview on vps. …" and no empty frame. See [Troubleshooting](/docs/reference/troubleshooting/#cant-open-the-preview-on-vps-next-beta).
- **Fix: screens fit a narrow phone.** A `pterm pair --via <home>` command with a long computer name no longer runs out of its box or lets the page slide sideways; it wraps.
- **Fix: the preview after Back in Safari.** After the browser's Back to the app, the preview loads again by itself. See [What are the limits in Safari?](/docs/guides/preview/#what-are-the-limits-in-safari)
- **Files.** The Changes screen has a **Files** tab: browse the session's project, search file names and read any text file, read-only and without control. Tap a path such as `src/app.ts:42` in the terminal to open that line. See [Files](/docs/guides/files/).
- **Fix: notifications after Log out or `pterm revoke` and a new pairing.** The **Notifications** switch could show on while your computer sent nothing, and **Send a test notification** said "No subscription on this phone". Termivoa now registers the phone with your computer again when the app opens, so the switch is true.
- **Fix: project buttons cannot get stuck.** A special file (a named pipe) with the name of a project file could make the list of project commands wait forever. Termivoa now reads only normal files.
- **Fix: setup after you rename a computer.** Setup said the page was already shared when only the old name shared it. It now asks to share the page at the new name. Setup and `pterm pair` also stop for a Funnel that runs in the foreground (`tailscale funnel` without `--bg`). See [Troubleshooting](/docs/reference/troubleshooting/#setup-asks-to-share-the-page-again-after-a-rename-next-beta).
- **Fix: setup stops while Tailscale Funnel is on.** Funnel puts your terminal on the public internet. `pterm setup` only warned and still paired a phone; now it changes nothing while Funnel is on for port 443 or 8443, and prints the command that turns it off. See [Troubleshooting](/docs/reference/troubleshooting/#setup-refuses-because-tailscale-funnel-is-on-next-beta).
- **Fix: `pterm uninstall` names the right file.** On macOS and Windows it told you to remove an app that the beta does not install. It now names the `pterm` file to delete.
- **Worktrees.** In a session inside a git repository, open the session menu (**⋯**) and tap **New worktree…** to start a task in its own folder and branch (`~/termivoa/worktrees/<repository>/<name>` on `termivoa/<name>`), from the default branch or one you pick. Claude or Codex can start in it at once. **Remove worktree…** deletes the folder and keeps the branch. See [Worktrees](/docs/guides/worktrees/).
- **Down next to Up while you type.** With the keyboard open, the key bar shows `↓` after `↑`, so you need not hide the keyboard to press Down. See [Use the agent keys and key bar](/docs/guides/live-terminal/#use-the-agent-keys-and-key-bar).
- **Notifications from every computer.** With several computers, each one now notifies your phone when an agent waits or finishes. One switch in **Settings → Notifications** controls all of them. The title and text of a notification from another computer start with its name (`vps · …`), and a tap opens that session. If one computer cannot be set up, Settings shows **No notifications from vps.** and the others still work. Not tested on a real iPhone yet. See [Do notifications come from every computer?](/docs/guides/notifications/#do-notifications-come-from-every-computer)
- **Scan QR** in **+ Add a computer**: scan the QR code that `pterm pair --via` prints, inside the app. **Paste link** still works. See [How do I scan or paste the link?](/docs/guides/computers/#how-do-i-scan-or-paste-the-link)
- **Settings → Computers** shows a **Home** badge on the home computer.
- **Computer health.** **Settings → Computers** shows each computer's Termivoa version and when it last answered, with a **Check again** button. **Check again** says how fast the computer answered, for example **vps answered in 0.2 s.** See [Use several computers](/docs/guides/computers/).
- **Fix: "can't reach" right after you add a computer.** For about 5 seconds after an add the row says **Connecting to vps…** and **Just added. Its sessions show in a moment.** while the app tries again. See [Connecting to vps…](/docs/reference/troubleshooting/#connecting-to-vps-next-beta)
- **Fix: "can't reach" while the computer got nothing.** An old app page could block a computer you added, until you reloaded by hand. The app now reloads once by itself. If that does not help, the row says **This app page is out of date. Close the app and open it again.** See [Troubleshooting](/docs/reference/troubleshooting/#this-app-page-is-out-of-date-close-the-app-and-open-it-again-next-beta).
- **"vps · can't reach" asks, it does not guess.** The line under it says when vps last answered and asks **Is it on, and in your tailnet?** only when nothing answered.
- **Fix: Add a computer shows the right command.** **+ Add a computer** showed `pterm pair --via` with the name you gave home on the phone. It now shows home's machine name, which the command needs.
- **Fix: Scan QR on iPhone during a call.** When a call takes the camera, the scan now ends after 2 seconds with "The camera did not start. …" instead of a frozen picture.
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

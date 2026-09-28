---
title: Live terminal
description: Type in a session from your phone, take control, use agent keys, snippets and history, and move between desktop and phone.
---

This page helps you read and type in a live session on your phone.

## Control and read-only

Many devices can watch a session at the same time. Only one device can type. That device has **control**.

- When you open a session, you only watch. The screen shows **READ-ONLY** and who has control.
- Tap **Take control** to type. The screen then shows **● CONTROL**.
- Control moves to you at once. The other device becomes read-only.
- To give control away, open the session menu and tap **Release control**.

When the phone reconnects, for example after you lock it, it comes back read-only. Tap **Take control** again.

:::note
Termivoa never queues or resends your typing on its own. If the connection breaks while you type, you see "Delivery uncertain—check the terminal before sending again". Look at the terminal before you send it again.
:::

## Tabs

Open sessions show as tabs at the top. Tap a tab to switch. A dot marks new output.

## Keys

With control, the dock under the terminal has:

- **Agent keys:** `esc`, `⌃C`, `⇧⇥`, `↑`, `1`, `2`, `y` and `⏎`. These answer coding-agent prompts in one tap.
- **Key bar:** `tab`, `ctrl`, `alt` and arrow keys. Tap `ctrl`, then a letter, to send for example Ctrl+C.
- **Actions:** **type** opens the phone keyboard, **compose**, **snips** and **history**.

When the keyboard is open, all keys fold into one row that scrolls. The terminal keeps its size, so full-screen agents do not redraw.

## Compose

Tap **compose** to write long or multi-line text before you send it.
Nothing is sent until you tap **Send ⏎**. It pastes the text and presses Enter.
Compose can also add a [photo or your voice](/docs/guides/photo-and-voice/).

## Snippets

Snippets are saved prompts and commands. Tap **snips** to open them.

- Tap **+ new** to save one. Choose **Press Enter after pasting** if it should run at once.
- Tap a snippet to send it. You need control.
- A snippet with many lines asks before it pastes.

Snippets are saved on this phone only.

## Scroll

- **Full-screen apps** such as Claude Code, Codex, `vim` or `less`: swipe up or down on the terminal. The app scrolls its own output. You need control.
- **A normal shell:** pull down on the terminal, or tap **history**, to see earlier output.

The `▲` and `▼` keys in the dock also scroll a full-screen app.

## History and copy

1. Tap **history** to scroll back through earlier output.
2. Select text.
3. Tap **Copy N lines** (for example **Copy 3 lines**).
4. Tap **Done** to go back to the live screen.

## Text size

Open **Settings**. Under **Terminal text size**, tap **A−** or **A+**.

## Move between desktop and phone

You can open the same session in a terminal on your computer.

1. List your sessions:

   ```sh
   pterm list
   ```

2. Open one to watch:

   ```sh
   pterm attach <session-id>
   ```

3. To type, open it with control:

   ```sh
   pterm attach --control <session-id>
   ```

4. Press Ctrl+] to leave. The session keeps running.

When you take control on the phone, the desktop becomes read-only, and the other way around.

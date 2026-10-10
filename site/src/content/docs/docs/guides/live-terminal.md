---
title: Live terminal
description: Read and type in a Termivoa session from your phone, with control, agent keys, touch gestures, snippets, history and a desktop attach.
lastUpdated: 2026-10-10
---

Termivoa's live terminal is the screen of one session, where you read output and type from your phone. Open it from the Sessions list or Wall.

## What changes for busy terminals?

Since `v0.1.0-beta.4`, Live, Wall and **history** use less CPU and temporary memory on the computer while output changes. The terminal controls and display stay the same.

## Take control to type

Many devices can watch a session; only the one with **control** can type.

- A session opens read-only, showing **Read-only** and who has control.
- Tap **Take control**. The screen shows **● Control**; the other device becomes read-only.
- To give it away, open the session menu (**⋯**) and tap **Release control**.

:::note
Termivoa never queues or resends typing. If the connection breaks while you type, you see "Delivery uncertain—check the terminal before sending again". Look at the terminal before you send it again.
:::

## Switch sessions with the tabs

The tab bar at the top shows your running sessions. Tap a tab to switch; each opens read-only. A dot marks new output; an amber dot means it waits for you. **+** starts a new session.

## What limits apply to the terminal screen?

Termivoa keeps terminal screens bounded while you read and type.

| Item | Limit |
| --- | --- |
| Screen width | Up to 400 columns |
| Screen height | Up to 200 rows |
| Screen updates | Rows must stay within the allowed screen bounds |

The computer also rejects malformed row positions in received
terminal screen updates. The phone already applies that limit. No setting is
needed.

## Use the agent keys and key bar

With control, the dock under the terminal shows one-tap agent keys and a key bar with keys a phone keyboard lacks.

![A session with control: the terminal shows the agent working, and the dock below has the agent keys, the key bar and the type, compose, snips, history and changes buttons.](../../../../assets/docs/phone-live-terminal.webp)

| Key | What it sends |
| --- | --- |
| `esc` | Escape. Stops a coding agent. |
| `⌃C` | Only Ctrl+C, even when `ctrl` is on. |
| `⇧⇥` | Shift+Tab. Changes the agent's mode. |
| `1`, `2`, `y` | That character. Picks an agent option. |
| `⏎`, `tab` | Enter, Tab. |
| `ctrl`, `alt` | Holds Ctrl or Alt for the next key. |
| `↑`, `←`, `↓`, `→` | Arrow keys. |
| `▲`, `▼` | Scrolls a full-screen app. Shows only while one is open. |

The action buttons are **type** (opens the phone keyboard), **compose**, **snips**, **history** and **changes** (see [Changes](/docs/guides/changes/)).

With the phone keyboard open, all keys fold into one scrolling row, and the terminal keeps its size so full-screen agents do not redraw. `↓` follows `↑` there.

![With the keyboard open, one row of keys stays under the terminal: esc, ^C, mode (⇧⇥), ↑, ↓, 1, 2 and hide keyboard.](../../../../assets/docs/key-row.webp)

## Which touch gestures does the terminal take?

:::note[Next beta]
Touch gestures are on `main` and come with the next beta.
:::

Termivoa's terminal takes five touch gestures. Each one also has a button, so you never need them.

| Gesture | What it does | Needs control |
| --- | --- | --- |
| Hold one finger still until **← → ↑ ↓** shows, then slide | Sends arrow keys: about one per letter sideways, one per line and a half up or down. It keeps the first direction until you lift. | Yes |
| With the keyboard open, tap a letter on the line you type | Moves the cursor there with arrow keys. | Yes |
| Swipe sideways on the line under the tabs | Opens the session to the left or right in the tabs. Off in herdr terminals. | No |
| Pinch the terminal | Changes the text size. The size shows while you pinch and applies when you lift. | No |
| Tap with two fingers at once | Sends Esc, the same as `esc`. | Yes |

A tap moves the cursor only when the keys surely land there: after a shell prompt such as `$ ` or `% `, on the cursor's own line, over plain one-column letters, and to the right only over text in the same style as the letter before the cursor. A tap on grey suggested text, a right-side prompt or empty space does nothing. At a password or `[y/N]` question, and in full-screen apps such as `vim`, a tap does not move the cursor. A tab or a control character such as `^A` in the line can make the cursor land a little off.

A stray two-finger touch sends Esc. If that happens to you, turn the gestures off.

### How do I turn the gestures off?

Open **Settings**, and under **This phone** turn off **Terminal gestures**. Touches then work as before: a swipe scrolls, and you can zoom the page. The setting is for this phone only.

## Write longer text with compose

Tap **compose** for long or multi-line text. Nothing is sent until **Send ⏎**, which pastes it and presses Enter. Text with more than one line asks before it sends. Compose can also add a [photo or your voice](/docs/guides/photo-and-voice/).

## Use sheets with a keyboard

Termivoa keeps your keyboard place when a sheet closes.

| Action | Focus after the sheet closes |
| --- | --- |
| Use **Close** or Escape | Returns to the control that opened the sheet, if it is still available. |
| Close a sheet opened inside another sheet | Returns to its control inside the previous sheet. |
| Move focus to another control before the sheet closes | Stays on the control you chose. |

If the opening control disappeared or became unavailable, pick another
control with Tab. This focus change does not send anything to the terminal.

## Save and send snippets

Snippets are prompts and commands you send with one tap, saved on this phone only. Tap **snips** to open the Snippets sheet.

- Save: tap **+ New**, fill in **Title** and **Text to send**, pick **Press Enter after pasting** to run it at once, and tap **Save**.
- Send: tap it (needs control). A snippet with more than one line asks first.
- Change or delete: tap **Edit**, then the snippet.
- **This project**: your project's own commands. See [Project buttons](/docs/guides/project-buttons/).

## Scroll the terminal

- **Full-screen apps** such as Claude Code, Codex, `vim` or `less`: swipe on the terminal or tap `▲`/`▼` (needs control).
- **A normal shell:** pull down, or tap **history**.

## Copy from history

1. Tap **history**.
2. Select text.
3. Tap **Copy N lines**, for example **Copy 3 lines**.
4. Tap **Done**.

Termivoa loads all retained History rows even when colored
output needs more pages. It shows a complete frozen view, or says
**History unavailable right now. Return to live and try again.**

| What you see | What to do |
| --- | --- |
| **Loading history…** | Wait for the frozen view. |
| **History unavailable right now. Return to live and try again.** | Tap **Done** and open **history** again. Try when output has slowed. |
| **New output · Return to live** | Your selection stays frozen; tap the button to see the new output. |

Termivoa keeps up to 5,000 history rows and 32 MiB on the computer. A loading
attempt stops after a minute or when its limits are reached; it shows unavailable
rather than joining an incomplete history to the current screen.

## Change the text size

In the session menu (**⋯**), open **Settings** and tap **A−** or **A+** under **Terminal text size**. Sizes go from 10 px to 24 px.

**Next beta:** you can also pinch the terminal. The new size shows as, for example, **16 px** while you pinch, and applies when you lift your fingers.

## Move between desktop and phone

Open the same session in a terminal on your computer. `pterm list` shows the IDs:

```sh
pterm attach <session-id>             # watch
pterm attach --control <session-id>   # type
```

Press Ctrl+] to leave; the session keeps running. When you take control on the phone, the desktop becomes read-only, and the other way around.

## Common questions

### Why did I lose control?

Another device took control, or you locked the phone or switched apps. Tap **Take control** again.

### How do I send Ctrl+C?

Tap `⌃C`, or tap `ctrl` and then type `c`.

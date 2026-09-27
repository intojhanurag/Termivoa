# Get started in 5 minutes

Termivoa lets you use the terminal sessions on your Mac from your phone.
Your shells and coding agents keep running on the Mac while the phone is away.

You need:

- an Apple Silicon Mac that stays awake and logged in;
- an iPhone (Safari) or Android phone (Chrome);
- [Tailscale](https://tailscale.com/download) on the Mac and on the phone,
  signed in to the same account.

## 1. Install

No release is published yet. Build `pterm` from source as shown in the
[README quick start](../README.md#quick-start), or build the unsigned app and
DMG with [`packaging/macos`](../packaging/macos/README.md).

With the DMG, open it and drag **Termivoa.app** to **Applications**. To
use `pterm` in a terminal:

```sh
mkdir -p ~/.local/bin
ln -s "/Applications/Termivoa.app/Contents/MacOS/pterm" ~/.local/bin/pterm
```

Builds are not signed yet, so macOS may block the first launch. Windows 11
steps are in [install.md](install.md#windows-11-x64); they are not yet verified
on a real Windows machine. Linux (Ubuntu desktop, server/VPS or WSL) steps are
in [install.md](install.md#linux-ubuntu-2204-and-2404-x86_64-or-arm64): run
`scripts/install-linux.sh`, which installs `~/.local/bin/pterm` and starts
`pterm setup`. On a server, answer `y` to the linger question.

## 2. Run setup

```sh
pterm setup
```

Setup checks your Mac, shows what it will do, and asks **once**. It then:

- starts Termivoa every time you log in;
- sets your phone address to your Mac's Tailscale name;
- asks before it shares the page with your own Tailscale devices (never with
  the public internet);
- adds your home folder as the project **Home**;
- shows a QR code.

## 3. Pair your phone

1. Scan the QR code with the phone camera and open the link.
2. Type a name for the phone and tap **Pair this device**.
3. The phone shows a code such as `485-431`. The Mac asks
   `Phone "My iPhone" shows code 485-431. Approve? [y/N]`. If the codes match,
   type `y`.

On iPhone, tap **Share → Add to Home Screen** to open Termivoa like an
app.

## Use it

- **Start a shell:** tap **New session**, pick a project and a shell, then tap
  **start session**. Fill in **run first** (for example `claude`) to type one
  command when the session opens; **save as preset** keeps it under **Launch**
  for one-tap starts.
- **Switch sessions:** use the tabs at the top. A dot marks new output.
- **Type:** tap **Take control**. The agent keys (esc, ⌃C, ⇧⇥, ↑, 1, 2, y, ⏎)
  answer coding-agent prompts in one tap. The key bar has Esc, Tab, Ctrl, Alt
  and arrows. Use **compose** for long text or multi-line prompts, and
  **snips** for saved snippets. In compose, **Photo** sends a picture to the
  project folder and adds its `@path` to the message, and **Voice** dictates.
  Nothing is sent until you tap **Send ⏎**.
- **Scroll back:** tap **history**. Select text and copy it.
- **Leave:** close the app. The session keeps running. When you come back you
  see the current screen; tap **Take control** to type again.
- **Same session on the Mac:** `pterm list`, then `pterm attach <session-id>`.
  Add `--control` to type. Ctrl+] leaves without ending the session.

Coding agents such as Claude Code or Codex run like any other command in the
session.

## Everyday commands

| Command | What it does |
| --- | --- |
| `pterm list` | Show your sessions. |
| `pterm project add <folder>` | Add another project folder. |
| `pterm pair` | Pair another phone. |
| `pterm devices` | Show paired phones. |
| `pterm revoke <device-id>` | Remove a phone at once. |
| `pterm terminate <session-id>` | End one session. |
| `pterm stop` | Stop phone access; sessions keep running. |
| `pterm shutdown` | End all sessions and stop Termivoa. |
| `pterm doctor` | Check what is wrong. Never prints secrets. |

## If something does not work

- **The phone cannot open the page:** check that Tailscale is on for both
  devices, then run `pterm doctor`. Any network works (home Wi-Fi, other
  Wi-Fi, mobile data) while Tailscale is on.
- **It stops working when the Mac sleeps:** a sleeping Mac cannot answer. Keep
  it on power and turn on **System Settings → Battery → Options → Prevent
  automatic sleeping on power adapter when the display is off**. Sessions
  continue when the Mac wakes.
- **"This pairing link expired":** links last 2 minutes. Run `pterm pair` again.
- **"Read-only":** tap **Take control**. Only one device can type at a time.
- **"Delivery uncertain":** the connection broke while you typed. Look at the
  terminal before you send it again. Termivoa never resends input on its
  own.
- **"Session lost":** the Mac restarted, logged out, or the broker stopped. A
  lost session cannot come back; start a new one.
- **You lost your phone:** on the Mac, run `pterm devices`, then
  `pterm revoke <device-id>`. That phone loses access at once.

Details: [docs/install.md](install.md). Things not yet verified on real devices:
[docs/owner-verification.md](owner-verification.md).

---
title: Install on macOS
description: Install the Termivoa pterm beta on an Apple Silicon Mac with the GitHub CLI, then let setup start it at every login with two LaunchAgents.
lastUpdated: 2026-10-01
---

Termivoa installs on an Apple Silicon Mac as one file, `pterm`, that you download with the GitHub CLI and set up with `pterm setup`.

:::note
Private beta. The Mac has the most testing, including with a real iPhone.
:::

## Requirements

- A Mac with Apple Silicon (M1 or later).
- [Tailscale](https://tailscale.com/download) on the Mac and the phone, signed in to the same account.
- The [GitHub CLI](https://cli.github.com/) (`gh`), signed in with `gh auth login`. You need an invite to the private repository.

No Node.js, Go or other tools are needed.

## Install

Make a folder, download the beta, make it runnable and run setup:

```sh
mkdir -p ~/.local/bin
gh release download v0.1.0-beta.2 -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm
chmod +x ~/.local/bin/pterm
~/.local/bin/pterm setup
```

Then follow [Pair your phone](/docs/pair-your-phone/).

:::caution
Download with `gh`, not a browser. The binary is not signed, and macOS blocks unsigned files from a browser, but not from `gh`.
:::

## Use `pterm` without the full path

If the shell says `pterm: command not found`, `~/.local/bin` is not on your PATH (the folders the shell searches).
Type `~/.local/bin/pterm`, or add the folder for zsh, the default Mac shell, and open a new terminal window:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

## Check the download (optional)

Download the release's `SHA256SUMS` file next to the binary and run:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
```

## Where does pterm put its files?

| What | Where |
| --- | --- |
| Program | `~/.local/bin/pterm` |
| Data | `~/Library/Application Support/Phone Terminal` |
| Logs | `~/Library/Application Support/Phone Terminal/logs/` |
| Cache | `~/Library/Caches/Phone Terminal` |
| Startup entries | `~/Library/LaunchAgents/dev.phone-terminal.broker.plist` and `dev.phone-terminal.gateway.plist` |

"Phone Terminal" is the old working name, kept so existing installs keep working.
Keep `pterm` where it is after setup. The startup entries point to that exact path.

## How does it start at login?

`pterm setup` registers two LaunchAgents (macOS entries that start a program at login). They start two background processes:

- the **broker**, which owns your sessions;
- the **gateway**, which serves the phone page on your Mac only (`127.0.0.1:8787`).

Both run as you, never as an administrator. If one crashes, macOS starts it again.

## Keep the Mac awake

A sleeping Mac cannot answer the phone. Keep it on power and turn on
**System Settings → Battery → Options → Prevent automatic sleeping on power adapter when the display is off**.

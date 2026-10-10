---
title: Install on macOS
description: Install the free Termivoa pterm beta on an Apple Silicon Mac with one curl download, then let setup start it at every login with two LaunchAgents.
lastUpdated: 2026-10-10
---

Termivoa installs on an Apple Silicon Mac as one file, `pterm`, that you download from GitHub and set up with `pterm setup`. It is free and open source; you need no account.

:::note
Open-source beta (`v0.1.0-beta.4`). The binaries are not signed. The Mac has the most testing, including with a real iPhone.
:::

## Requirements

- A Mac with Apple Silicon (M1 or later).
- [Tailscale](https://tailscale.com/download) on the Mac and the phone, signed in to the same account.

No Node.js, Go or other tools are needed. [Set up a GitHub repository](/docs/guides/github/) also needs `git` on the computer.

## Install

Make a folder, download the beta with `curl`, make it runnable and run setup:

```sh
mkdir -p ~/.local/bin
curl -fsSL -o ~/.local/bin/pterm https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4/pterm-darwin-arm64
chmod +x ~/.local/bin/pterm
~/.local/bin/pterm setup
```

Then follow [Pair your phone](/docs/pair-your-phone/).

:::caution
Download with `curl` or `gh`, not a browser. The binary is not signed. macOS blocks unsigned files that a browser downloads, but not files from `curl` or `gh`.
:::

With the [GitHub CLI](https://cli.github.com/) (`gh`), this line replaces the `curl` line:

```sh
gh release download v0.1.0-beta.4 -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm
```

## Use `pterm` without the full path

If the shell says `pterm: command not found`, `~/.local/bin` is not on your PATH (the folders the shell searches).
Type `~/.local/bin/pterm`, or add the folder for zsh, the default Mac shell, and open a new terminal window:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

## Check the download (optional)

Before you run setup, compare the checksum (a fingerprint of the file) with the release's `SHA256SUMS` file:

```sh
curl -fsSL https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4/SHA256SUMS | grep pterm-darwin-arm64
shasum -a 256 ~/.local/bin/pterm
```

The two long numbers must be the same. If they are not, delete `~/.local/bin/pterm` and download it again.

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

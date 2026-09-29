---
title: Install on macOS
description: Download the pterm beta binary on an Apple Silicon Mac and set it up to start at login.
---

This page helps you install Termivoa on a Mac with Apple Silicon.

:::note
This is a private beta. The Mac is the platform with the most testing, including a real iPhone.
:::

## Requirements

- A Mac with Apple Silicon (M1 or later).
- [Tailscale](https://tailscale.com/download) on the Mac and on your phone, signed in to the same account.
- The [GitHub CLI](https://cli.github.com/) (`gh`), signed in with `gh auth login`. You need an invite to the private repository.

No Node.js, Go or other tools are needed on the Mac.

## Install

1. Make a folder for the program:

   ```sh
   mkdir -p ~/.local/bin
   ```

2. Download the beta:

   ```sh
   gh release download v0.1.0-beta.2 -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm
   ```

3. Make it runnable:

   ```sh
   chmod +x ~/.local/bin/pterm
   ```

4. Run setup:

   ```sh
   ~/.local/bin/pterm setup
   ```

Then follow [Pair your phone](/docs/pair-your-phone/).

:::caution
Download with `gh`, not with a browser. The binary is not signed. macOS blocks unsigned files that come from a browser, but not files that `gh` downloads.
:::

## Use `pterm` without the full path

If the shell says `pterm: command not found`, `~/.local/bin` is not on your PATH (the list of folders the shell searches).
You can always type `~/.local/bin/pterm` in its place.
To add the folder for zsh, the default Mac shell:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

Open a new terminal window after this.

## Check the download (optional)

The release also has a `SHA256SUMS` file. Download it next to the binary and run:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
```

## Where files go

| What | Where |
| --- | --- |
| Program | `~/.local/bin/pterm` |
| Data (database) | `~/Library/Application Support/Phone Terminal` |
| Logs | `~/Library/Application Support/Phone Terminal/logs/` |
| Cache | `~/Library/Caches/Phone Terminal` |
| Startup entries | `~/Library/LaunchAgents/dev.phone-terminal.broker.plist` and `dev.phone-terminal.gateway.plist` |

Some names say "Phone Terminal". That is the old working name. It is kept so that existing installs keep working.

Keep `pterm` where it is after setup. The startup entries point to that exact path.

## How it starts at login

Setup registers two LaunchAgents. A LaunchAgent is a macOS entry that starts a program when you log in.
They start two background processes from the same `pterm` file:

- the **broker**, which owns your sessions;
- the **gateway**, which serves the phone page on your Mac only (`127.0.0.1:8787`).

Both run as you, never as an administrator. If one crashes, macOS starts it again.

## Keep the Mac awake

A sleeping Mac cannot answer the phone. Keep it on power and turn on
**System Settings → Battery → Options → Prevent automatic sleeping on power adapter when the display is off**.

Next: [Pair your phone](/docs/pair-your-phone/).

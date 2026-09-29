---
title: Install on Linux
description: Download the pterm beta on Ubuntu desktop, a server or WSL, and start it with systemd user services.
---

This page helps you install Termivoa on Ubuntu, on a desktop, a server or in WSL.

:::note
This is a private beta. Linux support was tested in Ubuntu 24.04 containers only, not yet on a real Linux computer.
:::

## Requirements

- Ubuntu 22.04 or 24.04, on x86_64 or arm64.
- [Tailscale](https://tailscale.com/download) on the computer and on your phone, signed in to the same account.
- The [GitHub CLI](https://cli.github.com/) (`gh`), signed in with `gh auth login`. You need an invite to the private repository.
- A normal user account. The install script refuses to run as root.

No root access, extra packages or Node.js are needed.

## Install

1. Download the binary and the install script. On arm64, use `pterm-linux-arm64` in both commands.

   ```sh
   gh release download v0.1.0-beta.2 -R intojhanurag/Termivoa -p pterm-linux-amd64 -p install-linux.sh
   ```

2. Run the install script:

   ```sh
   sh install-linux.sh ./pterm-linux-amd64
   ```

The script copies the file to `~/.local/bin/pterm` and starts `pterm setup`.
If `~/.local/bin` is not on your PATH yet, Ubuntu adds it at your next login. Until then, type `~/.local/bin/pterm`.

The release also has a `SHA256SUMS` file to check the download:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
```

## How it starts at login

Setup adds two systemd **user** services. A user service is a background program that systemd runs as you, not as root.

- `phone-terminal-broker.service` owns your sessions.
- `phone-terminal-gateway.service` serves the phone page on this computer only (`127.0.0.1:8787`).

Setup shows the unit files and every `systemctl --user` command before it runs them.
If a service crashes, systemd starts it again after 5 seconds.

## Server or VPS (SSH only)

A server has no desktop login. Without **linger**, systemd stops your services, and all sessions, when your last SSH login closes.
Linger lets your user services run without a login.

Setup asks a separate question:

```text
This runs: loginctl enable-linger <user> [y/N]
```

On a server, answer `y`. With linger on, Termivoa starts at boot and sessions survive logout.
pterm never uses `sudo`. If the system refuses, setup prints the `sudo` command for you to run.

## Tailscale operator

On Linux, only root or the Tailscale **operator** may share the page with your devices.
If you are not the operator, setup says so and skips the Tailscale questions. Run this once, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

## WSL (Windows Subsystem for Linux)

Windows stops the WSL virtual machine about a minute after the last WSL window closes. All sessions end then.

- **Recommended:** install the [Windows version](/docs/install/windows/) instead. It offers a **WSL bash** shell for your sessions.
- **Or:** install the Linux version in WSL and turn on systemd. Add these lines to `/etc/wsl.conf`, run `wsl --shutdown` in Windows, open WSL again, and run `pterm setup`:

  ```ini
  [boot]
  systemd=true
  ```

Without systemd, nothing can start Termivoa at login. Setup tells you, and starts it in the background for now. Run `pterm launch` after each restart.

## Where files go

| What | Where |
| --- | --- |
| Program | `~/.local/bin/pterm` |
| Data (database) | `~/.local/share/phone-terminal` |
| Logs | `~/.local/state/phone-terminal/logs` |
| Sockets | `/run/user/<uid>/phone-terminal` |
| Startup units | `~/.config/systemd/user` |

All of these are private to your user.

## Keep the computer awake

On an Ubuntu desktop, turn off **Settings → Power → Automatic Suspend** while you work from the phone.

Next: [Pair your phone](/docs/pair-your-phone/).

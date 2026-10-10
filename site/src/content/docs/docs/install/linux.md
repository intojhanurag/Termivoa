---
title: Install on Linux
description: Install the free Termivoa pterm beta on Ubuntu desktop, a server, a VPS or WSL with curl, and run it as systemd user services with no root.
lastUpdated: 2026-10-10
---

Termivoa installs on Ubuntu (desktop, server or WSL) as one file, `pterm`, with an install script that runs `pterm setup`.

:::note
Open-source beta (`v0.1.0-beta.4`). The binaries are not signed. Linux was tested in Ubuntu 24.04 containers and on one real Ubuntu 24.04 VPS (x86_64). arm64 and desktop Ubuntu are not yet tested on real hardware.
:::

## Requirements

- Ubuntu 22.04 or 24.04, on x86_64 or arm64. Other Linux distributions are not tested.
- [Tailscale](https://tailscale.com/download) on the computer and the phone, signed in to the same account.
- `curl`. Ubuntu Desktop does not always have it: `sudo apt install curl`.
- A normal user, not root. The install script refuses root.

No GitHub account, Node.js or other packages are needed. [Set up a GitHub repository](/docs/guides/github/) also needs `git` on the computer.

## Install

Download the binary, the install script and the checksum file, check them, then run the script.
On arm64, use `pterm-linux-arm64` in all three places:

```sh
mkdir -p ~/termivoa-download && cd ~/termivoa-download
base=https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4
curl -fsSL --remote-name-all $base/pterm-linux-amd64 $base/install-linux.sh $base/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
sh install-linux.sh ./pterm-linux-amd64
```

`sha256sum` must print `OK` for both files. If a line says `FAILED`, download again and do not run the script.

With the [GitHub CLI](https://cli.github.com/) (`gh`), this line replaces the `curl` line:

```sh
gh release download v0.1.0-beta.4 -R intojhanurag/Termivoa -p pterm-linux-amd64 -p install-linux.sh -p SHA256SUMS
```

The script installs `~/.local/bin/pterm` and starts setup.
If this terminal does not find `pterm` yet, the last lines of setup give the command to fix it:

```text
`pterm` is not on your PATH in this terminal yet. To use it here, run this now:
  export PATH="/home/you/.local/bin:$PATH"
New logins have it already: your ~/.profile adds ~/.local/bin when it exists.
```

With zsh, the last line says to add the `export` line to `~/.zshrc`. Until then, you can also type `~/.local/bin/pterm`.

Then follow [Pair your phone](/docs/pair-your-phone/). If Termivoa is already on your phone (this is your second computer), answer `2` when setup asks "Is Termivoa already on your phone?", then pick the computer you first paired. See [Use several computers](/docs/guides/computers/).

### What if I am logged in as root?

The script and `pterm setup` refuse root: your phone would get root's shell. A new VPS often has only root. The script then prints the commands that make a normal user with `sudo` rights (the `wheel` group on Fedora), give it your SSH key, and copy the two files to it:

```sh
adduser <name>
usermod -aG sudo <name>
install -d -m 700 -o <name> -g <name> /home/<name>/.ssh && install -m 600 -o <name> -g <name> ~/.ssh/authorized_keys /home/<name>/.ssh/
install -m 644 -o <name> -g <name> install-linux.sh ./pterm-linux-amd64 /home/<name>/
```

Then log in as that user (`ssh <name>@<server>`) and run the install again. If you ran it with `sudo`, run it again without `sudo`.

## How does it start at login?

`pterm setup` adds two systemd **user** services that run as you, not as root:

- `phone-terminal-broker.service`: the broker, which owns your sessions.
- `phone-terminal-gateway.service`: the gateway, which serves the phone page on this computer only (`127.0.0.1:8787`).

Setup lists this in a short plan before it asks. `pterm setup --verbose` also shows the unit files and every `systemctl --user` command.
If a service crashes, systemd restarts it after 5 seconds.

## Keep sessions running on a server or VPS

On a server or VPS you reach only by SSH, answer `y` to this setup question:

```text
This runs: loginctl enable-linger <user> [y/N]
```

Without **linger**, systemd stops your services, and all sessions, when your last SSH login closes. With linger on, Termivoa starts at boot and sessions survive logout.
If the system refuses, setup prints the `sudo` command to run; `pterm` never uses `sudo` itself.

## Why does setup skip the Tailscale questions?

On Linux, only root or the Tailscale **operator** may share the page. To become the operator, run this once, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

Until then your phone cannot reach the computer, so setup does not pair a phone. The plan and the last line of setup say so:

```text
Setup is done, but your phone cannot reach this computer yet: on Linux only root or the Tailscale operator may share it on port 443. Run this once: sudo tailscale set --operator=$USER, then run `pterm setup` again.
```

## Install in WSL (Windows Subsystem for Linux)

Windows stops the WSL virtual machine about a minute after the last WSL window closes, ending all sessions.

- **Recommended:** follow [Install on Windows](/docs/install/windows/) and use its **WSL bash** shell.
- **Or:** install the Linux version and turn on systemd. Add these lines to `/etc/wsl.conf`, run `wsl --shutdown` in Windows, open WSL again, and run `pterm setup`:

  ```ini
  [boot]
  systemd=true
  ```

Without systemd, nothing starts Termivoa at login. Setup starts it once in the background; run `pterm launch` after each restart.

## Where does pterm put its files?

| What | Where |
| --- | --- |
| Program | `~/.local/bin/pterm` |
| Data | `~/.local/share/phone-terminal` |
| Logs | `~/.local/state/phone-terminal/logs` |
| Sockets | `/run/user/<uid>/phone-terminal` |
| Startup units | `~/.config/systemd/user` |

All are private to your user.

## Keep the computer awake

On an Ubuntu desktop, turn off **Settings → Power → Automatic Suspend**.

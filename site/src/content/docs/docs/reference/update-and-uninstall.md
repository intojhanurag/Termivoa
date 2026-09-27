---
title: Update and uninstall
description: Replace pterm with a new version, remove it from your computer, and find where your data lives.
---

This page helps you update Termivoa, remove it, and find its data.

## Update

There is no automatic updater. Nothing downloads code on its own.

Your sessions live in the broker (the background process that owns them). A new version starts only when the broker restarts, and a restart ends all sessions.

1. Finish your work in all sessions.
2. Stop Termivoa. It asks first and ends all sessions:

   ```sh
   pterm shutdown
   ```

3. Download the new version over the old file, at the same path. For example, on macOS:

   ```sh
   gh release download <new-version> -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm --clobber
   ```

   On [Linux](/docs/install/linux/) and [Windows](/docs/install/windows/), use the file name and folder from your install page. The Linux program lives at `~/.local/bin/pterm`.

4. On macOS and Linux, make it runnable again:

   ```sh
   chmod +x ~/.local/bin/pterm
   ```

5. Start it again:

   ```sh
   pterm launch --no-open
   ```

Your paired phones, projects and settings stay.
If the phone shows **A new version is ready**, tap **Reload**.

:::tip
To keep a way back, copy the old file first, for example `cp ~/.local/bin/pterm ~/.local/bin/pterm-old`.
:::

## Uninstall

1. See what would be removed. This changes nothing:

   ```sh
   pterm uninstall --dry-run
   ```

2. Remove startup at login. Your data stays:

   ```sh
   pterm uninstall
   ```

   To also delete paired phones, projects, settings and logs:

   ```sh
   pterm uninstall --delete-data
   ```

3. Delete the program file:
   - macOS and Linux: `rm ~/.local/bin/pterm`
   - Windows: delete `%LOCALAPPDATA%\Programs\Termivoa\pterm.exe`

Uninstall refuses while a session is active. End your sessions first, or run `pterm shutdown`.

On Linux, uninstall does not turn linger off. To do that, run `loginctl disable-linger`.

### Remove Tailscale Serve

If you let setup share the page with Tailscale Serve, remove that yourself.
First check what Serve shares:

```sh
tailscale serve status
```

`tailscale serve reset` removes **all** Serve settings, also ones that are not from Termivoa.

## Where data lives

The folders use the old working name "Phone Terminal", so existing installs keep their data.

| System | Data |
| --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal` and `~/Library/Caches/Phone Terminal` |
| Linux | `~/.local/share/phone-terminal`, `~/.local/state/phone-terminal` and `/run/user/<uid>/phone-terminal` |
| Windows | `%LOCALAPPDATA%\Phone Terminal` |

Logs are in a `logs` folder inside these (on Linux, in `~/.local/state/phone-terminal/logs`).
Terminal content is never saved to disk.

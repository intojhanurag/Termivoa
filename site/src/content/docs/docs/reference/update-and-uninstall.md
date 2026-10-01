---
title: Update and uninstall
description: Update or uninstall pterm, the Termivoa host program, by hand, undo its Tailscale Serve setting, and find the folders that hold its data.
lastUpdated: 2026-10-01
---

Termivoa has no automatic updater: you stop `pterm`, replace the program file, and start it again. Nothing downloads code on its own.

## Update Termivoa

An update ends all sessions, so finish your work first.

1. Stop `pterm` (it asks first):

   ```sh
   pterm shutdown
   ```

2. Download the new version to the same path, over the old file. For example, on macOS:

   ```sh
   gh release download <new-version> -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm --clobber
   ```

   On [Linux](/docs/install/linux/) and [Windows](/docs/install/windows/), use the file name and folder from your install page.

3. On macOS and Linux, make it runnable again:

   ```sh
   chmod +x ~/.local/bin/pterm
   ```

4. Start it again:

   ```sh
   pterm launch --no-open
   ```

Paired phones, projects and settings stay. If the phone shows **A new version is ready**, tap **Reload**.

:::tip
To roll back later, copy the old file first: `cp ~/.local/bin/pterm ~/.local/bin/pterm-old`.
:::

## Uninstall Termivoa

Uninstall refuses while a session is active, so end sessions or run `pterm shutdown` first.

1. Preview what would be removed:

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

On Linux, uninstall leaves linger on; `loginctl disable-linger` turns it off.

## Remove Tailscale Serve

Uninstall does not change Tailscale. If `pterm setup` turned on Tailscale Serve, remove it yourself after you check what Serve shares:

```sh
tailscale serve status
```

:::caution
`tailscale serve reset` removes **all** Serve settings, also ones that are not from Termivoa.
:::

## Where does Termivoa store data?

The folders use the old working name "Phone Terminal", so existing installs keep their data.

| System | Data |
| --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal` and `~/Library/Caches/Phone Terminal` |
| Linux | `~/.local/share/phone-terminal`, `~/.local/state/phone-terminal` and `/run/user/<uid>/phone-terminal` |
| Windows | `%LOCALAPPDATA%\Phone Terminal` |

Logs are in a `logs` folder inside these (Linux: `~/.local/state/phone-terminal/logs`). Terminal content is never saved to disk.

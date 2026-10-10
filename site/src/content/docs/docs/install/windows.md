---
title: Install on Windows
description: Install the free Termivoa pterm beta on Windows 11 x64 with PowerShell and curl.exe, then start it at every sign-in with two Scheduled Tasks.
lastUpdated: 2026-10-10
---

Termivoa installs on Windows 11 (x64) as one file, `pterm.exe`, that you download from GitHub with `curl.exe` and set up with `pterm setup`. It is free and open source; you need no account.

:::caution
Open-source beta (`v0.1.0-beta.4`). The binaries are not signed. Windows support is written but not yet tested on real hardware.
:::

## Requirements

- Windows 11 on x64.
- [Tailscale](https://tailscale.com/download) on the computer and the phone, signed in to the same account.

No administrator rights are needed. [Set up a GitHub repository](/docs/guides/github/) also needs `git` on the computer.

## Install

In PowerShell, make a folder, download the beta with `curl.exe` (part of Windows 11) and run setup:

```powershell
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\Termivoa" | Out-Null
cd "$env:LOCALAPPDATA\Programs\Termivoa"
curl.exe -fsSL -o pterm.exe https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4/pterm-windows-amd64.exe
.\pterm.exe setup
```

Then follow [Pair your phone](/docs/pair-your-phone/).

`pterm` is not on your PATH. Where these docs say `pterm`, go to this folder and type `.\pterm.exe`, for example `.\pterm.exe doctor`.

With the [GitHub CLI](https://cli.github.com/) (`gh`), this line replaces the `curl.exe` line:

```powershell
gh release download v0.1.0-beta.4 -R intojhanurag/Termivoa -p pterm-windows-amd64.exe -O pterm.exe
```

## Check the download (optional)

Before you run setup, compare the checksum (a fingerprint of the file) with the release's `SHA256SUMS` file:

```powershell
(curl.exe -fsSL https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4/SHA256SUMS) | Select-String pterm-windows-amd64.exe
(Get-FileHash pterm.exe).Hash.ToLower()
```

The two long numbers must be the same. If they are not, delete `pterm.exe` and download it again.

## Why use `curl.exe` and this folder?

- The binary is not signed. A browser download gets a "downloaded from the internet" mark that makes SmartScreen warn; a `curl.exe` or `gh` download does not.
- Do not put `pterm.exe` on the Desktop, in Documents or in a Temp folder. OneDrive may move those folders, and setup refuses Temp. The startup tasks run this exact path.

## How does it start when you sign in?

`pterm setup` adds two Scheduled Tasks (Windows entries that start a program at sign-in) for your account only:

- `Phone Terminal Broker (<user>)`: the broker, which owns your sessions.
- `Phone Terminal Gateway (<user>)`: the gateway, which serves the phone page on this computer only (`127.0.0.1:8787`). It starts 10 seconds after the broker.

Both run as you, at normal priority, never as an administrator. Task Scheduler restarts them if they fail.
"Phone Terminal" is the old working name, kept so existing installs keep working.

## Where does pterm put its files?

| What | Where |
| --- | --- |
| Program | `%LOCALAPPDATA%\Programs\Termivoa\pterm.exe` |
| Data | `%LOCALAPPDATA%\Phone Terminal` |
| Logs | `%LOCALAPPDATA%\Phone Terminal\logs\` (`broker.log`, `gateway.log`) |

## Start WSL bash sessions

With a WSL distro installed, Termivoa offers a **WSL bash** shell next to PowerShell, which opens in the project folder.

## Keep the computer awake

A sleeping computer cannot answer the phone. Check **Settings → System → Power**.

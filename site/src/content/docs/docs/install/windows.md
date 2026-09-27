---
title: Install on Windows
description: Download the pterm beta on Windows 11 with PowerShell and set it up to start when you sign in.
---

This page helps you install Termivoa on Windows 11 (x64).

:::caution
This is a private beta. Windows support is written but not yet tested on a real Windows computer. Expect rough edges.
:::

## Requirements

- Windows 11 on x64.
- [Tailscale](https://tailscale.com/download) on the computer and on your phone, signed in to the same account.
- The [GitHub CLI](https://cli.github.com/) (`gh`), signed in with `gh auth login`. You need an invite to the private repository.

No administrator rights are needed.

## Install

Run these commands in PowerShell.

1. Make a folder for the program:

   ```powershell
   New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\Termivoa" | Out-Null
   ```

2. Go to that folder:

   ```powershell
   cd "$env:LOCALAPPDATA\Programs\Termivoa"
   ```

3. Download the beta:

   ```powershell
   gh release download v0.1.0-beta.1 -R intojhanurag/Termivoa -p pterm-windows-amd64.exe -O pterm.exe
   ```

4. Run setup:

   ```powershell
   .\pterm.exe setup
   ```

Then follow [Pair your phone](/docs/pair-your-phone/).

`pterm` is not on your PATH after this. In PowerShell, go to the folder above and type `.\pterm.exe` where these docs say `pterm`. For example: `.\pterm.exe doctor`.

## Why use `gh` and this folder

- The binary is not signed. A file from a browser gets a "downloaded from the internet" mark, and SmartScreen warns about it. Files that `gh` downloads do not get this mark.
- Do not put `pterm.exe` on the Desktop, in Documents or in a Temp folder. OneDrive may move those folders, and setup refuses Temp. The startup tasks run this exact path at every sign-in.

## How it starts when you sign in

Setup adds two Scheduled Tasks for your account only:

- `Phone Terminal Broker (<user>)` owns your sessions.
- `Phone Terminal Gateway (<user>)` serves the phone page on this computer only (`127.0.0.1:8787`). It starts 10 seconds after the broker.

A Scheduled Task is a Windows entry that starts a program at a set time, here when you sign in.
Both run as you, at normal priority, never as an administrator. Task Scheduler restarts them if they fail.

"Phone Terminal" is the old working name. It is kept so that existing installs keep working.

## Where files go

| What | Where |
| --- | --- |
| Program | `%LOCALAPPDATA%\Programs\Termivoa\pterm.exe` |
| Data | `%LOCALAPPDATA%\Phone Terminal` |
| Logs | `%LOCALAPPDATA%\Phone Terminal\logs\` (`broker.log`, `gateway.log`) |

## WSL bash sessions

If a WSL distro is installed, Termivoa offers a **WSL bash** shell next to PowerShell. It opens in the project folder.

## Keep the computer awake

A sleeping computer cannot answer the phone. Check **Settings → System → Power** while you work from the phone.

Next: [Pair your phone](/docs/pair-your-phone/).

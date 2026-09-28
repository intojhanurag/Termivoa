---
title: Troubleshooting
description: Short fixes for the most common Termivoa problems, from an unreachable computer to missing notifications.
---

This page helps you fix common problems. Start with `pterm doctor` on your computer. It checks everything and never prints secrets.

```sh
pterm doctor
```

## "Can't reach your computer"

The phone cannot talk to your computer. Your sessions are safe and keep running.

1. Check that Tailscale is on, on the phone and on the computer.
2. Check that the computer is awake and logged in.
3. Run `pterm doctor` on the computer.
4. Tap **Retry now**, or wait. The phone retries on its own.

Any network works (home Wi-Fi, other Wi-Fi, mobile data) while Tailscale is on.

## It stops working when the computer sleeps

A sleeping computer cannot answer. Keep it on power and stop sleep:

- macOS: **System Settings → Battery → Options → Prevent automatic sleeping on power adapter when the display is off**.
- Ubuntu desktop: turn off **Settings → Power → Automatic Suspend**.
- Windows: check **Settings → System → Power**.

Sessions continue when the computer wakes.

## It reconnects often

Your phone pauses the browser when you switch apps or lock the screen. When you come back, Termivoa first checks the old connection. It reconnects only if the old one does not answer, which usually takes 1–2 seconds. A short amber dot is normal. Your sessions never stop.

To make it steadier:

- Keep the computer awake (see above).
- In the Tailscale app on the phone, keep **VPN On Demand** turned on, so Tailscale reconnects by itself.
- Add Termivoa to the Home Screen and open it from there.

If it still drops often, look at `gateway.log` in the log folder that `pterm doctor` shows. It has one line for each phone connection, with its length and why it ended, for example `phone missed a ping`.

## "This pairing link expired or was already used"

A pairing link works once, for two minutes. Run `pterm pair` again and scan the new QR code.

## "Too many pairing attempts"

Wait a minute, then run `pterm pair` again.

## The phone shows the pairing screen again

The phone was removed with `pterm revoke`, logged out, or its access expired (30 days, or 7 days without use).
Run `pterm pair` and [pair it again](/docs/pair-your-phone/).

## "READ-ONLY" and you cannot type

Another device has control, or the phone reconnected. Tap **Take control**.

## "Delivery uncertain"

The connection broke while you typed. Termivoa does not know if your input arrived, and never resends it on its own.
Look at the terminal before you send it again.

## "This session is gone"

The computer restarted or logged out, or the broker crashed. A lost session cannot come back. Start a new one.

## "A new version is ready"

The app on your computer changed. Tap **Reload**. Your sessions keep running.
A small **update** pill on the Sessions list means the same thing.

## Notifications do not arrive

1. On iPhone, open Termivoa from the Home Screen icon, not from a Safari tab. You need iOS 16.4 or later.
2. Check that the switch in **Settings → Notifications** is on.
3. Check that the phone allows notifications for Termivoa in its own settings.
4. Tap **Send a test notification** in Settings.
5. Check that the computer is awake. It sends the notifications.

A "done" notification comes only after at least 20 seconds of agent work. See [Notifications](/docs/guides/notifications/).

## macOS blocks pterm (Gatekeeper)

The beta is not signed. macOS blocks unsigned files that come from a browser.
Download the file again with `gh release download`, as on the [macOS install page](/docs/install/macos/). Files from `gh` are not blocked.

## Windows shows a SmartScreen or Defender warning

The beta is not signed. Download the file again with `gh release download`, as on the [Windows install page](/docs/install/windows/).
Microsoft Defender may still warn about an unsigned `pterm.exe`.

## `pterm: command not found`

The program folder is not on your PATH. Type the full path in its place:

- macOS and Linux: `~/.local/bin/pterm`
- Windows: go to `%LOCALAPPDATA%\Programs\Termivoa` and type `.\pterm.exe`

## Setup does not ask the Tailscale questions (Linux)

You are not the Tailscale operator. Run this once, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

## Setup says port 443 serves something else

Tailscale Serve already shares another service on HTTPS port 443. Setup changes nothing, so it does not break that service.
Fix the conflict yourself. Then run `pterm setup` again.

## No "Preview ready" chip

- Check that you said `y` to the preview question in setup. `pterm doctor` shows if previews are set up.
- Only servers that the session itself started are shown. A server from another terminal window is never offered.

See [Dev-server preview](/docs/guides/preview/).

## Sessions end when you log out of a server

Turn on linger. Run `pterm setup` again and answer `y` to the linger question. See [Install on Linux](/docs/install/linux/#server-or-vps-ssh-only).

## Where are the logs?

| System | Logs |
| --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal/logs/` |
| Linux | `~/.local/state/phone-terminal/logs/` |
| Windows | `%LOCALAPPDATA%\Phone Terminal\logs\` |

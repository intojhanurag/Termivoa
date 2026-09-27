---
title: Security
description: How Termivoa keeps your terminal private, what it stores, and how to report a vulnerability.
---

This page explains how Termivoa protects your computer, in plain words.

## The short version

- Your terminal is reachable only from your own Tailscale devices.
- Each phone must be paired and approved by you on the computer.
- Only one device can type in a session at a time.
- Terminal content is never saved to disk. There is no telemetry and no cloud service.

## Network

- The page listens only on your own computer (`127.0.0.1`, also called loopback). Other machines cannot reach it directly.
- Tailscale Serve makes it available as private HTTPS inside your tailnet. A tailnet is your own private Tailscale network.
- `pterm setup` asks you before it changes any Tailscale Serve setting. It never overwrites another service.
- Termivoa **never** uses Tailscale Funnel or any public tunnel. Funnel would put your terminal on the public internet. `pterm setup` and `pterm doctor` warn you if Funnel is on.

## Pairing and approval

- A pairing link works once and expires after two minutes.
- The phone shows a code. You approve it on the computer only after you compare the codes.
- Only the computer can approve a phone. The phone cannot approve itself.
- A paired phone gets a secure browser cookie. Scripts on other sites cannot read or use it.
- Access ends after 30 days, or after 7 days without use.
- `pterm revoke <device-id>` removes a phone at once, also on open connections.

## Control

- Only one device controls a session at a time. That device holds a lease (a short-lived permission to type).
- Taking control is always your explicit action. Nothing takes control on its own.
- Typing is never queued or resent after a lost connection.
- Phones never receive the raw terminal byte stream, and cannot send raw bytes. They send only keys, text and pastes.

## What is stored, and where

| Stored | Not stored |
| --- | --- |
| Paired phones, projects, shell profiles, session names, settings | Terminal output and history |
| Notification subscriptions for each phone | Your typing |
| Logs | Pairing links and login secrets in clear text (only a hash, a one-way fingerprint, is kept) |

All data is private to your user account. See [Where data lives](/docs/reference/update-and-uninstall/#where-data-lives).

Photos you send are saved in your project folder, in `.termivoa/uploads/`. Snippets are saved on the phone.

## Other features

- **Notifications** carry only the session name, project name and status, encrypted to your phone. See [Notifications](/docs/guides/notifications/).
- **Previews** run on a separate address and show only servers of that session. See [Dev-server preview](/docs/guides/preview/).
- **Photos** can be sent only by the device in control. See [Photo and voice](/docs/guides/photo-and-voice/).

## What Termivoa does not protect against

- A session runs with your normal user permissions. A project folder is not a sandbox (a closed-off area).
- Malware that already runs as your user on the computer is outside this protection.
- The controls have automated tests, but no independent security audit yet.

## Report a vulnerability

Do not open a public issue for a security problem.

Email **aojharaj2004@gmail.com**. Start the subject with `[Termivoa security]`.
Include only the minimum safe details. Never send real secrets, pairing links or terminal content.

This is a one-person project. You get a reply within 7 days, and a first assessment within 30 days.

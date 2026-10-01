---
title: Security
description: How Termivoa keeps your computer's terminal private over Tailscale, what it stores and does not store, and how to report a vulnerability.
lastUpdated: 2026-10-01
---

Termivoa keeps your terminal reachable only from your own Tailscale devices, lets only phones you approve connect, and never saves terminal content to disk. There is no telemetry and no Termivoa cloud service; only notifications pass through your phone's push service (Apple or Google), encrypted.

## Who can reach the page?

- The page listens only on loopback (`127.0.0.1`), so other machines cannot reach it directly.
- Tailscale Serve offers it as private HTTPS in your tailnet (your private Tailscale network).
- `pterm setup` asks before it changes Tailscale Serve settings, and never overwrites another service.
- Termivoa **never** uses Tailscale Funnel or any public tunnel, which would put your terminal on the public internet. `pterm setup` and `pterm doctor` warn if Funnel is on.

## How does pairing work?

- A pairing link works once and expires after two minutes.
- Only the computer can approve a phone, after you compare the phone's code. A phone cannot approve itself.
- A paired phone gets a secure cookie that scripts on other sites cannot read or use.
- Access ends after 30 days, or after 7 days without use.
- `pterm revoke <device-id>` removes a phone at once, also on open connections.

## Who can type in a session?

- Only one device at a time controls a session, by holding a lease (a short-lived permission to type).
- Control never moves to a device by itself. A reconnect always starts read-only. Termivoa takes control by itself only to type the start command of a session you just started, and only when no other device has control.
- Typing is never queued or resent after a lost connection.
- Phones never receive the raw terminal byte stream and cannot send raw bytes, only keys, text, pastes, scroll steps and answer-card choices.

## Who can see changes, commit and push?

- Changes only **reads** git: uncommitted changes and untracked files that git does not ignore.
- **Any paired phone can see these diffs**, including secrets in files that are not ignored. Keep secrets in ignored files such as `.env`.
- Commit and push need **control**, which the computer checks right before git starts.
- A commit includes only ticked files that git lists as changed, and runs your repository's own hooks.
- Push **never forces**, uses your normal git and ssh settings, and never asks for a password on the phone.

## What does Termivoa store?

| Stored | Not stored |
| --- | --- |
| Paired phones, projects, shell profiles, session names, settings | Terminal output and history |
| Notification subscriptions | Your typing |
| Logs | Pairing links and login secrets in clear text (only a one-way hash is kept) |

All data is private to your user account ([Where does Termivoa store data?](/docs/reference/update-and-uninstall/#where-does-termivoa-store-data)). Photos go to the project's `.termivoa/uploads/`. Snippets and, from the next beta, the Wall tile order (session IDs only) stay on the phone.

## Can a worktree escape its folder?

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`.
:::

No. [Worktree](/docs/guides/worktrees/) requests send only a session, a name and a branch. The computer checks them and builds the path under `~/termivoa/worktrees` itself, so the phone cannot pick another folder. Removal needs control, keeps the branch, and asks before it deletes uncommitted changes.

## How do other features stay private?

[Notifications](/docs/guides/notifications/) carry only the session name, project name, status, how long the agent worked and the session ID, encrypted to your phone. [Dev-server previews](/docs/guides/preview/) use a separate address and show only that session's servers. Only the device in control can send [photos](/docs/guides/photo-and-voice/).

## What is not protected?

- A session runs with your normal user permissions. A project folder is not a sandbox (a closed-off area).
- Malware already running as your user is not covered.
- The controls have automated tests, but no independent security audit yet.

## Common questions

### What do I do if I lose my phone?

Run `pterm devices` for its ID, then `pterm revoke <device-id>`. It loses access and notifications at once.

### Can someone on the internet reach my terminal?

No. Only devices in your tailnet can.

## Report a vulnerability

Do not open a public issue for a security problem.

Email **aojharaj2004@gmail.com**. Start the subject with `[Termivoa security]`.
Include only the minimum safe details. Never send real secrets, pairing links or terminal content.

This is a one-person project. You get a reply within 7 days, and a first assessment within 30 days.

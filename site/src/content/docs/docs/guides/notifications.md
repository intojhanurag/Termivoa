---
title: Notifications
description: Get a phone notification when an agent waits for you or finishes, without terminal text leaving your computer.
---

This page helps you get a notification when a coding agent needs you.

## Turn on notifications

1. On iPhone, first add Termivoa to the Home Screen (**Share → Add to Home Screen**). Then open it from there.
2. On the Sessions list, find the card **Get a buzz when an agent needs you**.
3. Tap **Turn on**.
4. Allow notifications when the phone asks.

You can also use the switch in **Settings → Notifications**.
To check that it works, tap **Send a test notification** in Settings.

:::note
iPhone and iPad show notifications only for the Home Screen app, on iOS 16.4 or later. In a normal Safari tab, Settings shows "Add to Home Screen to get notifications".
:::

## What a notification says

| When | Title | Text |
| --- | --- | --- |
| An agent asks a question | `<session> is waiting for you` | `<project> · tap to answer` |
| An agent finished work | `<session> is done · your turn` | `Worked for N min` |
| Test | `Termivoa` | `Notifications are on.` |

A notification never has terminal text. It has no output, no question text, no path and no command.
It has only the session name, the project name and the status. The content is encrypted to your phone.

Tap a notification to open that session.

## When notifications come

- **Waiting:** once for each new question.
- **Done:** when an agent stops after at least 20 seconds of work. Short shell commands do not notify.
- At most one notification per session and kind every 60 seconds.
- Sessions that exited or were lost never notify.

You get a notification even when the session is open on the phone.

## How it works

Your computer sends notifications through your phone's normal push service (Apple, Google, Mozilla or Microsoft).
There is no Termivoa cloud service. Your computer must be awake to send them.

## Turn notifications off

- Turn off the switch in **Settings → Notifications**.
- Removing the phone with `pterm revoke`, or **Log out** on the phone, also stops notifications to that phone.

If notifications do not arrive, see [Troubleshooting](/docs/reference/troubleshooting/#notifications-do-not-arrive).

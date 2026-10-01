---
title: Notifications
description: Termivoa sends a phone notification when a coding agent waits for you or finishes, with only names and status, never any terminal text.
lastUpdated: 2026-10-01
---

Termivoa sends a notification to your phone when a coding agent waits for you or finishes.

## Turn on notifications

1. On iPhone or iPad, add Termivoa to the Home Screen (**Share → Add to Home Screen**) and open it there. Notifications need the Home Screen app and iOS 16.4 or later.
2. On the Sessions list, in the card **Get a buzz when an agent needs you**, tap **Turn on**, then allow notifications.

**Settings → Notifications** has the same switch and a **Send a test notification** button.

## What does a notification say?

| When | Title | Text |
| --- | --- | --- |
| An agent asks a question | `<session> is waiting for you` | `<project> · tap to answer` |
| An agent finished work | `<session> is done · your turn` | `Worked for N min` |

It has only the session name, project name and status, never terminal text (no output, question, path or command). The content is encrypted to your phone. Tapping it opens that session.

## When do notifications come?

- **Waiting:** once per new question.
- **Done:** when an agent stops after at least 20 seconds of work. Short shell commands do not notify.
- At most one per session and kind every 60 seconds, and 12 per minute in total.
- Exited or lost sessions never notify.
- They come even when the session is open on the phone.

## How are notifications sent?

Your computer, which must be awake, sends them through your phone's normal push service (Apple, Google, Mozilla or Microsoft). There is no Termivoa cloud service.

## Turn notifications off

Turn off the switch in **Settings → Notifications**. Removing the phone (`pterm revoke` or **Log out** on the phone) also stops them.

If they do not arrive, see [Notifications do not arrive](/docs/reference/troubleshooting/#notifications-do-not-arrive).

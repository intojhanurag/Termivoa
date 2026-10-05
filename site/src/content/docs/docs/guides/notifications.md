---
title: Notifications
description: Termivoa sends a phone notification when a coding agent waits for you or finishes, with only names and status, never any terminal text.
lastUpdated: 2026-10-04
---

Termivoa sends a notification to your phone when a coding agent waits for you or finishes.

## Turn on notifications

1. On iPhone or iPad, add Termivoa to the Home Screen (**Share → Add to Home Screen**) and open it there. In a Safari tab, **Settings** shows "To get notifications on iPhone, add Termivoa to your Home Screen." with these steps, and no switch; iPhone lets no website add itself. Notifications need the Home Screen app and iOS 16.4 or later. Until then, Termivoa shows **Add to Home Screen to get notifications**.
2. On the Sessions list, in the card **Get a buzz when an agent needs you**, tap **Turn on**, then allow notifications.

**Settings → Notifications** has the same switch and a **Send a test notification** button.

## What does a notification say?

| When | Title | Text |
| --- | --- | --- |
| An agent asks a question | `<session> is waiting for you` | `<project> · tap to answer` |
| An agent finished work | `<session> is done · your turn` | `Worked for N min` |
| You tap **Send a test notification** | `Termivoa` | `Notifications are on.` |

It has only the session name, project name, status and work time, never terminal text (no output, question, path or command). The content is encrypted to your phone. Tapping it opens that session.

## Do notifications come from every computer?

Yes. With [several computers](/docs/guides/computers/), each computer sends its own notifications, and one switch controls all of them.

| Question | Answer |
| --- | --- |
| How do I know which computer it is? | The title and the text of a notification from another computer both start with its name, for example `vps · <session> is waiting for you` and `vps · <project> · tap to answer`. That computer writes the words after the name. Home's notifications have no name in front. The name is the first part of the computer's address, also after you rename it in the app. |
| What does a tap open? | That session on that computer. |
| I added a computer later. | It starts to notify when it is added. There is no new permission prompt. |
| I removed a computer, or it shows **pair again**. | Its notifications stop. |
| One computer cannot be reached. | The others still notify. **Settings → Notifications** shows **No notifications from vps.** When the computer is back and the app is open, it starts to notify again by itself. |
| What does **Send a test notification** do? | Each computer sends one. The note says **Sent**, and names a computer that sent none. |

Before `v0.1.0-beta.3`, only home sent notifications. On iPhone, notifications from other computers are not tested on a real phone yet; if they do not arrive there, home's notifications still work.

## When do notifications come?

- **Waiting:** once per new question.
- **Done:** when an agent stops after at least 20 seconds of work. Short shell commands do not notify.
- At most one per session and kind every 60 seconds, and 12 per minute in total.
- Exited or lost sessions never notify.
- They come even when the session is open on the phone.

## How are notifications sent?

The computer that runs the session, which must be awake, sends them through your phone's normal push service (Apple, Google, Mozilla or Microsoft). There is no Termivoa cloud service.

## Turn notifications off

Turn off the switch in **Settings → Notifications**; it turns them off for every computer. Removing the phone (`pterm revoke` on a computer stops that computer's notifications; **Log out** on the phone stops all) also stops them.

If they do not arrive, see [Notifications do not arrive](/docs/reference/troubleshooting/#notifications-do-not-arrive).

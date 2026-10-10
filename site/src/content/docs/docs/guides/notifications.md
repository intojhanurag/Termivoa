---
title: Notifications
description: Termivoa sends a phone notification when a coding agent waits for you or finishes, with only names and status, never any terminal text.
lastUpdated: 2026-10-10
---

Termivoa sends a notification to your phone when a coding agent waits for you or finishes.

## Turn on notifications

1. On iPhone or iPad, add Termivoa to the Home Screen (**Share → Add to Home Screen**) and open it there. In a Safari tab, **Settings** shows "To get notifications on iPhone, add Termivoa to your Home Screen." with these steps, and no switch; iPhone lets no website add itself. The Sessions screen also shows one line, "For notifications, add Termivoa to your Home Screen", with **See how** and **Not now**. Notifications need the Home Screen app and iOS 16.4 or later.
2. On the Sessions list, in the card **Get a buzz when an agent needs you**, tap **Turn on**, then allow notifications.

**Settings → This phone → Notifications** has the same switch and a **Send a test notification** button.

## What does a notification say?

| When | Title | Text |
| --- | --- | --- |
| An agent asks a question | `<session> is waiting for you` | `<project> · tap to answer` |
| An agent finished work | `<session> is done · your turn` | `Worked for N min` |
| You tap **Send a test notification** | `Termivoa` | `Notifications are on.` |

It has only the session name, project name, status and work time, never terminal text (no output, question, path or command). The content is encrypted to your phone. Tapping it opens that session.

After a tap, the session also shows a **Next** button that goes to the next agent that waits. A notification has no answer buttons, on iPhone or on Android. See [What does a notification tap do?](/docs/guides/needs-you/#what-does-a-notification-tap-do)

## Do notifications come from every computer?

Yes. With [several computers](/docs/guides/computers/), each computer sends its own notifications, and one switch controls all of them.

| Question | Answer |
| --- | --- |
| How do I know which computer it is? | The title and the text of a notification from another computer both start with its name, for example `vps · <session> is waiting for you` and `vps · <project> · tap to answer`. That computer writes the words after the name. Home's notifications have no name in front. The name is the first part of the computer's address, also after you rename it in the app. |
| What does a tap open? | That session on that computer. |
| I added a computer later. | It starts to notify when it is added. There is no new permission prompt. |
| I removed a computer, or it shows **pair again**. | Its notifications stop. |
| One computer cannot be reached. | The others still notify. **Settings → This phone → Notifications** shows **No notifications from vps.** When the computer is back and the app is open, it starts to notify again by itself. |
| What does **Send a test notification** do? | Each computer sends one. The note says **Sent**, and names a computer that sent none. |

Before `v0.1.0-beta.3`, only home sent notifications. On iPhone, notifications from other computers are not tested on a real phone yet; if they do not arrive there, home's notifications still work.

## When do notifications come?

- **Waiting:** once per new question.
- **Done:** when an agent stops after at least 20 seconds of work. Short shell commands do not notify.
- At most one per session and kind every 60 seconds, and 12 per minute in total.
- **Send a test notification** has its own limit: one per phone every 5 seconds and 6 per minute for all phones together. Tests never use up the 12 above.
- Exited or lost sessions never notify.
- They come even when the session is open on the phone.

## Does herdr buzz too?

Yes. A computer with [herdr](/docs/guides/herdr/) also buzzes when a herdr agent is blocked or finished. Each computer's page in **Settings** has the switches **Buzz when blocked** and **Buzz when finished**. herdr buzzes have their own limit, 6 per minute, so they never use up the 12 above. See [How do I turn herdr off or the buzz off?](/docs/guides/herdr/#how-do-i-turn-herdr-off-or-the-buzz-off)

## How are notifications sent?

The computer that runs the session, which must be awake, sends them through your phone's normal push service (Apple, Google, Mozilla or Microsoft). There is no Termivoa cloud service.

Many other phones whose push services do not answer no longer use up the whole send window before your phone gets a delivery attempt. Delivery still depends on your phone's push service and internet connection.

| Push-service result | What happens next |
| --- | --- |
| A request takes too long or the service cannot be reached | Termivoa keeps the subscription and tries again on a later notification. |
| The service says the subscription has expired | Termivoa removes that subscription. Open Termivoa with notifications on to subscribe again. |

## Turn notifications off

Turn off the switch in **Settings → This phone → Notifications**; it turns them off for every computer. Removing the phone (`pterm revoke` on a computer stops that computer's notifications; **Log out** on the phone stops all) also stops them.

If they do not arrive, see [Notifications do not arrive](/docs/reference/troubleshooting/#notifications-do-not-arrive).

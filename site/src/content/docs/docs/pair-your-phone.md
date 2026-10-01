---
title: Pair your phone
description: Connect a phone to Termivoa with a one-time QR code and a 6-digit code check, add it to the Home Screen, then rename or remove paired phones.
lastUpdated: 2026-10-01
---

Termivoa pairs a phone with a one-time QR code from `pterm pair` and a 6-digit code that you check on both screens.

## Pair a phone

`pterm setup` ends with pairing. At other times, run `pterm pair`.

1. Scan the QR code with the phone camera and open the link.
2. Name the phone, for example "My iPhone", and tap **Pair this device**.
3. The phone shows a code, such as `123-456`. Your computer asks:

   ```text
   Approve only if the phone shows exactly this code.
   Phone "My iPhone" shows code 123-456. Approve? [y/N]
   ```

4. If the codes match, type `y` and press Enter. The phone opens the Sessions list.

![The Sessions screen after pairing shows "Your computer · connected" and the sessions on the computer.](../../../assets/docs/phone-pair-done.webp)

If the codes are different, type `n`, then tap **Codes don't match? Cancel** on the phone. The phone stays unpaired.
`pterm` never approves a phone without your answer.

To pair another phone, run `pterm pair` again. Each phone gets its own access.

:::caution
The link and the QR code are a secret. Do not share them in chats, screenshots or issues. The link works once and expires after two minutes.
:::

## Approve later

`pterm pair --no-wait` prints the link and returns. Later, list waiting phones and their codes, compare, and approve one:

```sh
pterm pair pending
pterm pair approve <pending-id>
```

`--no-qr` prints only the link. `--origin <url>` uses another address for this link.

## Add to the Home Screen

On iPhone, in Safari, tap **Share**, then **Add to Home Screen**. Termivoa then opens like an app. iPhone needs this for [Notifications](/docs/guides/notifications/).
On Android, Chrome can also add it to the home screen.

## See and rename your phones

`pterm devices` lists paired phones, their status and last use.
Rename one with `pterm devices rename <device-id> <label>`, or on the phone in **Settings → This device**.

## Remove a phone

If you lose a phone or stop using it:

1. Find its ID with `pterm devices`.
2. Run `pterm revoke <device-id>`.
3. Type `y` to confirm.

The phone loses access at once, also on open connections, and stops getting notifications.
On the phone itself, **Settings** → **Log out** removes its access.

## When must a phone pair again?

After 30 days from pairing, or after 7 days without use. The phone then shows the pairing screen; run `pterm pair` again.

## Common questions

### What if I press Enter by mistake at the approve question?

Enter means no. Run `pterm pair` and pair it again.

### Does logging out stop my sessions?

No. **Log out** only removes that phone's access.

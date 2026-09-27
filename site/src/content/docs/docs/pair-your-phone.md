---
title: Pair your phone
description: Connect a phone to your computer with a QR code and a code check, add it to the Home Screen, and remove phones.
---

This page helps you connect a phone to your computer, and remove it later.

## Pair a phone

`pterm setup` starts pairing at the end. To pair at any other time, run:

```sh
pterm pair
```

1. Your computer shows a link and a QR code.
2. Scan the QR code with the phone camera and open the link.
3. Type a name for the phone, for example "My iPhone". Tap **Pair this device**.
4. The phone shows a code, such as `123-456`, and waits.
5. Your computer asks:

   ```text
   Approve only if the phone shows exactly this code.
   Phone "My iPhone" shows code 123-456. Approve? [y/N]
   ```

6. If the codes are the same, type `y` and press Enter.

The phone then opens your Sessions list.

If the codes are different, type `n` or press Enter. On the phone, tap **Codes don't match? Cancel**.
The phone stays unpaired. pterm never approves a phone without your answer.

:::caution
The link and the QR code are a secret. Do not share them in chats, screenshots or issues. The link works once and expires after two minutes.
:::

## Approve later

If you cannot wait at the computer, print the link and return at once:

```sh
pterm pair --no-wait
```

Later, show the phones that wait for approval, with their codes:

```sh
pterm pair pending
```

Then approve one after you compare its code:

```sh
pterm pair approve <pending-id>
```

Other options: `--no-qr` prints only the link. `--origin <url>` uses another address for this one link.

## Add to the Home Screen

On iPhone, in Safari:

1. Tap **Share**.
2. Tap **Add to Home Screen**.

Termivoa then opens like an app. On iPhone, you need this for [notifications](/docs/guides/notifications/).

On Android, Chrome can also add the page to the home screen.

## Pair another phone

Run `pterm pair` again and repeat the steps. Each phone gets its own access.

## See and rename your phones

List paired phones, their status and last use:

```sh
pterm devices
```

Rename one:

```sh
pterm devices rename <device-id> <label>
```

You can also change the phone's name on the phone, in **Settings → This device**.

## Remove a phone

If you lose a phone, or no longer use it:

1. Find its ID:

   ```sh
   pterm devices
   ```

2. Remove it:

   ```sh
   pterm revoke <device-id>
   ```

3. Type `y` to confirm.

The phone loses access at once, also on open connections. It also stops getting notifications.

To remove access from the phone itself, open **Settings** and tap **Log out**. Your sessions keep running.

## When a phone must pair again

A phone's access ends after 30 days, or after 7 days without use. The phone then shows the pairing screen. Run `pterm pair` and pair it again.

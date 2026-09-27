---
title: Quick start
description: Install pterm, run setup, pair your phone and open your first session in about five minutes.
---

This page takes you from zero to a paired phone and a first session.

## Before you start

- Install [Tailscale](https://tailscale.com/download) on your computer and your phone. Sign in to the same account on both.
- Install the [GitHub CLI](https://cli.github.com/) (`gh`) on your computer and run `gh auth login`. The beta is in a private repository, so you need an invite.

## 1. Install pterm

Follow the page for your computer. Each page ends with `pterm setup`.

- [macOS](/docs/install/macos/)
- [Linux](/docs/install/linux/)
- [Windows](/docs/install/windows/)

## 2. Run setup

If the install page did not start it, run:

```sh
pterm setup
```

Setup first checks your computer and changes nothing.
Then it shows a list of what it will do and asks once: `Continue? [y/N]`.

1. Read the list.
2. Type `y` and press Enter.
3. Setup asks a separate question before it shares the page with your Tailscale devices. Type `y` if the command it shows is correct.
4. Setup may ask a second, separate question about dev-server previews (port 8443). This one is optional.

Setup starts Termivoa at every login and adds your home folder as the project **Home**.
It never uses Tailscale Funnel, so the page never goes on the public internet.

## 3. Scan the QR code

At the end, setup shows a QR code.

1. Scan the QR code with your phone camera and open the link.
2. Type a name for the phone and tap **Pair this device**.

The link works once and expires after two minutes.
If it expires, run `pterm pair` to get a new one.

## 4. Approve the code

The phone shows a code, such as `485-431`. Your computer asks:

```text
Phone "My iPhone" shows code 485-431. Approve? [y/N]
```

If the codes are the same, type `y`. If they are different, type `n`.

On iPhone, tap **Share**, then **Add to Home Screen**. You need this for notifications.

## 5. Start your first session

1. On the phone, tap **New session**.
2. Pick a project and a shell.
3. Optional: in **run first**, type a command such as `claude`.
4. Tap **start session**.
5. Tap **Take control** to type.

Close the app when you like. The session keeps running on your computer.

## Next steps

- [Pair your phone](/docs/pair-your-phone/): more phones, removing a phone.
- [Sessions](/docs/guides/sessions/) and [the live terminal](/docs/guides/live-terminal/).
- [Troubleshooting](/docs/reference/troubleshooting/) if something does not work.

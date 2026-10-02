---
title: Use several computers
description: Termivoa can use several computers, such as a Mac, a Linux server and a VPS, from one phone app, with one Sessions list and one Wall for all of them.
lastUpdated: 2026-10-02
---

Termivoa lets one phone app use several computers, for example a Mac, a Linux server and a VPS, with one Sessions list and one Wall.

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`, and it is not tested on a real phone yet. See [Is it tested on a real phone?](#is-it-tested-on-a-real-phone)
:::

:::caution[iOS 16.4 or later]
From the next beta, every Termivoa app needs iOS 16.4 or later, also with one computer. See [The app cannot load after the update](/docs/reference/troubleshooting/#the-app-cannot-load-after-the-update-iphone-next-beta).
:::

## How does it work?

![Your phone runs one Termivoa app, loaded from home. It connects directly to three computers: mac, marked HOME, which "serves the app and keeps your list"; vps, which "approves your phone itself"; and pi, which "keeps its own login for you". A hand note says: one app, every computer. each one says yes to your phone itself.](../../../../assets/docs/illo-computers.webp)

| Word | What it means |
| --- | --- |
| Home | The computer whose address is on your Home Screen. It serves the app. |
| Peer | Any other computer you add. The app talks to it directly; nothing goes through home. |
| Via | The home address a peer accepts your phone from. `pterm devices` shows it in the **VIA** column. |

Each computer runs its own `pterm`, approves your phone itself, keeps its own login cookie for it, and can revoke it itself.

## How do I choose home?

Pick a computer that is **always on** and **safe**, such as a small server. Not the laptop where you run `npm install`, agents and dev servers.

:::caution
If home is hacked, every computer you added through it is at risk too, because home serves the app's code. See [What do I do if home was hacked?](#what-do-i-do-if-home-was-hacked)
:::

| Rule | Why |
| --- | --- |
| Only Termivoa on port 443 of each computer's Tailscale address | Any other Tailscale Serve handler there could control Termivoa. `pterm setup` refuses, and `pterm doctor` shows how to move it. See [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers-next-beta). |
| Changing home later means starting over | A new home is a new app, so you add every peer again. |

## How do I add a computer?

You need `pterm setup` done on the new computer, and both computers in the same tailnet with the same Tailscale user.

![Three steps. 1, on the new computer: vps $ pterm pair --via mac. 2, on your phone: tap the link, check the address, tap Add vps, then Continue. 3, same code on both? 742-172, type y on vps. A hand note says: vps is added. that's it. a link alone never adds a computer.](../../../../assets/docs/illo-add-computer.webp)

1. **On the new computer**, run `pterm pair --via mac`. Use home's machine name, or its full address such as `https://mac.tailx.ts.net`. **Settings → Computers** shows the exact command.
2. **Get the link into the app.** It works once, for two minutes.
   - **Tap it**, if you ran the command in a Termivoa session (for example after `ssh vps`).
   - **Paste it**: **Settings** → **+ Add a computer** → paste → **Check link**.
3. **Check the sheet.** **Add this computer?** shows the **Address**, **Machine** and **Owner** that home found in its Tailscale status. Tap **Add vps**, then **Continue**.

   ![The Add this computer? sheet with Address https://vps.tailx.ts.net, Machine vps, Owner ada@example.com, and the buttons Add vps and Cancel.](../../../../assets/docs/phone-computers-confirm.webp)

4. **Compare the code.** The app asks **Does vps show this code?** If vps shows the same code, type `y` there. The app says **vps is added**.

   ![The screen Does vps show this code? with the code 742-172, the text If it matches, type y in the terminal on vps., Waiting for vps, and Codes don't match? Cancel.](../../../../assets/docs/phone-computers-code.webp)

You can leave the page to type `y` in the app's own terminal: tap **Back to terminal**. A bar says **Adding vps… approve on vps** until it is done.

| If… | Then |
| --- | --- |
| The codes are different | Type `n`, then tap **Codes don't match? Cancel**. |
| You change your mind | Tap **Cancel** on the sheet, the page or the bar. After a `y`, the app also signs out of vps. |
| A computer is a tagged Tailscale node | Check its tags, then run `pterm pair --via mac --allow-tagged`. |
| You want to approve later | `pterm pair --via mac --no-wait`, then `pterm pair pending` and `pterm pair approve <pending-id>`. |
| The link opened in Safari or the camera | Tap **Copy link** there and paste it in the app. Safari never adds the computer. |

## What changes in the app?

![Sessions with 2 computers · connected, the chips All, mac and vps, and the sessions mac-1 and vps-1, each with its computer's colour dot and name, above the button new session on….](../../../../assets/docs/phone-computers-sessions.webp)

| Screen | With 2 or more computers |
| --- | --- |
| Sessions | Chips **All**, **mac**, **vps** filter by computer. Each row has a colour dot and the computer's name. Waiting sessions come first, from all computers. |
| New session | **❯ new session on…** and a **Computer** row pick where it runs. Projects and shells come from that computer. |
| [Wall](/docs/guides/wall/) | Tiles from all computers. At most 4 are live at once (5 updates per second each); the rest show **Paused**. |
| Live, [Changes](/docs/guides/changes/), [Files](/docs/guides/files/) | Work the same on every computer. |

With one computer, the app looks the same as before.

## What if a computer is offline?

The other computers keep working. The one that does not answer gets its own row.

![Sessions with three mac sessions and a dashed row vps · can't reach with a Retry button.](../../../../assets/docs/phone-computers-offline.webp)

| Row | Meaning | Do this |
| --- | --- | --- |
| **vps · can't reach** | The phone cannot reach vps now. | Check that vps is on and in your tailnet, then tap **Retry**. |
| **vps · pair again** | vps no longer accepts this phone (revoked, logged out, expired, or Tailscale changed on home). | Run `pterm pair --via mac` on vps and add it again. |
| **Update Termivoa on vps** | Termivoa on vps is too old for this app. | Update Termivoa on vps. |

If home is offline, the app still opens from its saved copy and reaches the peers.

## How do I update the computers?

One at a time, in any order. The app refuses only a computer that is too old ("Update Termivoa on vps"). After you update home, tap **update** on the Sessions list, or close and open the app.

## How do I rename, remove or log out?

| Action | Where | What happens |
| --- | --- | --- |
| Rename | **Settings → Computers → Rename** | Changes the name on this phone only. |
| Remove | **Settings → Computers → Remove** | Signs this phone out of vps first; its sessions keep running. If vps cannot be reached, run `pterm revoke <device-id>` on vps. |
| Log out | **Settings → Log out** | Signs out of every computer it can reach, and names the ones it could not. Run `pterm revoke` there. |
| Remove for every phone | On home: `pterm computers remove https://vps.tailx.ts.net` | Takes vps off every phone's list. Run `pterm revoke` on vps too. |

Home cannot be renamed or removed here.

## What do I do if home was hacked?

1. On **every peer**, run `pterm revoke --via https://mac.tailx.ts.net` and type `y`.
2. Clean or replace home.
3. Remove the app's data from the phone:
   - **iPhone:** delete the Termivoa app from the Home Screen.
   - **Android:** remove the app, then in Chrome go to **Settings → Site settings → All sites**, pick home's address and delete its data.
4. Add home to the Home Screen again, [pair your phone](/docs/pair-your-phone/), and add each peer again.

Every step matters: a hacked home can leave a hidden background script on the phone, and can hold a peer cookie for up to 30 days. Step 1 ends that cookie.

## What do I do if I lose my phone?

1. On home: `pterm devices`, then `pterm revoke <device-id>`. It lists the other computers the phone used.
2. On each of them: `pterm devices` (the **VIA** column shows home), then `pterm revoke <device-id>`.

## What are the limits?

| Limit | Why |
| --- | --- |
| One tailnet, one Tailscale user | Each computer checks that home belongs to you. |
| At most 8 computers plus home | For each phone. |
| WSL cannot be a peer | Tailscale usually runs on Windows, so WSL cannot check the tailnet. |
| Notifications and Preview come from home only | Peer sessions show **Waiting** and **Working** while the app is open. |
| Reinstalling Tailscale | On home: peers show **pair again**. On a peer: home drops it from the list; add it again. |

## Is it tested on a real phone?

Not yet. Chrome and WebKit pass all 59 checks with two test computers, but the real iPhone and Android check (Gate 0, `scripts/gate0/`) has not run. If it fails on iPhone, the design changes before release.

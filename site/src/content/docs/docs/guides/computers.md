---
title: Use several computers
description: Termivoa can use several computers, such as a Mac, a Linux server and a VPS, from one phone app, with one Sessions list and one Wall for all of them.
lastUpdated: 2026-10-02
---

Termivoa lets one phone app use several computers, for example a Mac, a Linux server and a VPS, with one Sessions list and one Wall.

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`. It is also not tested on a real iPhone or Android phone yet. See [Is it tested on a real phone?](#is-it-tested-on-a-real-phone)
:::

:::caution[iOS 16.4 or later]
From the next beta, every Termivoa app needs iOS 16.4 or later on iPhone and iPad, also with one computer. Older Safari does not tell the computer where a request comes from, so the computer refuses it. See [The app cannot load after the update](/docs/reference/troubleshooting/#the-app-cannot-load-after-the-update-iphone-next-beta).
:::

## What is multi-computer?

Each computer runs its own `pterm`. One computer, **home**, serves the app on your phone. The app talks directly to each other computer over your tailnet. Nothing goes through home.

| Word | What it means |
| --- | --- |
| Home | The computer whose address you added to the Home Screen. It serves the app. |
| Peer | Any other computer you add. It approves your phone itself, and it can remove your phone itself. |
| Via | The home address that a peer accepts your phone from. `pterm devices` shows it in the **VIA** column. |

Each computer keeps its own pairing, its own approval and its own `pterm revoke`. Each peer gives the phone its own login cookie. Scripts cannot read it, and no other computer can use it.

## How do I choose home?

Choose a computer that is **always on** and **safe**: for example a small server where you do not run code you do not trust. Do not choose the laptop where you run `npm install`, coding agents and dev servers.

:::caution
If home is hacked, every computer that you added through it is hacked too. Home serves the app's code, so bad code from home can use every peer, also when the app is closed. Termivoa makes this risk smaller, but it cannot remove it. See [What do I do if home was hacked?](#what-do-i-do-if-home-was-hacked)
:::

On home, Tailscale Serve must share only Termivoa on its HTTPS address (port 443). Any other handler there could control every peer. `pterm setup` refuses to run while other handlers share it, and `pterm doctor` reports them as a problem, with the commands to move them. This is true on every computer, not only on home. See [Setup refuses other Tailscale Serve handlers](/docs/reference/troubleshooting/#setup-refuses-other-tailscale-serve-handlers-next-beta).

To change home later, you need a new Home Screen app, and you must add every peer again.

## How do I add a computer?

Before you start:

- Termivoa is set up on the new computer with `pterm setup`, including Tailscale Serve.
- Both computers are in the same tailnet and belong to the same Tailscale user.

Then:

1. On the new computer, run `pterm pair --via <home>`. `<home>` is home's machine name, such as `mac`, or its full address, such as `https://mac.tailx.ts.net`. **Settings → Computers** in the app shows the exact command.
2. The command prints an add-computer link. It works once and expires after two minutes. Get it into the app in one of two ways:
   - **Tap it:** run the command in a Termivoa session in the app (for example after `ssh vps`), then tap the link in the terminal.
   - **Paste it:** in the app, open **Settings**. Under **Computers**, tap **+ Add a computer**, paste the link and tap **Check link**.
3. The sheet **Add this computer?** shows the **Address**, the **Machine**, and the **Owner** (or the **Tags**). Home checks the computer in its own Tailscale status first. If all is correct, tap **Add vps**.
4. The **Add a computer** page shows the full **Address** again: "Continue only if you just ran pterm pair --via on vps. Next, this phone shows a code. Check it on vps." Tap **Continue**. Nothing is sent to vps before you tap it.
5. The app shows **Connecting to vps…**, then **Does vps show this code?** with a code.
6. The new computer asks:

   ```text
   Approve only if the phone shows exactly this code.
   Phone "My iPhone" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailx.ts.net, owner you@example.com). Approve? [y/N]
   ```

7. If the codes match, type `y` and press Enter. The app shows **vps is added**. Tap **Open sessions**.

You can leave the page to type `y`, for example in your `ssh vps` session in the app. The add goes on, and a bar at the top says **Adding vps… approve on vps**, with **Open** and **Cancel**. When vps approves, the bar says **vps is added**. If the add fails, it says **vps was not added**; tap **Open** to see why.

If the codes are different, type `n`, then tap **Codes don't match? Cancel** in the app. **Cancel** on the sheet, the **Add a computer** page or the bar also stops the add: home deletes its note of the computer. If you cancel after you typed `y`, the app also signs this phone out of vps.

A tap on the link never adds a computer by itself. A program can print a false link, so you always see the sheet and the full address, and home always checks the computer first.

| Case | What to do |
| --- | --- |
| Home or the new computer is a tagged Tailscale node (no user owner) | Check the tags, then run `pterm pair --via mac --allow-tagged`. |
| You want to approve later | Run `pterm pair --via mac --no-wait`, then `pterm pair pending` (see the **VIA** column) and `pterm pair approve <pending-id>`. |
| You opened the link with the camera or in Safari | That page says "Open your Termivoa app on mac and paste this link there." Tap **Copy link** and paste it in the app. Safari keeps other cookies than the Home Screen app, so that page never adds the computer. |

There is no QR code for this link, because the phone camera opens links in Safari, not in the app.

## What changes on the Sessions list?

With more than one computer:

| You see | What it means |
| --- | --- |
| "2 computers · 1 waiting · 1 working" | The top line counts all computers. |
| **All**, **mac**, **vps** chips | Tap a chip to show one computer only. **All** shows every computer. |
| A colour dot and a name in each row | The computer the session runs on. Up to 8 computers, each has its own colour; home always has the first one. The colour never stands alone; the name is always there. |
| **❯ new session on…** | Start a session on any computer. |

Sessions that wait for you come first, from all computers. Tap a session to open it. The live terminal, control, the composer, [Changes](/docs/guides/changes/), [Files](/docs/guides/files/) and photos work the same on every computer.

## What changes on the Wall?

The [Wall](/docs/guides/wall/) shows sessions from all computers, and each tile shows its computer's name.

To keep the phone fast, at most 4 tiles are live at a time, with up to 5 updates per second each. The other tiles keep their last screen and show **Paused** until a live place is free. A tile that was not live yet shows the session name and **Paused**. All tiles pause while a session is open or the app is in the background.

## What changes in New session?

**New session** has a **Computer** row with one chip for each computer. The line under the title says **Runs on vps**. The projects and shells in the list are those of that computer.

A computer that cannot be reached cannot be picked; its line shows why. When you start from a session, its computer is picked first; else home.

## What if a computer is offline?

The other computers keep working. Each computer that does not answer gets its own line on the Sessions list:

| Line | What it means | What to do |
| --- | --- | --- |
| "vps · can't reach · last seen 5 min ago" | The phone cannot reach vps now. | Check that vps is on and in your tailnet, then tap **Retry**. |
| "vps · pair again · Run pterm pair --via mac on vps" | vps does not accept this phone now. It was revoked or logged out, it expired, or Tailscale changed on home. | Tap **Computers**. Run `pterm pair --via mac` on vps again and add it again. |
| "Update Termivoa on vps" | Termivoa on vps is too old for this app, or too old to tell its version. The line uses the name you gave vps on this phone. | Update Termivoa on vps. |

In **Settings → Computers**, each computer shows **online**, **checking**, **offline** or **needs you**. Home also shows **this computer**.

If home is offline, the app still opens and reaches the peers. The app keeps a full copy of itself on the phone (never your sessions or terminal text). Settings then says "Your home computer cannot be reached. Device name, notifications and log out need it."

## How do I update the computers?

Update them one at a time, in any order. Each computer tells the app its protocol version, and the app refuses only a computer that is too old for it ("Update Termivoa on vps"). `pterm doctor` shows the version, for example `protocol revision 1 (oldest supported 1)`.

The app comes from home. After you update home, tap **update** on the Sessions list, or close and open the app, to load the new app. An app that stays open from before the update may fail to read settings until you reload it.

## How do I rename or remove a computer?

To rename: in **Settings → Computers**, tap **Rename**, type the **Name on this phone**, and tap **Save name**. The name changes on this phone only.

To remove: tap **Remove**. The app asks:

> Remove vps? This phone signs out of vps first. Its sessions keep running there.

Tap **Remove vps**, or **Keep**. If the app cannot reach vps, it says "vps is removed. Run pterm revoke on vps to sign this phone out there." Then run `pterm revoke <device-id>` on vps.

Home cannot be renamed or removed here.

**Settings → Log out** signs this phone out of every computer it can reach. With 2 or more computers, it says "Signs this phone out of every computer. Running sessions keep running." If a computer cannot be reached, the app names it: "You logged out. This phone no longer has access. Could not sign out of vps. Run pterm revoke there to remove this phone." Then run `pterm revoke <device-id>` on that computer. While it works, the app says **Signing out…**; it waits at most about 6 seconds for each computer. The phone also forgets its list of computers.

On home, `pterm computers` lists each phone's computers. `pterm computers remove https://vps.tailx.ts.net` removes vps from every phone's list. It cannot sign the phones out on vps, so it tells you to run `pterm revoke` there. The phone reads home's list when the app opens. If a vps session is already open then (for example from a link), the app goes to Sessions and says "vps is no longer in your list."

## What do I do if home was hacked?

Do all of these steps:

1. On **every peer**, run `pterm revoke --via https://mac.tailx.ts.net` (home's address) and type `y`. This ends the access of every phone that uses this peer from that home.
2. Clean or replace home.
3. Remove the app's data from the phone:
   - **iPhone:** delete the Termivoa app from the Home Screen. Its storage, service workers and cookies go with it.
   - **Android:** remove the app from the home screen. The app shares Chrome's storage, so also clear the site's data in Chrome: **Settings → Site settings → All sites**, pick home's address, then delete its data.
4. Add home to the Home Screen again and [pair your phone](/docs/pair-your-phone/) with home. Then add each peer again.

Why every step? A hacked home can leave a hidden service worker (a background script) on the phone, with its own notification key. The app removes unknown service workers each time it opens, but only removing the app's data is sure. A hacked home can also get a peer cookie that works without your phone, for up to 30 days. `pterm revoke --via` ends it.

## What do I do if I lose my phone?

Revoke it on **each** computer:

1. On home, run `pterm devices`, then `pterm revoke <device-id>`. After it revokes the phone, it prints "This phone also used these computers. Run `pterm revoke` on each:" and the list.
2. On each computer in that list, run `pterm devices`. The **VIA** column shows home's address for that phone. Run `pterm revoke <device-id>`.

## What are the limits?

| Limit | Why |
| --- | --- |
| One tailnet only | Computers in different tailnets cannot be added. |
| WSL cannot be a peer | Under WSL, Tailscale usually runs on Windows, not in WSL, so the peer cannot check the tailnet. |
| Tagged nodes need `--allow-tagged` | A tagged node has no user owner to compare, so you must check its tags and allow it. |
| Notifications come from home only | Sessions on peers show **Waiting** and **Working** while the app is open, but do not send a buzz yet. |
| No dev-server previews from peers | **Preview** works for sessions on home only. |
| At most 8 computers plus home | For each phone. |
| A new home means pairing again | A new home is a new app with its own cookies, so you add every peer again. |
| Reinstalling Tailscale on home | Home gets a new Tailscale node ID. Each peer then revokes the phones that use it, and shows "vps · pair again". |
| Reinstalling Tailscale on a peer | The peer gets a new Tailscale node ID. Home drops it from the list. Add it again with `pterm pair --via mac` on the peer. |
| iOS 16.4 or later | Older Safari does not send the headers that the computer checks. This is true for every Termivoa app, also with one computer. |

## Is it tested on a real phone?

Not yet. Multi-computer needs the Home Screen app on iPhone and Android to keep a cookie for another address in the same tailnet. This cookie check passes in Chrome and WebKit on a computer, but the real-phone check (Gate 0, `scripts/gate0/`) has not run. If it fails on iPhone, the design changes before release.

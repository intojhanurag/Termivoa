---
title: Dev-server preview
description: Termivoa opens a dev server from a session, such as Vite on port 5173, full screen on your phone over a private Tailscale address.
lastUpdated: 2026-10-03
---

Termivoa opens the web page of a dev server that a session runs, full screen in the app on your phone.

## Turn on previews

Previews are optional and need a second, private Tailscale address on port 8443. `pterm setup` asks about it: answer `y` if the command is correct. With [several computers](/docs/guides/computers/), each computer asks its own question. To set it up later, run:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:8789
```

`pterm doctor` checks the preview setup.

## Open a preview

1. Start a dev server in a session, such as `npm run dev`, or ask Claude Code to start it.
2. Within a few seconds, the live terminal shows a **Preview ready** chip with the port, such as `:5173`.
3. Tap **Open ↗**.

Live reload works. A stopped server shows "Dev server stopped".

| Control | What it does |
| --- | --- |
| **‹ terminal** | Goes back to the live terminal. |
| **↻** | Loads the preview again (**Next beta**). |
| **Tell agent** | Goes back with the compose sheet open. |

## Can I preview a session on another computer?

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`, and it is not tested on a real phone yet.
:::

Yes. With [several computers](/docs/guides/computers/), open a session on that computer, wait for **Preview ready**, and tap **Open ↗**. The bar over the page shows the computer's name and **untrusted page**, such as `vps · untrusted page`.

| You need | Why |
| --- | --- |
| A yes to the preview question in `pterm setup` on that computer | Each computer shares its own dev servers. Home's answer does not count for the others. |
| The same new Termivoa on every computer | A session on a computer with an older Termivoa shows no **Preview ready** chip. |
| Port 8443 of that computer open to your phone in your tailnet | If your Tailscale access rules (ACLs) block it, the preview cannot open. |

| You see | Meaning |
| --- | --- |
| "Opening preview…" | The app asks that computer's preview address first. It waits at most 5 seconds. |
| "Can't open the preview on vps. …" | Nothing answered. See [Troubleshooting](/docs/reference/troubleshooting/#cant-open-the-preview-on-vps-next-beta). |
| **vps · pair again**, **vps · can't reach** or **Update Termivoa on vps** | That computer does not accept or answer the phone now. The open preview is removed and this text shows. See [What if a computer is offline?](/docs/guides/computers/#what-if-a-computer-is-offline) |

If you added the computer after you opened the app, the app reloads itself once when you first open a preview on it, then shows the preview. You do nothing.

## What does "untrusted page" mean?

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`, and it is not tested on a real phone yet.
:::

It means that the other computer chooses the page in the frame, not the app. The app shows the computer's name and **untrusted page** over every preview from another computer. The page cannot cover or change that bar.

| The page from another computer | |
| --- | --- |
| Cannot | Read the app, the terminal, your sessions or the app's sign-in. |
| Can | Show false content, for example a false "pair again" screen. |

Do not type pairing links or passwords into a preview. The real app never asks for them inside a preview. More in [Security](/docs/reference/security/#can-a-preview-from-another-computer-harm-the-app).

## What does ↻ do?

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`, and it is not tested on a real phone yet.
:::

**↻** loads the preview again from the start. Use it when the page is stuck, or when the frame shows a short refusal text such as "Origin not allowed."

## What are the limits in Safari?

:::note[Next beta]
Not released yet. This is not in `v0.1.0-beta.2`, and it is not tested on a real phone yet.
:::

These limits apply on iPhone and iPad, to previews from home and from other computers.

| When | What happens | Do this |
| --- | --- | --- |
| You go to another page and use the browser's Back to return to the app | The preview loads again by itself. | If a short refusal text shows in the frame, tap **↻**. |
| The previewed page uses its own "back" (for example a back button of the site) | It can move the whole app back, not only the preview. | Open the preview again with **Open ↗**. |

## How are previews kept private?

- Only ports that the session's own programs listen on are shown. Other servers, such as databases, are never offered, even if you type their port.
- The preview runs on a separate address, so the dev site cannot read the app, the terminal or your sessions.
- Every request checks that the phone is still paired, so a removed phone loses the preview within 5 seconds.
- The dev server never gets Termivoa's cookies or your Tailscale identity.
- The preview is private to your Tailscale devices. Termivoa never uses Tailscale Funnel.
- A preview from another computer (**Next beta**) is marked **untrusted page**, and only computers you added and confirmed can be shown.

:::note
All previews from one computer share one address. Do not treat two dev sites of your own sessions as isolated from each other.
:::

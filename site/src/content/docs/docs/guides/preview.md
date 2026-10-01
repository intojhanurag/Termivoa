---
title: Dev-server preview
description: Termivoa opens a dev server from a session, such as Vite on port 5173, full screen on your phone over a private Tailscale address.
lastUpdated: 2026-10-01
---

Termivoa opens the web page of a dev server that a session runs, full screen in the app on your phone.

## Turn on previews

Previews are optional and need a second, private Tailscale address on port 8443. `pterm setup` asks about it: answer `y` if the command is correct. To set it up later, run:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:8789
```

`pterm doctor` checks the preview setup.

## Open a preview

1. Start a dev server in a session, such as `npm run dev`, or ask Claude Code to start it.
2. Within a few seconds, the live terminal shows a **Preview ready** chip with the port, such as `:5173`.
3. Tap **Open ↗**.

**‹ terminal** goes back. **Tell agent** goes back with the compose sheet open, to describe what you see.

Live reload works. A stopped server shows "Dev server stopped".

## How are previews kept private?

- Only ports that the session's own programs listen on are shown. Other servers, such as databases, are never offered, even if you type their port.
- The preview runs on a separate address, so the dev site cannot read the app, the terminal or your sessions.
- Every request checks that the phone is still paired, so a removed phone loses the preview within 5 seconds.
- The dev server never gets Termivoa's cookies or your Tailscale identity.
- The preview is private to your Tailscale devices. Termivoa never uses Tailscale Funnel.

:::note
All previews share one address. Do not treat two dev sites of your own sessions as isolated from each other.
:::

---
title: Dev-server preview
description: Open a web page that a session serves, such as a Vite dev server on port 5173, inside the app on your phone.
---

This page helps you see a dev server from a session, such as `npm run dev`, on your phone.

## Turn it on

Previews need a second, private Tailscale address on port 8443.
`pterm setup` asks about it in a separate question. Answer `y` if the command is correct.

To set it up yourself later, run this on your computer:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:8789
```

`pterm doctor` tells you if previews are set up. They are optional.

## Open a preview

1. In a session, start a dev server. For example, run `npm run dev`, or ask Claude Code to start it.
2. Within a few seconds, the live screen shows a **Preview ready** chip with the port, such as `:5173`.
3. Tap **Open ↗**. The page opens full screen inside the app.

On the preview screen:

- **‹ terminal** goes back to the terminal.
- **Tell agent** goes back with the composer open, so you can describe what you see.
- The agent status stays at the bottom.

Live reload works. When you edit a file, the page updates.
If the server stops, the preview shows "Dev server stopped".

## Privacy rules

- Only ports that the session's own programs listen on are shown. Other servers on your computer, such as databases, are never offered, even if you type their port by hand.
- The preview runs on a separate address from the app. The dev site cannot read the app, your terminal or your sessions.
- Every preview request checks that the phone is still paired. A removed phone loses the preview within 5 seconds.
- Termivoa never sends its own cookies or your Tailscale identity to the dev server.
- The preview is private to your Tailscale devices. Termivoa never uses Tailscale Funnel.

:::note
All previews share one address. Do not treat two dev sites of your own sessions as isolated from each other.
:::

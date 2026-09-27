# Dev-server preview

When a program in a session serves a web page on this computer (for example
`npm run dev` started by Claude Code), the phone shows a **Preview ready**
chip on the Live screen. **Open ↗** shows the page full screen inside the app,
with a way back to the terminal and the agent status at the bottom.

This note describes the design and its security model. The wire format is in
`protocol/v1` (`previews`, `gateway.preview.check`, `GET /api/v1/preview`).

## Parts

```text
phone app  https://host.ts.net        (Tailscale Serve 443  → 127.0.0.1:8787, app + API)
  └ iframe https://host.ts.net:8443   (Tailscale Serve 8443 → 127.0.0.1:8789, preview only)
             └ gateway proxy → 127.0.0.1:<port> or [::1]:<port>, a dev server of the session
```

| Part | Job |
| --- | --- |
| Broker (`internal/broker/devpreview.go`) | Finds the TCP ports that the session's own processes listen on. Publishes them as `previews: [{port, since}]` on the session record and on `session.state`. Answers `gateway.preview.check`. |
| ptyhost (`internal/ptyhost/listen*.go`) | Reads the session's process tree and its listening sockets from the operating system. |
| Gateway preview listener (`internal/gateway/preview.go`) | A second loopback listener, 127.0.0.1:8789 (development default 127.0.0.1:8790, `--preview-listen`, `off` disables). It proxies one selected port per browser. It never serves the app or its API. |
| Web (`PreviewChip.tsx`, `Preview.tsx`) | The chip on Live and the `/sessions/<id>/preview?port=<n>` screen with the iframe. |
| `pterm setup` / `pterm doctor` | A second, separate consent question for `tailscale serve --bg --https=8443 http://127.0.0.1:8789`, and a doctor line that says whether it is set up. |

## Which ports are previewable

Only a port that the operating system reports in `LISTEN` state, on
127.0.0.0/8, ::1, 0.0.0.0 or ::, and owned by a process inside the session's
process tree. Screen text never names a port. Other addresses (LAN,
Tailscale) are ignored because the proxy dials loopback.

| OS | Process tree | Listening sockets |
| --- | --- | --- |
| macOS | One `kern.proc.all` snapshot: members of the PTY session (`getsid`, the same rule the broker uses to end a session) plus all their descendants by parent PID, so a dev server that a coding agent detached with `setsid` still counts. | `/usr/sbin/lsof -w -a -nP -iTCP -sTCP:LISTEN -p <tree pids> -F ptn` (absolute path, `LC_ALL=C` only, 2 s timeout, 256 KiB output cap). |
| Windows | The process IDs in the session's Job Object (`JobObjectBasicProcessIdList`). | `GetExtendedTcpTable(TCP_TABLE_OWNER_PID_LISTENER)` for IPv4 and IPv6, owning PID per row. |
| Linux | `/proc/<pid>/stat` session and parent fields, the same rule as macOS. | LISTEN rows of `/proc/net/tcp` and `/proc/net/tcp6` (the broker's network namespace), matched by socket inode to the `socket:[inode]` links in `/proc/<pid>/fd` of the tree's processes. No `lsof`. Another user's `fd` directory cannot be read, which can only hide ports. |

The ownership filter is a pure function (`ptyhost.PreviewablePorts`) over
(listening sockets, tree PIDs). At most 8 ports per session. After the
session's leader exits, the scan returns nothing (its PID could be reused).

**Bounded work.** A session is scanned only while it runs: 1 s after output,
then every 2 s while output is less than 30 s old, then every 10 s while it
still has open ports (to see them close). One scan at a time, off the session
actor. A gateway check reuses a scan at most 2 s old. There are no screen
hints for URLs: output already starts a scan within a second, and the OS
answer is the only one that counts.

## Request flow

1. The app reads `GET /api/v1/preview` → `{origin}`: `https://<host>:8443` for
   the private HTTPS name, or `http://<host>:<preview listener port>` for a
   loopback page (development).
2. The iframe loads `<origin>/__termivoa/select?session=<id>&port=<n>`. The
   gateway checks the paired-device cookie and asks the broker
   (`gateway.preview.check`). If allowed, it sets the preview-only cookie
   `__Host-pterm_preview` (development: `pterm_dev_preview`) with the value
   `<session>.<port>` and redirects to `/`.
3. Every later request, including each WebSocket upgrade, is checked again the
   same way (credential + ownership) before it is proxied. Open sockets and
   streams are checked again every 5 s and closed when the check fails.

## Security model

**Separate origin.** The dev site runs on `https://host:8443`, a different
origin from the app (`https://host`). It cannot read the app's DOM, storage or
responses. The app's CSP allows framing exactly that origin (`frame-src
https://host:8443`, nothing else), and `frame-ancestors 'none'` still stops
anything from framing the app. The iframe has `sandbox="allow-scripts
allow-forms allow-same-origin"` (no top navigation, no popups) and
`referrerPolicy="no-referrer"`. The preview listener has its own router: the
app, `/api/*` and `/healthz` of the app are never served there; unknown
`/__termivoa/*` paths are 404, everything else goes to the selected dev
server or gets a short plain-text error.

**Authentication: the existing cookie.** Cookies are scoped by host, not by
port, so the browser sends the `__Host-pterm_session` cookie (Secure,
HttpOnly, SameSite=Strict, no Domain) to `https://host:8443` as well. The
frame request is same-site (same host, same scheme), so SameSite=Strict still
sends it. This was checked in Chromium on the development profile (app on
127.0.0.1:8798, iframe on 127.0.0.1:8790: the cookie arrived and the page
loaded). The HTTPS/Tailscale case on iOS Safari is on the owner checklist. No
new credential or ticket was needed. The broker authorizes every preview
request, so a revoked or expired device gets 401 on its next request and its
open sockets close within 5 s.

**Main origin refuses the preview origin.** The dev site shares the host name,
so its requests to `https://host` carry the owner's cookie. The app therefore
answers 403 to any request whose `Origin` is a preview origin
(`https://<allowed host>:8443`, or `http://<any>:<preview port>`), reads and
WebSocket upgrades included, before routing. Mutations and WebSockets also
still need the exact app origin.

**The preview listener refuses other origins.** A request with an `Origin`
header must carry the preview origin itself. The Host must be the preview
listener's loopback address, `localhost:<port>`, or an allowed private HTTPS
name on `:8443` (production only). A cross-site page gets no SameSite=Strict
cookie, so it gets 401.

**Proxy rules.**

- Dials only `127.0.0.1:<port>` or `[::1]:<port>` from the broker's answer; no
  environment proxy; 5 s dial timeout; 2 min to first response byte; 32 MiB
  request bodies; at most 32 open WebSockets per gateway.
- Sends `Host: localhost:<port>` (Vite and Next.js reject unknown hosts). An
  `Origin` equal to the preview origin becomes `http://localhost:<port>`, and a
  `Referer` on the preview origin is rewritten the same way; other referers are
  dropped.
- Removes Termivoa cookies (`__Host-pterm_session`, `pterm_dev_session`,
  `__Host-pterm_preview`, `pterm_dev_preview`) from `Cookie` and keeps the dev
  site's own cookies byte for byte. Removes `X-Pterm-*`, `Tailscale-*` (the
  tailnet user identity that Serve adds) and all `Forwarded`/`X-Forwarded-*`
  headers. `Authorization` passes, because the dev site may use it.
- Drops any `Set-Cookie` from the dev server that names a Termivoa cookie.
- Never follows redirects. A `Location` to the dev server itself
  (`localhost`, `127.0.0.1` or `[::1]` on the same port) is rewritten to the
  preview origin. Other hosts pass unchanged; the app's `frame-src` keeps the
  frame from showing them.

## Known limits

- All previews share the one preview origin. A dev site can call
  `/__termivoa/select` with the owner's cookie and then read another previewable
  port of the same owner's sessions. That port's server already runs as the
  same user with access to the same loopback, so this adds no new access, but
  do not treat two dev sites as isolated from each other.
- A dev site's cookies are also sent to the app (same host). The app ignores
  them. A dev site that sets very many or very large cookies can make the app's
  requests exceed the 16 KiB header limit until those cookies are cleared.
- Another process of the same user that binds a more specific address on the
  same port (for example 127.0.0.1:5173 while the session's server binds
  0.0.0.0:5173) would receive the proxied traffic. Same-user processes are
  outside the threat model.
- A PID in the tree can exit and be reused between the process snapshot and
  the socket query (milliseconds).
- On Windows the Job Object list holds 1024 PIDs; more can only hide ports.

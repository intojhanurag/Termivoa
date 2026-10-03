# Dev-server preview

When a program in a session serves a web page on this computer (for example
`npm run dev` started by Claude Code), the phone shows a **Preview ready**
chip on the Live screen. **Open ↗** shows the page full screen inside the app,
with a way back to the terminal and the agent status at the bottom.

This note describes the design and its security model. The wire format is in
`protocol/v1` (`previews`, `gateway.preview.check`, `GET /api/v1/preview`).

With several computers ([multi-computer.md](multi-computer.md)), preview works
for sessions on every computer in the phone's list. Each computer runs its own
preview listener and proxies only its own sessions; the home app frames a
peer's preview origin directly. The rules for that, the reasons and the trust
statement are in [peer-preview.md](peer-preview.md) (ADR
[0006](decisions/0006-peer-preview.md)). A peer needs protocol revision 2 and
its own Tailscale Serve on 8443.

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
| Web (`PreviewChip.tsx`, `Preview.tsx`, `previewFor` in `App.tsx`) | The chip on Live and the preview screen with the iframe: `/sessions/<id>/preview?port=<n>` for home, `/c/<host>/sessions/<id>/preview?port=<n>` for a peer. |
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

1. The preview origin. Home: the app reads `GET /api/v1/preview` → `{origin}`:
   `https://<host>:8443` for the private HTTPS name, or
   `http://<host>:<preview listener port>` for a loopback page (development).
   A peer: the app computes `https://<peer host>:8443` from its own computer
   list (`previewOriginOf`); it never uses an origin string that a peer sent.
   A peer has a preview only while the app's poll of it answers, at protocol
   revision 2 or higher (`previewFor`).
2. The probe. Before the frame shows, the app sends
   `fetch('<origin>/__termivoa/select', {mode: 'no-cors', credentials: 'omit'})`
   with a 5 s timeout (`probePreview`). Any HTTP answer (it is a 403, and the
   app cannot read it) means that something serves the origin. No answer
   shows "Can't open the preview on <computer>. If <computer> does not share
   dev-server previews yet, run pterm setup on <computer> and answer yes to
   the preview question." and no frame. `GET /api/v1/preview` cannot say
   this: it answers an origin whenever the listener runs, also when Tailscale
   Serve 8443 is not set up.
3. The selection. The iframe loads
   `<origin>/__termivoa/select?session=<id>&port=<n>` with
   `referrerPolicy="origin"`, so the request carries the app's origin as
   `Referer`. The listener needs that `Referer` (see "Who may send a
   request"), then checks the paired-device cookie and asks the broker
   (`gateway.preview.check`). If allowed, it sets the preview-only cookie
   `__Host-pterm_preview` (development: `pterm_dev_preview`) with the value
   `<session>.<port>` and answers `200` with a small page that moves the frame
   to `/` (a meta refresh; a replace navigation). It is not a redirect: after
   a redirect the request for `/` would still count as started by the app's
   page, and the listener would refuse it.
4. Every later request, including each WebSocket upgrade, must come from the
   preview origin itself and is checked again the same way (credential +
   ownership) before it is proxied. Open sockets and streams are checked
   again every 5 s and closed when the check fails.

The bar over the frame has **‹ terminal**, the address and **↻**. ↻ mounts a
new frame, so steps 2 and 3 run again. Over a peer's page the bar always shows
the computer's label and **untrusted page**. When a peer shows "pair again",
"can't reach" or the update text, the app removes its frame and shows that
text.

## Security model

**Separate origin.** The dev site runs on `https://host:8443`, a different
origin from the app (`https://host`). It cannot read the app's DOM, storage or
responses. The app's CSP `frame-src` holds the computer's own preview origin
and, on the app document of a paired phone, `https://<host>:8443` of each
**confirmed** computer in that phone's list (never a pending one); nothing
else. `connect-src` holds the same preview origins for the probe, never as
`wss:`. `frame-ancestors 'none'` still stops anything from framing the app.
These entries come from the page load. A computer confirmed in this visit is
not in them yet: the Preview screen sees the `securitypolicyviolation` event
for that preview origin and reloads the app once (a `sessionStorage` guard,
`pterm.previewReload`, stops a loop). The iframe has `sandbox="allow-scripts
allow-forms allow-same-origin"` (no top navigation, no popups, no full
screen, no device features) and `referrerPolicy="origin"` (the app's origin
only, no path; the app document itself stays `no-referrer`). The preview
listener has its own router: the
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
new credential or ticket was needed. A peer's cookie reaches the frame the
same way: all computers of one tailnet are same-site (checked in Chrome and
Playwright WebKit; real phones are on the owner checklist). The broker
authorizes every preview
request, so a revoked or expired device gets 401 on its next request and its
open sockets close within 5 s.

**Main origin refuses the preview origin.** The dev site shares the host name,
so its requests to `https://host` carry the owner's cookie. The app therefore
answers 403 to any request whose `Origin` is a preview origin
(`https://<allowed host>:8443`, or `http://<any>:<preview port>`), reads and
WebSocket upgrades included, before routing. Mutations and WebSockets also
still need the exact app origin.

**Who may send a request to the preview listener.** Checked in this order
(`previewHandler.ServeHTTP`), before the dev server is reached:

1. Host: the preview listener's loopback address, `localhost:<port>`, or an
   allowed private HTTPS name on `:8443` (production only). Else 421.
2. The selection (`/__termivoa/select`) must carry no `Origin` header and
   exactly one `Referer`: an app origin plus one `/`. The app origin is this
   computer's own origin, or the exact app origin of another computer that a
   device here is bound to; that one is forwarded to the broker, which checks
   it against the request's own credential as for every `/api` call
   (per-device origin binding). A page cannot send another origin's
   `Referer`, and a dev site sends its `:8443` origin, which is never an app
   origin.
3. **Rule L1**, every other path: exactly one `Origin` equal to the preview
   origin itself (form posts, module scripts, WebSocket handshakes), or no
   `Origin` and `Sec-Fetch-Site: same-origin`. So no other page (the app,
   another machine in the tailnet, another computer's dev site) can load
   from, frame or open a socket to a dev server. A `curl` or a typed URL is
   refused too.
4. The failed-attempt limiter, then the device cookie, then the preview
   cookie and the broker check.

On every preview check for a device that is bound to another computer's app,
the broker also requires that its Tailscale snapshot still trusts that
computer's pinned node and tags (`appTrust`), so the multi-computer trust
rules hold for open previews too.

A same-tailnet page is same-site, so SameSite=Strict does not keep the cookie
away from it; rules 2 and 3 do. A cross-site page gets no cookie at all.

Answers from the listener itself are short plain-text pages without
`frame-ancestors`, so the app's frame shows them:

| Status | Text | Cause |
| --- | --- | --- |
| 403 | "Open the preview from the Termivoa app." | Selection: missing, doubled or wrong `Referer`, or any `Origin` header |
| 403 | "Origin not allowed." | Other paths: not from the preview origin (L1) |
| 403 | "This app may not use this computer." | The broker refused the app origin for this credential, or the app computer's tags, owner or name changed |
| 401 | "This phone is no longer paired. Open Termivoa to pair again." | Revoked or expired, or the app computer's node changed |
| 401 | "Open the preview from your paired phone." | No device cookie |
| 400 | "Open the preview from the Termivoa app." / "Invalid preview request." | No preview cookie; bad selection parameters |
| 404 | "No dev server of this session listens on that port." / "Unknown preview route." | |
| 409 | "The session is not running." | |
| 421 | "Unknown host." | |
| 429 | "Too many failed attempts." / "Too many open preview sockets." | |
| 502 | "The dev server did not answer." | |
| 503 | "The computer is not available." | Other broker errors, for example Tailscale status not readable past the grace period |

The selection page (200) may be framed only by the app origin of its
`Referer` (`frame-ancestors`).

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
- A peer's framed page is chosen by that peer. It can show false content (for
  example a false "pair again" page) and can sign the phone out of all
  computers by filling the cookie jar; recovery is to pair again. The bar
  always shows the label and **untrusted page**. Full statement:
  [peer-preview.md](peer-preview.md), section 7.
- WebKit: when the app page is restored from history (browser Back to the
  app), the frame loads its last address directly and gets 403 "Origin not
  allowed." until it loads again. The app mounts a new frame on `pageshow`
  with `persisted`; ↻ does the same.
- WebKit: `history.back()` inside the sandboxed frame, past its first entry,
  moves the whole app back.
- A dev-site service worker on the preview origin can re-send navigations as
  same-origin and serve cached content after revocation (accepted by the
  owner).
- L1 gives 403 to requests without `Sec-Fetch-Site` (Safari before 16.4,
  below the minimum) and to sub-resources of opaque-origin frames inside the
  dev site (a sandboxed frame without `allow-same-origin`).
- The preview needs the `Referer` header. If a browser setting or extension
  removes it, the frame shows "Open the preview from the Termivoa app." An
  app bundle from before this rule shows the same until the app updates.
- The probe says that something answers on the preview origin, not that
  Termivoa does.

# Preview for sessions on peers: design

Status: **implemented, version 3 (2026-10-03)**. ADR [0006](decisions/0006-peer-preview.md), accepted by the owner on 2026-10-03 (AGENTS.md rule 24): the `Referer` variant, the trust statement of section 7, and the service-worker limit of section 13. One pull request holds the host rules, the app and the harness, and closes #143 and #144. Version 2 answered two adversarial reviews; version 3 records a final plan review (13 changes), a real-browser spike (Chrome 154, Playwright WebKit 26.5) and what was built. Section 12 lists every finding and its resolution. Not yet checked on a real phone (`docs/owner-verification.md`, "Preview for sessions on peers").

What it does (it was roadmap item "Preview for sessions on peers" in `docs/pending-work.md`): a session on a peer computer (for example `vps`) shows the **Preview ready** chip, and the home app shows that peer's dev server in the frame. Read [preview.md](preview.md) and [multi-computer.md](multi-computer.md) first; this document describes what changed and why.

Names used below: **home** `https://mac.tailx.ts.net` serves the app. **Peer** `https://vps.tailx.ts.net`. A computer's **preview origin** is its host on port 8443 (`PreviewPublicPort` in `internal/gateway/preview.go`).

Code is named by function, not by line number.

## 1. What the code did before this change

| Fact before | What it is now |
| --- | --- |
| The app offered a preview for home sessions only, and only when home's own previews were on. | `previewFor` in `web/src/screens/App.tsx` decides per computer (W5). |
| Peer session records already carried `previews` (`protocol/v1/envelope.schema.json`); `Live` got no `onPreview` for a peer, so the chip did not show. | `App.tsx` passes `onPreview` whenever `previewFor` gives an origin. |
| Home's CSP framed only its own preview origin. Peers got `connect-src` only. | H1: confirmed computers' `:8443` in `frame-src` and `connect-src` (`deviceConnectSources`, `internal/gateway/handler.go`). |
| The preview listener accepted a request when the `Origin` header was absent or equal to the preview origin. Nothing else was checked before the cookie. | L1 and L2 (`fromPreviewOrigin`, `appReferer` in `preview.go`). |
| The listener's broker call carried no origin, and the broker treats a missing origin as its own (`isOwnOrigin`, `internal/broker/computers.go`). So **per-device origin binding was not applied on the preview listener** (#143): a credential bound to home's app was accepted there from any page that could make the browser send the cookie. | The selection of another computer's app forwards its origin; every preview check of a bound device runs `appTrust` (B1). |
| The selection was a `303` to `/`. | A `200` page that moves the frame to `/` (`previewSelectedPage`). |
| The listener's own text pages carried `frame-ancestors 'self'`. `'self'` is the `:8443` origin and the frame's parent is the app origin, so these pages were blank inside the app's frame (#144). | `previewPage` sends no `frame-ancestors`. |
| `GET /api/v1/preview` reports an origin whenever the preview listener runs. It does not know whether Tailscale Serve 8443 is set up (#144). | Unchanged; the app probes the preview origin before it shows the frame (section 6). |

Unchanged facts this design rests on:

- A main origin refuses its **own** preview origin as `Origin` (403, before routing; `ServeHTTP` and `isPreviewOrigin`). Other origins on `/api` need an exact own or bound `Origin`, or no `Origin` plus `Sec-Fetch-Site: same-origin`; `no-cors` is refused (`admitAPI`).
- The gateway keeps the set of bound app origins from the broker probe (`boundOrigin`, `refreshOrigin` in `handler.go`).
- Open preview sockets and streams are checked again every 5 s (`previewRecheckEvery`, `watch` in `preview.go`).
- Serve 8443 is a separate consent question in `pterm setup`, per computer; `pterm doctor` reports it.

## 2. What the browser sends (the hard part)

The frame navigates from a top-level page on home to `https://vps.tailx.ts.net:8443/__termivoa/select?…`.

| Header | Value on the frame's first request | Why |
| --- | --- | --- |
| `Cookie` | The peer's `__Host-pterm_session` | Cookies ignore ports (RFC 6265 §8.5). The cookie is host-only for `vps.tailx.ts.net`. `SameSite=Strict` is decided by site, and `ts.net` is a public suffix, so home and the peer are same-site (`docs/multi-computer.md`, section 1). |
| `Origin` | **absent** | Browsers send `Origin` on CORS requests and on non-`GET` requests, not on a `GET` navigation. |
| `Sec-Fetch-Site` | `same-site` | Other origin, same site. The same value arrives from a page on **any** machine in the tailnet (another owner's machine in a team tailnet, another computer's dev site). |
| `Sec-Fetch-Mode` / `Sec-Fetch-Dest` | `navigate` / `iframe` | They say what kind of request it is, not who made it. |
| `Referer` | With `referrerPolicy="origin"` on the frame: exactly one `https://mac.tailx.ts.net/`. Without it (the frame before this change): none, because the app has `Referrer-Policy: no-referrer`. | It is the URL (cut to the origin) of the document that started the navigation. A page cannot send another origin's `Referer`. The frame attribute overrides the app's `no-referrer` header. |

Conclusion: the only header that can name the app on a `GET` navigation is `Referer`.

After the selection, requests made by the framed dev page itself are different: `fetch`, scripts, images and same-origin navigations carry `Sec-Fetch-Site: same-origin`; form posts, module scripts and WebSocket handshakes carry `Origin: https://vps.tailx.ts.net:8443`. A `303` does **not** give that: after a redirect the request still counts from the page that started it, so `GET /` would still say `same-site`. That is why the selection answers with a page, not a redirect (section 4).

### Spike results (Chrome 154 and Playwright WebKit 26.5)

A throwaway harness ran before the build (two origins of one site, self-signed TLS). It is not in the repository, so this is reported evidence, not reproducible evidence; the multi-computer harness (`scripts/e2e-multicomputer/README.md`) now checks the built feature. Every row above held in both engines:

- The Strict host cookie arrives on the frame's select request (peer host, and same host on another port). The framed page can set and read back cookies.
- The select request has no `Origin`, `Sec-Fetch-Site: same-site`, `navigate`, `iframe`.
- `referrerpolicy="origin"` on the frame overrides the app's `no-referrer` meta and header: exactly one `Referer: https://mac.tailx.ts.net/`.
- The `200` page's move to `/` (meta refresh or `location.replace('/')`) is `Sec-Fetch-Site: same-origin` with no `Referer`, and it is a replace navigation (the selection is not in history). A `303` leaves `/` as `same-site`.
- L1 passes everything a dev page does: sub-resources, module scripts, `fetch`, form `POST`, links, reload, Back, `EventSource`, workers, video and audio. **WebSocket: Chrome sends no `Sec-Fetch` headers on the handshake, only `Origin`**, so the `Origin` branch of L1 must admit it. Module scripts send `Origin` equal to the preview origin.
- L1 refuses an image, script, `no-cors` fetch, frame, form `POST` and WebSocket from a same-site or cross-site attacker page. The `Referer` rule separates the app from `evil`, from a dev site (`https://mac.tailx.ts.net:8443/`: the port shows) and from a removed `Referer`.
- The move to `/` after an attacker-framed selection page would be `same-origin`. So L1 alone does not stop a foreign frame once a selection page is served: the `Referer` rule on the selection (and `frame-ancestors` on the selection page) is the gate, and the listener refuses **before** it serves the page.

Two WebKit differences:

- **A. History restore.** When the app document is restored from history (browser Back to the app tab), WebKit loads the frame's last URL directly, with `Sec-Fetch-Site: same-site` and no select request. L1 answers `403` "Origin not allowed." Fix in the app (W7): the Preview screen mounts a new frame on `pageshow` with `persisted`, and the bar's ↻ does the same. The `403` text is readable in the frame.
- **B. Back inside the frame.** In WebKit, `history.back()` inside a sandboxed frame, past the frame's first entry, moves the **top** page back. This exists for home previews too and is not fixed; it is known limit 7 in section 13.

Not covered by the spike: a real phone, the installed app, a real Tailscale Serve. Those are in `docs/owner-verification.md`.

## 3. Options for authenticating the frame

| | A. Fetch-metadata and `frame-ancestors` only | **B. `Referer` as origin proof (chosen and built)** | C. Preview ticket (not built) |
| --- | --- | --- | --- |
| Rule on the selection | cookie + `Sec-Fetch-Site: same-site` + `Sec-Fetch-Dest: iframe` | cookie + exactly one `Referer` whose origin is this computer's own origin or the credential's bound app origin | cookie + a single-use ticket that the app fetched through the authenticated CORS API |
| What stops another same-tailnet page from selecting | Only that it does not know the session ID | The browser never lets a page send another origin's `Referer`. A dev site on `:8443` sends its own origin, which is never a bound origin (a bound origin has the default port, `ValidateComputerOrigin` in `internal/protocol/computers.go`). | Only the bound app origin can get a ticket |
| Broker check | cookie only | the same `admitDevice` as every API call, with the `Referer` origin as the forwarded origin | the same `admitDevice`, on the ticket call |
| Works when a header is removed | Yes | **No.** `Referer` is optional. If a browser setting or extension removes it, the preview fails (closed). | Yes |
| New parts | one response header | one frame attribute, one `Referer` rule on the listener. No new route, RPC or state. | one HTTP route, one gateway RPC, one optional RPC field, an in-memory ticket table |
| Secret in a URL | no | no | yes: 30 s, single use, useless without the cookie |

**B was chosen by the owner on 2026-10-03.** Both reviews found it not weaker than the ticket against the attackers of this design (another tailnet page, a dev site on any `:8443`), and it is much less code and state. Version 1 of this document recommended C; that changed in review (S1, F1).

Honest costs of B:

- **It fails closed when `Referer` is removed.** The user then sees "Open the preview from the Termivoa app." in the frame, not a preview.
- **`Referer` is a second origin proof** next to `Origin`. Every later reviewer of the preview listener must know that.
- **A script-free HTML injection on home's origin could start a selection.** Markup injected into the app's page (for example an `<iframe>` with `referrerpolicy="origin"`) sends home's `Referer`. With the ticket, the attacker would need script. The app renders terminal text as text and has a strict CSP, so this needs an app bug first; it also needs a real session ID, and it shows only what a tap on **Open ↗** would show.

**A was rejected:** `same-site` is not "same owner" (`docs/multi-computer.md`, "Same tailnet is not same owner").

Also rejected: a preview-only bearer cookie that replaces the device cookie (a new credential type; against "No new secret type", `docs/multi-computer.md`, "Roles"), and a relay of the peer's preview through home (home would hold standing peer credentials; `docs/multi-computer.md`, section 2, option B).

## 4. Design

### Request flow

```text
phone app  https://mac.tailx.ts.net  (home)
  0. fetch <peer>:8443/__termivoa/select  (no-cors, no credentials, 5 s)
       any HTTP answer → show the frame; no answer → text, no frame
  1. iframe referrerpolicy="origin"
       src = https://vps.tailx.ts.net:8443/__termivoa/select?session=<id>&port=<n>
     vps preview listener: Host; no Origin; Referer https://mac.tailx.ts.net/
       is a bound app origin → forwarded to the broker as the request origin;
       vps cookie
     vps broker: admitDevice (origin bound to this credential, node, tags),
       session runs, port owned
     → 200 small page, sets the preview cookie, moves the frame to /
  2. GET /  (and every later request of the dev page)
       same-origin by Sec-Fetch-Site or Origin (L1); cookie + preview cookie;
       broker re-check (credential, trust of the bound node, port) on every
       request and every 5 s for open sockets
```

A home session uses the same steps against home; the `Referer` origin is then home's own origin, and nothing is forwarded.

### Rules that changed on every computer (each computer runs the same build)

**Admission order** in `previewHandler.ServeHTTP` (`internal/gateway/preview.go`):

1. Host (`hostAllowed`), else `421`.
2. The selection path: L2. Every other path: L1. Else `403`. This is before the cookie is read and before the dev server is reached.
3. (Removed, #149: the failed-attempt limiter. Failed attempts are not counted.)
4. The device cookie (`401` without a valid one).
5. The selection: `selectPort`. Other paths: an unknown `/__termivoa/*` path is `404`; the preview cookie (`400` without one); then the broker check and the proxy.

**L1. Who may send a request** (`fromPreviewOrigin`). A request other than the selection passes only with:

- exactly one `Origin` header equal to the preview origin itself, compared without case (form posts, module scripts, WebSocket handshakes), or
- no `Origin` header and exactly one `Sec-Fetch-Site: same-origin` (`sameOriginFetch`, shared with `/api`).

Everything else is `403` "Origin not allowed." This is the `/api` rule on the preview listener. It refuses a script tag, image, `no-cors` fetch, frame or WebSocket from the app, from another tailnet machine and from another computer's dev site. A `curl` or a typed URL is `403` too. There is **no fallback** that accepts `same-site` navigations.

**L2. The selection** (`appReferer`, then `selectPort`). `GET /__termivoa/select?session=<uuid>&port=<n>`, exactly these two parameters. The request must carry **no `Origin` header at all** and **exactly one `Referer` header that ends in one `/`**. The `/` is cut off and the rest is the candidate; it is not parsed as a URL.

- The candidate is this computer's own origin (`originAllowedValue`, which compares without case) → the broker call carries no forwarded origin.
- The candidate is a valid computer origin (`protocol.ValidateComputerOrigin`) and is in the gateway's bound set, compared as an exact string (`boundOrigin`, the same lookup `/api` uses for CORS) → the broker call carries it through `gatewayapi.WithForwardedOrigin`, so the broker runs `admitDevice` against **this credential's** binding.
- Anything else → `403` "Open the preview from the Termivoa app.": a path, no trailing `/`, an explicit `:443`, a preview origin (`https://<host>:8443/`), `null`, a missing or doubled `Referer`, or any `Origin` header.

The answer is `200 text/html` with `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'; frame-ancestors <the app origin of the Referer>`, the preview cookie, and a body that only moves the frame to `/` (`<meta http-equiv="refresh" content="0;url=/">`; a meta refresh needs no script allowance in the CSP). The move is a replace navigation started by a document on the preview origin, so `GET /` is `same-origin` and passes L1. This page is not built with `previewPage`. The dev server never sees home's `Referer`: the selection is not proxied.

Only the selection forwards an origin. The per-request check and the 5 s recheck (`watch`) send none; that is correct only because of B1.

**Text pages** (`previewPage`, #144). Every other answer from the listener itself is a short `text/plain` body with `default-src 'none'` and **no `frame-ancestors`**: the text is static, and it must be readable inside the app's frame, whose parent is another origin. Section 6 lists the texts.

**B1. Broker: preview check** (the `GatewayPreviewCheck` case in `internal/broker/gateway_rpc.go`). No wire change; the envelope's existing `origin` field is used.

- `authorize` runs first, as for every call: it authenticates the credential and runs `admitDevice` on the forwarded origin (a missing origin counts as the own origin).
- Then, **whenever the device is bound to an app origin (`principal.App.Bound()`), the check requires `appTrust(principal.App)`, with or without a forwarded origin.** A changed node ID is `ErrRevoked` (the listener answers `401`); a changed tag set, another owner or a missing name is `ErrOriginRefused` (`invalid_request`; the listener's `fail` answers `403` "This app may not use this computer."); a status that cannot be read after the grace period is `host_unavailable` (`503`). A device that paired with this computer directly is checked as before.
- Then the port ownership check as before (`internal/broker/devpreview.go`).

### Rules that changed on home only

**H1. CSP of the app document.** `gatewayHandler.ServeHTTP` puts the own preview origin into `connect-src` and `frame-src` on every response when previews are on. On `index.html`, with a usable cookie, `deviceConnectSources` adds, for each **confirmed** computer of the cookie's device (`list.Computers`, never `list.PendingOrigins`), `<origin>:8443` to `frame-src` and to `connect-src` (for the probe). `frame-src` is created when home's own preview listener is off. A pending computer gets only its `https` and `wss` API origins in `connect-src`, as before. Entries whose node changed are already left out by the broker (`listComputers`). At most 8 (`protocol.MaxComputers`). **Never `wss://<peer>:8443`**: the app opens no socket to a preview origin. The known limit for the cached offline shell (`docs/threat-model.md`, "Multi-computer") now also covers `frame-src`.

### App rules

- **W1.** The frame has `referrerPolicy="origin"` and `sandbox="allow-scripts allow-forms allow-same-origin"` (`PreviewBody` in `web/src/screens/Preview.tsx`): no top navigation, popups, modals or downloads; no `allow` attribute (no camera, microphone, location) and no `allowfullscreen`.
- **W2.** Over a frame of a peer, the app's bar **always** shows the computer's label (from the phone's own list) and **untrusted page** (`PreviewBar`). It does not depend on how many computers the phone has. Home's frame has neither.
- **W3.** The app shows a peer's frame only while its own poll of that computer has `status.kind === 'ok'` (`previewFor` in `App.tsx`). For `pair`, `unreachable` or `outdated` the frame is removed and the existing text shows ("vps · pair again", "vps · can't reach", the update text). The app cannot see the frame's own responses (another origin), and it never sees a `403` for a trust refusal on its own calls (that is `400 invalid_request`, `503`, or a CORS failure that looks like a network error), so the computer's status is the signal. The poll runs while the preview is open.
- **W4.** The app never uses an origin string that a peer sent. A peer's preview origin is always `https://<host of computer.origin>:8443` (`previewOriginOf` in `web/src/computers.ts`), and home computes its `frame-src` the same way. Home's own preview origin still comes from `GET /api/v1/preview`, so the development profile keeps working.
- **W5.** A peer preview does not depend on home's previews being on. `previewFor` gives a peer an origin when its status is `ok` and its `protocolRevision` is 2 or higher (`PEER_PREVIEW_REVISION`). `route()` decides a peer preview address without reading home's preview origin (W9 says when it opens). Route: `/c/<host>/sessions/<id>/preview?port=<n>`; home keeps `/sessions/<id>/preview?port=<n>`.
- **W6. Probe.** Before the frame shows (home and peers), `probePreview` sends `fetch('<previewOrigin>/__termivoa/select', {mode: 'no-cors', credentials: 'omit', signal: AbortSignal.timeout(5000)})`. The request has no `Referer` (the app's `no-referrer` policy), so the listener answers `403`; the answer is opaque and never read. A resolved fetch means "something answers there": the frame shows. A rejected fetch (network error, timeout, a block by the app's CSP) shows `cantPreview` and no frame (section 6).
- **W7. Reload.** ↻ in the bar mounts a new frame, so the selection and the probe run again. The screen does the same on `pageshow` with `persisted` (WebKit difference A).
- **W8. A computer confirmed in this visit.** The app document's `frame-src` and `connect-src` come from the page load, and a computer becomes confirmed after that load. Until the app loads again, that computer's preview origin is blocked by the app's own CSP. The Preview screen listens for the `securitypolicyviolation` event. When the blocked address is on the preview origin that it is trying to open, the app reloads once. A `sessionStorage` guard (key `pterm.previewReload`, `reloadForPolicy` in `Preview.tsx`, with `reloadOnce` in `storage.ts`) allows one reload per preview origin and so stops a reload loop; it is cleared when the probe is answered. The reload goes past the service worker's cached page (`pterm-fresh=1`, [multi-computer.md](multi-computer.md), "The app's own CSP") and keeps `history.state`, so the tap mark of a peer preview stays. If the policy still blocks after the reload (an old cached page), the probe's text shows. After the reload, the route opens the same preview again under the new CSP. The pending-versus-confirmed rule does not change: a pending computer is never in `frame-src`.
- **W9. A link never opens a peer's frame.** A compromised peer knows its own session IDs and could send the user a link to `/c/<host>/sessions/<id>/preview?port=<n>`. `navigate` pushes the preview entry with `PREVIEW_TAP` as `history.state`; `route()` (page load, reload, Back, Forward) opens a peer preview only when `peerPreviewOnLoad` finds that mark and the port in the session's current `previews`, and otherwise replaces the address with the session's Live address. The reload of W8 keeps the mark. The push path already refused preview addresses (`notificationPath`). Found by the security review of the joined build.

### What did not change

Proxy rules, cookie stripping, port ownership and the 5 s recheck (`docs/preview.md`, "Proxy rules"); push, pairing, `/api` admission and CORS; the CLI; the store; `GET /api/v1/preview`.

### Alternative that was not built: preview ticket (variant C)

A new route `POST /api/v1/sessions/{id}/preview` would give a single-use, 30-second ticket; the selection would need the cookie and the ticket; the broker would keep ticket hashes in memory. It does not depend on an optional header, and an HTML injection without script cannot use it. It needs a route, an RPC and a ticket table, and both reviews found it not stronger than `Referer` against these attackers. If `Referer` ever fails in a supported browser, this is the next design to review.

## 5. What each side refuses

| Request | Who answers | Result | Rule |
| --- | --- | --- | --- |
| Dev site on peer A (`https://a:8443`) calls home's or peer B's `/api` with `fetch` or a WebSocket | main origin | `403`: the `Origin` is not own and not bound | existing, `admitAPI` |
| Same dev site sends a `no-cors` `GET` or navigates a nested frame to `/api/...` | main origin | `403`: `no-cors`, or no `Origin` without `same-origin` | existing, `admitAPI` |
| A computer's own dev site calls that computer's main origin | main origin | `403` before routing | existing, `isPreviewOrigin` |
| Dev site on peer A loads a script or image from, frames, or opens a WebSocket to `https://b:8443` or `https://mac:8443` | preview listener | `403` before the dev server | **L1** |
| A page on another tailnet machine, or a dev site on any `:8443`, frames a selection URL | preview listener | `403`: its `Referer` origin is not own and not bound | **L2** |
| A page on an origin that is bound for **another** device frames a selection URL with this device's cookie | broker | refused: `principal.App.Origin` differs (`403` "This app may not use this computer.") | existing `admitDevice`, reached through L2 |
| Home's app selects on a peer with a credential that paired with the peer directly (`app_origin` NULL) | peer broker | refused (`ErrOriginRefused`), same `403` | existing `admitDevice` |
| A bound device's preview request after home's node ID changed, its tag set changed, or the status is older than the grace period | peer broker | refused; a node change also revokes | **B1** via `appTrust` |

Does a dev site on peer A get new power over home or peer B? **Over the APIs: no.** Over the other preview origins: without L1 it would (blind `GET`s and script loads against the selected dev server, with the user's cookies), because a framed peer dev site is the first foreign same-site page that runs inside the installed app's cookie jar. L1 removes that. What stays is in section 7.

## 6. A peer without a preview origin, older peers, and the texts the user sees

| Case | How the app knows | What the user sees |
| --- | --- | --- |
| Peer below protocol revision 2 | `settings.protocolRevision` of that computer | No chip. A preview link to it shows the update text ("Update Termivoa on vps") and no frame. The app never frames a peer whose listener lacks L1 and L2. |
| Peer never ran `tailscale serve --https=8443`, an ACL blocks 8443, or nothing answers in 5 s | The probe (W6) is rejected | "Can't open the preview on vps. If vps does not share dev-server previews yet, run pterm setup on vps and answer yes to the preview question." No frame. The words are neutral, because a blocked request, a timeout and a missing Serve look the same to the app. |
| Computer confirmed in this visit | `securitypolicyviolation` on its preview origin | The app reloads once and opens the preview again (W8). |
| The phone is no longer paired there, or the computer cannot be reached, or it must update | The app's poll of that computer | The frame is removed; "vps · pair again", "vps · can't reach" or the update text shows (W3). |
| The session ended or the port closed | The listener's text in the frame; the app's poll | The text in the frame; the **Dev server stopped** banner. |

The probe is on the phone on purpose: it tests the real path, and it needs no Tailscale call in the broker. It also covers home, where the chip shows even when Serve 8443 was declined. Rejected: letting the broker run `tailscale serve status --json` (more host code, and it cannot see ACLs).

Texts from the listener itself, shown in the frame (plain text):

| Status | Text | Cause |
| --- | --- | --- |
| 403 | "Open the preview from the Termivoa app." | Selection: missing, doubled or wrong `Referer`, or any `Origin` header |
| 403 | "Origin not allowed." | Other paths: not from the preview origin (also WebKit history restore, difference A) |
| 403 | "This app may not use this computer." | Broker `invalid_request`: wrong app origin for this credential, or the app computer's tags, owner or name changed |
| 401 | "This phone is no longer paired. Open Termivoa to pair again." | Broker: revoked, expired, or the app computer's node changed |
| 401 | "Open the preview from your paired phone." | No valid device cookie |
| 400 | "Open the preview from the Termivoa app." | Not the selection, and no valid preview cookie |
| 400 | "Invalid preview request." | Selection with bad or extra parameters |
| 404 | "No dev server of this session listens on that port." / "Unknown preview route." | Port not owned by the session; unknown `/__termivoa/*` path |
| 409 | "The session is not running." | |
| 421 | "Unknown host." | Host not allowed |
| 429 | "Too many open preview sockets." | More than 32 upgrades |
| 502 | "The dev server did not answer." | Proxy error |
| 503 | "The computer is not available." | Any other broker error, for example Tailscale status not readable past the grace period |

Each computer's Serve 8443 stays that computer's own consent in `pterm setup` (rule 12). Nothing in this design changes Tailscale configuration, and no computer configures another.

**Protocol revision.** `protocol/v1/revision.json` is at revision 2; `minRevision` stays 1. No schema changed, but the listener's behavior is a change a peer's app can observe, and the app needs the number to know that a peer's listener has L1 and L2. Home and the app are one build. No path for the old selection stays (rule 26).

## 7. Honest trust statement

Accepted by the owner on 2026-10-03.

**When you open a preview of a session on a peer, that peer puts a page of its choice inside your app.** It does not need a real dev server: a compromised peer can report any port in `previews` and answer on its `:8443` with anything.

**This withdrew a guarantee.** `docs/multi-computer.md` ("Peer responses are untrusted") said that a peer cannot show phishing text in the home app. With this feature a peer can: inside the frame, after the user opens that peer's preview. That document now says so.

What that page **cannot** do:

- Read or change the app: it is another origin, and the app keeps `frame-ancestors 'none'` and its CSP.
- Navigate the app away, open popups, show dialogs, start downloads or go full screen (frame sandbox, W1).
- Call the API of home or of any peer, or load from another computer's preview (section 5).
- Appear without a tap: the app frames a peer only after **Open ↗** on a session of that peer. The app's own `navigate` marks that history entry (`PREVIEW_TAP` in `history.state`); the mark stays with the entry through a reload, Back and Forward, and no other page can set it. `route()` shows a peer's preview address only with that mark and only for a port in that session's current `previews` (`peerPreviewOnLoad` in `App.tsx`); a link from a web page, a message or a notification opens the session's terminal, with no request to the peer's preview origin. Home's own preview address keeps its behavior (it opens from a link).

What it **can** do, stated plainly. "It" is the peer, or any script in a peer's dev site (a project dependency, for example):

- **Show a false page.** A fake "pair again" screen, a fake login. The only limit is the app's bar with the computer label and **untrusted page** (W2).
- **Sign the phone out of every computer.** About 200 cookies with `Domain=<tailnet>.ts.net` make the browser evict older cookies of the site, and every computer's `__Host-pterm_session` is one of them. The user must **pair again** with every computer. Larger cookies also push every request past the 16 KiB header limit until the app's data is cleared. The exact number depends on the browser and was not tested. Before this feature only a peer's HTTP responses could set such cookies (`docs/multi-computer.md`, "Honest trust statement"); a home dev site already could (`docs/preview.md`, "Known limits").
- **Send cookies to other dev servers.** A `Domain=<tailnet>.ts.net` cookie goes to every computer's `:8443`, and the proxy forwards cookies that are not Termivoa's (`forwardedCookies` in `preview.go`). A dev server on another computer can receive a cookie it did not set.
- **Send blind requests from the phone** to other machines in the tailnet (`no-cors`), also machines that the peer itself cannot reach under your ACLs. It cannot read the answers. Any web page that the phone opens can do the same.
- **Keep state on its own origin** (storage, a service worker for `https://vps:8443`; see section 13, limit 1).
- **Track that you opened it**, and when.

What stays as before: a compromised home compromises every peer (`docs/multi-computer.md`, "Honest trust statement"). A compromised peer still controls only itself, plus the frame above.

Known limits kept from `docs/preview.md`: all previews of one computer share one origin, and the preview cookie is not signed; script on that origin can replace it. No isolation between two dev sites of one computer is claimed.

## 8. Tailscale rules and revocation timing for preview streams

| Rule from multi-computer | Does the preview on a peer follow it? |
| --- | --- |
| Node-ID pinning | Yes. The selection goes through `admitDevice`; every check of a bound device goes through `appTrust` (B1). A changed node ID answers `ErrRevoked`, and the sweep revokes the devices as before (`sweepTrust` in `internal/broker/computers.go`). |
| Tagged nodes | Yes, no new rule. `appTrust` compares the pinned tag set: a changed set is refused, not revoked. |
| 60 s status cache, 10-minute grace | Yes. `appTrust` only reads the snapshot. Devices that paired with the peer directly are not affected. |
| Already-open WebSockets | Not at once. Live streams close on a new snapshot. Preview sockets are not broker streams; the gateway asks again every 5 s (`watch`). |

Timing, stated exactly:

| Event | Effect on the preview |
| --- | --- |
| Device revoked or expired (`pterm revoke`, logout) | `401` on the next request; open sockets close within 5 s. The app removes the frame when its own poll of that computer sees the refusal (W3). |
| Home's node ID or tag set changed | Seen with the next status snapshot: up to about 65 s (60 s cache plus the 5 s recheck) |
| Tailscale status cannot be read | The last good snapshot is used for up to 10 minutes; after that, bound devices are refused (not revoked) |

## 9. Protocol changes (additive, rule 6)

| File | Change |
| --- | --- |
| `protocol/v1/revision.json` | `revision` 2; `minRevision` 1 |
| `protocol/v1/http-api.md` | "Preview listener": the rules in order, the selection page, the probe, the CSP entries; revision 2 in the "Protocol revision" table |
| `protocol/v1/authentication-rpc.md` | `gateway.preview.check`: the forwarded origin on the selection, and the trust check for bound devices |
| `internal/protocol/generated.go`, `web/src/generated/protocol.ts` | regenerated (the revision constants) |

No schema, message, error code or store change.

## 10. How it was built

| Step | What | State |
| --- | --- | --- |
| Spike | Two origins of one site in Chrome 154 and Playwright WebKit 26.5 (section 2). The stop rule was: if the move to `/` is not `same-origin`, or the `Referer` is absent, in any engine, the design goes back to review. | Done; both held. The real-phone part of the spike was not run; it is in `docs/owner-verification.md`. |
| Final plan review | 13 changes (section 12). | Done; all in the build. |
| Build | Host rules (L1, L2, B1, H1, text pages, revision 2), the app (W1 to W9), the harness and the docs, as **one** pull request that closes #143 and #144. Version 2 planned separate pull requests for the `main` gap, the text pages and the probe; the owner's approval covered all of them together. | Done. |
| Real-phone checks | `docs/owner-verification.md`, "Preview for sessions on peers". | Open (owner). |

### Files

| File | What |
| --- | --- |
| `internal/gateway/preview.go` | Admission order in `ServeHTTP`; `fromPreviewOrigin` (L1); `appReferer` (L2); `selectPort` with the forwarded origin and the `200` page; `fail` maps `invalid_request` to `403`; `previewPage` without `frame-ancestors` |
| `internal/gateway/handler.go` | `boundOrigin` split out of `crossOrigin`; the own preview origin in `connect-src`; `deviceConnectSources` (H1) |
| `internal/broker/gateway_rpc.go` | B1 in the `GatewayPreviewCheck` case |
| `protocol/v1/*`, generated files | section 9 |
| `web/src/computers.ts` | `previewOriginOf` |
| `web/src/screens/App.tsx` | `previewFor`, `PEER_PREVIEW_REVISION`, the peer route in `pathFor` / `parseRoute`, `route()` |
| `web/src/screens/Preview.tsx` | `probePreview`, `cantPreview`, `PreviewBar` (label, **untrusted page**, ↻), `PreviewBody` (the frame), `Preview` (probe, remount, reload-once) |
| `scripts/e2e-multicomputer/` | The front also answers `CONNECT <host>:8443` and routes it to that stack's preview listener; stacks start with a preview listener; a `devserver` mode of the front binary is the dev site. See its README. |

No CLI change. `pterm setup` and `pterm doctor` already ask about and report Serve 8443 on each computer.

## 11. Test plan

This section says where each check lives. It holds no results: those are in `docs/progress.md`.

### Unit and loopback tests (automated, `scripts/check`)

| Area | Tests | What they prove |
| --- | --- | --- |
| `internal/gateway/preview_test.go` | `TestPreviewRequestsOnlyFromThePreviewOrigin`, `TestPreviewSelectNeedsAnAppReferer`, `TestPreviewProbeAnswersWithoutACookie`, `TestPreviewShowsABrokerRefusalAsForbidden`, `TestPreviewProxiesWebSocketsAndClosesThemOnRevocation`, and the earlier preview tests (which now send `Sec-Fetch-Site: same-origin`) | L1; L2 (own, bound and forwarded, refused forms); the probe gets an answer without a cookie; `invalid_request` shows as `403`; revocation closes sockets within the recheck |
| `internal/gateway/multicomputer_test.go` | `TestDocumentCSPFramesConfirmedComputersOnly`, `TestDocumentCSPListsDeviceComputers` | H1: confirmed computers only in `frame-src`; no `wss:` preview entry |
| `internal/broker/computers_test.go` | `TestPreviewCheckFollowsAppTrust` | B1 |
| `web/src/screens/Preview.test.tsx`, `App.test.ts`, `web/src/computers.test.ts` | — | The frame attributes (W1); the bar over a peer (W2); no frame for `pair` / `unreachable` / `outdated` (W3); the computed peer origin (W4); `previewFor` and the routes (W5); the probe and its neutral text (W6) |

### The multi-computer harness (automated, Chrome and Playwright WebKit)

`scripts/e2e-multicomputer/` runs two computers (mac as home, vps as peer) behind an HTTPS front that stands in for Tailscale Serve. For peer preview the front also routes `<host>:8443` to each stack's preview listener, and a small dev site runs inside a vps session. Chrome goes through the real app screens with the app's real CSP; a Playwright WebKit run covers the engine differences. The steps it runs are listed in `scripts/e2e-multicomputer/README.md`; a check that the README does not list is not automated.

The checks this design asks of the harness, and the harness check that runs each (IDs as in the harness README; results are in `docs/progress.md`):

| ID | Check | Harness |
| --- | --- | --- |
| P1 | In the mac app, the vps session shows **Preview ready**; **Open ↗** shows the vps page in the frame. The selection has no `Origin`, `Sec-Fetch-Site: same-site` and `Referer: https://mac.tailx.ts.net/`; then `GET /` has `Sec-Fetch-Site: same-origin`. | `PV-chip`, `PV-vps-page`, `PV-vps-select`, `PV-vps-root`; WebKit W5 |
| P2 | The frame's own WebSocket connects (`101`). ↻ works. | `PV-vps-page`, `PV-vps-root`, `PV-vps-reload` (exactly one new selection); WebKit W6. A reload started inside the frame is not automated. |
| P3 | A page on `evil.tailx.ts.net` frames the same selection URL, with the vps cookie present: `403`, no preview cookie. | `PV-evil`, `PV-refuse-evil`; WebKit W7 |
| P4 | After a correct selection, the evil page tries an image, a `no-cors` fetch, a frame and a WebSocket to `https://vps.tailx.ts.net:8443`: all `403`. | `PV-evil` (the image address is the dev site's script) |
| P5 | The framed vps page tries `fetch` to mac's and vps's `/api`: both fail. | `PV-vps-api`, and `PV-home-api` for mac's own dev site. Nested frames, a script from another preview origin, WebSocket to `/api` and `top.location` from inside the frame are not automated; the rules they meet are the ones `PV-refuse-devsite`, `PV-refuse-direct` and Phase A check with direct requests. |
| P6 | A device paired directly with vps: the mac app's frame is refused. | `PV-direct` |
| P7 | With a preview socket open, change mac's node ID in vps's fake Tailscale status: the socket closes after the next snapshot, new requests fail, and the devices are revoked. | `PV-node`, `B7-node`. A changed tag set (refuse without revoking) is a Go test only (`internal/broker`). |
| P8 | `pterm revoke --via` on vps while the preview is open: the socket closes within 5 s, and the app removes the frame. | `PV-revoke-socket`, `PV-revoke-app`, `PV-revoke-after` |
| P9 | The real CSP of the mac app page: the frame loads for confirmed vps; a pending computer is not in `frame-src`. | `PV-pending`, `PV-added` |
| P10 | The bar over the vps frame shows `vps` and **untrusted page**; home's has neither. | `PV-vps-bar`, `PV-home`; WebKit W5 |
| P11 | A computer confirmed in this visit: the app reloads once and the preview opens (W8). | `PV-added` |
| P12 | WebKit: after a history restore of the app page, the frame is mounted again and shows the preview (difference A). | Not automated; real-phone check (`docs/owner-verification.md`). The spike saw it (section 2). |
| P13 | A cold navigation to a peer's preview address shows that session's terminal, with no frame and no request to the peer's preview origin; a port the session does not list does the same, also with the tap mark; a reload after a real tap keeps the preview (W9). | `PV-link-cold`, `PV-link-port`, `PV-link-reload` |
| | Also run: every near miss of the `Referer` rule with the phone's valid cookies; `GET /` with each `Sec-Fetch-Site`; home's own preview; a computer with no preview listener (the probe's text). | `PV-refuse-*`, `PV-home*`, `PV-off` |

What this harness cannot run (it uses the development profile):

- Development gateways use `pterm_dev_session` and `pterm_dev_preview` without `Secure` and without the `__Host-` prefix.
- A development gateway knows its own preview origin only as a loopback address (`previewOriginFor`), and its preview listener admits no `:8443` Host (`previewHandler.hostAllowed`). The front maps that one name both ways (Host, the own `Origin` and `Referer`, the own preview origin in the CSP and in `GET /api/v1/preview`). So the production gateway's own CSP text (`https://<host>:8443` in `frame-src`) and its Host rule for `:8443` are real-phone checks. A peer's `frame-src` entry comes from the computer list and is the real `https://vps.tailx.ts.net:8443`.
- The front stands in for Tailscale Serve and the tailnet: no real ACL, no real `tailscale serve --https=8443`.

### Real-phone checks (owner, not automated)

The list is in `docs/owner-verification.md`, "Preview for sessions on peers": the cookie in the frame in the installed iOS app and on Android Chrome; the move to `/`; ↻ and browser Back; video and audio; Vite HMR through a real Tailscale Serve; `tailscale serve --https=8443` and `pterm doctor` on a peer; the probe under a tailnet ACL that blocks 8443; the production CSP and `__Host-` cookies; the reload-once after adding a computer; revocation with an open preview.

## 12. Reviews: findings and resolutions

Two independent reviews ran on version 1. Security: "sound with changes". Feasibility: "build with changes". The full review texts are not in this repository; the findings below follow the coordinator's summary, and the severity column is this document's reading, not the reviewers'. Each point was checked against the code before it was accepted. A final plan review ran on version 2 before the build.

### Security review

| # | Finding | Severity | Resolution |
| --- | --- | --- | --- |
| S1 | `Referer` is not weaker than the ticket against these attackers: a page cannot forge it, redirects keep the first one, a dev site on `:8443` sends another origin, and the broker runs the same `admitDevice`. The ticket adds a route, an RPC and state for no gain. | high | Accepted. B was built (section 3). |
| S2 | State the costs of `Referer`: fails closed when removed; a script-free HTML injection on home could start a selection; a second origin proof. | medium | Accepted; section 3 "Honest costs" and the ADR. |
| S3 | The "accept `same-site` navigations" fallback reopens blind navigations; remove it. | high | Accepted. L1 has no fallback. The spike showed that none is needed. |
| S4 | The frame withdraws the guarantee of `docs/multi-computer.md` (no phishing text from a peer); a peer dev site can show a fake "pair again". | high | Accepted; section 7, the ADR, and `docs/multi-computer.md` itself. |
| S5 | About 200 `Domain=<tailnet>.ts.net` cookies evict every computer's `__Host-pterm_session` (forced sign-out; recovery is "pair again"), and such cookies reach other dev servers. | medium | Accepted; section 7. The eviction number is browser-specific and not tested here. |
| S6 | The bar over a peer frame must always show the computer label and an "untrusted page" mark; the sandbox stays without fullscreen and `allow`; on `401`/`403` the app removes the frame. | high | Built as W1 to W3. Correction: the app cannot see the frame's own answers and gets no `403` for a trust refusal, so W3 follows the computer's status in the app's poll. |
| S7 | Delete the sentence that the ticket "makes it harder" to switch sessions: script on the same host can replace the unsigned preview cookie. | low | Accepted; deleted. Section 7 states that no isolation is claimed. |
| S8 | A dev-site service worker on `:8443` can re-send navigations as `same-origin` and serve cached content after revocation. | medium | Limit 1 in section 13. The owner accepted it (2026-10-03). |
| S9 | "Within about 5 s" is wrong for a node change and for the status grace. | medium | Accepted; section 8 gives the three timings. |
| S10 | The gap exists on `main` for home alone; fix it first, as its own pull request. | high | The gap (#143) is closed for home and peers by L1, L2 and B1. It shipped in the same pull request as the peer work, with the owner's approval, not as its own. |

### Feasibility review

| # | Finding | Severity | Resolution |
| --- | --- | --- | --- |
| F1 | `Referer` is small; the ticket is 300 to 400 lines plus a route, an RPC and a table. | high | Accepted. B built. |
| F2 | Browser facts are untested; run a spike (Chrome, Playwright WebKit, one real phone) before any build. | high | Done for Chrome 154 and Playwright WebKit 26.5 (section 2). The real phone is still open (`docs/owner-verification.md`). |
| F3 | Fix the `main` gap for home as its own pull request before the peer work. | high | See S10. |
| F4 | The listener's text pages are blank in the app frame; the selection page must not use `previewPage`. | high | Built: `previewPage` has no `frame-ancestors`; the selection page is its own page (#144). |
| F5 | L1 answers `403` to requests without `Sec-Fetch-Site`: Safari before 16.4, possibly iOS media loads. | medium | Limit 2. iOS 16.4 is already the minimum (`docs/threat-model.md`, "Browser requirement"). Video and audio passed in the spike and are on the real-phone list. |
| F6 | L1 refuses sub-resources of opaque-origin frames inside the dev site. | low | Limit 3. |
| F7 | An old cached app bundle cannot open a preview until it updates. | low | Limit 4. The frame now shows the refusal text, not a blank page. |
| F8 | The app tied every preview to home's `previewAt`. | medium | Built as W5 (`previewFor`, `route()`). |
| F9 | Do not add `wss://<peer>:8443` to `connect-src`. | low | Built; H1 says never, and a test checks it. |
| F10 | The probe needs `AbortSignal.timeout(5000)` and neutral failure text (a stale cached CSP looks like a network error). | medium | Built as W6, in the same pull request. |
| F11 | In the development profile home's `frame-src` and `GET /api/v1/preview` give the loopback address, so harness checks that need `https://mac.tailx.ts.net:8443` cannot pass as written. | high | Accepted. The front answers `CONNECT <host>:8443` and maps the stack's loopback preview origin to `https://<host>:8443` both ways (see the comment at the top of `scripts/e2e-multicomputer/main.go`). The production profile stays a real-phone check. Not accepted: computing `<origin>:8443` for home in the app, because it breaks the loopback development profile. |
| F12 | The test dev server must use relative redirects only; list what must stay on a real phone. | medium | Accepted; section 11 and `docs/owner-verification.md`. |

### Final plan review (13 changes) and the open point

| # | Change | Where |
| --- | --- | --- |
| 1 | `Referer` parse rule: no `url.Parse`; exactly one header that ends in one `/`; own origin through `originAllowedValue`; a bound origin as an exact string after `ValidateComputerOrigin`; the lookup split out of `crossOrigin` as `boundOrigin` | `appReferer`, `boundOrigin` |
| 2 | Check order: Host; selection = no `Origin`, then the `Referer` rule; other paths = L1; then the cookie (the failed-attempt limiter was removed later, #149). The probe path keeps its no-cookie answer (`401`) | `previewHandler.ServeHTTP` |
| 3 | B1 the simple way: `appTrust` whenever the device is bound, with or without a forwarded origin | `GatewayPreviewCheck` case |
| 4 | Forward the origin on the selection only | `selectPort` |
| 5 | A broker `invalid_request` shows as `403` "This app may not use this computer.", not `503` | `fail` |
| 6 | `previewPage` without `frame-ancestors`; the selection page is its own page with `frame-ancestors <validated Referer origin>`, `Referrer-Policy: no-referrer`, `no-store`, `nosniff`; one mechanism for the move (meta refresh, which `default-src 'none'` allows) | `previewPage`, `selectPort`, `previewSelectedPage` |
| 7 | The probe is in this pull request: own preview origin in `connect-src`, confirmed computers' `:8443` in `connect-src`, never `wss:` | `gatewayHandler.ServeHTTP`, `deviceConnectSources`, `probePreview` |
| 8 | `frame-src` from confirmed computers only; append to an existing `frame-src` or add one | `deviceConnectSources` |
| 9 | The app never sees a `403` for a trust refusal; show the peer frame only while that computer's status is `ok` | `previewFor` (W3) |
| 10 | A peer preview link must not need home's preview origin | `route()` (W5) |
| 11 | Stale line numbers in version 2 | This version names functions |
| 12 | Harness: the front's log needs `Referer` and `Sec-Fetch-Dest`; the `:8443` route rewrites an `Origin` of exactly `https://H:8443` to the preview listener's loopback origin and leaves another computer's `Referer` alone; stacks start with a preview listener | `scripts/e2e-multicomputer/` (see its README) |
| 13 | Protocol revision 2, regenerated types, protocol docs; `minRevision` stays 1 | section 9 |

Open point found after the build: a computer confirmed in this visit was blocked by the app's own CSP until the next load, and the app showed the probe's text, which points at `pterm setup` (the wrong fix). Resolved by W8 (reload once on a `securitypolicyviolation` for that preview origin).

Two adversarial reviews of the joined build (security: safe to ship with one required fix; correctness: ship after fixes), and what was done:

| Finding | Resolution |
| --- | --- |
| Security, required: a link opened a peer's frame with no tap (`route()` took any in-range port). | W9: the tap mark in `history.state` and the port in the session's `previews`. Checks `PV-link-*`. |
| `fail` in `preview.go` showed a broker `protocol_mismatch` as "This app may not use this computer." (both are 400 on the API). | `fail` matches the error code `invalid_request`; a mismatch keeps the 503 text (Go test). |
| A home preview whose origin became undefined while open showed "Opening preview…" without end. | `PreviewBody` shows the `cantPreview` text for home without an origin; the bar keeps the way back. |
| The frame is remounted only on `pageshow` with `persisted`; a history restore without the page cache is unverified. | No guessed fix. It is a real-phone check (`docs/owner-verification.md`), and the troubleshooting page says to tap **↻**. |
| ↻ mounted the frame twice; a computer confirmed in this visit was blocked by the CSP. | Fixed before the reviews came back (W8, and one probe per frame). |

## 13. Known limits, open risks, things not verified

Known limits (accepted):

1. **Service worker on `:8443`.** A dev site can register a service worker for its preview origin. It can answer a navigation that another page started, and its own requests are `same-origin`, so for that origin L1 does not stop framing by another page. It can also serve cached content after the phone was revoked. It cannot read anything new. The owner accepted this on 2026-10-03; the other choice (refuse `Sec-Fetch-Dest: serviceworker`) breaks dev sites that use a service worker.
2. **Requests without `Sec-Fetch-Site` get `403`.** Safari before 16.4 (already below the minimum). iOS media loads are a real-phone check.
3. **Opaque-origin frames inside a dev site** (a sandboxed frame without `allow-same-origin`, `srcdoc` with a sandbox): their sub-resources are not `same-origin` and get `403`.
4. **An app bundle from before this change** sends no `Referer`; the frame shows "Open the preview from the Termivoa app." until the app updates.
5. **A `Referer` removed by the browser** (a setting or an extension) means no preview; same text.
6. **A dev site that sets `Referrer-Policy: no-referrer` and posts a form** sends `Origin: null`; L1 refuses it (it was refused before this change too).
7. **WebKit: Back inside the frame.** `history.back()` in the sandboxed frame, past its first entry, moves the whole app back (difference B). Exists for home previews too.
8. **WebKit: history restore.** When the app page is restored from history, the frame first loads its last URL and gets `403` "Origin not allowed."; the app mounts a new frame on `pageshow` with `persisted`, and ↻ does the same (difference A).
9. **A plain `curl` or a typed URL** on the preview listener is `403` (a `curl` that forges `Sec-Fetch-Site` or `Origin` passes L1; the cookie and the broker stop it).
10. **The probe says "something answers on 8443", not "Termivoa answers".** Its failure text names one possible cause only.

Open risks:

1. **Real phones are not checked**: cookies in the frame in the installed iOS and Android apps (also not checked for home), media loads, HMR through a real Tailscale Serve (`docs/owner-verification.md`).
2. **Phishing in the frame** has no technical limit beyond the bar.
3. **Cookie eviction and bloat** from a peer dev site have no fix in this design. The number of cookies that evicts a session cookie was not tested.
4. **`Referer` is optional.** A browser that stops sending it for framed navigations breaks every preview (closed); variant C is the fallback design.
5. **Home's own preview address opens from a link** (`/sessions/<id>/preview?port=<n>`), also in the installed Android app. It needs a real session ID of home and shows only home's own dev server. A peer's address does not: it needs the tap mark (section 7, W9).

Issues that this work closed: #143 (the preview listener skipped per-device origin binding) and #144 (listener text pages blank in the app frame; `GET /api/v1/preview` answers an origin when Serve 8443 is not set up, covered by the probe).

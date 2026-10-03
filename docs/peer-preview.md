# Preview for sessions on peers: design

Status: **proposed 2026-10-03 — DO NOT MERGE without owner review** (AGENTS.md rule 24; ADR [0006](decisions/0006-peer-preview.md)). Design only: no code exists. The adversarial reviews (security; feasibility with alternatives) have not run yet; their findings go in section 12.

Goal (roadmap item 3, `docs/pending-work.md:37`): a session on a peer computer (for example `vps`) shows the **Preview ready** chip, and the home app shows that peer's dev server in the frame. Read [preview.md](preview.md) and [multi-computer.md](multi-computer.md) first; this document only describes what changes.

Names used below: **home** `https://mac.tailx.ts.net` serves the app. **Peer** `https://vps.tailx.ts.net`. A computer's **preview origin** is its host on port 8443 (`internal/gateway/preview.go:28`).

## 1. What happens today (from the code)

| Fact | Evidence |
| --- | --- |
| The app offers a preview for home sessions only. | `web/src/screens/App.tsx:854` (`!view.session.computer.home`), `:117-118`, `:875` |
| Peer session records already carry `previews`; the app drops them. | `protocol/v1/envelope.schema.json:165-180`, `web/src/screens/Live.tsx:716` |
| Home's CSP frames only its own preview origin. Peers get `connect-src` only, per device, on `index.html`. | `internal/gateway/handler.go:199-201`, `:484-515` |
| The preview listener accepts a request when the `Origin` header is absent, or equal to the preview origin. Nothing else is checked before the cookie. | `internal/gateway/preview.go:211-215`, `:221-225` |
| The listener's broker call carries no origin. | `internal/gateway/preview.go:241`, `:277`, `:302`; `internal/gatewayclient/client.go:339-346`, `:627` |
| The broker treats a missing origin as its own origin and admits the device. | `internal/broker/gateway_rpc.go:301` → `:108-114`; `internal/broker/computers.go:40-43`, `:75-78` |
| So **per-device origin binding is not applied on the preview listener**: a credential bound to home's app (`devices.app_origin`) is accepted there, from any page that can make the browser send the cookie. | The three rows above |
| The selection is a `303` to `/`. | `internal/gateway/preview.go:315` |
| Open preview sockets and streams are checked again every 5 s. | `internal/gateway/preview.go:41`, `:263`, `:268-285` |
| A main origin refuses its **own** preview origin as `Origin` (403, before routing). Other origins on `/api` need an exact own or bound `Origin`, or no `Origin` plus `Sec-Fetch-Site: same-origin`; `no-cors` is refused. | `internal/gateway/handler.go:214-219`, `:258-273`, `:279-282`; `internal/gateway/preview.go:134-149` |
| `GET /api/v1/preview` reports an origin whenever the preview listener runs. It does not know whether Tailscale Serve 8443 is set up. | `internal/gateway/preview.go:117-130`, `:164-167` |
| Serve 8443 is a separate consent question in `pterm setup`, per computer; `pterm doctor` reports it. | `cmd/pterm/lifecycle.go:812-847`, `:370-377`; `internal/lifecycle/lifecycle.go:43` |

## 2. What the browser sends (the hard part)

The frame navigates from a top-level page on home to `https://vps.tailx.ts.net:8443/__termivoa/select?…`.

| Header | Value on the frame's first request | Why |
| --- | --- | --- |
| `Cookie` | The peer's `__Host-pterm_session` | Cookies ignore ports (RFC 6265 §8.5). The cookie is host-only for `vps.tailx.ts.net`. `SameSite=Strict` is decided by site, and `ts.net` is a public suffix, so home and the peer are same-site (`docs/multi-computer.md:13-14`). |
| `Origin` | **absent** | Browsers send `Origin` on CORS requests and on non-`GET` requests, not on a `GET` navigation. |
| `Sec-Fetch-Site` | `same-site` | Other origin, same site. The same value arrives from a page on **any** machine in the tailnet (another owner's machine in a team tailnet, another computer's dev site). |
| `Sec-Fetch-Mode` / `Sec-Fetch-Dest` | `navigate` / `iframe` | They say what kind of request it is, not who made it. |
| `Referer` | absent | The frame has `referrerPolicy="no-referrer"` (`web/src/screens/Preview.tsx:73`). |

Conclusion: **no header on this request names home's app.** `devices.app_origin` cannot be compared with anything. A rule that says only "`same-site` navigation into an iframe with a valid cookie" would let every same-tailnet page select and frame the peer's preview. The proof that the app asked must come from somewhere else.

After the selection, requests made by the framed dev page itself are different: `fetch`, scripts, images and same-origin navigations carry `Sec-Fetch-Site: same-origin`; form posts and WebSocket handshakes carry `Origin: https://vps.tailx.ts.net:8443`. A `303` does **not** give that: after a redirect the request still counts from the page that started it, so `GET /` would still say `same-site`. That is why the selection must answer with a page, not a redirect (section 4).

Not verified in a real browser by this design: every row above is from the Fetch, Fetch Metadata and cookie specifications. Section 9 and 10 list the checks.

## 3. Options for authenticating the frame

| | A. Fetch-metadata and `frame-ancestors` only | B. `Referer` as origin proof | **C. Preview ticket (recommended)** |
| --- | --- | --- | --- |
| Rule on the selection | cookie + `Sec-Fetch-Site: same-site` + `Sec-Fetch-Dest: iframe`; responses carry `frame-ancestors <app origin>` | cookie + `Referer` whose origin is the credential's bound app origin (frame uses `referrerPolicy="origin"`); the gateway forwards it to the broker as the request origin | cookie + a single-use ticket that the app fetched through the authenticated CORS API |
| What stops another same-tailnet page from selecting | Only that it does not know the session ID. Session IDs are in app URLs and are not designed as secrets. | The browser never lets a page send another origin's `Referer`. | Only the bound app origin can get a ticket: the ticket route runs the existing bound-`Origin` check (`internal/broker/computers.go:75-83`). |
| Works when a header is removed | Yes | **No**: `Referer` is optional; privacy settings and extensions remove it. The preview then fails (closed). | Yes |
| New trust anchor | none (and too weak) | `Referer` becomes a second kind of origin proof next to `Origin` | none: the proof is the existing `Origin` check on `/api` |
| New parts | one response header | one frame attribute, a `Referer` parse | one HTTP route, one gateway RPC, one optional RPC field, a small in-memory ticket table in the broker |
| Secret in a URL | no | no | yes: 30 s, single use, useless without the cookie |

**Recommendation: C.** It is the only option whose proof goes through the path that was already reviewed for multi-computer (exact bound `Origin`, `X-Pterm-Request`, broker-side per-device binding, node and tag checks), and it binds the selection to one device, one session and one port. **B is the fallback** if the owner wants the least code: it is safe in current browsers but rests on an optional header. **A is rejected:** `same-site` is not "same owner" (`docs/multi-computer.md:62-64`), and `frame-ancestors` is enforced only after the request already ran.

Also rejected: a preview-only bearer cookie that replaces the device cookie (a new credential type with its own revocation; against "no new secret type", `docs/multi-computer.md:52`), and a relay of the peer's preview through home (home would hold standing peer credentials; `docs/multi-computer.md:23-31`, option B).

All three options also need the listener rule of section 4 (L1). Without it, after a correct selection, any same-tailnet page could still load scripts from, frame, or send blind `GET`s to the selected dev server.

## 4. Design

### Request flow

```text
phone app  https://mac.tailx.ts.net  (home)
  1. POST https://vps.tailx.ts.net/api/v1/sessions/<id>/preview  {port}
       credentialed CORS: Origin = home, vps cookie, X-Pterm-Request
       vps broker: bound-origin check, node and tags, session runs, port owned → {ticket}
  2. iframe src = https://vps.tailx.ts.net:8443/__termivoa/select?session=<id>&port=<n>&ticket=<t>
       vps preview listener: vps cookie + ticket → broker consumes the ticket
       → 200 small page, sets the preview cookie, moves the frame to /
  3. GET /  (and every later request of the dev page)
       same-origin by Sec-Fetch-Site or Origin; cookie + preview cookie;
       broker re-check on every request and every 5 s for open sockets
```

A home session uses the same three steps against home (step 1 is then a same-origin `POST`).

### Rules that change on every computer (each computer runs the same build)

**L1. Preview listener: who may send a request** (`internal/gateway/preview.go`, replaces `:208-215`). A request other than the selection passes only with:

- exactly one `Origin` header equal to the preview origin itself (form posts, WebSocket handshakes), or
- no `Origin` header and exactly one `Sec-Fetch-Site: same-origin` (reuse `sameOriginFetch`, `internal/gateway/handler.go:279-282`).

Everything else is `403` before the cookie is read and before the dev server is reached. This is the `/api` rule (`handler.go:264-273`) applied to the preview listener. It refuses a script tag, image, `no-cors` fetch, frame or WebSocket from the app, from another tailnet machine and from another computer's dev site.

**L2. Preview listener: the selection** (`selectPort`, `preview.go:289-316`). `GET /__termivoa/select?session=<uuid>&port=<n>&ticket=<secret>`, exactly these three parameters. It needs the device cookie and the ticket; fetch metadata is not checked here, because the ticket is the stronger proof. The broker consumes the ticket (B2). The answer is `200 text/html` with `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'`, the preview cookie as today (`preview.go:309-312`), and a body that only moves the frame to `/` (`<meta http-equiv="refresh" content="0;url=/">`). The move is started by a document on the preview origin, so `GET /` is `same-origin` and passes L1. A selection without a valid ticket never sets the preview cookie.

**A1. Ticket route on the main origin** (`internal/gateway/handler.go:127-150`, new route). `POST /api/v1/sessions/{id}/preview`, body `{"port": <1..65535>}`, answer `{"ticket": "<secret>"}`. It is a mutation (`internal/gateway/api.go:490-525`): exact own `Origin` or the admitted bound `Origin`, `X-Pterm-Request: 1`, JSON. When the preview listener is off (`previewPort == ""`), it answers `404 preview_missing`.

**B1. Broker: mint** (new `gateway.preview.ticket`, next to `internal/broker/gateway_rpc.go:296`). `authorize(credential)` as every other call (`gateway_rpc.go:108-114`: authenticate, then `admitDevice` with the forwarded origin), then the session must run and own the port now (`Session.PreviewTarget`, `internal/broker/devpreview.go:119-137`). The ticket is 256 random bits in the existing `secret` text form; the broker keeps only its SHA-256, with the device ID, session ID, port and an expiry 30 s ahead, in memory. Bounds (rule 11): at most 4 unexpired tickets per device and 64 in total; the oldest is dropped. A broker restart drops all tickets.

**B2. Broker: check** (`gateway.preview.check`, `gateway_rpc.go:296-312`). The payload gains an optional `ticket`.

- For every check: authenticate the credential. If the device is bound to an app origin (`principal.App.Bound()`), also require `appTrust(principal.App)` (`internal/broker/computers.go:59-72`). A device that paired with this computer directly is checked as today. The call no longer goes through "missing origin means own origin".
- With a `ticket` (the selection): the ticket must exist, be unexpired, and match the credential's device, the session and the port. It is deleted on first use, pass or fail.
- Without a `ticket` (every later request and the 5 s recheck): as the first bullet, then the port ownership check as today.

### Rules that change on home only

**H1. CSP of the app document** (`deviceConnectSources`, `internal/gateway/handler.go:484-515`). For each **confirmed** computer of the cookie's device (`list.Computers`, never `list.PendingOrigins`), add `<origin>:8443` to `frame-src` and to `connect-src`. `frame-src` is created when home's own preview listener is off. Entries whose node changed are already left out by the broker (`internal/broker/computers.go:216-241`). At most 8 (`internal/protocol/computers.go:10`). Like `connect-src` today, this is on `index.html` only and only with a usable cookie; another device's cookie gets that device's list. The known limit for the cached offline shell (`docs/threat-model.md:276-279`) now also covers `frame-src`.

`connect-src` is needed for one thing: the reachability probe in section 6. The app sends nothing else to a preview origin.

### What does not change

- The frame keeps `sandbox="allow-scripts allow-forms allow-same-origin"` and `referrerPolicy="no-referrer"` (`web/src/screens/Preview.tsx:72-73`): no top navigation, no popups, no modals, no downloads, and no `allow` attribute, so no camera, microphone or location.
- The app never uses an origin string that a peer sent. A peer's preview origin is always computed as `<computer.origin>:8443` from the phone's own list (`web/src/computers.ts:13-14`), and home's `frame-src` is computed the same way by home.
- Proxy rules, cookie stripping, port ownership and the 5 s recheck (`docs/preview.md:105-123`).
- Push, pairing, `/api` admission and CORS.

## 5. What each side must refuse

| Request | Who answers | Result | Rule |
| --- | --- | --- | --- |
| Dev site on peer A (`https://a:8443`) calls home's or peer B's `/api` with `fetch` or a WebSocket | main origin | `403`: the `Origin` is not own and not bound (a bound origin has the default port, `internal/protocol/computers.go:26`) | existing, `handler.go:264-273` |
| Same dev site sends a `no-cors` `GET` or navigates a nested frame to `/api/...` | main origin | `403`: `no-cors`, or no `Origin` without `same-origin` | existing, `handler.go:258-273` |
| A computer's own dev site calls that computer's main origin | main origin | `403` before routing | existing, `handler.go:214-219` |
| Dev site on peer A asks peer B (or home) for a ticket | main origin | `403` as row 1 | A1 + existing |
| Dev site on peer A loads a script or image from, frames, or opens a WebSocket to `https://b:8443` or `https://mac:8443` | preview listener | `403` before the dev server | **new, L1** |
| Any page selects without a ticket, with a used or expired ticket, or with a ticket of another device, session or port | preview listener + broker | refused; no preview cookie | **new, L2 + B2** |
| Home's app asks a peer for a ticket with a credential that paired with the peer directly (`app_origin` NULL) | peer broker | refused (`ErrOriginRefused`) | existing, `computers.go:79-81` |
| A bound device's preview request after home's node ID changed, its tag set changed, or the status is older than the grace period | peer broker | refused; a node change also revokes | **new, B2** via `appTrust` |

Does a dev site on peer A get new power over home or peer B? **Over the APIs: no.** Over the other preview origins: without L1 it would (blind `GET`s and script loads against the selected dev server, with the user's cookies), because a framed peer dev site is the first foreign same-site page that runs inside the installed app's cookie jar. L1 removes that. What stays is in section 7.

## 6. A peer without a preview origin, and older peers

| Case | How the app knows | What the user sees |
| --- | --- | --- |
| Peer below protocol revision 2 | `settings.protocolRevision` of that computer (`web/src/screens/allSessions.ts:28`, `web/src/api/http.ts:91-97`) | No chip, as today. The app never sends the new route to it (`docs/multi-computer.md:127`). |
| Peer never ran `tailscale serve --https=8443`, or an ACL blocks 8443 | When the user taps **Open ↗**, the app sends one `fetch('<peer>:8443/__termivoa/select', {mode: 'no-cors', credentials: 'omit'})`. Any HTTP answer (here a `401`, because the probe sends no cookie) means the port is served. A network error, while the ticket call on 443 just worked, means it is not. | Instead of the frame: "vps does not share dev-server previews. On vps, run `pterm setup` and answer yes to the preview question." |
| Peer's preview listener is off, the port closed, or the session ended | The ticket call answers `404` or `409` | "That preview is gone." and a way back to the terminal |
| Peer answers `401` | existing | "vps · pair again" (`allSessions.ts:39`) |
| Peer unreachable | existing | "vps · can't reach" |

The probe is on the phone on purpose: it tests the real path from the phone, and it needs no Tailscale call in the broker. The same probe fixes the home case, where the chip shows today even when Serve 8443 was declined. Rejected: letting the broker run `tailscale serve status --json` (more host code, and it cannot see ACLs).

Each computer's Serve 8443 stays that computer's own consent in `pterm setup` (`cmd/pterm/lifecycle.go:812-847`, rule 12). Nothing in this design changes Tailscale configuration, and no computer configures another.

**Protocol revision.** `protocol/v1/revision.json` goes from 1 to 2; `minRevision` stays 1. Home and the app are one build, so home always has the ticket route. A new peer with an older home app: the old app never frames a peer, so nothing breaks. No fallback path exists for the old selection without a ticket (rule 26).

## 7. Honest trust statement

**When you open a preview of a session on a peer, that peer puts a page of its choice inside your app.** It does not need a real dev server: a compromised peer can report any port in `previews` and answer on its `:8443` with anything.

What that page **cannot** do:

- Read or change the app: it is another origin, and the app keeps `frame-ancestors 'none'` and its CSP.
- Navigate the app away, open popups, show dialogs or start downloads (frame sandbox).
- Call the API of home or of any peer, or use another computer's preview (section 5).
- Appear without a tap: the app frames a peer only after **Open ↗** on a session of that peer (or a reload of that same screen).

What it **can** do, stated plainly:

- **Phishing inside the frame.** It fills the screen under the app's own bar. It can draw a false "pair again" page or a false login. Limit: the bar is drawn by the app and must name the computer (section 8, W3) with the label from the phone's own list.
- **Break the app for all computers.** Script in the frame can set large cookies with `Domain=<tailnet>.ts.net`. Every request to every computer then grows past the 16 KiB header limit until the app's data is cleared. Before this feature only a peer's HTTP responses could do that (`docs/multi-computer.md:45`); now any code that runs in a peer's dev site can (a project dependency, for example). A home dev site already could (`docs/preview.md:132-134`).
- **Send blind requests from the phone** to other machines in the tailnet (`no-cors`), also machines that the peer itself cannot reach under your ACLs. It cannot read the answers. Any web page that the phone opens can do the same.
- **Keep state on its own origin** (storage, a service worker for `https://vps:8443`). It controls only that origin.
- **Track that you opened it**, and when.

What stays as before: a compromised home compromises every peer (`docs/multi-computer.md:37-39`). A compromised peer still controls only itself; this feature gives it the frame above and nothing more.

Known limits kept from `docs/preview.md:125-141`: all previews of one computer share one origin; do not treat two dev sites of one computer as isolated from each other. The ticket makes it harder for one dev site to switch the selection to another session (it cannot get a ticket), but this design does not claim isolation.

## 8. Tailscale rules for preview streams

| Rule from multi-computer | Does the preview on a peer follow it? |
| --- | --- |
| Node-ID pinning | Yes. Mint goes through `admitDevice`; every preview check of a bound device goes through `appTrust` (B2). A changed node ID answers `ErrRevoked`, and the sweep revokes the devices as today (`internal/broker/computers.go:270-293`). |
| Tagged nodes | Yes, no new rule. `appTrust` compares the pinned tag set (`internal/tailnet/status.go:217-228`): a changed set is refused, not revoked. |
| 60 s status cache, 10-minute grace | Yes. `appTrust` only reads the snapshot (`internal/tailnet/monitor.go:78-94`). After the grace period preview requests of bound devices are refused, not revoked. Devices that paired with the peer directly are not affected. |
| Already-open WebSockets closed on revocation | Yes, with one difference. Live streams close at once on a new snapshot (`CloseUntrusted`, `internal/auth/service.go:545-551`). Preview sockets are not broker streams; the gateway asks again every 5 s (`preview.go:268-285`) and closes them when B2 refuses. **So a preview socket closes within about 5 s, not at once.** This is the existing preview bound (`docs/preview.md:88-90`). |
| A device revoked or expired | As today: `401` on the next request, open sockets close within 5 s. |

## 9. Protocol changes (additive, rule 6)

| File | Change |
| --- | --- |
| `protocol/v1/revision.json` | `revision` 2 |
| `protocol/v1/http-api.schema.json` | `previewTicketRequest {port}` and `previewTicketResponse {ticket}` (`ticket` uses `#/$defs/secret`) |
| `protocol/v1/gateway-rpc.schema.json` | new `gateway.preview.ticket` (payload: credential, sessionId, port) and `gateway.preview.ticket.result` (`ticket`); `previewCheckPayload` (`:188-199`) gains optional `ticket` |
| `protocol/v1/types.schema.json` | the new type names |
| `protocol/v1/http-api.md` | the new route; "Preview listener" (`:289-300`): the selection needs `ticket` and answers `200`, and the L1 rule; revision 2 in "Protocol revision" |
| `protocol/v1/authentication-rpc.md` | `gateway.preview.ticket`; the changed `gateway.preview.check` (`:57`, `:118-121`) |
| Generated Go and TypeScript | regenerate; the drift check must pass |

No new error code: `preview_missing`, `session_missing`, `unauthorized` and `invalid_request` exist. No WebSocket message changes. No store schema change: tickets live in memory.

## 10. Implementation plan (file by file; not started)

Tracks own separate files. P first; then H1, H2, W, E in parallel.

| Track | File | Change |
| --- | --- | --- |
| P | `protocol/v1/*` (section 9), `internal/protocol/generated.go`, `web/src/generated/protocol.ts`, `internal/protocol/auth_rpc.go` (`:61`, `:294-307`: bind the new message and its result) | schemas, revision, generation |
| H1 broker | `internal/broker/devpreview.go` | the ticket table (mint, consume, bounds, expiry); pure functions with a clock |
| H1 | `internal/broker/computers.go` | `admitPreview(principal)`: bound → `appTrust`, else nil |
| H1 | `internal/broker/gateway_rpc.go` (`:296-312`) | `GatewayPreviewTicket` case; `GatewayPreviewCheck` uses `admitPreview` and the optional ticket |
| H1 | `internal/gatewayapi/gatewayapi.go` (`:67-70`), `internal/gatewayclient/client.go` (`:339-346`) | `PreviewTicket(ctx, credential, sessionID, port)`; `PreviewTarget` gains a `ticket` argument |
| H2 gateway | `internal/gateway/preview.go` | L1 in `ServeHTTP`; L2 in `selectPort` (three parameters, `200` page); the ticket handler (A1) |
| H2 | `internal/gateway/handler.go` | route `/api/v1/sessions/{id}/preview`; H1 CSP in `deviceConnectSources` |
| H2 | `internal/gateway/fake_broker_test.go` | the fake broker learns tickets |
| W | `web/src/api/http.ts`, `web/src/protocol/httpBody.ts` | `previewTicket(sessionId, port)` on the per-computer client |
| W | `web/src/screens/PreviewChip.tsx` | `previewSrc(origin, sessionId, port, ticket)` |
| W | `web/src/computers.ts` | `previewOriginOf(computer)`: home uses the origin from `GET /api/v1/preview` (as today, so the development profile keeps working); a peer uses `<origin>:8443` |
| W | `web/src/screens/Preview.tsx` | W1: get a ticket on open and on every reload, then set `src`; W2: the probe and the texts of section 6; W3: the bar shows `<label> · localhost:<port>` with the computer's colour when there is more than one computer |
| W | `web/src/screens/App.tsx` | offer `onPreview` for a peer whose revision is 2 or higher (`:854`); the route `/c/<host>/sessions/<id>/preview?port=<n>` (`:114-118`, `:157-164`); pass that computer's sessions to `Preview` (`:875`) |
| E | `scripts/e2e-multicomputer/main.go` (`:152-157` accepts only `CONNECT host:443`), `lib.mjs` (`:158` starts stacks with `--preview-listen off`), `phase-b.mjs`, `webkit.mjs`, `README.md` | route `<host>:8443` to each stack's preview listener with the same Host and Origin rewrite; start the preview listeners; the checks of section 11 |
| D (at implementation, rules 22 and 25; not in this design commit) | `docs/preview.md`, `docs/multi-computer.md` ("No previews from peers"), `docs/threat-model.md` (boundary table and the two sections), `docs/architecture.md`, `docs/owner-verification.md` (`:190-212`: the manual selection check needs a ticket now), `docs/pending-work.md`, `docs/progress.md`, `CHANGELOG.md`, `README.md`, site docs (computers guide, preview guide, security, troubleshooting, release notes) | |

No CLI change. `pterm setup` and `pterm doctor` already ask about and report Serve 8443 on each computer.

Prerequisite to check first: section 13, finding 1 (the listener's text pages may be blocked inside the frame). The texts of section 6 that come from the listener depend on it.

## 11. Test plan

### Unit and loopback tests

| Area | Must prove |
| --- | --- |
| `internal/broker/devpreview_test.go` | A ticket works once; it expires after 30 s; it fails for another device, session or port; per-device and total bounds hold; a failed use also deletes it. |
| `internal/broker/computers_test.go` | Preview check of a bound device: trusted → pass; node changed → `ErrRevoked`; tags changed, other owner, name gone → refused; no snapshot and grace expired → refused, not revoked. A directly paired device is not affected by the snapshot. Mint from an origin that is not the credential's bound origin → refused. |
| `internal/gateway/preview_test.go` | L1 table (like `multicomputer_test.go:270-280`): own `Origin` pass; no `Origin` + `same-origin` pass; no `Origin` + `same-site`, `cross-site`, `none`, missing or doubled header → `403`, and the dev server sees no request. L2: no ticket, bad ticket → no `Set-Cookie`; good ticket → `200`, cookie, the page. A WebSocket without `Origin` → `403`. Revocation still closes sockets within the recheck. |
| `internal/gateway/multicomputer_test.go` | H1: `frame-src` and `connect-src` hold exactly the own preview origin and `<origin>:8443` of confirmed computers; pending and other devices' computers are absent; no cookie → unchanged; home previews off → `frame-src` lists only peers. A1: the ticket route refuses a missing or unbound `Origin`, a missing `X-Pterm-Request`, and a preview origin as `Origin`. |
| `web` | `previewSrc` with a ticket; no chip for a peer at revision 1; the peer preview origin is computed, never read from a peer's answer; route parse and build for `/c/<host>/sessions/<id>/preview`; the probe texts; the bar label. |

### What `scripts/e2e-multicomputer` must prove (Chrome and WebKit)

Setup: mac and vps as today, vps paired through mac, a small HTTP server with a WebSocket started **inside a vps session** (so the port belongs to the session's process tree), `vps.tailx.ts.net:8443` and `mac.tailx.ts.net:8443` routed to the preview listeners.

| ID | Check |
| --- | --- |
| P1 | In the mac app, the vps session shows **Preview ready**; **Open ↗** shows the vps page in the frame. The front log shows: ticket `POST` with `Origin` mac; the selection with no `Origin` and `Sec-Fetch-Site: same-site`; then `GET /` with `Sec-Fetch-Site: same-origin`. **This row proves the browser facts of section 2 in both engines.** |
| P2 | The frame's own WebSocket connects (`101`); a file change reaches the page. |
| P3 | Replaying the same selection URL is refused. A ticket older than 30 s is refused. |
| P4 | A page on `evil.tailx.ts.net` and a page on `https://mac.tailx.ts.net:8443` cannot get a ticket from vps (`403`, no CORS headers), with the vps cookie present. |
| P5 | After a correct selection, the evil page tries a script tag, an image, a `no-cors` fetch, a frame and a WebSocket to `https://vps.tailx.ts.net:8443`: all `403`, and the dev server's log has no such request. |
| P6 | The framed vps page tries: `fetch` and WebSocket to mac's and vps's `/api`; a nested frame and a script from `https://mac.tailx.ts.net:8443`; `top.location`. All fail. |
| P7 | A device paired directly with vps: the mac app gets no ticket (like `B7-direct`, `phase-b.mjs:443`). |
| P8 | With a preview socket open, change mac's node ID in vps's fake Tailscale status: the socket closes within the recheck plus the snapshot delay, new requests fail, and the devices are revoked (like `B7-node`, `phase-b.mjs:471`). A changed tag set refuses without revoking. |
| P9 | `pterm revoke` of the device on vps while the preview is open: the socket closes within 5 s. |
| P10 | The real CSP of the mac app page (Phase B keeps it, `README.md` "Limits"): the frame loads for confirmed vps; after **Remove computer** and a reload it is blocked. |
| P11 | With `vps:8443` not routed: **Open ↗** shows the "does not share dev-server previews" text. |
| P12 | Home preview still works through the same ticket path. |

Harness limit to record: development gateways use `pterm_dev_session` and `pterm_dev_preview` without `Secure` and without the `__Host-` prefix (`scripts/e2e-multicomputer/README.md`, "Limits"), and the front stands in for Tailscale Serve.

### Real-phone checks (owner; cannot be inferred from the harness)

Installed Home Screen app on iPhone, and Android, two real computers in one tailnet, both with Serve 8443, over IPv4:

1. The frame request to the peer's `:8443` carries the peer's `__Host-pterm_session` cookie (the page loads; not "Open the preview from your paired phone").
2. The preview cookie set by the selection inside the frame is stored and sent (`GET /` shows the dev site, not "Open the preview from the Termivoa app").
3. The move to `/` is accepted, which means WebKit sent `Sec-Fetch-Site: same-origin` for it.
4. Vite HMR works over the frame's WebSocket.
5. After 8 days without opening that peer's preview, it still opens (the WebKit 7-day cap, `docs/multi-computer.md:17`, `:90-92`, applies to the peer's cookie as before; the preview cookie is a session cookie).
6. A peer without Serve 8443 shows the text of section 6.
7. `pterm revoke --via <home>` on the peer while a preview is open: it stops within 5 s.
8. A link in the dev page with `target="_top"` or `target="_blank"` does nothing.
9. The home-only preview checks of `docs/owner-verification.md:190-212` again, because home uses the new path too.

Gate 0 for multi-computer (`docs/pending-work.md:31`) comes first: if the peer cookie fails in the installed app, this feature has no base.

## 12. Reviews

Not run yet. Rule 24 needs at least a security review and a feasibility review with alternatives, and a record here of how each finding is resolved, before the owner decides.

## 13. Open risks and things not verified

1. **Browser facts are from specifications, not from a test.** Especially: `Sec-Fetch-Site: same-origin` on the page-driven move to `/` inside a sandboxed frame in WebKit, and on history navigation or `location.reload()` inside the frame. If the move is not `same-origin` in some engine, L1 as written breaks the preview there. The fallback (accept `same-site` for a `navigate` + `iframe` `GET`, and add `frame-ancestors`) is weaker: another tailnet page could again send blind `GET` navigations to a selected dev server. It would need a new owner decision.
2. **Cookies in the frame on installed iOS and Android apps** are not verified, also not for home today (`docs/threat-model.md:43`).
3. **The ticket is in a URL.** It is single use, 30 s, and useless without the cookie. Nothing may log the query string; this design did not audit every log line of the gateway, and Tailscale Serve's own logging was not checked.
4. **L1 can break a dev site that today works**, if it depends on requests the browser does not mark `same-origin` (for example a page that sets `Referrer-Policy: no-referrer` and posts a form sends `Origin: null`; that case is refused today too, `preview.go:211-215`).
5. **The probe says "something answers on 8443", not "Termivoa answers".** If another service uses the peer's 8443, the frame shows that service's refusal or page.
6. **Phishing in the frame** has no technical limit beyond the bar label.
7. **Cookie bloat** from a peer dev site has no fix in this design; recovery is clearing the app's data (`docs/multi-computer.md:47`).
8. **Android opens in-scope links in the installed app.** A link to `/c/<host>/sessions/<id>/preview?port=<n>` opens the frame without a tap in the app. It needs a real session ID of a confirmed peer and gives nothing beyond what that peer shows after a tap. The home route has the same behavior today.

Found while reading the code, **not part of this task** (rule 21; to be filed as issues):

1. `previewPage` sends `frame-ancestors 'self'` (`internal/gateway/preview.go:456`). `'self'` there is the preview origin, and the frame's parent is the app origin (another port), so the listener's own text pages ("This phone is no longer paired…", "No dev server…") are probably blocked inside the app's frame. `docs/owner-verification.md:199-203` expects to read such text in the frame. Not checked in a browser.
2. The binding gap of section 1 exists on `main` today: the preview listener accepts a credential bound to another app origin, and any Origin-less request. `docs/preview.md:99-103` says a cross-site page gets no cookie, which is true, but a same-tailnet page does. This design closes it; until then it is open.
3. `GET /api/v1/preview` answers an origin when Serve 8443 is not set up, so the chip shows and the frame cannot load (`internal/gateway/preview.go:117-130`). Section 6 covers it for peers and home.

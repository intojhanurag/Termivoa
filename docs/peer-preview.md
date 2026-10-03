# Preview for sessions on peers: design

Status: **proposed 2026-10-03, version 2 — DO NOT MERGE without owner review** (AGENTS.md rule 24; ADR [0006](decisions/0006-peer-preview.md)). Design only: no code exists. Version 2 answers two adversarial reviews (security: "sound with changes"; feasibility: "build with changes"); section 12 lists every finding and its resolution.

Goal (roadmap item 3, `docs/pending-work.md:37`): a session on a peer computer (for example `vps`) shows the **Preview ready** chip, and the home app shows that peer's dev server in the frame. Read [preview.md](preview.md) and [multi-computer.md](multi-computer.md) first; this document only describes what changes.

Names used below: **home** `https://mac.tailx.ts.net` serves the app. **Peer** `https://vps.tailx.ts.net`. A computer's **preview origin** is its host on port 8443 (`internal/gateway/preview.go:28`).

## 1. What happens today (from the code)

| Fact | Evidence |
| --- | --- |
| The app offers a preview for home sessions only, and only when home's own previews are on (`previewAt`). | `web/src/screens/App.tsx:854`, `:870`, `:117-118`, `:875` |
| Peer session records already carry `previews`; the app drops them. | `protocol/v1/envelope.schema.json:165-180`, `web/src/screens/Live.tsx:716` |
| Home's CSP frames only its own preview origin. Peers get `connect-src` only, per device, on `index.html`. | `internal/gateway/handler.go:199-201`, `:484-515` |
| The preview listener accepts a request when the `Origin` header is absent, or equal to the preview origin. Nothing else is checked before the cookie. | `internal/gateway/preview.go:211-215`, `:221-225` |
| The listener's broker call carries no origin. | `internal/gateway/preview.go:241`, `:277`, `:302`; `internal/gatewayclient/client.go:339-346`, `:627` |
| The broker treats a missing origin as its own origin and admits the device. | `internal/broker/gateway_rpc.go:301` → `:108-114`; `internal/broker/computers.go:40-43`, `:75-78` |
| So **per-device origin binding is not applied on the preview listener**: a credential bound to home's app (`devices.app_origin`) is accepted there, from any page that can make the browser send the cookie. | The three rows above. Filed as the issue "Preview listener skips per-device origin binding". |
| The selection is a `303` to `/`. | `internal/gateway/preview.go:315` |
| Open preview sockets and streams are checked again every 5 s. | `internal/gateway/preview.go:41`, `:263`, `:268-285` |
| The listener's own text pages carry `frame-ancestors 'self'`. `'self'` is the `:8443` origin, and the frame's parent is the app origin, so these pages are blank inside the app's frame. | `internal/gateway/preview.go:456`. Filed as the issue "Preview listener error pages are blank inside the app frame". |
| A main origin refuses its **own** preview origin as `Origin` (403, before routing). Other origins on `/api` need an exact own or bound `Origin`, or no `Origin` plus `Sec-Fetch-Site: same-origin`; `no-cors` is refused. | `internal/gateway/handler.go:214-219`, `:258-273`, `:279-282`; `internal/gateway/preview.go:134-149` |
| The gateway keeps the set of bound app origins from the broker probe. | `internal/gateway/handler.go:40-44`, `:288-303`, `:356-375` |
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
| `Referer` | absent today (`referrerPolicy="no-referrer"`, `web/src/screens/Preview.tsx:73`). With `referrerPolicy="origin"` on the frame: `https://mac.tailx.ts.net/` | It is the URL (cut to the origin) of the document that started the navigation. A page cannot send another origin's `Referer`. A redirect keeps the first one. |

Conclusion: with today's frame, **no header names home's app**, so `devices.app_origin` cannot be compared with anything. The only header that can name the app on a `GET` navigation is `Referer`.

After the selection, requests made by the framed dev page itself are different: `fetch`, scripts, images and same-origin navigations carry `Sec-Fetch-Site: same-origin`; form posts and WebSocket handshakes carry `Origin: https://vps.tailx.ts.net:8443`. A `303` does **not** give that: after a redirect the request still counts from the page that started it, so `GET /` would still say `same-site`. That is why the selection must answer with a page, not a redirect (section 4).

Not verified in a real browser: every row above is from the Fetch, Fetch Metadata, Referrer Policy and cookie specifications. The spike in section 10 checks them first.

## 3. Options for authenticating the frame

| | A. Fetch-metadata and `frame-ancestors` only | **B. `Referer` as origin proof (recommended)** | C. Preview ticket (alternative) |
| --- | --- | --- | --- |
| Rule on the selection | cookie + `Sec-Fetch-Site: same-site` + `Sec-Fetch-Dest: iframe` | cookie + exactly one `Referer` whose origin is this computer's own origin or the credential's bound app origin | cookie + a single-use ticket that the app fetched through the authenticated CORS API |
| What stops another same-tailnet page from selecting | Only that it does not know the session ID | The browser never lets a page send another origin's `Referer`. A dev site on `:8443` sends its own origin, which is never a bound origin (a bound origin has the default port, `internal/protocol/computers.go:26`). | Only the bound app origin can get a ticket |
| Broker check | cookie only | the same `admitDevice` as every API call (`internal/broker/computers.go:75-83`), with the `Referer` origin as the forwarded origin | the same `admitDevice`, on the ticket call |
| Works when a header is removed | Yes | **No.** `Referer` is optional. If a browser setting or extension removes it, the preview fails (closed). | Yes |
| New parts | one response header | one frame attribute, one `Referer` parse in `selectPort`. No new route, RPC or state. The reviews estimate about 20 lines. | one HTTP route, one gateway RPC, one optional RPC field, an in-memory ticket table. The reviews estimate 300 to 400 lines. |
| Secret in a URL | no | no | yes: 30 s, single use, useless without the cookie |

**Recommendation: B.** Both reviews found it not weaker than the ticket against the attackers of this design (another tailnet page, a dev site on any `:8443`), and it is much less code and state. Version 1 of this document recommended C; that changed in review (S1, F1).

Honest costs of B:

- **It fails closed when `Referer` is removed.** The user then sees the listener's refusal text, not a preview.
- **`Referer` becomes a second origin proof** next to `Origin`. Every later reviewer of the preview listener must know that.
- **A script-free HTML injection on home's origin could start a selection.** Markup injected into the app's page (for example an `<iframe>`) sends home's `Referer`. With the ticket, the attacker would need script. The app renders terminal text as text and has a strict CSP, so this needs an app bug first; it also needs a real session ID, and it shows only what a tap on **Open ↗** would show.

**C stays documented as the alternative** (section 4, "Alternative: preview ticket") for the owner to choose. **A is rejected:** `same-site` is not "same owner" (`docs/multi-computer.md:62-64`).

Also rejected: a preview-only bearer cookie that replaces the device cookie (a new credential type; against "no new secret type", `docs/multi-computer.md:52`), and a relay of the peer's preview through home (home would hold standing peer credentials; `docs/multi-computer.md:23-31`, option B).

Both B and C need L1, the `200` selection page, the trust check and the CSP change of section 4.

## 4. Design

### Request flow (variant B)

```text
phone app  https://mac.tailx.ts.net  (home)
  1. iframe referrerpolicy="origin"
       src = https://vps.tailx.ts.net:8443/__termivoa/select?session=<id>&port=<n>
     vps preview listener: vps cookie; Referer origin = https://mac.tailx.ts.net
       is in the bound set → forwarded to the broker as the request origin
     vps broker: admitDevice (origin bound to this credential, node, tags),
       session runs, port owned
     → 200 small page, sets the preview cookie, moves the frame to /
  2. GET /  (and every later request of the dev page)
       same-origin by Sec-Fetch-Site or Origin; cookie + preview cookie;
       broker re-check (credential, trust of the bound node, port) on every
       request and every 5 s for open sockets
```

A home session uses the same steps against home; the `Referer` origin is then home's own origin.

### Rules that change on every computer (each computer runs the same build)

**L1. Preview listener: who may send a request** (`internal/gateway/preview.go`, replaces `:208-215`). A request other than the selection passes only with:

- exactly one `Origin` header equal to the preview origin itself (form posts, WebSocket handshakes), or
- no `Origin` header and exactly one `Sec-Fetch-Site: same-origin` (reuse `sameOriginFetch`, `internal/gateway/handler.go:279-282`).

Everything else is `403` before the cookie is read and before the dev server is reached. This is the `/api` rule (`handler.go:264-273`) on the preview listener. It refuses a script tag, image, `no-cors` fetch, frame or WebSocket from the app, from another tailnet machine and from another computer's dev site. There is **no fallback** that accepts `same-site` navigations: if an engine does not send `same-origin` for the move to `/`, this design goes back to review (S3).

**L2. Preview listener: the selection** (`selectPort`, `preview.go:289-316`). `GET /__termivoa/select?session=<uuid>&port=<n>` with the device cookie and exactly one `Referer` header. Its origin (scheme, host, port; lower case) decides:

- this computer's own origin (`originAllowedValue`, `handler.go:319-327`) → the broker call carries no forwarded origin;
- an origin in the gateway's bound set (`handler.go:296`) → the broker call carries it through `gatewayapi.WithForwardedOrigin` (`internal/gatewayapi/gatewayapi.go:93`), so the broker runs `admitDevice` against **this credential's** binding;
- anything else, a missing or a doubled `Referer` → `403`.

The answer is `200 text/html` with `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'; frame-ancestors <the Referer origin>`, the preview cookie as today (`preview.go:309-312`), and a body that only moves the frame to `/` (`<meta http-equiv="refresh" content="0;url=/">`). The move is started by a document on the preview origin, so `GET /` is `same-origin` and passes L1. This page is **not** built with `previewPage`, whose `frame-ancestors 'self'` blanks it in the app's frame (`preview.go:456`). The dev server never sees home's `Referer`: the selection is not proxied.

**B1. Broker: preview check** (`gateway.preview.check`, `internal/broker/gateway_rpc.go:296-312`). No wire change; the envelope's existing `origin` field is used.

- With a forwarded origin (the selection from another computer's app): `admitDevice` as every other call (`gateway_rpc.go:108-114`).
- With no origin (the selection from the own app, every later request, the 5 s recheck): authenticate; **if the device is bound to an app origin (`principal.App.Bound()`), also require `appTrust(principal.App)`** (`internal/broker/computers.go:59-72`). A device that paired with this computer directly is checked as today.
- Then the port ownership check as today (`internal/broker/devpreview.go:119-137`).

### Rules that change on home only

**H1. CSP of the app document** (`deviceConnectSources`, `internal/gateway/handler.go:484-515`). For each **confirmed** computer of the cookie's device (`list.Computers`, never `list.PendingOrigins`), add `<origin>:8443` to `frame-src`. `frame-src` is created when home's own preview listener is off. Entries whose node changed are already left out by the broker (`internal/broker/computers.go:216-241`). At most 8 (`internal/protocol/computers.go:10`). Like `connect-src` today, this is on `index.html` only and only with a usable cookie. The known limit for the cached offline shell (`docs/threat-model.md:276-279`) now also covers `frame-src`.

`connect-src` gains `https://<origin>:8443` only together with the reachability probe (section 6), which may come in a later pull request. **Never `wss://<peer>:8443`**: the app opens no socket to a preview origin.

### App rules (requirements, not options)

- **W1.** The frame gets `referrerPolicy="origin"`. It keeps `sandbox="allow-scripts allow-forms allow-same-origin"` (`web/src/screens/Preview.tsx:72`): no top navigation, popups, modals or downloads; no `allow` attribute (no camera, microphone, location) and no `allowfullscreen`.
- **W2.** Over a frame of a peer, the app's bar **always** shows the computer's label (from the phone's own list, `web/src/computers.ts:13-14`) and an **untrusted page** mark. It does not depend on how many computers the phone has.
- **W3.** When that computer answers `401` or `403` to the app, the app **removes the frame**; a banner over a live frame is not enough. The app cannot see the frame's own responses (another origin), so the signal is the app's own poll of that computer, which runs while the preview is open (`web/src/screens/App.tsx:492`).
- **W4.** The app never uses an origin string that a peer sent. A peer's preview origin is always `<computer.origin>:8443`, and home computes its `frame-src` the same way.
- **W5.** A peer preview does not depend on home's previews being on: `App.tsx:854` and `:870` test home's `previewAt` today and must test the session's own computer.

### What does not change

Proxy rules, cookie stripping, port ownership and the 5 s recheck (`docs/preview.md:105-123`); push, pairing, `/api` admission and CORS; the CLI; the store.

### Alternative: preview ticket (variant C, not recommended)

If the owner chooses C, L1, B1's trust check, H1 and W1 to W5 stay (the frame keeps `no-referrer`), and L2 changes:

- New route `POST /api/v1/sessions/{id}/preview`, body `{port}`, answer `{ticket}`; a mutation (`internal/gateway/api.go:490-525`), so it needs the exact own or bound `Origin` and `X-Pterm-Request`.
- New RPC `gateway.preview.ticket`; `gateway.preview.check` gains an optional `ticket`. The broker keeps the SHA-256 of a 256-bit ticket with device, session, port and a 30 s expiry, in memory; at most 4 per device and 64 in total.
- The selection needs the cookie and `ticket`. The broker **looks up and deletes the ticket atomically under one lock**, pass or fail.
- The app gets **one ticket per frame mount** (also on reload), never one per render.
- A test proves that the selection page moves to `/` as a **replace** navigation, so that Back inside the frame never returns to a used ticket.

## 5. What each side must refuse

| Request | Who answers | Result | Rule |
| --- | --- | --- | --- |
| Dev site on peer A (`https://a:8443`) calls home's or peer B's `/api` with `fetch` or a WebSocket | main origin | `403`: the `Origin` is not own and not bound | existing, `handler.go:264-273` |
| Same dev site sends a `no-cors` `GET` or navigates a nested frame to `/api/...` | main origin | `403`: `no-cors`, or no `Origin` without `same-origin` | existing, `handler.go:258-273` |
| A computer's own dev site calls that computer's main origin | main origin | `403` before routing | existing, `handler.go:214-219` |
| Dev site on peer A loads a script or image from, frames, or opens a WebSocket to `https://b:8443` or `https://mac:8443` | preview listener | `403` before the dev server | **new, L1** |
| A page on another tailnet machine, or a dev site on any `:8443`, frames a selection URL | preview listener | `403`: its `Referer` origin is not own and not bound | **new, L2** |
| A page on an origin that is bound for **another** device frames a selection URL with this device's cookie | broker | refused: `principal.App.Origin` differs | existing `computers.go:79-81`, reached through L2 |
| Home's app selects on a peer with a credential that paired with the peer directly (`app_origin` NULL) | peer broker | refused (`ErrOriginRefused`) | existing, `computers.go:79-81` |
| A bound device's preview request after home's node ID changed, its tag set changed, or the status is older than the grace period | peer broker | refused; a node change also revokes | **new, B1** via `appTrust` |

Does a dev site on peer A get new power over home or peer B? **Over the APIs: no.** Over the other preview origins: without L1 it would (blind `GET`s and script loads against the selected dev server, with the user's cookies), because a framed peer dev site is the first foreign same-site page that runs inside the installed app's cookie jar. L1 removes that. What stays is in section 7.

## 6. A peer without a preview origin, and older peers

| Case | How the app knows | What the user sees |
| --- | --- | --- |
| Peer below protocol revision 2 | `settings.protocolRevision` of that computer (`web/src/screens/allSessions.ts:28`, `web/src/api/http.ts:91-97`) | No chip, as today. The app never frames a peer whose listener lacks L1 and L2. |
| Peer never ran `tailscale serve --https=8443`, or an ACL blocks 8443 | First pull request: it does not know; the frame stays empty. With the probe: on **Open ↗** one `fetch('<peer>:8443/__termivoa/select', {mode: 'no-cors', credentials: 'omit', signal: AbortSignal.timeout(5000)})`. Any HTTP answer means the port is served; a failure means the phone could not reach it. | With the probe, neutral words, because a stale cached CSP or a timeout looks the same as a missing Serve: "Can't open the preview on vps. If vps does not share dev-server previews yet, run `pterm setup` on vps and answer yes to the preview question." |
| The session ended, the port closed, the phone is no longer paired there | The listener answers with its text page inside the frame (needs track T, section 10); the app's poll shows the session and computer state | The text in the frame; for `401` the frame is removed and "vps · pair again" shows (`allSessions.ts:39`) |
| Peer unreachable | existing | "vps · can't reach" |

The probe is on the phone on purpose: it tests the real path, and it needs no Tailscale call in the broker. It also covers home, where the chip shows today even when Serve 8443 was declined. Rejected: letting the broker run `tailscale serve status --json` (more host code, and it cannot see ACLs).

Each computer's Serve 8443 stays that computer's own consent in `pterm setup` (`cmd/pterm/lifecycle.go:812-847`, rule 12). Nothing in this design changes Tailscale configuration, and no computer configures another.

**Protocol revision.** `protocol/v1/revision.json` goes from 1 to 2; `minRevision` stays 1. Variant B changes no schema, but the listener's behavior is a change a peer's app can observe (`protocol/v1/http-api.md:127-128`), and the app needs the number to know that a peer's listener has L1 and L2. Home and the app are one build. No path for the old selection stays (rule 26).

## 7. Honest trust statement

**When you open a preview of a session on a peer, that peer puts a page of its choice inside your app.** It does not need a real dev server: a compromised peer can report any port in `previews` and answer on its `:8443` with anything.

**This withdraws a guarantee.** `docs/multi-computer.md:115` says that a peer cannot show phishing text in the home app. With this feature a peer can: inside the frame, after the user opens that peer's preview.

What that page **cannot** do:

- Read or change the app: it is another origin, and the app keeps `frame-ancestors 'none'` and its CSP.
- Navigate the app away, open popups, show dialogs, start downloads or go full screen (frame sandbox, W1).
- Call the API of home or of any peer, or load from another computer's preview (section 5).
- Appear without a tap: the app frames a peer only after **Open ↗** on a session of that peer (or a reload of that same screen).

What it **can** do, stated plainly. "It" is the peer, or any script in a peer's dev site (a project dependency, for example):

- **Show a false page.** A fake "pair again" screen, a fake login. The only limit is the app's bar with the computer label and the **untrusted page** mark (W2).
- **Un-pair the phone from every computer.** About 200 cookies with `Domain=<tailnet>.ts.net` make the browser evict older cookies of the site, and every computer's `__Host-pterm_session` is one of them. The user must **pair again** with every computer. Larger cookies also push every request past the 16 KiB header limit until the app's data is cleared. The exact number depends on the browser and was not tested. Before this feature only a peer's HTTP responses could set such cookies (`docs/multi-computer.md:45`); a home dev site already could (`docs/preview.md:132-134`).
- **Send cookies to other dev servers.** A `Domain=<tailnet>.ts.net` cookie goes to every computer's `:8443`, and the proxy forwards cookies that are not Termivoa's (`internal/gateway/preview.go:404-416`). A dev server on another computer can receive a cookie it did not set.
- **Send blind requests from the phone** to other machines in the tailnet (`no-cors`), also machines that the peer itself cannot reach under your ACLs. It cannot read the answers. Any web page that the phone opens can do the same.
- **Keep state on its own origin** (storage, a service worker for `https://vps:8443`; see section 13, limit 1).
- **Track that you opened it**, and when.

What stays as before: a compromised home compromises every peer (`docs/multi-computer.md:37-39`). A compromised peer still controls only itself, plus the frame above.

Known limits kept from `docs/preview.md:125-141`: all previews of one computer share one origin, and the preview cookie is not signed; script on that origin can replace it. No isolation between two dev sites of one computer is claimed.

## 8. Tailscale rules and revocation timing for preview streams

| Rule from multi-computer | Does the preview on a peer follow it? |
| --- | --- |
| Node-ID pinning | Yes. The selection goes through `admitDevice`; every later check of a bound device goes through `appTrust` (B1). A changed node ID answers `ErrRevoked`, and the sweep revokes the devices as today (`internal/broker/computers.go:270-293`). |
| Tagged nodes | Yes, no new rule. `appTrust` compares the pinned tag set (`internal/tailnet/status.go:217-228`): a changed set is refused, not revoked. |
| 60 s status cache, 10-minute grace | Yes. `appTrust` only reads the snapshot (`internal/tailnet/monitor.go:78-94`). Devices that paired with the peer directly are not affected. |
| Already-open WebSockets | Not at once. Live streams close on a new snapshot (`CloseUntrusted`, `internal/auth/service.go:545-551`). Preview sockets are not broker streams; the gateway asks again every 5 s (`preview.go:268-285`). |

Timing, stated exactly:

| Event | Effect on the preview |
| --- | --- |
| Device revoked or expired (`pterm revoke`, logout) | `401` on the next request; open sockets close within 5 s |
| Home's node ID or tag set changed | Seen with the next status snapshot: up to about 65 s (60 s cache plus the 5 s recheck) |
| Tailscale status cannot be read | The last good snapshot is used for up to 10 minutes; after that, bound devices are refused (not revoked) |

## 9. Protocol changes (additive, rule 6)

Variant B:

| File | Change |
| --- | --- |
| `protocol/v1/revision.json` | `revision` 2 |
| `protocol/v1/http-api.md` | "Preview listener" (`:289-300`): the selection needs a `Referer` of the own or a bound app origin and answers `200`; the L1 rule; revision 2 in "Protocol revision" |
| `protocol/v1/authentication-rpc.md` | `gateway.preview.check` (`:57`, `:118-121`): the forwarded origin, and the trust check for bound devices |
| Generated Go and TypeScript | regenerate (the revision constants); the drift check must pass |

No schema, message, error code or store change.

Variant C adds: `previewTicketRequest`/`previewTicketResponse` in `http-api.schema.json`; `gateway.preview.ticket` and its result, and an optional `ticket` in `previewCheckPayload` (`gateway-rpc.schema.json:188-199`); the type names in `types.schema.json`; the binding in `internal/protocol/auth_rpc.go:61`, `:294-307`.

## 10. Build order and implementation plan (not started)

### Order

| Step | What | Gate |
| --- | --- | --- |
| **(a) Spike**, about one hour, throwaway | Two origins of one site, in Chrome, Playwright WebKit and one real phone (installed app). Record: (1) the Strict host cookie arrives inside the frame; (2) `Sec-Fetch-Site` on the meta-refresh move to `/`, on `location.reload()` and on Back inside the frame; (3) `Referer` with `referrerpolicy="origin"` on the frame; (4) the meta refresh is a replace navigation; (5) a `<video>` load inside the frame sends `Sec-Fetch-Site`. | If (2) is not `same-origin`, or (3) is absent, in any engine: **stop; new design review.** No fallback. |
| **(b) Fix `main` for home alone**, its own pull request | Issue "Preview listener skips per-device origin binding": L1, the `200` selection page, B1's trust check. Recommended in the same pull request: L2 with `Referer` and W1, because without L2 the selection stays open to any same-tailnet page that knows a session ID, and home needs L2 anyway. | Owner review (rule 24: it changes authorization). |
| **(T) Text pages**, its own pull request, before or with (b) | Issue "Preview listener error pages are blank inside the app frame": `previewPage` (`preview.go:451-460`) must be visible in the app's frame. The texts of section 6 depend on it. | — |
| **(c) Peer preview** | H1, W2 to W5, revision 2, harness, docs. | Owner review. Gate 0 for multi-computer (`docs/pending-work.md:31`) first. |
| **(d) Probe**, may be a later pull request | The probe of section 6 and its `connect-src` entry. | — |

### Files (variant B)

| Step | File | Change |
| --- | --- | --- |
| b | `internal/gateway/preview.go` | L1 in `ServeHTTP` (`:208-215`); L2 in `selectPort` (`:289-316`): `Referer`, forwarded origin, the `200` page with its own headers |
| b | `internal/broker/gateway_rpc.go` (`:296-312`), `internal/broker/computers.go` | B1: the preview check runs `appTrust` when there is no origin and the device is bound |
| b | `web/src/screens/Preview.tsx` (`:73`) | W1: `referrerPolicy="origin"` |
| b | `internal/gateway/preview_test.go`, `internal/gateway/fake_broker_test.go`, `internal/broker/computers_test.go` | section 11 |
| T | `internal/gateway/preview.go` (`:451-460`) | the text pages' `frame-ancestors` |
| c | `protocol/v1/revision.json`, `protocol/v1/*.md`, generated files | section 9 |
| c | `internal/gateway/handler.go` (`:484-515`) | H1 |
| c | `web/src/computers.ts` | `previewOriginOf(computer)`: home uses the origin from `GET /api/v1/preview` (so the development profile keeps working); a peer uses `<origin>:8443` |
| c | `web/src/screens/App.tsx` | W5 (`:854`, `:870`); offer `onPreview` for a peer at revision 2 or higher; the route `/c/<host>/sessions/<id>/preview?port=<n>` (`:114-118`, `:157-164`); pass that computer's sessions to `Preview` (`:875`); W3 |
| c | `web/src/screens/Preview.tsx` | W2: label and **untrusted page** mark; W3 |
| c | `scripts/e2e-multicomputer/main.go` (`:152-157` accepts only `CONNECT host:443`), `lib.mjs` (`:158` starts stacks with `--preview-listen off`), `phase-b.mjs`, `webkit.mjs`, `README.md` | route `<host>:8443` to each stack's preview listener with the same Host and Origin rewrite; start the listeners; the checks of section 11 |
| d | `web/src/screens/Preview.tsx`, `internal/gateway/handler.go` | the probe; `connect-src` gains `https://<origin>:8443` (never `wss:`) |
| c (rules 22 and 25; not in this design commit) | `docs/preview.md`, `docs/multi-computer.md` (line 115, "No previews from peers"), `docs/threat-model.md`, `docs/architecture.md`, `docs/owner-verification.md` (`:190-212`), `docs/pending-work.md`, `docs/progress.md`, `CHANGELOG.md`, `README.md`, site docs (computers guide, preview guide, security, troubleshooting, release notes) | |

No CLI change. `pterm setup` and `pterm doctor` already ask about and report Serve 8443 on each computer.

## 11. Test plan

### Unit and loopback tests

| Area | Must prove |
| --- | --- |
| `internal/gateway/preview_test.go` | **L1** table (like `multicomputer_test.go:270-280`): own `Origin` pass; no `Origin` + `same-origin` pass; no `Origin` + `same-site`, `cross-site`, `none`, missing or doubled header → `403`, and the dev server sees no request; a WebSocket without `Origin` → `403`. **L2:** `Referer` own → pass with no forwarded origin; bound → pass with that forwarded origin; missing, doubled, unbound, near-miss, the preview origin itself, `https://<bound>:8443` → `403` and no `Set-Cookie`. The selection answers `200` with the refresh body and without `previewPage`'s headers. Revocation still closes sockets within the recheck. |
| `internal/broker/computers_test.go` | Preview check, no origin, bound device: trusted → pass; node changed → `ErrRevoked`; tags changed, other owner, name gone → refused; no snapshot and grace expired → refused, not revoked. A directly paired device is not affected by the snapshot. With a forwarded origin that is not the credential's → refused. |
| `internal/gateway/multicomputer_test.go` | H1: `frame-src` holds exactly the own preview origin and `<origin>:8443` of confirmed computers; pending and other devices' computers are absent; no cookie → unchanged; home previews off → `frame-src` lists only peers; no `wss:` entry for a preview origin. |
| `web` | No chip for a peer at revision 1; a peer preview with home's previews off (W5); the peer preview origin is computed, never read from a peer's answer; route parse and build; the bar shows the label and the mark for every peer frame (W2); a `401` poll removes the frame (W3). |
| Variant C only | Ticket: works once; atomic consume under concurrent use; **expiry (with a fake clock, here and not in the harness)**; wrong device, session or port; bounds; one ticket per mount; replace navigation. |

### What `scripts/e2e-multicomputer` must prove (Chrome and WebKit)

Setup: mac and vps as today, vps paired through mac. A small HTTP server with a WebSocket runs **inside a vps session** (so the port belongs to the session's process tree); it uses **relative redirects only**, because a development gateway rewrites absolute ones to its loopback address (`preview.go:422-437`, `:172-178`). The front accepts `CONNECT host:8443` and routes `vps.tailx.ts.net:8443` and `mac.tailx.ts.net:8443` to the preview listeners.

Harness fact: in the development profile, home's own `frame-src` and `GET /api/v1/preview` give the loopback address (`preview.go:117-130`), not `https://mac.tailx.ts.net:8443`. So **home's own preview through the app cannot be checked in this harness**; it stays in the loopback Chromium check and on the real phone. The peer's `frame-src` entry comes from the computer list and is the real `https://vps.tailx.ts.net:8443`.

| ID | Check |
| --- | --- |
| P1 | In the mac app, the vps session shows **Preview ready**; **Open ↗** shows the vps page in the frame. The front log shows the selection with no `Origin`, `Sec-Fetch-Site: same-site` and `Referer: https://mac.tailx.ts.net/`; then `GET /` with `Sec-Fetch-Site: same-origin`. |
| P2 | The frame's own WebSocket connects (`101`); a change reaches the page. Reload and Back inside the frame still work. |
| P3 | A page on `evil.tailx.ts.net` frames the same selection URL, with the vps cookie present: `403`, no preview cookie. |
| P4 | After a correct selection, the evil page tries a script tag, an image, a `no-cors` fetch, a frame and a WebSocket to `https://vps.tailx.ts.net:8443`: all `403`, and the dev server's log has no such request. |
| P5 | The framed vps page tries: `fetch` and WebSocket to mac's and vps's `/api`; a script from and a nested frame of `https://mac.tailx.ts.net:8443` (the front's route; answers `403` by L1); a nested frame of its own selection URL for another session (`403`: `Referer` is the `:8443` origin); `top.location`. All fail. |
| P6 | A device paired directly with vps: the mac app's frame is refused (like `B7-direct`, `phase-b.mjs:443`). |
| P7 | With a preview socket open, change mac's node ID in vps's fake Tailscale status: the socket closes after the next snapshot, new requests fail, and the devices are revoked (like `B7-node`, `phase-b.mjs:471`). A changed tag set refuses without revoking. |
| P8 | `pterm revoke` of the device on vps while the preview is open: the socket closes within 5 s, and the app removes the frame. |
| P9 | The real CSP of the mac app page (Phase B keeps it, `README.md` "Limits"): the frame loads for confirmed vps; after **Remove computer** and a reload it is blocked. |
| P10 | The bar over the vps frame shows `vps` and **untrusted page**. |

Harness limits to record: development gateways use `pterm_dev_session` and `pterm_dev_preview` without `Secure` and without the `__Host-` prefix (`scripts/e2e-multicomputer/README.md`, "Limits"); the front stands in for Tailscale Serve.

### Must stay on a real phone (owner)

Installed Home Screen app on iPhone, and Android, two real computers in one tailnet, both with Serve 8443, over IPv4:

1. The frame request to the peer's `:8443` carries the peer's `__Host-pterm_session` and the `Referer` of home.
2. The `__Host-pterm_preview` cookie set inside the frame is stored and sent.
3. The move to `/` is accepted (WebKit sent `same-origin`); reload and Back inside the frame work.
4. Vite HMR works over the frame's WebSocket.
5. **A page with a `<video>` and an `<audio>` element plays** (iOS media loads may lack `Sec-Fetch-Site`, and L1 would refuse them).
6. Home's own preview (`docs/owner-verification.md:190-212`) again, because home uses the new rules and the harness cannot check it.
7. A peer without Serve 8443: what the frame shows; with the probe, its text.
8. `pterm revoke --via <home>` on the peer while a preview is open: it stops within 5 s and the frame goes away.
9. A link in the dev page with `target="_top"` or `target="_blank"` does nothing.
10. After 8 days without opening that peer's preview, it still opens (the WebKit 7-day cap, `docs/multi-computer.md:17`, `:90-92`).
11. An app that was open during the update (old cached bundle) shows a blank frame until it reloads; after the reload it works.

## 12. Reviews: findings and resolutions

Two independent reviews ran on version 1. Security: "sound with changes". Feasibility: "build with changes". The full review texts are not in this repository; the findings below follow the coordinator's summary, and the severity column is this document's reading, not the reviewers'. Each point was checked against the code before it was accepted.

### Security review

| # | Finding | Severity | Resolution |
| --- | --- | --- | --- |
| S1 | `Referer` is not weaker than the ticket against these attackers: a page cannot forge it, redirects keep the first one, a dev site on `:8443` sends another origin, and the broker runs the same `admitDevice`. The ticket adds a route, an RPC and state for no gain. | high | Accepted. B is recommended (section 3); C stays as the alternative. Checked: the gateway already holds the bound set (`handler.go:296`) and forwards an origin with `WithForwardedOrigin`. |
| S2 | State the costs of `Referer`: fails closed when removed; a script-free HTML injection on home could start a selection; a second origin proof. | medium | Accepted; section 3 "Honest costs" and the ADR. |
| S3 | The "accept `same-site` navigations" fallback reopens blind navigations; remove it. | high | Accepted. L1 has no fallback; a failed spike means a new review (section 10). |
| S4 | The frame withdraws the guarantee of `docs/multi-computer.md:115` (no phishing text from a peer); a peer dev site can show a fake "pair again". | high | Accepted; section 7 and the ADR say so. |
| S5 | About 200 `Domain=<tailnet>.ts.net` cookies evict every computer's `__Host-pterm_session` (forced un-pair; recovery is "pair again"), and such cookies reach other dev servers. | medium | Accepted; section 7. The eviction number is browser-specific and not tested here. Cookie forwarding checked at `preview.go:404-416`. |
| S6 | The bar over a peer frame must always show the computer label and an "untrusted page" mark; the sandbox stays without fullscreen and `allow`; on `401`/`403` the app removes the frame. | high | Accepted as W1 to W3. One correction: the app cannot see the frame's own `401`/`403` (another origin), so W3 uses the app's own poll of that computer (`App.tsx:492`). |
| S7 | Delete the sentence that the ticket "makes it harder" to switch sessions: script on the same host can replace the unsigned preview cookie. | low | Accepted; deleted. Section 7 states that no isolation is claimed. |
| S8 | A dev-site service worker on `:8443` can re-send navigations as `same-origin` and serve cached content after revocation. | medium | Stated as limit 1 in section 13. Owner decision: accept, or refuse `Sec-Fetch-Dest: serviceworker`. |
| S9 | "Within about 5 s" is wrong for a node change and for the status grace. | medium | Accepted; section 8 gives the three timings. Checked: 60 s cache and 10-minute grace at `monitor.go:15-22`, `:78-94`. |
| S10 | The gap exists on `main` today for home alone; fix it first, as its own pull request. If the ticket is kept: atomic consume, one ticket per mount, replace navigation. | high | Accepted; step (b) in section 10 and the alternative in section 4. Addition from this document: (b) should include L2, or the selection stays open to a same-tailnet page that knows a session ID. |

### Feasibility review

| # | Finding | Severity | Resolution |
| --- | --- | --- | --- |
| F1 | `Referer` is about 20 lines; the ticket is 300 to 400 plus a route, an RPC and a table. | high | Accepted as the reviewers' estimate; not measured here. B recommended. |
| F2 | Browser facts are untested; run a one-hour spike (Chrome, Playwright WebKit, one real phone) before any build. | high | Accepted; step (a), with a stop rule. |
| F3 | Fix the `main` gap for home as its own pull request before the peer work. | high | Accepted; step (b). |
| F4 | The listener's text pages are blank in the app frame (`preview.go:456`); the selection page must not use `previewPage`. | high | Accepted. Track T in section 10; L2 builds its own page. Checked in the code; not seen in a browser by this author. |
| F5 | L1 answers `403` to requests without `Sec-Fetch-Site`: Safari before 16.4, possibly iOS media loads. | medium | Stated as limit 2. iOS 16.4 is already the minimum (`docs/threat-model.md:281-284`). Video and audio are on the real-phone list. |
| F6 | L1 refuses sub-resources of opaque-origin frames inside the dev site. | low | Stated as limit 3. |
| F7 | An old cached app bundle shows a blank frame until it updates. | low | Stated as limit 4; real-phone check 11. |
| F8 | `App.tsx:854` and `:870` tie every preview to home's `previewAt`. | medium | Accepted as W5. Checked at those lines. |
| F9 | Do not add `wss://<peer>:8443` to `connect-src`. | low | Accepted; H1 says never, and a test checks it. Version 1 did not add it but did not say so. |
| F10 | The probe needs `AbortSignal.timeout(5000)` and neutral failure text (a stale cached CSP looks like a network error); it may move to a later pull request. | medium | Accepted; section 6 and step (d). |
| F11 | In the development profile home's `frame-src` and `GET /api/v1/preview` give the loopback address, so harness checks that need `https://mac.tailx.ts.net:8443` cannot pass as written. | high | Accepted. The front accepts `CONNECT host:8443`; checks that only need the listener to answer on that name stay (P5); home's own preview through the app moves to the real-phone list. Not accepted: computing `<origin>:8443` for home in the app, because it breaks the loopback development profile. |
| F12 | The test dev server must use relative redirects only; expiry checks belong in unit tests; list what must stay on a real phone. | medium | Accepted; section 11. |

## 13. Known limits, open risks, things not verified

Known limits (accepted unless the owner decides otherwise):

1. **Service worker on `:8443`.** A dev site can register a service worker for its preview origin. It can answer a navigation that another page started, and its own requests are `same-origin`, so for that origin L1 does not stop framing by another page. It can also serve cached content after the phone was revoked. It cannot read anything new. Owner decision: accept this, or refuse `Sec-Fetch-Dest: serviceworker` on the listener, which breaks dev sites that use a service worker.
2. **Requests without `Sec-Fetch-Site` get `403`.** Safari before 16.4 (already below the minimum), and possibly iOS media loads (real-phone check 5).
3. **Opaque-origin frames inside a dev site** (a sandboxed frame without `allow-same-origin`, `srcdoc` with a sandbox): their sub-resources are not `same-origin` and get `403`.
4. **An old cached app bundle** sends no `Referer` and gets a blank frame until the app updates.
5. **A `Referer` removed by the browser** means no preview (variant B).
6. **A dev site that sets `Referrer-Policy: no-referrer` and posts a form** sends `Origin: null`; refused today too (`preview.go:211-215`).

Open risks:

1. **Browser facts are from specifications, not from a test**, until the spike runs. Without `same-origin` on the move to `/` in every engine there is no design.
2. **Cookies in the frame on installed iOS and Android apps** are not verified, also not for home today (`docs/threat-model.md:43`).
3. **Phishing in the frame** has no technical limit beyond the bar.
4. **Cookie eviction and bloat** from a peer dev site have no fix in this design.
5. **The probe says "something answers on 8443", not "Termivoa answers".**
6. **Android opens in-scope links in the installed app.** A link to `/c/<host>/sessions/<id>/preview?port=<n>` opens the frame without a tap in the app. It needs a real session ID of a confirmed peer. The home route has the same behavior today.
7. Variant C only: the ticket is in a URL for up to 30 s; the gateway's and Tailscale Serve's logging of query strings was not audited.

Found while reading the code, outside this task (rule 21):

1. Filed as #144: "Preview listener error pages are blank inside the app frame" (`preview.go:456`); track T.
2. Filed as #143: "Preview listener skips per-device origin binding" (section 1); step (b). `docs/preview.md:99-103` speaks only of cross-site pages.
3. Filed in #144: `GET /api/v1/preview` answers an origin when Serve 8443 is not set up, so the chip shows and the frame cannot load (`preview.go:117-130`); step (d) covers it.

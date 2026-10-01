# Multi-computer: architecture

Status: **proposed, version 2 after two adversarial reviews; owner review required** (ADR [0005](decisions/0005-multi-computer.md)). Nothing here is implemented. Section 7 lists every review finding and how it is resolved.

One phone uses several computers (for example a Mac, a Linux server and a VPS) from one Home Screen app: one Sessions list, one Wall, new sessions on any computer. This document fixes the architecture, the trust model and the build order before any code is written.

![Concept: one Sessions list across all computers, with a colour dot and label per computer, and a Computers screen to add and manage computers](images/multi-computer-concept.png)

## 1. Facts this design rests on

| Question | Finding | Evidence |
| --- | --- | --- |
| Is `ts.net` a public suffix? | Yes (PRIVATE section: `ts.net`, `*.c.ts.net`, submitted by Tailscale). The site of `mac.tailX.ts.net` is `tailX.ts.net`: two machines in **one** tailnet are cross-origin but **same-site**; machines in different tailnets are cross-site. | `publicsuffix/list` `public_suffix_list.dat` |
| What do `SameSite=Strict` and `__Host-` allow from origin A to B? | SameSite is decided by site, not origin, so B's Strict cookie **is** sent on same-tailnet requests from A's page (`fetch` with `credentials: 'include'`, WebSocket handshake). `__Host-` and HttpOnly mean A can never read, set or overwrite B's cookie. Inside one tailnet, SameSite therefore gives **no** isolation today; B is protected only by its exact Origin checks. | `internal/gateway/handler.go:226-244`, `api.go:401`, `live.go:74`, `live.go:107-110` |
| CORS today | B sends no `Access-Control-Allow-Origin`, so A cannot read B's responses; mutations need `X-Pterm-Request`, which forces a preflight that B answers with 405. | `handler.go:371` |
| iOS Home Screen app leaving its origin | Links outside the manifest scope open in Safari View Controller, with a different cookie jar and no push. A "switcher" between origins is not a usable app. | WWDC23 "What's new in web apps"; firt.dev iOS 14 |
| WebKit IP-based cookie cap | WebKit caps persistent cookies to 7 days when a same-site subresource's IP differs from the page's IP in the first 16 bits (IPv4) or 64 bits (IPv6). Tailscale IPv4 addresses (100.64.0.0/10) usually differ in the second octet, so peer cookies will be capped over IPv4. | WebKit `NetworkTaskCocoa.mm`, PR #5347, bug 246477 |
| Does it work in real engines? | **Verified by both reviewers:** in Chrome 154 (also with third-party-cookie phase-out) and Playwright WebKit 26.4, a credentialed CORS request from one same-site origin stored the other origin's `__Host-` `SameSite=Strict` HttpOnly cookie and sent it on later `fetch`, preflighted requests and the WebSocket handshake; a cross-site control origin never got it. Not yet verified: standalone iOS Home Screen mode, the IP cap, Android WebAPK. | Throwaway harnesses (two reviewers), 2026-10-01 |
| What can a home service worker do? | **Verified:** a push woke home's service worker with no page open (Chrome), and the worker made credentialed requests and a WebSocket to the peer with `Origin` set to home. | Security review experiment |

## 2. Options

| | A. Switcher | B. Relay through home | C. Bearer tokens on the phone | **D. Direct links (chosen)** |
| --- | --- | --- | --- | --- |
| One list and one Wall | No | Yes | Yes | **Yes** |
| Works as an iOS Home Screen app | No (other origin opens in Safari View Controller) | Yes | Yes | **Yes, if Gate 0 passes** |
| Phone identity on each computer | Per phone | **Lost**: every phone is "home"; breaks per-device leases and uploads (`broker/upload.go:216`, `gateway_rpc.go:168`) | Per phone | **Per phone, unchanged** |
| Revocation | Per computer | All relayed phones at once | Per computer; a stolen token works until revoked | **Per computer** |
| A compromised home computer | Nothing | Holds standing peer credentials: 24/7 shells on every peer | Its JavaScript can exfiltrate every token | **Also compromises every peer** (service worker woken by push; phishing a new pairing); narrowed by per-device binding, owner checks and bound grants (section 3) |
| Latency and availability | Direct | Extra hop; peers unreachable when home sleeps | Direct | **Direct** |
| Plan compliance | OK | Against `plan.md:230` (loopback, same-origin) | **Violates `plan.md:248`** (no reusable credential in localStorage) | Keeps HttpOnly cookies and exact Origin checks |

Also rejected: one origin through Tailscale Serve path mapping (every `Path=/` `__Host-` cookie would go to every proxied peer, so each peer would receive home's credential; cookie names clash; all traffic goes through home; each peer needs a Serve change) and Tailscale identity headers (`plan.md:230`: forwarded identity is ignored; Tailscale is not app authentication).

## 3. Design

### Honest trust statement

**If the home computer is compromised, every computer paired through it is compromised too.** A compromised home serves the app and its service worker, so it can wake the worker with its own push and drive peers with no app open, and it can show the user a fake "pair again" to capture a new pairing. The design narrows this (per-device binding, bound grants, owner checks, `pterm revoke --via`) but cannot remove it: a peer that accepts an app from home trusts home's code. Option D is chosen over B for per-phone identity, leases and direct links, not because it removes this risk. Pick as home the **least exposed always-on** computer (for example a small server you do not run untrusted code on), not the laptop that runs `npm install`, agents and dev previews. Same-user malware on home moves into scope for every peer (`docs/threat-model.md`).

### Roles

- **Home**: the computer whose origin serves the app. Its origin must serve only `pterm`: setup and `pterm doctor` refuse other Tailscale Serve handlers on that origin, because any other handler there would control every peer.
- **Peer**: any other computer. Every request from the app carries that peer's own HttpOnly `__Host-` cookie, scoped to the peer and stored hashed on the peer. No new secret type.

### Per-device origin binding (not a union)

The gateway forwards the request `Origin` with every authenticated call. The broker accepts it only if it is the peer's own origin, or **the app origin bound to that exact credential** (`devices.app_origin`). The gateway keeps a union of bound origins only to emit CORS headers. So a device that paired directly with the peer can never be driven from another computer's app it never approved, and revocation takes effect on the next request, not the next probe. Existing devices migrate with `app_origin = NULL`, meaning "own origin only".

Origin parsing is strict: `https` only, default port only (an explicit `:443` is rejected), no trailing dot, no control characters, never `null`, exact host match (near misses such as `xtailX.ts.net` or `tailX.ts.net.evil.com` are rejected), punycode shown as is. WebSocket origin patterns become per request (today they are fixed at start, `handler.go:112-125`, `live.go:109`); `InsecureSkipVerify` is never used. Preflight `OPTIONS` is answered only for exact bound origins.

### Same tailnet is not same owner

Team tailnets, Funnel nodes and reused MagicDNS names all share the site `tailX.ts.net`. At approval the peer looks up the app host in `tailscale status --json`, requires the **same owner** as itself, and stores that node's **stable node ID**. On each probe, if the node is gone or its ID changed, the peer drops the origin. This only narrows access; it is never used as authentication (`plan.md:230`). The approval prompt shows the owner and machine name.

### Pairing a second computer (bound grant)

1. On the new computer, the user runs `pterm pair --via https://mac.tailX.ts.net`. The grant is **bound to that exact app origin** at creation. Redeem and poll accept only that origin; there is no candidate-origin CORS and no prompt to misread.
2. In the home app: **Settings → Computers → Add computer**, then paste the link. (The iPhone camera opens links in Safari, a different cookie jar, so the MVP is paste-only; add-to-app links use a distinct marker so the peer's own pair page never consumes them.)
3. The app opens `/computers/add#…` and removes the fragment at once (the existing pairing pattern; the grant never goes to storage or a query).
4. The app redeems the grant on the peer with `credentials: 'include'`. The peer checks the bound origin, the owner and the node ID, then its CLI asks: `Phone "iPhone" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailX.ts.net, owner ada@example.com). Approve? [y/N]`
5. On approval the peer sets its cookie and records `devices.app_origin` and the node ID.
6. Only after the app's first authenticated `GET /api/v1/settings` on the peer succeeds does home add the computer — **to this device's list** (`computers` keyed by device ID), never a global list. Unconfirmed entries are shown nowhere; abandoned adds leave nothing behind.

Peer CORS, for bound origins only: exact `Access-Control-Allow-Origin`, `Access-Control-Allow-Credentials: true`, `Vary: Origin`; preflight allows `GET, POST, PATCH, DELETE`, headers `Content-Type, X-Pterm-Request`, `Max-Age: 600`.

### Keeping the cookie alive (7-day cap)

The peer refreshes its cookie on an authenticated read (for example `GET /api/v1/settings`), at most once a day, with `Max-Age` up to the credential's remaining lifetime. With daily use, the WebKit 7-day cap never ends a session. This is in the MVP.

### One list, one Wall

- Computer key: the canonical origin. Session key: `host/sessionId`; a peer cannot collide with or impersonate another computer's session.
- Routes: home keeps `/sessions/<id>` (push links keep working); peers use `/c/<host>/sessions/<id>`.
- Label: the first DNS label (`vps`), renameable. Colour from a fixed palette by origin hash, always with the text label. Labels come from the origin the phone dialled, never from the peer.
- The HTTP client becomes per computer (`client(origin)`); `Connection` already takes any URL.
- New session: pick the computer first, then its projects and shells. Unreachable computers are disabled.
- Wall: tiles from all computers, but only **visible** tiles connect (IntersectionObserver), and reconnects are staggered per computer, so three computers do not open dozens of sockets or reconnect at once.

### Peer responses are untrusted

Every HTTP response from a peer is validated with precompiled (standalone) Ajv validators — never `'unsafe-eval'` — in a lenient mode that drops unknown fields but rejects wrong types. Requests use `redirect: 'error'` and size limits. Peer error messages are replaced with generic text, so a peer cannot show phishing text in the home app. No previews from peers in the MVP.

### Home offline, and one computer offline

- Navigation becomes network-first with a cached app shell (the cached response keeps its CSP). The computer list (origins and labels, not secret) is kept in `localStorage`. If home is unreachable, the app still opens and reaches the peers; home is treated as one more computer.
- Each computer loads independently (`Promise.allSettled`, per-computer timeouts). An unreachable computer shows **"vps · can't reach"** with Retry; session lists are not cached (titles and folders are private).
- A 401 from a peer shows **"vps · pair again"** and never logs the app out of home or of other peers.
- Changing which computer is home means a new Home Screen icon (a new cookie jar), so every peer must be paired again. Documented.

### Versions

`HTTPSettingsResponse.protocolRevision` (an integer) plus a minimum supported revision. Because rule 6 keeps v1 changes additive and peer responses are validated leniently, computers can be updated one at a time; the app refuses only a peer below the minimum ("Update Termivoa on vps").

### Removing a computer, and a lost phone

- **Remove computer** first calls `POST /api/v1/logout` on the peer (revoking this device's credential there), then removes it from the list. If the peer is unreachable, the app says to run `pterm revoke` on it.
- `pterm revoke --via <origin>` on a peer revokes every device bound to that app origin (for example after home is compromised).
- `pterm revoke` on home prints the computers that phone was using, so a lost phone can be revoked everywhere.

### Notifications

Push from home only in the MVP; peer Waiting and Working states show in the list and on the Wall while the app is open. A service-worker registration must be on the page's own origin, so peer push means several scoped registrations on home's origin, each subscribed with one peer's VAPID key. Gate 0 tests this on iOS; if it works, it is the first follow-up.

## 4. Protocol and storage changes (rule 6)

All additive within v1, regenerated Go and TypeScript types:

- `http-api.schema.json`: `protocolRevision`, `minProtocolRevision`; `HTTPComputer {origin, label}`; `GET/POST /api/v1/computers`, `DELETE /api/v1/computers/{host}` (per device).
- `gateway-rpc.schema.json`: forwarded request `origin` on authenticated calls; `boundOrigins[]` (at most 64) on the origin probe result for CORS headers; `gateway.computers.*`.
- `admin-rpc.schema.json`: `appOrigin`, owner and node ID on pairing grants, pending pairings and devices; `admin.computers.list/remove`; `revoke` by `via`.
- WebSocket envelope: unchanged.
- Store migration `008_multi_computer.sql`: `pairing_grants.app_origin`, `pending_pairings.app_origin`, `devices.app_origin`, `devices.app_node_id` (NULL = own origin only); `computers(device_id, origin, label, created_at_ms, PRIMARY KEY (device_id, origin))`.

CLI: `pterm pair --via <origin>`; `pterm computers` and `pterm computers remove <origin>` on home; `pterm devices` gains a **Via** column; `pterm revoke --via <origin>`; `pterm doctor` reports bound origins, the CORS list, the protocol revision, tailnet and owner mismatches, and other Serve handlers on home's origin.

## 5. Stack review: which language is best here?

The owner asked for the best stack for performance, in any language.

| Option | Fit |
| --- | --- |
| **Go (current)** | One static binary per platform, fast startup, low memory, excellent networking and TLS-free loopback HTTP, mature PTY and SQLite support already in place, cross-compiles to macOS, Linux and Windows. |
| Rust | Lower memory and no GC pauses, but the hot paths here are network I/O and terminal parsing at human speed; GC pauses are not a bottleneck at this scale. A rewrite would discard a tested broker, gateway and protocol stack and slow every feature. |
| Node or Bun (TypeScript) | Shares code with the web app, but needs a runtime on every computer (against ADR 0001 and AGENTS.md rule 2), uses more memory, and is weaker for PTY supervision on Windows. |
| Zig or C++ | Highest control, far more risk and effort, no benefit for this workload. |

**Decision: keep Go.** Multi-computer adds direct phone-to-computer links, not a new hot path; per-request work is small and bounded. The phone side stays TypeScript and React (shared types are generated from the JSON Schemas). If profiling ever shows a hot spot (for example the terminal emulator), that one component can be optimized in place.

## 6. Build plan

### Gate 0 (blocking; owner, real iPhone and Android)

A small committed harness (`scripts/gate0/`, about 40 lines: one HTTPS endpoint that sets and echoes a `__Host-` Strict cookie with credentialed CORS and a WebSocket) runs behind Tailscale Serve on two machines in one tailnet. Running it changes Tailscale Serve, so it needs the owner's approval (rule 12). From the installed home app, over **IPv4**:

1. A credentialed `fetch` to the peer sets its cookie; later `fetch` and the WebSocket send it.
2. The cookie survives killing the app and rebooting the phone, and stays in the app's own cookie jar.
3. The cookie's expiry, read in Safari Web Inspector (shows whether the 7-day cap applies; no need to wait 7 days).
4. A push-woken service worker can make credentialed requests (this is the threat, documented either way).
5. Scoped service-worker registrations on home's origin can each receive push with a different VAPID key (peer push).
6. Paste-pairing inside the installed app works end to end.
7. The same checks on Android (Chrome WebAPK).

The 7-day cap is not a stop condition (rolling refresh covers it). If checks 1 or 2 fail, stop and decide on plan B.

**Plan B (if cookies fail on iOS):** one non-extractable WebCrypto key per peer in IndexedDB signs requests and a WebSocket challenge, over non-credentialed CORS. It keeps per-device identity and does not depend on cookies, but it changes `plan.md:248` (no reusable credential in browser storage), so it needs an owner decision and its own security review.

### Tracks (no shared files)

| Track | Owns | After |
| --- | --- | --- |
| P. Protocol | `protocol/v1/*`, generated Go and TypeScript, protocol revision | Gate 0 |
| H1. Broker | store migration and store, `internal/auth/service.go`, broker gateway and admin RPC (origin binding, owner and node checks, `revoke --via`), `internal/gatewayclient`, `internal/gatewayapi` | P |
| H2. Gateway and CLI | `internal/gateway/handler.go`, `api.go`, `live.go` (CORS, per-request WebSocket origins, cookie refresh, forwarded Origin), `internal/cli/admin.go`, `cmd/pterm` (`pair --via`, `computers`, `devices`, `doctor`, Serve-handler check) | P (fakes until H1) |
| W1. Web core | `web/src/api/http.ts`, `web/src/computers.ts`, `web/src/pairing/*`, `web/src/screens/hooks.ts`, `web/src/terminal/Connection.ts`, `web/src/protocol/validate.ts`, `web/vite.config.ts` (lenient peer validators) | P |
| W2. Screens and app shell | `App.tsx` (per-computer auth and offline), `Sessions.tsx`, `Wall.tsx` (visible-only tiles), `NewSession.tsx`, `Settings.tsx` (Computers: add, rename, remove, retry), `Live.tsx`, `web/src/sw.js` (network-first shell), `web/src/push.ts` | W1 |
| D. Docs | guide `computers.md`, security, CLI, troubleshooting, pairing, release notes, `docs/threat-model.md` (new trust boundaries), HTTP API doc, CHANGELOG, README, progress, landing page | drafts now, final after integration |

### Integration tests

Cookies ignore ports, so two loopback dev stacks would overwrite each other's cookie. Integration tests instead run Playwright (Chromium and WebKit) with `--host-resolver-rules="MAP *.tailx.ts.net 127.0.0.1"`, self-signed TLS and those names in the dev Host allow-list. Required tests: per-device origin binding, near-miss and `null` origins, a reused node name (owner and node ID checks), bound grants refused from other origins, cookie refresh, a push-woken service worker, one computer offline, a 401 from one peer, and home offline with the cached shell.

### MVP and follow-up

**MVP:** bound-grant pairing with owner and node checks; per-device origin binding; per-device computer list; one Sessions list with labels and colours; Live (control, compose, history, Files and Changes, uploads); new session on any computer; Wall with visible-only tiles; per-computer offline and "pair again"; home-offline app shell; lenient validated peer responses; protocol revision gate; rolling cookie refresh; remove-with-logout and `revoke --via`; push from home only; no previews from peers.

**Follow-up:** push from peers (if Gate 0 check 5 passes); previews from peers; QR scanning inside the app. Different tailnets stay out of scope.

## 7. Adversarial reviews: findings and resolutions

Two independent reviews (security; feasibility and alternatives) ran on version 1. Both concluded: keep option D, with changes. Every finding is resolved in this version:

| # | Finding (severity) | Resolution |
| --- | --- | --- |
| 1 | A compromised home compromises every peer, even with no app open (push-woken service worker; phished re-pairing) (high) | Stated plainly in "Honest trust statement" and the ADR; home choice guidance; bound grants, owner and node checks, `revoke --via` |
| 2 | Allowed origins were a union, not bound per device (high) | Per-device origin binding in the broker; union used only for CORS headers |
| 3 | Same tailnet is not same owner (team tailnets, Funnel, reused names) (high) | Owner and stable node ID checked at approval and on every probe |
| 4 | Open CORS on pairing routes made pairing phishable (medium-high) | Grants bound to one app origin at creation (`pterm pair --via`); no candidate-origin CORS |
| 5 | Global computer list, written before approval (medium) | Per-device list, added only after peer approval and an authenticated read |
| 6 | 7-day WebKit cookie cap would log peers out weekly (high) | Rolling cookie refresh in the MVP; Gate 0 reads the expiry |
| 7 | Home offline kills the app (high) | Network-first navigation with a cached app shell; home treated as one more computer |
| 8 | Exact fingerprint forces updating all computers at once (high) | Integer protocol revision with a minimum; lenient peer validation |
| 9 | Peer push as written cannot be built (high) | Scoped registrations on home's origin, tested in Gate 0; follow-up |
| 10 | Integration test plan broken (cookies ignore ports) (high) | Host-resolver mapping, self-signed TLS, Playwright Chromium and WebKit |
| 11 | Gate 0 not cheap without a test server (medium) | Committed harness and a 7-point checklist; plan B defined |
| 12 | Camera opens pairing links in Safari (medium) | Paste-only MVP; distinct add-to-app link marker |
| 13 | Remove and revoke gaps; lost phone (medium) | Remove calls logout on the peer; `revoke --via`; home lists computers to revoke |
| 14 | Grant carried across a reload (medium) | `/computers/add#…` with immediate fragment removal |
| 15 | Other Serve handlers on home's origin control all peers (medium) | Setup and doctor refuse other handlers on that origin |
| 16 | Many Wall sockets on a phone (medium) | Only visible tiles connect; staggered reconnects |
| 17 | Track gaps (`Connection.ts`, `validate.ts`, `sw.js`, `push.ts`) (medium) | Assigned to W1 and W2 |
| 18 | Missing migration, Settings UX, doctor output, abandoned adds (medium) | `app_origin = NULL` means own origin only; Settings and doctor scoped; adds only after approval |
| 19 | Malicious peer text in the home app (low) | Generic peer errors, `redirect: 'error'`, size limits, standalone validators |
| 20 | WebSocket origins fixed at start; origin parsing traps (low) | Per-request WebSocket origins; strict origin parsing rules |
| 21 | One reason for rejecting Serve path proxying was wrong (low) | Corrected in section 2 |

## Sources

- [WebKit NetworkTaskCocoa.mm (IP cookie cap)](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/NetworkProcess/cocoa/NetworkTaskCocoa.mm)
- [WebKit bug 246477](https://bugs.webkit.org/show_bug.cgi?id=246477)
- [WebKit: full third-party cookie blocking](https://webkit.org/blog/10218/full-third-party-cookie-blocking-and-more/)
- [RFC 6265 §8.5: cookies ignore ports](https://www.rfc-editor.org/rfc/rfc6265#section-8.5)
- [Tailscale ipn/serve.go](https://github.com/tailscale/tailscale/blob/main/ipn/serve.go)

- [Public Suffix List](https://raw.githubusercontent.com/publicsuffix/list/main/public_suffix_list.dat)
- [WWDC23: What's new in web apps](https://developer.apple.com/videos/play/wwdc2023/10120/)
- [firt.dev: iOS 14 for PWA developers](https://firt.dev/ios-14/)
- [WebKit PR #5347: 7-day cap for cookies from a different IP](https://github.com/WebKit/WebKit/pull/5347)

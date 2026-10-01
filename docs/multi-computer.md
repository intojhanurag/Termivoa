# Multi-computer: architecture

Status: **proposed, owner review required** (ADR [0005](decisions/0005-multi-computer.md)). Nothing here is implemented.

One phone uses several computers (for example a Mac, a Linux server and a VPS) from one Home Screen app: one Sessions list, one Wall, new sessions on any computer. This document fixes the architecture, the trust model and the build order before any code is written.

![Concept: one Sessions list across all computers, with a colour dot and label per computer, and a Computers screen to add and manage computers](images/multi-computer-concept.png)

## 1. Facts this design rests on

| Question | Finding | Evidence |
| --- | --- | --- |
| Is `ts.net` a public suffix? | Yes (PRIVATE section: `ts.net`, `*.c.ts.net`, submitted by Tailscale). The site of `mac.tailX.ts.net` is `tailX.ts.net`: two machines in **one** tailnet are cross-origin but **same-site**; machines in different tailnets are cross-site. | `publicsuffix/list` `public_suffix_list.dat` |
| What do `SameSite=Strict` and `__Host-` allow from origin A to B? | SameSite is decided by site, not origin, so B's Strict cookie **is** sent on same-tailnet requests from A's page (`fetch` with `credentials: 'include'`, WebSocket handshake). `__Host-` and HttpOnly mean A can never read, set or overwrite B's cookie. Inside one tailnet, SameSite therefore gives **no** isolation today; B is protected only by its exact Origin checks. | `internal/gateway/handler.go:226-244`, `api.go:401`, `live.go:74`, `live.go:107-110` |
| CORS today | B sends no `Access-Control-Allow-Origin`, so A cannot read B's responses; mutations need `X-Pterm-Request`, which forces a preflight that B answers with 405. | `handler.go:371` |
| iOS Home Screen app leaving its origin | Links outside the manifest scope open in Safari View Controller, with a different cookie jar and no push. A "switcher" between origins is not a usable app. | WWDC23 "What's new in web apps"; firt.dev iOS 14 |
| WebKit IP-based cookie cap | Since Safari 16.4, cookies set by a response from an IP whose first /16 differs from the page's IP may be capped to 7 days. Tailscale addresses (100.64.0.0/10) often differ between machines. | WebKit PR #5347 (must be checked on a device) |

## 2. Options

| | A. Switcher | B. Relay through home | C. Bearer tokens on the phone | **D. Direct links (chosen)** |
| --- | --- | --- | --- | --- |
| One list and one Wall | No | Yes | Yes | **Yes** |
| Works as an iOS Home Screen app | No (other origin opens in Safari View Controller) | Yes | Yes | **Yes, if Gate 0 passes** |
| Phone identity on each computer | Per phone | **Lost**: every phone is "home"; breaks per-device leases and uploads (`broker/upload.go:216`, `gateway_rpc.go:168`) | Per phone | **Per phone, unchanged** |
| Revocation | Per computer | All relayed phones at once | Per computer; a stolen token works until revoked | **Per computer** |
| A compromised home computer | Nothing | Holds standing peer credentials (stored recoverably): 24/7 shells on every peer | Its JavaScript can exfiltrate every token | Can act through the phone **only while the app is open**; cannot copy peer cookies |
| Latency and availability | Direct | Extra hop; peers unreachable when home sleeps | Direct | **Direct** |
| Plan compliance | OK | Against `plan.md:230` (loopback, same-origin) | **Violates `plan.md:248`** (no reusable credential in localStorage) | Keeps HttpOnly cookies and exact Origin checks |

Also rejected: one origin through Tailscale Serve path mapping (cookie name and path clash, peers must leave loopback) and Tailscale identity headers (`plan.md:230`: forwarded identity is ignored; Tailscale is not app authentication).

## 3. Design

### Roles and trust

- **Home**: the computer whose origin serves the app. Choose an always-on machine.
- **Peer**: any other computer. A peer lets a phone's credential act only when the request `Origin` is the peer's own origin, or an **app origin that this peer approved for that device during pairing**.
- No new secret type: each peer keeps the existing HttpOnly cookie, scoped to its own host and stored hashed.

| Threat | Control |
| --- | --- |
| Unpaired tailnet node, or a dev site such as `mac:8443` (same-site with the peer) | Exact Origin on every mutation and WebSocket. App origins must be `https`, default port, same tailnet suffix, never a preview origin. GETs without `Access-Control-Allow-Origin` stay unreadable. |
| Compromised home computer | Can act only through an open app; cannot copy cookies. The peer shows the trusted app origin ("Via") in `pterm devices` and in the approval prompt. |
| Malicious peer responses | Every HTTP response from a peer is validated with generated Ajv validators (today `web/src/api/http.ts:103` casts). Session keys are `computer + id`; computer labels come from the origin the phone dialled, never from the peer. No previews from peers in the MVP. |
| Script on the home page sending data elsewhere | CSP `connect-src` lists only the exact peer origins (`https://` and `wss://`) from home's computer list (at most 8, same tailnet). |
| Lost phone | `pterm revoke` on **each** computer (documented). Removing a computer on home also removes it from the CSP. |

### Pairing a second computer

1. On the new computer: `pterm pair` (QR code and link, as today).
2. On the phone, in the home app: **Settings → Computers → Add computer**, paste or scan the link.
3. The app checks the link (HTTPS, same tailnet suffix, not its own origin, grant format), then `POST /api/v1/computers {origin}` on home, then reloads so the new CSP applies.
4. The app redeems the grant on the peer with `credentials: 'include'`. The peer's gateway accepts same-tailnet candidate origins **on the pairing routes only** and passes the request `Origin` (never a body field) to the broker as `appOrigin`, stored on the pending pairing.
5. The peer's CLI asks: `Phone "iPhone" shows code 123-456 and will use this computer from the Termivoa app at https://mac.tailX.ts.net. Approve? [y/N]`
6. On approval the peer sets its `__Host-` cookie in the app's cookie jar and records `devices.app_origin`.
7. The peer's gateway receives the allowed app origins (from active devices) on each broker probe; from then on CORS and the WebSocket from that origin are allowed. Revoking the last device bound to an origin removes it.

Peer CORS, for allowed app origins only: exact `Access-Control-Allow-Origin`, `Access-Control-Allow-Credentials: true`, `Vary: Origin`; preflight allows `GET, POST, PATCH, DELETE`, headers `Content-Type, X-Pterm-Request`, `Max-Age: 600`. The WebSocket accepts the same origins.

### One list, one Wall

- Computer key: the canonical origin (home is `location.origin`). Session key: `host/sessionId`, so a peer cannot collide with or impersonate another computer's session.
- Routes: home keeps `/sessions/<id>` (push links keep working); peers use `/c/<host>/sessions/<id>`.
- Label: the first DNS label (`vps`), renameable on home. Colour from a fixed palette by origin hash, always with the text label (colour is never the only signal).
- The HTTP client becomes per computer (`client(origin)`); `Connection` already takes any URL; each Wall tile already opens its own connection.
- New session: pick the computer first, then its projects and shells. Unreachable computers are disabled.
- Notifications in the MVP come from home only; peer Waiting and Working states show in the list and on the Wall while the app is open. Push from peers is a follow-up (one service-worker registration per computer, each with that computer's VAPID key).

### Offline and errors

- Each computer loads independently (`Promise.allSettled`, per-computer timeouts). An unreachable peer shows **"vps · can't reach"** with Retry; session lists are not cached (titles and folders are private).
- A 401 from a peer shows **"vps · pair again"** and never logs the app out of home.
- If home is offline the app cannot load (documented); a peer can also be paired on its own as a separate icon.

### Versions

The app runs one build's code against several computers for the first time. `HTTPSettingsResponse.protocolFingerprint` (SHA-256 of `protocol/v1/*.json`, generated at build time) lets the app refuse a peer that differs: "Update Termivoa on vps to match mac". Compatibility ranges are a follow-up.

## 4. Protocol and storage changes (rule 6)

All additive within v1, regenerated Go and TypeScript types:

- `http-api.schema.json`: `protocolFingerprint`; `HTTPComputer {origin, label}`; `GET/POST /api/v1/computers`, `DELETE /api/v1/computers/{host}`.
- `gateway-rpc.schema.json`: `appOrigins[]` (at most 64) on the origin probe result; optional `appOrigin` on redeem; `gateway.computers.*`.
- `admin-rpc.schema.json`: optional `appOrigin` on pending pairings and devices; `admin.computers.list/remove`.
- WebSocket envelope: unchanged.
- Store migration `008_multi_computer.sql`: `pending_pairings.app_origin`, `devices.app_origin`, `computers(origin PRIMARY KEY, label, created_at_ms)`.

CLI: `pterm computers` and `pterm computers remove <origin>` on home; `pterm pair` and `pterm pair pending` show the app origin; `pterm devices` gains a **Via** column; `pterm doctor` reports the computer list and tailnet mismatches.

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

**Gate 0 (blocking, owner, real iPhone and Android):** two machines in one tailnet; from the installed home app, confirm that a credentialed CORS `fetch` to the peer sets and sends the peer's Strict `__Host-` cookie, that a WebSocket to the peer carries it, and whether the 7-day IP cookie cap applies. If Gate 0 fails, stop and ship option A as documentation (one Home Screen icon per computer).

| Track | Owns (no overlap) | After |
| --- | --- | --- |
| P. Protocol | `protocol/v1/*`, generated Go and TypeScript, fingerprint generation | Gate 0 |
| H1. Broker | store migration and store, `internal/auth/service.go`, broker gateway and admin RPC, `internal/gatewayclient`, `internal/gatewayapi` | P |
| H2. Gateway and CLI | `internal/gateway/handler.go`, `api.go`, `live.go`, `internal/cli/admin.go`, `cmd/pterm` | P (fakes until H1) |
| W1. Web core | `web/src/api/http.ts`, pairing, new `web/src/computers.ts`, `hooks.ts`, `web/vite.config.ts` | P |
| W2. Screens | `App.tsx`, `Sessions.tsx`, `Wall.tsx`, `NewSession.tsx`, `Settings.tsx`, `Live.tsx` | W1 |
| D. Docs | guide `computers.md`, security, CLI, troubleshooting, pairing, release notes, threat model, HTTP API doc, CHANGELOG, README, progress, landing page | drafts now, final after integration |

Integration testing runs two `pterm up --dev` stacks on loopback ports (`127.0.0.1` on different ports is same-site), plus the Gate 0 devices.

**MVP:** add a computer with local approval on the peer; one Sessions list with labels and colours; Live (control, compose, history, Files and Changes, uploads); new session on any computer; Wall across computers; per-computer offline and "pair again" states; validated peer responses; exact protocol fingerprint; push from home only; no previews from peers.

**Follow-up:** push from peers; previews from peers; rolling cookie refresh if the 7-day cap applies; compatibility ranges instead of an exact fingerprint. Different tailnets stay out of scope.

## Sources

- [Public Suffix List](https://raw.githubusercontent.com/publicsuffix/list/main/public_suffix_list.dat)
- [WWDC23: What's new in web apps](https://developer.apple.com/videos/play/wwdc2023/10120/)
- [firt.dev: iOS 14 for PWA developers](https://firt.dev/ios-14/)
- [WebKit PR #5347: 7-day cap for cookies from a different IP](https://github.com/WebKit/WebKit/pull/5347)

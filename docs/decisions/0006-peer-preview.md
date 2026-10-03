# ADR 0006: Dev-server preview for sessions on peers, through a preview ticket

- Status: **Proposed — DO NOT MERGE without owner review** (AGENTS.md rule 24). No implementation exists. The adversarial reviews (security; feasibility with alternatives) have not run yet.
- Date: 2026-10-03
- Design: [`docs/peer-preview.md`](../peer-preview.md)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/preview.md`](../preview.md)

## Context

The dev-server preview works for home sessions only (`docs/preview.md:11-12`, `web/src/screens/App.tsx:854`). Roadmap item 3 (`docs/pending-work.md:37`) asks for the same chip and frame for a session on a peer: the home app at `https://mac.<tailnet>.ts.net` frames `https://vps.<tailnet>.ts.net:8443`.

Three facts make this a trust change, not a UI change:

1. **The frame request cannot say who made it.** It is a navigation `GET`: browsers send the peer's `__Host-` `SameSite=Strict` cookie (all computers of one tailnet are same-site, `docs/multi-computer.md:13-14`), no `Origin` header, and `Sec-Fetch-Site: same-site`, which is true for a page on **any** machine of the tailnet. The peer's rule "a cross-origin request passes only from the app origin bound to this credential" (`internal/broker/computers.go:75-83`) has nothing to check.
2. **The preview listener skips per-device origin binding today.** Its broker call carries no origin (`internal/gateway/preview.go:241`, `internal/gatewayclient/client.go:627`), and the broker treats a missing origin as its own (`internal/broker/computers.go:40-43`, `internal/broker/gateway_rpc.go:301`). The listener also accepts every request that has no `Origin` header (`internal/gateway/preview.go:211-215`). So a cookie bound to home's app already works on a peer's preview listener, from any same-tailnet page. Until now this was hard to reach because no other tailnet page ran inside the app's cookie jar. A framed peer dev site is exactly such a page.
3. **A peer is untrusted input** (`docs/multi-computer.md:113-115`, "No previews from peers in the MVP"). Framing its `:8443` origin puts peer-controlled pages inside the app.

## Decision

1. **Preview ticket.** The app asks the computer that owns the session for a ticket through the existing authenticated API: `POST /api/v1/sessions/{id}/preview` with `{port}`. On a peer this is the credentialed CORS path, so the broker checks the exact bound `Origin`, the pinned node and the tags as for every other call. The ticket is random (256 bits), lives 30 seconds, works once, and is bound to the device, the session and the port. The frame loads `https://<computer>:8443/__termivoa/select?session=…&port=…&ticket=…`. The preview listener accepts a selection only with **both** the device cookie and a valid ticket. The ticket is not a credential: without the cookie it opens nothing.
2. **The preview listener gets the `/api` request rule.** Every request other than the selection must carry the preview origin itself as `Origin`, or no `Origin` and `Sec-Fetch-Site: same-origin`. The selection answers with a small page that moves the frame to `/` (not a `303`), so that this first load is same-origin too. No page on another origin (another tailnet machine, another computer's dev site, the app itself) can then frame, load from, or send requests to a selected dev server.
3. **Preview requests of a bound device follow the multi-computer trust rules.** For a credential bound to an app origin, every preview check also requires that the latest Tailscale snapshot still trusts the pinned node and tag set (60 s cache, 10-minute grace, refuse; a changed node ID revokes). Open preview sockets close on the existing 5-second recheck.
4. **Home's CSP** adds `https://<peer>:8443` to `frame-src` (and to `connect-src`, for a reachability probe) for the **confirmed** computers of the cookie's device only, in the same place that builds the per-device `connect-src` today.
5. **One path.** Home previews use the same ticket and the same listener rule. No second, older selection path stays (rule 26).
6. **Additive protocol** (rule 6): protocol revision 2. The app shows the chip for a peer only when that peer reports revision 2 or higher.
7. Each computer still needs its own `tailscale serve --https=8443`, asked by `pterm setup` on that computer (rule 12). No computer changes another computer's Tailscale configuration.

## Options not chosen

| Option | Why not |
| --- | --- |
| `Referer` of the frame request as proof of the app origin (smallest change) | Sound in current browsers, and less code (no new route, RPC or broker state). But it makes `Referer` a second kind of origin proof next to `Origin`, it is an optional header (privacy settings can remove it; the preview then fails), and it needs the frame to stop using `no-referrer`. It is the fallback if the owner prefers the least code. |
| `Sec-Fetch-Site: same-site` plus `frame-ancestors` only | `same-site` is true for every machine in the tailnet, also other owners' machines in a team tailnet. `frame-ancestors` acts after the request ran. The only secret would be the session ID, which is not designed as one. |
| A preview-only bearer cookie that replaces the device cookie | A new credential type with its own revocation path. Against "no new secret type" of ADR 0005. |
| Relay the peer's preview through home | Home would need standing credentials on peers; rejected in ADR 0005 (option B). |

## Consequences

- A session on a peer shows **Preview ready**, and the app shows that peer's dev server in the frame.
- The per-device binding gap on the preview listener (Context, point 2) closes, for home too.
- **A peer, or any dev site on a peer, gets a full-screen page inside the app when the user opens its preview.** It cannot read the app, navigate the top window or open popups (frame sandbox), and it cannot call any computer's API. It **can** show a false page (phishing), set large tailnet-wide cookies that break the app for all computers until the app is deleted, and send blind requests from the phone to other machines in the tailnet, like any web page. The app must therefore show which computer a preview comes from, with the label from the phone's own list.
- A compromised home still compromises every peer (ADR 0005). This design adds nothing to that and removes nothing from it.
- Not verified on real phones: that an installed iOS or Android app sends the peer's cookie in the frame, and that the page-driven move to `/` is `same-origin` in WebKit. Both are release gates, listed in the design.
- A ticket appears in a frame URL for at most 30 seconds. It must never be logged or stored.

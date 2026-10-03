# ADR 0006: Dev-server preview for sessions on peers

- Status: **Proposed, version 2 — DO NOT MERGE without owner review** (AGENTS.md rule 24). No implementation exists. Two adversarial reviews ran on version 1 (security: "sound with changes"; feasibility: "build with changes"); the design records each finding.
- Date: 2026-10-03
- Design: [`docs/peer-preview.md`](../peer-preview.md)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/preview.md`](../preview.md)

## Context

The dev-server preview works for home sessions only (`docs/preview.md:11-12`, `web/src/screens/App.tsx:854`). Roadmap item 3 (`docs/pending-work.md:37`) asks for the same chip and frame for a session on a peer: the home app at `https://mac.<tailnet>.ts.net` frames `https://vps.<tailnet>.ts.net:8443`.

Three facts make this a trust change, not a UI change:

1. **The frame request does not say who made it.** It is a navigation `GET`: browsers send the peer's `__Host-` `SameSite=Strict` cookie (all computers of one tailnet are same-site, `docs/multi-computer.md:13-14`), no `Origin` header, and `Sec-Fetch-Site: same-site`, which is true for a page on **any** machine of the tailnet. Today's frame also sends no `Referer`.
2. **The preview listener skips per-device origin binding today.** Its broker call carries no origin (`internal/gateway/preview.go:241`, `internal/gatewayclient/client.go:627`), and the broker treats a missing origin as its own (`internal/broker/computers.go:40-43`, `internal/broker/gateway_rpc.go:301`). The listener also accepts every request that has no `Origin` header (`internal/gateway/preview.go:211-215`). So a cookie bound to home's app already works on a peer's preview listener, from any same-tailnet page. A framed peer dev site is the first such page inside the app's cookie jar.
3. **A peer is untrusted input** (`docs/multi-computer.md:113-115`, "No previews from peers in the MVP").

## Decision

1. **`Referer` proves the app origin on the selection.** The frame uses `referrerPolicy="origin"`. The preview listener reads the origin of the selection's `Referer`: this computer's own origin passes; an origin in the bound set is forwarded to the broker, which runs the same per-device check as for every API call (`admitDevice`: bound origin, pinned node, tags); anything else, or no `Referer`, is `403`.
2. **The preview listener gets the `/api` request rule.** Every request other than the selection must carry the preview origin itself as `Origin`, or no `Origin` and `Sec-Fetch-Site: same-origin`. The selection answers `200` with a small page that moves the frame to `/` (not a `303`), so that this first load is same-origin too. There is no fallback for `same-site` navigations.
3. **Preview requests of a bound device follow the multi-computer trust rules.** Every preview check of a device bound to an app origin also requires that the latest Tailscale snapshot still trusts the pinned node and tag set.
4. **Home's CSP** adds `https://<peer>:8443` to `frame-src` for the **confirmed** computers of the cookie's device only.
5. **The app marks a peer's frame.** Its bar always shows the computer's label and an "untrusted page" mark; the app removes the frame when that computer refuses the phone.
6. **One path.** Home previews use the same rules. No older selection path stays (rule 26). Protocol revision 2; the app shows the chip for a peer only at revision 2 or higher.
7. **Order.** First a one-hour browser spike (Chrome, WebKit, one real phone). Then the gap of Context point 2 is fixed for home alone, as its own pull request. Then the peer work.
8. Each computer still needs its own `tailscale serve --https=8443`, asked by `pterm setup` on that computer (rule 12).

## Options not chosen

| Option | Why not |
| --- | --- |
| Preview ticket: the app fetches a single-use, 30-second ticket through the authenticated CORS API and passes it in the selection URL (recommended in version 1) | Both reviews found it not stronger than `Referer` against these attackers, and it needs a new route, a new RPC and a ticket table. It does not depend on an optional header, and an HTML injection without script cannot use it. Kept in the design as the alternative. |
| `Sec-Fetch-Site: same-site` plus `frame-ancestors` only | `same-site` is true for every machine in the tailnet. The only secret would be the session ID. |
| A preview-only bearer cookie that replaces the device cookie | A new credential type with its own revocation path. |
| Relay the peer's preview through home | Home would need standing credentials on peers; rejected in ADR 0005. |

## Consequences

- A session on a peer shows **Preview ready**, and the app shows that peer's dev server in the frame.
- The binding gap on the preview listener closes, for home too.
- **This withdraws the guarantee of `docs/multi-computer.md:115` that a peer cannot show phishing text in the home app.** A peer, or any script in a peer's dev site, gets a full-screen page inside the app when the user opens its preview. It cannot read the app, navigate the top window, open popups or call any computer's API. It **can** show a fake "pair again" page; it can evict every computer's session cookie with many tailnet-wide cookies, so the phone must pair again with every computer; it can send cookies to dev servers on other computers; and it can send blind requests from the phone into the tailnet.
- `Referer` becomes a second origin proof next to `Origin`. If a browser removes it, the preview fails closed. Markup injected into the app's page without script could start a selection.
- Revocation: `401` on the next request and open sockets within 5 s; a node-ID change up to about 65 s; up to 10 minutes while the Tailscale status cannot be read.
- A dev-site service worker on a preview origin can answer framed navigations and serve cached content after revocation.
- A compromised home still compromises every peer (ADR 0005).
- Not verified in any browser yet; the spike is the first step and has a stop rule.

## Decisions for the owner

1. **Origin proof on the selection:** `Referer` (recommended; small, fails closed when the header is removed) or the ticket (more code and state; no optional header).
2. **Accept the trust statement** (`docs/peer-preview.md`, section 7): a peer's frame can show phishing and can un-pair the phone from all computers.
3. **Service worker limit:** accept it, or refuse `Sec-Fetch-Dest: serviceworker` on the preview listener (this breaks dev sites that use a service worker).

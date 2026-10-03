# ADR 0006: Dev-server preview for sessions on peers

- Status: **Accepted 2026-10-03; implemented.** The owner approved it (AGENTS.md rule 24) with three choices: the `Referer` variant (not the ticket), the trust statement, and the dev-site service-worker limit. Two adversarial reviews ran on version 1 (security: "sound with changes"; feasibility: "build with changes"), a final plan review ran on version 2, and a browser spike ran before the build (Chrome 154, Playwright WebKit 26.5); the design records each finding. Real-phone checks are still open (`docs/owner-verification.md`).
- Date: 2026-10-03
- Design: [`docs/peer-preview.md`](../peer-preview.md) (version 3)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/preview.md`](../preview.md)
- Closes: #143, #144

## Context

The dev-server preview worked for home sessions only. The roadmap asked for the same chip and frame for a session on a peer: the home app at `https://mac.<tailnet>.ts.net` frames `https://vps.<tailnet>.ts.net:8443`.

Three facts made this a trust change, not a UI change:

1. **The frame request does not say who made it.** It is a navigation `GET`: browsers send the peer's `__Host-` `SameSite=Strict` cookie (all computers of one tailnet are same-site, `docs/multi-computer.md`, section 1), no `Origin` header, and `Sec-Fetch-Site: same-site`, which is true for a page on **any** machine of the tailnet. The frame before this change also sent no `Referer`.
2. **The preview listener skipped per-device origin binding** (#143). Its broker call carried no origin, and the broker treats a missing origin as its own. The listener also accepted every request that had no `Origin` header. So a cookie bound to home's app already worked on a peer's preview listener, from any same-tailnet page. A framed peer dev site is the first such page inside the app's cookie jar.
3. **A peer is untrusted input** (`docs/multi-computer.md`, "Peer responses are untrusted").

## Decision

1. **`Referer` proves the app origin on the selection.** The frame uses `referrerPolicy="origin"`. The selection must carry no `Origin` header and exactly one `Referer`: an app origin plus one `/`. This computer's own origin passes; an exact bound app origin is forwarded to the broker, which runs the same per-device check as for every API call (`admitDevice`: bound origin, pinned node, tags); anything else, or no `Referer`, is `403` before the cookie is read.
2. **The preview listener gets the `/api` request rule (L1).** Every request other than the selection must carry the preview origin itself as `Origin`, or no `Origin` and `Sec-Fetch-Site: same-origin`. The selection answers `200` with a small page that moves the frame to `/` with a meta refresh (not a `303`), so that this first load is same-origin too. That page may be framed only by the app origin of the `Referer`. There is no fallback for `same-site` navigations.
3. **Preview requests of a bound device follow the multi-computer trust rules.** Every preview check of a device bound to an app origin, with or without a forwarded origin, also requires that the latest Tailscale snapshot still trusts the pinned node and tag set.
4. **Home's CSP** adds `https://<peer>:8443` to `frame-src` and `connect-src` for the **confirmed** computers of the cookie's device only; never a pending one, never `wss:`.
5. **The app marks a peer's frame.** Its bar always shows the computer's label and **untrusted page**. The app shows the frame only while its own poll of that computer answers; it removes the frame when that computer refuses the phone, cannot be reached or must update.
6. **The app probes before it frames.** A `no-cors` request to the selection path, without credentials, 5 s. No answer shows a neutral text and no frame (#144). A computer confirmed in the same visit is not yet in the loaded page's CSP; the app sees the CSP block and reloads once.
7. **The listener's own text pages can be framed** (no `frame-ancestors`), so the app's frame shows a refusal as text (#144).
8. **One path.** Home previews use the same rules. No older selection path stays (rule 26). Protocol revision 2; the app shows the chip for a peer only at revision 2 or higher.
9. Each computer still needs its own `tailscale serve --https=8443`, asked by `pterm setup` on that computer (rule 12).

Order as built: the browser spike first (it had a stop rule; both engines passed), then everything as one pull request. Version 2 planned to fix the gap of Context point 2 for home alone in its own pull request; the owner's approval covered the whole change together.

## Options not chosen

| Option | Why not |
| --- | --- |
| Preview ticket: the app fetches a single-use, 30-second ticket through the authenticated CORS API and passes it in the selection URL (recommended in version 1) | Both reviews found it not stronger than `Referer` against these attackers, and it needs a new route, a new RPC and a ticket table. It does not depend on an optional header, and an HTML injection without script cannot use it. It stays in the design as the next option if `Referer` fails in a supported browser. |
| `Sec-Fetch-Site: same-site` plus `frame-ancestors` only | `same-site` is true for every machine in the tailnet. The only secret would be the session ID. |
| A preview-only bearer cookie that replaces the device cookie | A new credential type with its own revocation path. |
| Relay the peer's preview through home | Home would need standing credentials on peers; rejected in ADR 0005. |
| Refuse `Sec-Fetch-Dest: serviceworker` on the preview listener | It breaks dev sites that use a service worker. The owner accepted the limit instead. |

## Consequences

- A session on a peer shows **Preview ready**, and the app shows that peer's dev server in the frame.
- The binding gap on the preview listener is closed, for home too (#143).
- **This withdrew the guarantee of `docs/multi-computer.md` that a peer cannot show phishing text in the home app** (accepted by the owner). A peer, or any script in a peer's dev site, gets a full-screen page inside the app when the user opens its preview. It cannot read the app, navigate the top window, open popups or call any computer's API. It **can** show a fake "pair again" page; it can evict every computer's session cookie with many tailnet-wide cookies, so the phone must pair again with every computer; it can send cookies to dev servers on other computers; and it can send blind requests from the phone into the tailnet.
- `Referer` is a second origin proof next to `Origin`. If a browser removes it, the preview fails closed. Markup injected into the app's page without script could start a selection.
- A `curl` or a typed URL on the preview listener is `403`.
- Revocation: `401` on the next request and open sockets within 5 s; a node-ID change up to about 65 s; up to 10 minutes while the Tailscale status cannot be read.
- A dev-site service worker on a preview origin can answer framed navigations and serve cached content after revocation (accepted by the owner).
- WebKit: after a history restore of the app page the frame must load again (the app does it); Back inside the frame past its first entry moves the whole app back (not fixed; it exists for home previews too).
- A compromised home still compromises every peer (ADR 0005).
- Checked in Chrome and Playwright WebKit (the spike, and the multi-computer harness, `scripts/e2e-multicomputer/README.md`). Not yet checked on a real phone.

## Decisions of the owner (2026-10-03)

1. **Origin proof on the selection:** `Referer`.
2. **The trust statement** (`docs/peer-preview.md`, section 7) is accepted: a peer's frame can show phishing and can sign the phone out of all computers.
3. **Service worker limit:** accepted.

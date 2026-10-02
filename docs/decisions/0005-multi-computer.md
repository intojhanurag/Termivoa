# ADR 0005: Multi-computer through direct same-site links

- Status: Accepted by the owner on 2026-10-02 (version 4). Implementation in progress; Gate 0 (real iPhone and Android) must pass before release, and the implementation pull request needs the owner's approval before merge
- Date: 2026-10-01
- Design: [`docs/multi-computer.md`](../multi-computer.md)

## Context

One phone should use several computers (for example a Mac, a Linux server and a VPS) from one Home Screen app, with one Sessions list and one Wall. Today each computer runs its own `pterm` gateway at its own `https://<machine>.<tailnet>.ts.net` origin, and a phone pairs with one computer through an HttpOnly `__Host-` cookie scoped to that origin. `plan.md` lists multiple computers as reserved future work; the owner authorized it on 2026-10-01 as a critical, owner-reviewed feature.

## Decision

The phone keeps one app, served by a chosen "home" computer. That app talks **directly** to every other paired computer over the tailnet with credentialed CORS and WebSockets, each authenticated by **that computer's own** `__Host-` HttpOnly cookie. Every computer stays the only authority for its own pairing, approval, revocation, controller leases and device identity. There is no relay and no bearer token.

A peer accepts a cross-origin request only when its `Origin` is the app origin bound to **that exact device credential**, approved on the peer for a grant created with `pterm pair --via <origin>`, owned by the same Tailscale user and still the same Tailscale node. Peer responses are schema-validated in the app; computers update independently against a minimum protocol revision; the peer refreshes its cookie so WebKit's 7-day cap does not end sessions.

The host runtime stays Go, with a profiling requirement before any component is rewritten (see the stack review). The phone, not the host, is the measured bottleneck, so the design adds a phone-wide terminal-work budget with broker-enforced thumbnail rates, and performance release gates that keep `plan.md` §13 targets (p95 under 30 ms app-added latency, p95 under 2 s reconnect).

Build work starts only after Gate 0: a real iPhone and Android check that a Home Screen app on one tailnet origin can get, keep and send another tailnet origin's `SameSite=Strict` `__Host-` cookie on credentialed `fetch` and WebSocket requests.

## Consequences

One app, one Sessions list and one Wall across computers, with per-phone identity and per-computer revocation unchanged. **A compromised home computer compromises every computer paired through it**, even with no app open (a push-woken service worker, or a phished re-pairing); the design narrows this but cannot remove it, so home should be the least exposed always-on computer. Peers show the app origin they trust ("Via"), and `pterm revoke --via` cuts one off. Recovery after a home compromise is required and documented: `pterm revoke --via` on every peer, clear the app's data on the phone (platform-specific; on Android also the site's data in Chrome), then pair again (a compromised home can leave a service worker on the phone and can keep a peer cookie that works without the phone). A lost phone must be revoked on each computer. Tagged Tailscale nodes pair only with an explicit `--allow-tagged`. Computers in different tailnets are not supported. If Gate 0 fails, ship a documented one-icon-per-computer setup instead.

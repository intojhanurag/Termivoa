# ADR 0005: Multi-computer through direct same-site links

- Status: Proposed (architecture only; not implemented; owner review required before any build work)
- Date: 2026-10-01
- Design: [`docs/multi-computer.md`](../multi-computer.md)

## Context

One phone should use several computers (for example a Mac, a Linux server and a VPS) from one Home Screen app, with one Sessions list and one Wall. Today each computer runs its own `pterm` gateway at its own `https://<machine>.<tailnet>.ts.net` origin, and a phone pairs with one computer through an HttpOnly `__Host-` cookie scoped to that origin. `plan.md` lists multiple computers as reserved future work; the owner authorized it on 2026-10-01 as a critical, owner-reviewed feature.

## Decision

The phone keeps one app, served by a chosen "home" computer. That app talks **directly** to every other paired computer over the tailnet with credentialed CORS and WebSockets, each authenticated by **that computer's own** `__Host-` HttpOnly cookie. Every computer stays the only authority for its own pairing, approval, revocation, controller leases and device identity. There is no relay and no bearer token.

A peer accepts cross-origin requests only from an app origin that was shown and approved on that peer during pairing, is HTTPS on the default port, and is in the same tailnet. All peer responses are schema-validated in the app, and the app refuses peers whose protocol fingerprint differs.

The host runtime stays Go (see the stack review in the design document).

Build work starts only after Gate 0: a real iPhone and Android check that a Home Screen app on one tailnet origin can get, keep and send another tailnet origin's `SameSite=Strict` `__Host-` cookie on credentialed `fetch` and WebSocket requests.

## Consequences

One app, one Sessions list and one Wall across computers, with per-phone identity and per-computer revocation unchanged. A compromised home computer can act through a phone only while that phone has the app open, and cannot copy peer cookies; peers show the app origin they trust ("Via"). A lost phone must be revoked on each computer. Computers in different tailnets are not supported. If Gate 0 fails, ship a documented one-icon-per-computer setup instead.

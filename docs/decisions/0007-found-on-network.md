# ADR 0007: Found on your network (add a computer with no link)

- Status: **accepted (owner approved 2026-10-03); v2 after two reviews; security re-check of the push shape pending**. Do not merge an implementation before the security re-check is recorded below and the owner approves it (AGENTS.md rule 24).
- Date: 2026-10-03
- Design: [`docs/found-on-network.md`](../found-on-network.md) (version 2)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/multi-computer.md`](../multi-computer.md)

## Context

Today a second computer is added with a secret link. The owner runs `pterm pair --via mac` on the new computer; the link (a single-use grant, 2 minutes, bound to home's app origin and pinned node) must reach the phone app by a tap in a terminal, a paste or a scan.

The owner wants: "login to Tailscale, see all devices, Add". In the app, **Settings → Computers** shows the computer that waits, with an **Add** button. No link to move.

The grant is what makes adding safe: the new computer makes it, binds it to the app the user named, and accepts a redeem only from that app's origin. Version 1 removed the grant (a phone-initiated request and a typed code). Both reviews found that this opens a code relay through any hostile computer of the same user, and that the fixes cost more than the feature (see "Reviews").

## Decision

**The new computer pushes its own bound grant to the home the user named.** Everything after that is today's link flow.

1. **New computer.** While `pterm pair --via <home>` waits for a phone (also setup's answer 2), its broker makes a fresh bound grant every 30 seconds (`resolveVia`, `CreateBoundGrant`, 2 minutes, unchanged) and sends it to home: `POST https://<home host>/api/v1/computers/offer {origin, grant}`. The dial goes to the Tailscale IP of the pinned home node from its own status, TLS name = home host, system roots, no proxy, no redirects, no cookies, no `Origin`, 3 s.
2. **Home.** The new route has no authentication and accepts only requests **without** `Origin` (so no browser page can send one), with `X-Pterm-Request: 1`, JSON, 1 KiB. Home keeps the offer only when `origin` is a peer in its own snapshot with home's owner and no tags. Offers live in memory only: 2 minutes, at most 16, the newest per origin wins, own rate bucket.
3. **Phone.** `GET /api/v1/computers/found` (cookie, own origin) lists the offers. **Add** gives `{origin, grant}` to the existing **Add this computer?** sheet: the same path as a pasted link.
4. **Approval** is today's compare-and-`y` prompt on the new computer. The phone shows a code only after its own redeem.
5. **Not changed:** schema, redeem, poll, cookie, CSP, bindings, `/healthz`. No fresh database. Protocol revision 3, additive. An old home answers the offer with an error the CLI recognizes and the CLI points to the QR code and the link; an old new computer never offers and uses the link.

## Rejected: v1 (phone-initiated request with a typed code)

Version 1 let the phone ask a waiting computer for a pairing with no grant; the user typed the phone's code on the computer.

- **Code relay (high).** A hostile computer M of the same user forwards its own forged-`Origin` request to the waiting computer and shows that code to the user as if it were the phone's. Typing it gives M a shell.
- **The prompt cannot tell requests apart (high).** The app origin is only a header; any program can send the real home's origin.
- **The proposed fix ("home vouches") does not close it.** A compromised M can vouch truthfully for itself, and admission was "any same-owner node".
- **Cost.** v1 plus vouching is about 1,300–1,500 non-test lines, a nullable `grant_id` and a fresh database on every computer.

Also not chosen (from v1): a phone-side probe (CORS on an unauthenticated route of every computer), a home-side `/healthz` probe of every machine (a new outbound request on the home side, and v2 needs no discovery: a computer that waits tells home itself), an always-open request route.

## Consequences

- Add a computer: `pterm setup` (answer 2) or `pterm pair --via mac` there, **Add** in the app, compare the code, `y` there. The QR code and the link stay on screen as the fallback.
- The grant now also travels new computer → home (TLS, to the named home's tailnet IP) and sits in home's memory for 2 minutes. A compromised home sees every grant in today's link flow too: nothing new.
- Any tailnet machine can post a dead or overwriting offer for a computer of home's owner. That breaks only **Add** for a while; the QR code and the link still work.
- A hostile computer of the same user can offer **itself** and show in **Found**. The user must still tap **Add** and press `y` on that computer: the same as adding any hostile peer today.
- The new computer's broker makes its first outbound request into the tailnet (one fixed path on one pinned node).

## Reviews

### v1 security review: "sound with changes"

| # | Finding (severity) | Resolution in v2 |
| --- | --- | --- |
| S1 | Code relay through a hostile same-owner "found" computer (high) | Moot: no unauthenticated pairing route. Only the grant starts a pairing, and it goes only to the named home |
| S2 | The approval prompt cannot tell requests apart: the app origin is a forgeable header (high) | Moot: a pending pairing exists only after a redeem of a grant bound to one origin and node (today's rule) |
| S3 | Grant-less `/computers/add` fragments (medium) | Moot: **Add** carries a grant like a pasted link |
| S4 | `waitForPhone` and requested rows (medium) | Moot: no requested rows; `waitForPhone` is today's code with a 10-minute deadline |
| S5 | Nullable `grant_id` breaks the `ListPendingPairings` scan (medium) | Moot: no schema change |
| S6 | Requested rows survive windows (medium) | Moot: no window, no rows; offers are memory only, 2 minutes |
| S7 | The window is only for a terminal (medium) | Kept on purpose: the new computer offers only while `pterm pair --via` or setup waits in a terminal, because a person must press `y` there |
| S8 | Dial `TailscaleIPs`, not DNS (low) | Adopted for the new outbound dial |
| S9 | Preflight `Max-Age` (low) | Moot: the offer route never sends CORS; `found` is own origin |
| S10 | `MaxPairingOrigins` has no spare (low) | Moot: offers never enter the origin sets; the new computer's grants all carry one origin |

### v1 feasibility and simplicity review: "redesign"

| # | Finding | Resolution in v2 |
| --- | --- | --- |
| F1 | Vouching alone does not stop the relay | Agreed; vouching dropped with v1 |
| F2 | v1 + vouching is about 1,300–1,500 non-test lines and a fresh database | v2 is about 600–700 non-test lines (estimate), no storage change |
| F3 | Simpler shape: the new computer pushes its own bound grant to the home the user named | Adopted as v2 |

### Security re-check of v2

(empty: to run on the push shape before implementation; record each finding and its resolution here)

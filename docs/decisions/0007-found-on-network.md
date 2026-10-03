# ADR 0007: Found on your network (add a computer with no link)

- Status: **accepted (owner approved 2026-10-03); v2 after two reviews; security re-check of the push shape done ("sound with changes"), resolutions in this text**. Do not merge an implementation before the owner approves this version (AGENTS.md rule 24).
- Date: 2026-10-03
- Design: [`docs/found-on-network.md`](../found-on-network.md) (version 2)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/multi-computer.md`](../multi-computer.md)

## Context

Today a second computer is added with a secret link. The owner runs `pterm pair --via mac` on the new computer. The link must then reach the phone app by a tap in a terminal, a paste or a scan. It holds a grant: single use, 2 minutes, bound to home's app origin and pinned node.

The owner wants: "login to Tailscale, see all devices, Add". In the app, **Settings → Computers** shows the computer that waits, with an **Add** button. There is no link to move.

The grant is what makes adding safe. The new computer makes it, binds it to the app the user named, and accepts a redeem only from that app's origin. Version 1 removed the grant (the phone sent a request, and the user typed a code). Both reviews found that this opens a code relay through any hostile computer of the same user, and that the fixes cost more than the feature (see "Reviews").

## Decision

**The new computer pushes its own bound grant to the home the user named.** Everything after that is today's link flow.

1. **New computer.**
   - While `pterm pair --via <home>` waits for a phone (setup's answer 2 also does this), its broker sends one bound grant to home every 30 s: `POST https://<home host>/api/v1/computers/offer {origin, grant}`.
   - It re-sends the **same** grant (`resolveVia`, `CreateBoundGrant`, 2 minutes, unchanged), and it replaces the grant only when 60 s are left. It deletes the old unredeemed grant then, and it deletes the remaining grants when the command ends.
   - The dial goes only to the pinned home node's Tailscale IP on port 443, checked again before each push. The TLS name is home's host, with system roots and TLS 1.2 or later. There is no proxy, no redirect, no cookie and no `Origin`, and the time limit is 3 s.
   - There is no push with `--allow-tagged`: home accepts offers only between untagged nodes of one owner.
2. **Home.**
   - The new route has no authentication.
   - At the top of `admitAPI` it refuses any `Origin` or `Sec-Fetch-*` header, it never sends CORS, and it never reads the cookie. So no browser page can reach it.
   - It needs `X-Pterm-Request: 1`, JSON and at most 1 KiB, and it has its own rate bucket.
   - Every well-formed offer gets `204`. Home keeps an offer only when its own snapshot (never a `tailscale` run) shows `origin` as a peer with home's owner and no tags; else it drops the offer silently.
   - Offers are in memory only, for 60 s, at most 16. The newest offer for an origin wins.
3. **Phone.**
   - `GET /api/v1/computers/found` (cookie, `authorizeOwn`) lists the offers, with labels from home's own status.
   - **Add** gives `{origin, grant}` to the existing **Add this computer?** sheet: the same path as a pasted link.
4. **Approval** is today's compare-and-`y` prompt, which now also names **this** computer: "Phone … wants to use THIS computer (vps) from the Termivoa app at mac … Approve only if your phone shows 123-456 and says "type y in the terminal on vps"". Setup's list of homes shows each full address and OS. The screen prints "Sent to mac (https://…)".
5. **Not changed:** schema, redeem, poll, cookie, CSP, bindings, `/healthz`. No fresh database. Protocol revision 3, additive. If home is old, any reply other than 204 makes the CLI point to the QR code and the link. An old new computer never offers, and uses the link.

## Rejected: v1 (phone-initiated request with a typed code)

In version 1, the phone asked a waiting computer for a pairing with no grant, and the user typed the phone's code on the computer.

- **Code relay (high).** A hostile computer M of the same user sends its own request with a forged `Origin` to the waiting computer. It then shows that code to the user as if it were the phone's. Typing it gives M a shell.
- **The prompt cannot tell requests apart (high).** The app origin is only a header; any program can send the real home's origin.
- **The proposed fix ("home vouches") does not close it.** A compromised M can vouch truthfully for itself, and v1 admitted any node of the same owner.
- **Cost.** v1 plus vouching is about 1,300–1,500 non-test lines. It also needs a nullable `grant_id` and a fresh database on every computer.

Also not chosen from v1:
- A probe from the phone: it needs CORS on an unauthenticated route of every computer.
- A home-side `/healthz` probe of every machine: v2 needs no discovery, because a computer that waits tells home itself.
- A request route that is always open.

**Rejected for now: identify the sender from `X-Forwarded-For` (Tailscale Serve).** This would let home accept an offer only from the node it names, which closes junk offers and their replacement of the real offer. But it makes a Serve header a trust input, against the rule that forwarded identity headers are ignored. That needs its own ADR and a check on a real Serve. It is also not verified that Serve sets this header. The DoS that remains is accepted: the QR code and the link still work.

## Consequences

- To add a computer: run `pterm setup` (answer 2) or `pterm pair --via mac` there, tap **Add** in the app, compare the code and the computer name, and press `y` there. The QR code and the link stay on screen as the fallback.
- **A written rule changes.** Until now, a grant's value went only to the restricted local CLI (`CreateBoundGrant`; `docs/threat-model.md`). Now it also goes:
  - from the new computer to home, over TLS to the pinned IP of the named home;
  - into home's memory, for at most 60 s;
  - to **every device paired with home**. Each such device is treated as the owner's.

  This is not a new kind of exposure:
  - The in-terminal path of today (`pterm pair --via` in a Termivoa session on home) already sends the grant through home and to every viewer of that session.
  - A compromised home already sees every grant that is pasted or scanned into its app.
  - From this feature, a compromised home gains only three things: it learns that a computer waits, it gets one live grant at a time for up to 10 minutes, and it sends replies of at most 2 KiB.
- Anyone who can reach home's port 443 can post junk or replacement offers for a computer of home's owner. **Add** then fails for a while. They learn nothing (always `204`), and they cannot start a `tailscale status` run. The QR code and the link still work.
- A hostile computer of the same user can offer **itself** and show in **Found**. The user must still tap **Add** and press `y` on that computer: the same as adding any hostile peer today.
- If the user names a hostile computer as home, it could relay the code. The prompt that names THIS computer, and the phone that names the computer it talks to, are the defence. The user must read them.
- A tagged home (which `docs/multi-computer.md` recommends) never shows **Found**. It uses the link, as today.
- The new computer's broker makes its first outbound request into the tailnet: one fixed path, to one pinned IP and port.

## Reviews

### v1 security review: "sound with changes"

| # | Finding (severity) | Resolution in v2 |
| --- | --- | --- |
| S1 | Code relay through a hostile same-owner "found" computer (high) | Moot: there is no unauthenticated pairing route. Only the grant starts a pairing, and it goes only to the named home |
| S2 | The approval prompt cannot tell requests apart: the app origin is a forgeable header (high) | Moot: a pending pairing exists only after a redeem of a grant bound to one origin and node (today's rule) |
| S3 | Grant-less `/computers/add` fragments (medium) | Moot: **Add** carries a grant, like a pasted link |
| S4 | `waitForPhone` and requested rows (medium) | Moot: there are no requested rows. `waitForPhone` is today's code with a 10-minute deadline |
| S5 | Nullable `grant_id` breaks the `ListPendingPairings` scan (medium) | Moot: no schema change |
| S6 | Requested rows survive windows (medium) | Moot: there is no window and there are no rows. Offers are in memory only, for 60 s |
| S7 | The window is only for a terminal (medium) | Kept on purpose: the new computer offers only while `pterm pair --via` or setup waits in a terminal, because a person must press `y` there |
| S8 | Dial `TailscaleIPs`, not DNS (low) | Adopted for the new outbound dial |
| S9 | Preflight `Max-Age` (low) | Moot: the offer route never sends CORS, and `found` is own origin only |
| S10 | `MaxPairingOrigins` has no spare (low) | Moot: offers never enter the origin sets, and the new computer's grants all carry one origin |

### v1 feasibility and simplicity review: "redesign"

| # | Finding | Resolution in v2 |
| --- | --- | --- |
| F1 | Vouching alone does not stop the relay | Agreed; vouching is dropped together with v1 |
| F2 | v1 + vouching is about 1,300–1,500 non-test lines and a fresh database | v2 is about 650–750 non-test lines (estimate), with no schema change |
| F3 | Simpler shape: the new computer pushes its own bound grant to the home the user named | Adopted as v2 |

### Security re-check of v2: "sound with changes"

The re-check confirmed these points as sound:
- The v1 relay from any hostile same-owner node is closed: a grant works only on its issuer, and it is sent only to the named, pinned home.
- The app origin in the prompt comes from the grant row; the redeemer cannot forge it.
- Browsers cannot reach the offer route.
- A compromised home gains only three things: it learns that a computer waits, it gets one live grant at a time, and it sends replies of at most 2 KiB.

Each finding below was checked against the code (design, section 7).

| # | Finding (severity) | Resolution |
| --- | --- | --- |
| R1 | Relay when the user names the wrong home: M redeems X's grant with a forged `Origin`, offers itself to the real home, and returns X's code to the phone (medium) | The prompt names THIS computer and quotes the phone's code-screen sentence ("type y in the terminal on vps"). The setup list shows each full address and OS. The screen prints "Sent to mac (https://…)". Typing the code in place of `y` is not chosen: in this relay the codes match, so only the computer name stops it |
| R2 | A new grant every 30 s keeps 4–5 grants live and can fill the 4 pending pairings (low–medium) | One live offered grant, re-sent every 30 s. It is replaced at 60 s left and the old one is deleted. The grants are deleted on every exit. One command run makes at most one pending pairing from offers |
| R3 | The unauthenticated route allows junk and replacement offers, empties the bucket, and its answers show which names are untagged nodes of home's owner (low) | `204` for every well-formed offer, and silent drops. Snapshot only. Labels come from home's status. The DoS is accepted. The `X-Forwarded-For` sender check is rejected for now |
| R4 | `admitAPI` sends CORS and answers preflights on every `/api` path for bound origins; an old home answers 403, not 404 (low) | The exception sits at the top of `admitAPI` for this exact path: no CORS, any `Origin` or `Sec-Fetch-*` refused, no cookie read, no origin forwarded. The CLI shows one fixed line for any reply other than 204 and never prints home's body |
| R5 | `/found` read from another app bound on home (low) | `authorizeOwn` in the broker; a test shows that a bound origin gets 400 |
| R6 | Outbound dial details (low) | `TailscaleIPs` checked against Tailscale's ranges; a dial guard that allows only the pinned IP on port 443 (not the webpush guard); a node-ID check before each push; no proxy, no redirect, no jar; TLS 1.2 or later with `ServerName` and system roots; never http; 2 KiB; in the broker only |
| R7 | The rule "grants go only to the local CLI" changes (info) | Stated under Consequences. A threat-model row "New computer → home (offer)" is added, and the text says that every device paired with home is the owner's |
| R8 | A tagged home or new computer always has its offers refused (info) | No push with `--allow-tagged`; the CLI points to the QR code and the link |

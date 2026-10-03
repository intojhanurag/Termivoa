# ADR 0007: Found on your network (add a computer with no link)

- Status: **proposed (owner approved the idea 2026-10-03; design under review)**. Adversarial reviews (security; feasibility and alternatives) are still to run (AGENTS.md rule 24). Do not merge an implementation before the owner approves this design.
- Date: 2026-10-03
- Design: [`docs/found-on-network.md`](../found-on-network.md) (version 1)
- Builds on: ADR [0005](0005-multi-computer.md), [`docs/multi-computer.md`](../multi-computer.md)

## Context

Today a second computer is added with a secret link. The owner of the new computer runs `pterm pair --via mac` there; the link (a single-use grant, 2 minutes, bound to home's app origin) must reach the phone app by a tap in a terminal, a paste or a scan. The grant is also what admits home's app on the new computer at all: first-pairing CORS exists only for an origin with an unexpired grant or pending pairing (`docs/multi-computer.md`, "First-pairing CORS").

The owner wants: "we should just login to tailscale then show all devices and just add". The phone must start the pairing, with no link, no QR and no copy and paste.

Three facts make this a trust change:

1. **No secret reaches the new computer first.** A request that starts a pairing is unauthenticated. `Origin` is only a header: a browser cannot forge it, but a program on any tailnet machine can.
2. **The new computer cannot know which machine is the user's home.** It can only check, in its own Tailscale status, that the `Origin` host is a node of its tailnet with the same owner (the rule of `pterm pair --via`, `internal/broker/computers.go:170-183`). Every computer of that user passes.
3. **Home needs a new outbound request** to learn which machines run Termivoa. The broker has made outbound requests only to push services until now.

## Decision

1. **Discovery by home's broker.** `GET /api/v1/computers/found` (cookie, own origin only). The broker takes the nodes of its own Tailscale status that are in its own tailnet, online, not itself, not tagged and of the same owner (at most 16), and asks each `GET https://<node>/healthz`: fixed path, tailnet addresses only, no redirects, no proxy, no cookies, 2 s, 2 KiB. A Termivoa answer has the known shape; it now also carries `protocolRevision`. The phone never probes.
2. **The phone starts the pairing.** New route on every computer: `POST /api/v1/pair/request` with `{deviceLabel}`. It needs no secret. The answer is a pending pairing (pending ID, poll secret, verification code), the same as after a redeemed grant. Polling, the cookie, `devices.app_origin` and the node ID binding are unchanged.
3. **The new computer listens only while its owner waits.** The route is admitted only while a local command is waiting for it: `pterm approve`, or the last step of `pterm setup`. Outside that window the computer answers as if the route did not exist for other origins (no CORS, no stored state).
4. **Admission in the window.** The broker (not the gateway) admits an `Origin` only when the latest Tailscale snapshot shows that host as an own-tailnet node with the same owner, not tagged. It pins the node ID, owner and tags in the pending pairing, as `pterm pair --via` does. The gateway asks the broker for each request and each preflight and caches nothing.
5. **Approval is the typed code.** The command on the new computer shows who asks (phone name, app computer, full origin, owner) and asks the user to **type the 6-digit code that the phone shows**. The broker approves only the request that has that code. A request the user did not make can never be approved by a careless `y`. There is no approval of such a request by its ID.
6. **Tailscale identity headers stay unused.** The source of the connection is not checked.
7. **Bounds.** At most 4 such pending pairings, 2 minutes each, counted apart from link pairings; their own rate limits in the gateway and the broker. A flood can stop this flow while it runs; it cannot stop link pairing, paired phones or anything else.
8. The link, paste and scan flows stay. Tagged computers (home or new) use `pterm pair --via … --allow-tagged`.
9. Protocol revision 3 (additive). One schema change (`pending_pairings.grant_id` may be NULL), so each computer needs a fresh database once (rule 26).

## Options not chosen

| Option | Why not |
| --- | --- |
| List every same-owner online machine, no probe | Least code and no outbound request. But the list shows machines without Termivoa (a NAS, a phone), and the app cannot tell "not Termivoa" from "not waiting". Kept as the fallback if the owner refuses the probe (decision 3 below). |
| The phone probes the machines | Needs CORS on an unauthenticated route, readable by every page on every machine of the tailnet, and home's CSP would have to allow every candidate before it is added. |
| The new computer finds home and makes the grant itself | The grant must reach the phone. A grant given to every caller that says it is home is no secret; a grant given by Tailscale source identity makes Tailscale the authentication (refused in `docs/implementation-plan.md:232`). |
| Accept requests always, approve with `y` | A tailnet machine can keep a forged request waiting at all times; one `y` without a real comparison gives it a shell. |
| The new computer shows a code; the user types it in the app | A 6-digit secret sent over the network: it can be guessed (needs a lock-out, which an attacker can trigger) and the user types on the phone. |
| Use Tailscale Serve identity headers to admit only the user's devices | A new trust dependency. With the typed code, a forged request gains nothing that can be approved, so the headers would only reduce noise. |

## Consequences

- A new computer is added with: `pterm setup` there, **Add** in the app, six digits typed there.
- Any computer of the same Tailscale user can ask a waiting computer for a pairing. Only the name in the prompt tells the user which app gets the computer. A compromised home, or a false Termivoa page on another computer of the user, can ask the user to run `pterm approve` and type a code; this is a little easier than today's false "pair again" (no link to move). The honest trust statement gains this sentence.
- Home's broker makes outbound HTTPS requests into the tailnet. `/healthz` tells every tailnet machine the protocol revision (the app's files already tell the build).
- While a flood runs during the window, **Add** fails; `pterm pair --via` still works.
- The verification code alone still gives no access: the phone needs its poll secret, and only the local owner can type the code into the local command.
- Updating needs a fresh database on each computer: every phone pairs again once.

## Decisions for the owner

1. **Approval:** type the six digits (recommended), or compare and press `y`.
2. **When a computer listens:** only while `pterm setup` or `pterm approve` waits (recommended), or always.
3. **Discovery:** home's broker asks each machine `/healthz` (recommended), or no probe and a list of every machine of the user.
4. **Fresh database** on update (recommended; rule 26), or a marker without a schema change.

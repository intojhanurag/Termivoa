# Found on your network: design

Status: **proposed, version 1 (2026-10-03). The owner approved the idea; this design is under review (AGENTS.md rule 24). No code exists yet.** ADR [0007](decisions/0007-found-on-network.md). Builds on [multi-computer.md](multi-computer.md) (ADR [0005](decisions/0005-multi-computer.md)).

Goal, in the owner's words: "we should just login to tailscale then show all devices and just add". A computer of the same Tailscale user that runs Termivoa shows in **Settings → Computers** under **Found on your network** with an **Add** button. No link, no QR, no copy and paste. The link, paste and scan flows stay.

Words used here: **home** serves the app (for example `mac`). The **new computer** is the one to add (for example `vps`). A **requested pairing** is a pending pairing that the phone started without a grant.

## 1. What the code does today (evidence)

| Fact | Where |
| --- | --- |
| A pending pairing exists only after a grant was redeemed. The grant is a 256-bit secret that the owner of the new computer made on it, 2 minutes, single use. | `internal/auth/service.go:138-157`, `:163-209`; `internal/auth/expiry.go:10` |
| `pterm pair --via` resolves home in a fresh status of the new computer, applies the owner rule and pins node ID, owner and tags. | `internal/broker/computers.go:170-183` (`resolveVia`); `internal/tailnet/status.go:184-195` (`CheckOwner`) |
| Redeem and poll pass only from the origin the grant or pending pairing is bound to, and only while the snapshot trusts the pinned node. | `internal/broker/computers.go:96-114` (`pairingCheck`), `:59-72` (`appTrust`) |
| The gateway gives CORS on `/api/v1/pair/*` only to origins of unexpired bound grants and pending pairings, which the broker reports in the origin probe. | `internal/gateway/handler.go:289-304`; `internal/broker/computers.go:138-166`; `internal/store/computers.go:146-151` |
| After an approved poll the gateway probes again at once, so the app's next request has CORS. | `internal/gateway/api.go:78-83` |
| Every later request of the device is bound to its app origin and pinned node. | `internal/broker/computers.go:74-83` (`admitDevice`) |
| Approval is local: the CLI shows the code and asks `y/N`, or `pterm pair approve <pending-id>`. It needs no code. | `internal/cli/admin.go:254-263`, `:265-312`, `:331-342`; `internal/auth/service.go:216-218` |
| The code is 6 digits (`NNN-NNN`), uniform, "comparison only, never authorization". | `internal/auth/service.go:692-706`; `protocol/v1/authentication-rpc.md`, "Pairing limits and lifetimes" |
| Pending pairings: at most 4 **in total** (the count has no filter), 5 minutes, deleted after 5 wrong poll secrets. Broker limits: 30 redeems and 120 polls per minute. | `internal/auth/service.go:21-26`, `:126-127`; `internal/store/auth.go:186-192` |
| Gateway limits on redeem (5, then 1 per 12 s) and poll (10, then 1 per s) are keyed by client address, and behind Tailscale Serve every client is `127.0.0.1`: one bucket for all. | `internal/gateway/handler.go:106-107`; `internal/gateway/api.go:41`, `:65`, `:646-658`; `docs/threat-model.md:96-101` |
| Before the phone can reach a new computer, home records it as pending for this device (home checks the host in its own status: own tailnet, same owner or tagged; pins the node), and the page loads again so the CSP allows it. | `internal/broker/computers.go:191-204`, `:244-263`; `internal/gateway/handler.go:503-547`; `web/src/pairing/addComputer.ts:119-133` |
| The status snapshot holds only own-tailnet nodes; shared-in nodes are dropped. It does not read `Online` today. | `internal/tailnet/status.go:68-74`, `:110-118` |
| `GET /healthz` is unauthenticated, outside `/api`, and already answers `{status, broker, brokerGeneration}` to every tailnet machine. Static app files are unauthenticated too. | `internal/gateway/handler.go:127`, `:402-422`, `:448-494` |
| The only outbound requests of the broker go to push services, behind a guard: exact hosts, https 443, no redirects, no proxy, public addresses only, 10 s. | `internal/webpush/webpush.go:111-132`; `docs/threat-model.md:45` |
| Forwarded identity headers are ignored; Tailscale is never authentication. | `docs/implementation-plan.md:232`; `docs/threat-model.md:82-84`; `protocol/v1/http-api.md`, "Common rules" |
| `pterm setup` already ends with a pairing step that waits. | `cmd/pterm/lifecycle.go:653-657` |
| A schema change needs a fresh database; there are no migrations. | `internal/store/migrations.go:18-22`; AGENTS.md rule 26 |

## 2. The flow

### What the user does

1. On `vps`: install Termivoa and run `pterm setup`. Its last step now waits: "Waiting for your Termivoa app to add this computer…".
2. On the phone: **Settings → Computers** shows **Found on your network**: `vps` — **Add**.
3. Tap **Add**. The sheet **Add this computer?** shows address, machine and owner. Tap **Add vps**. The phone shows a code.
4. On `vps`: type the code. `vps` is in the app.

If `pterm setup` is no longer waiting, step 4 needs one command first: `pterm approve`. The order does not matter: the app keeps asking for 2 minutes.

### What the machines do

```
phone (app from mac)            mac (home)                       vps (new computer)
  |  GET /api/v1/computers/found   |                                  |
  |------------------------------->|  status: same owner, online      |
  |                                |  GET https://vps…/healthz ------>|
  |<-- [{origin vps, revision 3}]--|<-- {status, broker, revision} ---|
  |  tap Add                       |                                  |
  |  POST /api/v1/computers        |                                  |
  |   {origin, label, pending} --->|  lookupComputer, pin node        |
  |<-- node (owner)  --------------|                                  |
  |  tap "Add vps": load /computers/add#origin=…   (new CSP)          |
  |                                |             `pterm approve` or   |
  |                                |             setup waits: window  |
  |  POST https://vps…/api/v1/pair/request {deviceLabel}  ----------->|
  |                                |   gateway: gateway.pair.admit    |
  |                                |   broker: window, snapshot,      |
  |                                |   same owner, pin node, limits   |
  |<-- pendingId, pollSecret, verificationCode, expiresAt ------------|
  |  shows the code                |             CLI: who asks;       |
  |                                |             user types the code  |
  |  POST /api/v1/pair/poll  ---------------------------------------->|
  |<-- approved + Set-Cookie (device bound to mac's origin, node) ----|
  |  GET /api/v1/settings (first authenticated read) ---------------->|
  |  POST /api/v1/computers {confirmed} -->|                          |
```

Everything from the poll on is the code of today.

## 3. Rules on home

### H1. `GET /api/v1/computers/found`

- Cookie required. Own origin only (`admitOwn`, as for the other computer routes). A bound app origin gets `invalid_request`.
- The broker reads a fresh status (`Tailnet.Fresh`: cached under 60 s, else one `tailscale status`, 10 s limit), as `lookupComputer` does.
- Candidates: nodes in `status.peers` (own tailnet only; never shared-in) that are **online**, **not tagged**, with **the same owner as home** (`CheckOwner(node, false)`), and not already a confirmed computer of the calling device. If home itself is tagged, there are no candidates. Sorted by host; at most **16**.
- Each candidate gets one probe (H2), at most 4 at a time. The whole answer is cached in memory for 30 s per broker (one probe run at a time; a second caller waits for it).
- Answer: `HTTPComputerFoundResponse { computers: [ { origin, protocolRevision } ] }`, at most 8 entries. `protocolRevision` is `0` when the computer answers as Termivoa but sends no revision (an older build).
- Nothing a probed machine sends is shown. The label is the first DNS label of the origin, which home took from its own status.

### H2. The probe (the new outbound request of the broker)

| Bound | Value |
| --- | --- |
| Who can cause it | Only a paired device, from home's own origin, by opening **Settings → Computers** or **Look again**. Never a timer. |
| Target | `https://<host>/healthz`, where `<host>` is the MagicDNS name of a candidate from home's own status. No part of the URL comes from the request. |
| Address | The dial is refused unless the resolved address is a Tailscale address (`100.64.0.0/10` or `fd7a:115c:a1e0::/48`). The certificate must be valid for `<host>` (system roots). |
| Redirects | Never followed: a 3xx counts as "not Termivoa". |
| Proxy, cookies, credentials | None. `GET` only, no body, no custom headers. |
| Time and size | 2 s per machine; body read up to 2 KiB; no keep-alive. |
| What counts | Status 200, `Content-Type: application/json`, an object with `status` and `broker` as strings. `protocolRevision` is read when it is an integer in 1..1 000 000. Anything else: not found. |

Compared with the push guard: push refuses private addresses and allows only named public hosts; this probe is the reverse and allows only tailnet addresses of nodes from home's own status. The input is not chosen by a client, so the usual SSRF path (a client names the URL) does not exist here.

### H3. The rest is unchanged

**Add** runs today's path: `POST /api/v1/computers` with `state: pending` (home's own check and node pin, `lookupComputer`), the confirm sheet, a full load of `/computers/add`, later `state: confirmed` after the first authenticated read. Only confirmed computers enter `frame-src`.

## 4. Rules on the new computer

### P1. `GET /healthz`

Gains `protocolRevision` (integer). Nothing else changes. It does not say whether the computer is waiting.

### P2. The window

- The computer accepts pairing requests only while a local command waits for one. The command sends `admin.pair.pending` with `listen: true` about once per second; the broker keeps `listenUntil = now + 15 s` in memory. When the command ends or dies, the window closes within 15 s. A broker restart closes it.
- Commands that open it: `pterm approve` and the last step of `pterm setup`. Both wait at most 10 minutes.
- Outside the window the broker refuses before it reads the status or the database.

### P3. Admission (`gateway.pair.admit`)

For `POST` and its preflight `OPTIONS` on `/api/v1/pair/request`, the gateway requires exactly one `Origin` that is a canonical computer origin (`protocol.ValidateComputerOrigin`) and not its own, then asks the broker `gateway.pair.admit {origin}`. The broker answers `admitted: true` only when all hold:

1. the window is open;
2. the latest snapshot exists (`Tailnet.Snapshot()`; it never runs `tailscale` here);
3. the origin's host is a node in `status.peers` (own tailnet, not shared-in, not this computer);
4. neither that node nor this computer is tagged, and the node's owner user ID equals this computer's (`CheckOwner(node, false)`).

Admitted: the gateway sends exact CORS headers (as for bound origins) and goes on. Not admitted: `403 invalid_request` "origin not allowed" **without CORS headers**, the same answer as for any unknown origin today. So for a browser a closed window, another owner and a computer without this route all look the same, and nothing is stored. The gateway does not cache the answer and does not use its `pairing` origin set for this route.

The existing rules still apply first: the Host check, `Sec-Fetch-Mode: no-cors` refused, the preview origin refused, then `X-Pterm-Request: 1`, JSON, 64 KiB, strict decoding.

### P4. The request (`gateway.pair.request`)

The gateway takes one token from a **new** limiter of this route (5, then 1 per 12 s; its own bucket, so it never uses tokens of redeem or poll), checks `deviceLabel`, and forwards the request with its `Origin`. The broker, in this order:

1. repeats every check of P3 itself (it never trusts the gateway's earlier answer) and builds the app binding from the snapshot: origin, node ID, owner, tags — the same value `resolveVia` builds;
2. takes one of **12 requests per minute** (a new broker-wide window, as for redeem);
3. in one transaction: deletes expired rows; counts unapproved pending pairings **without a grant**; refuses with `limit_exceeded` when there are 4; inserts a pending pairing with no grant, the binding, a new poll secret (256 bit, stored as a hash), a new verification code that no other pending pairing has, and `expires = now + 2 minutes`.

Answer: `HTTPPairRedeemResponse` (`pendingId`, `pollSecret`, `verificationCode`, `expiresAt`). The gateway then probes origins at once (as after an approved poll), so the poll's preflight from that origin passes.

A request from this computer's own origin is refused (`invalid_request`): own-origin pairing has no app binding and keeps the link flow.

### P5. Poll, cookie, device

Unchanged. The poll passes only from the bound origin while the snapshot trusts the pinned node (`pairingCheck`); 5 wrong poll secrets delete the pending pairing; the approved poll makes the device with `devices.app_origin` and `app_node_id` from the pending pairing and sets the cookie once.

### P6. Approval (`admin.pair.approve_code`)

- `admin.pair.pending` lists requested pairings with `requested: true` and **without** `verificationCode`. The code of a requested pairing never leaves the broker except in the answer to the request that made it.
- `admin.pair.approve_code {code}`: the broker finds the unexpired, unapproved requested pairing with that code and approves it (same store call as today). No match: `pairing_expired`.
- `admin.pair.approve {pendingId}` refuses a requested pairing ("type the code: run `pterm approve`"). So no path approves a requested pairing without the code that only the requester's screen shows.
- Refusing is doing nothing: the pairing ends after 2 minutes. `pterm revoke --via <origin>` deletes requested pairings of that origin too (`DeletePairingsVia`).

The code is still not authorization on the wire: no network client can submit it. Only the local owner can type it into the local command, and the phone still needs its poll secret.

## 5. CLI

`pterm approve [--profile …]` (new top-level command; it is what the app tells the user to type).

```
$ pterm approve
Waiting for your Termivoa app to add this computer (Ctrl+C stops waiting).
In the app: Settings → Computers → Found on your network → Add.
```

When one request is waiting:

```
Phone "iPhone" wants to add this computer to the Termivoa app at mac (https://mac.tailx.ts.net, owner ada@example.com).
Go on only if you tapped Add in that app just now.
Type the code that the phone shows (Enter refuses): 
```

When several are waiting, each is one line, then one prompt:

```
3 requests are waiting:
  Phone "iPhone", app at mac (https://mac.tailx.ts.net, owner ada@example.com)
  Phone "x", app at laptop (https://laptop.tailx.ts.net, owner ada@example.com)
  Phone "iPhone", app at mac (https://mac.tailx.ts.net, owner ada@example.com)
You made only one of them. Type the code that YOUR phone shows (Enter refuses): 
```

| Input | Output |
| --- | --- |
| The code of a waiting request (`123456`, `123-456` or `123 456`) | `Approved. Phone "iPhone" can now use this computer from the Termivoa app at mac (https://mac.tailx.ts.net).` Exit 0. |
| A code no request has | `No request has that code. Look at the phone and type it again.` Three tries, then `Not approved.` and exit 1. |
| Enter | `Not approved. The request ends in 2 minutes.` Exit 0. |
| Nothing for 10 minutes | `No request came. In the app tap Add, then run pterm approve again.` Exit 1. |
| Tailscale status not readable | `This computer cannot read its Tailscale status; check Tailscale and retry.` Exit 1. |
| This computer is a tagged node | `This computer is a tagged Tailscale node. Use pterm pair --via <home> --allow-tagged.` Exit 1. |

Machine names, owners and labels are printed through the existing `describeApp` and `displayText`. The prompt reads one bounded line with the existing cancellable reader (`internal/cli/confirm.go`).

`pterm setup`: the parallel work makes setup ask "What is this computer? 1) my first computer 2) add it to the app I already use". For answer 2, after it prints its link and QR, setup runs the same wait as `pterm approve` in the same loop as today's `waitForPhone`: a pairing from the link gets today's `y/N` prompt; a request from the app gets the code prompt. First to come wins. Text before the wait:

```
Or add it with no link: in the Termivoa app open Settings → Computers and tap Add next to "vps".
Waiting for your app (Ctrl+C stops waiting; later run pterm approve).
```

`pterm pair pending` shows requested pairings with `(typed)` in the CODE column. `pterm doctor` needs no change (it already lists pending pairing origins).

## 6. Phone screens and texts

**Settings → Computers**, between the list and **+ Add a computer** (where the parallel work puts **Paste link** and **Scan QR**):

| State | Text |
| --- | --- |
| Heading | `Found on your network` |
| Loading | `Looking for your computers…` |
| Row | `vps` · `vps.tailx.ts.net` · button **Add** |
| Row, `protocolRevision` below 3 | `vps` · `Update Termivoa on vps to add it here, or run pterm pair --via mac there.` No button. |
| None found | The heading and rows are hidden. One line stays: `New computer? Run pterm setup on it. It then shows here.` with the link **Look again**. |
| List full | `This phone already has the most computers allowed (8).` No buttons. |
| Home cannot read Tailscale | `mac cannot read its Tailscale status. Check Tailscale on mac.` |
| Help line under the rows | `Computers in your tailnet that are yours and run Termivoa.` · **Look again** |

**Add** opens today's sheet **Add this computer?** (Address, Machine, Owner; `AddComputerSheet`). Only the hint text differs for an entry without a link:

> Next, this phone shows a code. Type it on vps: a new `pterm setup` asks for it; else run `pterm approve` there.

Buttons: **Add vps**, **Cancel** (deletes the pending entry on home, as today).

**Add vps** loads `/computers/add#origin=…&back=…` (no grant in the fragment). The sheet sets a one-time mark in `sessionStorage` (the origin only, no secret); with the mark the page starts at once, without the mark it shows **Continue** first, as today.

| Phase | Title and text |
| --- | --- |
| Asking | `Add a computer` — `Asking vps… On vps, run pterm approve (a new pterm setup already waits).` The app sends the request again every 3 s for up to 2 minutes while there is no answer. **Cancel** |
| Code | `Type this code on vps` — the code tiles — `vps asks for it in the terminal.` — `Waiting for vps` — **Cancel** — **Sessions** / **Back to terminal** (as today, the add keeps running) |
| Done | `vps is added` — `Its sessions now show in your list.` — **Open sessions** |

Failures:

| What happened | What the phone shows |
| --- | --- |
| The computer is off, or is not waiting, for 2 minutes | `vps did not answer. On vps, run pterm approve, then tap Add again. If vps is off, turn it on first.` |
| The computer is too old | No **Add**; the row text above. |
| Nobody typed the code in 2 minutes, or the user pressed Enter on the computer | `The code expired or vps refused it. Tap Add again.` |
| A wrong code was typed on the computer | Nothing changes on the phone; the computer asks again. |
| Limits (`429`) | `Too many attempts on vps. Wait a minute, or use pterm pair --via mac there.` |
| Home refuses the entry (other owner, name moved to another machine, list full) | Home's text in the sheet, as today. |
| The computer needs a newer app (first settings read) | Today's `computer_outdated` / `app_outdated` text. |
| **Cancel** after approval | As today: the phone signs out on the computer and home deletes the entry. |

## 7. Answers to the hard questions

### A. Discovery

- **Who lists:** home's broker, from the status it already reads. **Who checks "runs Termivoa":** home's broker, with the probe of H2. The phone does not probe: that needs CORS on an unauthenticated route of every computer, so any page on any machine of the tailnet (same site) could list the user's Termivoa computers through the user's browser, and home's CSP `connect-src` would have to allow every candidate before the user adds it. Today the CSP holds only computers this phone added (`handler.go:496-502`).
- **What the answer reveals:** that the machine runs Termivoa, whether its broker is up (already in `/healthz`), and the protocol revision (new). No name, no owner, no build text, not whether it is waiting. Every tailnet machine that can reach port 443 can already see `/healthz` and the app's files (which carry the build). So the revision is no real new fact. Accepted.
- **Shown:** own tailnet, same owner as home, online, not tagged, not home, not already confirmed for this phone, and answering as Termivoa.
- **Not shown:** tagged nodes (they need `--allow-tagged` on the new computer; link flow), shared-in nodes (never in the snapshot), nodes of other users, offline nodes (they show at the next **Look again** when online). A tagged home finds nothing.

### B. The request

- **What admits it:** P3. The gateway gives CORS only after the broker said yes for this exact origin, now; the broker checks again on the request itself.
- **`Origin` can be forged** by a program on any machine that can reach port 443 of the new computer. In the window it gains: one pending pairing (of at most 4), bound to the forged origin and its pinned node, with a code and a poll secret that only the forger knows. Outside the window it gains nothing and nothing is stored.
- **What stops approval of the wrong request:** the typed code (P6). The user types the code from the phone. The forger's request has another code (codes are unique among pending pairings), so it is never the one approved. The forger cannot make its code show on the user's phone unless it controls the app, which is the compromised-home case (E).
- **A browser cannot forge `Origin`.** So a page on another owner's machine can never start a request through the user's browser. A page on another machine **of the same user** can; its request names that machine in the prompt, and the user would have to read a code from that page and type it.
- **Source of the connection:** not checked. Tailscale Serve headers name the Tailscale user of the **phone** (the phone connects to the new computer directly), not home, so they cannot prove which app asks. They would narrow senders to devices of the same user, but: the gateway cannot tell Serve from another local client on `127.0.0.1` (the headers prove nothing against local users); the rule so far is that these headers are ignored (`docs/implementation-plan.md:232`) and the threat model says a use needs its own trust decision (`docs/threat-model.md:96-101`); and with the typed code a forged request gains nothing that can be approved. The gain would be less noise in team tailnets only. Not worth a new trust dependency now. It stays a possible later step if floods become a real problem.

### C. Approval on the new computer

- **What and where:** the six digits, in `pterm approve` or the waiting last step of `pterm setup` (section 5). The prompt names the phone, the app computer, its full origin and its owner **before** the user types.
- **Several requests:** all are listed; the typed code selects exactly one. The user never picks from a list.
- **Fresh install needs no extra command:** setup waits.
- **Code:** 6 digits from `crypto/rand` (`verificationCode`), unique among pending pairings. No network client can submit a code, so there is no remote guessing and no attempt counter on the wire. The local command allows 3 tries (a typing aid, not a security limit: local administration is trusted).
- **Expiry:** 2 minutes, like a grant. **Single use:** approval and the approved poll are each once, as today (`ApprovePendingPairing`, `CompletePendingPairing`).
- **Binding:** `pending_pairings.app_origin`, `app_node_id`, owner and tags, written by the new computer's broker from **its own** snapshot at request time; copied to `devices.app_origin` and `app_node_id` by the approved poll; checked on every later request (`admitDevice`, `appTrust`). The same binding as with `pterm pair --via`.

### D. Abuse

| Attack | Bound |
| --- | --- |
| Requests while no command waits | Refused in memory. One broker call per request, no status read, no database, no token used, no CORS. The same cost class as a request with a bad cookie today. |
| Flood in the window | Gateway: own bucket (5, then 1 per 12 s). Broker: 12 per minute. Rows: at most 4, each 2 minutes. Counted apart from link pairings (the count of `store/auth.go:186-192` gets a filter), so link pairing is never blocked by it. Paired phones are not affected. |
| Flood of preflights | One in-memory broker call each. |
| Poll bucket | Shared with link pairing, as today (known limit, not made worse). |
| Unasked prompts | Never. Requests show only inside `pterm approve` or the setup wait, which the user started. |
| Approval fatigue | There is no `y` for a requested pairing. A wrong request needs the user to type **its** code, which the user's phone does not show. |

**Honest limit:** while a flood runs during the window, the user's own request can get `429` or find 4 rows taken, so **Add** fails. The user then uses `pterm pair --via`. This is the same class as the known limit of the pairing routes; it does not touch them.

**When to accept requests — recommendation: only in the window.** Always-on would let any tailnet machine keep 4 forged rows on every Termivoa computer at all times and fill every later `pterm approve` list with them. The window costs the user nothing on a fresh install (setup waits) and nothing later either: to approve, the user must run a command on the computer in both designs, and the app keeps asking until that command runs. The real cost is code: one optional field, one time value in memory, a retry loop in the app. "Only when enabled by a setting" adds a setting and a step; rejected.

### E. Compromised home

A compromised home could already drive every paired peer and show a false "pair again" (`docs/multi-computer.md`, "Honest trust statement"). New with this feature:

- It can send requests to every Termivoa computer of the owner, at any time, with no grant. They are refused unless the user runs `pterm approve` or `pterm setup` there. No approval, no access.
- Its false story is shorter: "run `pterm approve` on vps and type 123-456". Before, the user had to run `pterm pair --via mac` and move a link. The anchor is the same: a command the user runs on that computer, and a prompt that names the app computer.
- In a real add it can show the code of **its own** request (made by a program, not the phone). The user types it, and home holds a raw cookie that works without the phone. This exists today with a grant (finding 26 of `multi-computer.md`).
- **The same holds for any computer of the same Tailscale user**, not only home: the new computer cannot know which one is the real home. Before, the user named home (`--via mac`). Now the prompt names it, and the user must read it.

What the prompt shows so the user can tell: the phone's name, the app computer's machine name, full origin and owner, and the sentence "Go on only if you tapped Add in that app just now."

Recovery is unchanged: `pterm revoke --via <origin>` on each peer, clean home, clear the app's data, pair again.

### F. Protocol, storage, CLI, screens

Sections 5, 6 and 9.

### G. Simpler designs

| Design | Code | Safe? | Result |
| --- | --- | --- | --- |
| **1. Chosen:** home probes; phone asks in the window; typed code | Small: 2 HTTP routes, 3 gateway RPCs, 1 admin RPC, 1 field, 1 probe client, 1 command | Yes | **Recommended** |
| 2. No probe: list every same-owner online machine; **Add** on a machine without Termivoa fails with a text | About 100 lines of Go less; no outbound request | Yes | The list shows a NAS or a phone. The app cannot tell "no Termivoa" from "not waiting" (both are silent by design, P3), so the failure text is vague. Fallback if the owner refuses the probe. |
| 3. The new computer finds home in `pterm setup` and makes the bound grant itself | — | No | The grant has no channel to the phone. If home's broker fetches it, the new computer gives a secret to whoever says it is home (any tailnet machine), or it trusts Tailscale source identity as authentication. If the phone fetches it, the grant protects nothing and the design is design 1 with an extra step. |
| 4. Always-on, `y` to approve (today's prompt) | Least CLI code | No | A waiting forged request plus one careless `y` is a shell. Today that needs the grant. |
| 5. The new computer shows a code; the user types it in the app | Similar | Weak | A 6-digit secret on the wire: guessable without a lock-out, and a lock-out is a tool for an attacker. More typing on the phone. |

Design 1 is the least code among the safe ones that show only computers that run Termivoa. Inside it, these were cut: no cancel route (a request ends by itself), no deny RPC, no per-origin slot, no setting, no source check, no new table.

## 8. Trust statement (to add to `docs/multi-computer.md`)

> **Found on your network.** A computer that waits in `pterm setup` or `pterm approve` accepts a pairing request from **any computer of the same Tailscale user in the same tailnet**, and a program on any machine that can reach it can pretend to be one. Such a request gives no access. Access needs the six digits that the requester's screen shows, typed by you on that computer. So: type a code only when you tapped **Add** in your own app just now, and read the app computer's name in the prompt first. A compromised home, or a false Termivoa page on another of your computers, can ask you to run `pterm approve` and type its code; if you do, it gets that computer. Tailscale decides who "the same user" is: this check only narrows who can ask and is never authentication. While someone floods a waiting computer with requests, **Add** can fail; use `pterm pair --via` then.

The first sentence of the statement stays true and unchanged: if home is compromised, every computer paired through it is compromised too.

Also to update: `docs/threat-model.md` (table rows "Home broker → tailnet nodes (outbound probe)" and "App origin → pairing request"; the "Multi-computer" attacker list; the pairing known limit gets the new route's bucket) and `docs/architecture.md` section 6 (Pairing and Multi-computer rows).

## 9. Protocol and storage changes (rule 6)

Additive within v1; regenerate Go and TypeScript; `protocol/v1/revision.json` → `revision: 3`, `minRevision` stays 1. The app sends `pair/request` only to a computer whose found `protocolRevision` is 3 or more.

| File | Change |
| --- | --- |
| `http-api.schema.json` | `HTTPPairRequestRequest { deviceLabel }` (answer: existing `HTTPPairRedeemResponse`); `HTTPFoundComputer { origin, protocolRevision (0..1000000) }`; `HTTPComputerFoundResponse { computers (max 8) }`; `HTTPHealthResponse` gains `protocolRevision` if the health body is in the schema, else documented in `http-api.md` |
| `http-api.md` | Routes `POST /api/v1/pair/request` (auth: none; admitted origin in the window) and `GET /api/v1/computers/found` (cookie; own origin); "Cross-origin requests" gets the admission rule of P3; "Pairing flow" gets the requested flow; `/healthz` body |
| `gateway-rpc.schema.json` | `gateway.pair.admit { origin } → { admitted }`; `gateway.pair.request` (`HTTPPairRequestRequest` + forwarded origin → `HTTPPairRedeemResponse`); `gateway.computers.found → HTTPComputerFoundResponse` |
| `admin-rpc.schema.json` | `admin.pair.pending` payload gains optional `listen` (boolean); `AdminRPCPendingPairing` gains `requested` (boolean) and `verificationCode` becomes optional (absent when requested); new `admin.pair.approve_code { code } → AdminRPCPendingPairing` |
| `authentication-rpc.md` | The RPCs above; limits table: "Requested pairing: no grant; at most 4 unapproved; 2 minutes; 12 requests per minute per broker; approved only by its code" |

Storage (`internal/store/migrations/001_schema.sql`): `pending_pairings.grant_id` becomes `TEXT UNIQUE CHECK (grant_id IS NULL OR length(grant_id) = 36)`; NULL means requested. No new table or column. **This needs a fresh database on every computer (rule 26): every phone pairs again once.** The window is memory only.

New constants (`internal/auth`): `MaxRequestedPairings = 4`, `RequestedPairingLifetime = 2 * time.Minute`, `RequestAttemptsPerMin = 12`, `ListenGrace = 15 * time.Second`.

## 10. Implementation plan

Order: **P** first and alone. Then **H1**, **H2** and **W** in parallel (H2 and W use fakes until H1 lands). Then **T**. No file is in two tracks. W starts after the parallel "Scan QR" and "Home badge" work is merged (it edits `Computers.tsx` and `AddComputer.tsx` too); H2's `cmd/pterm/lifecycle.go` change starts after the parallel setup-question work is merged.

| Track | Files | Work |
| --- | --- | --- |
| **P. Protocol** | `protocol/v1/http-api.schema.json`, `gateway-rpc.schema.json`, `admin-rpc.schema.json`, `revision.json`, `http-api.md`, `authentication-rpc.md`; generated `internal/protocol/generated.go`, `web/src/generated/protocol.ts`; `internal/protocol/auth_rpc.go`, `computers.go` (+ tests) | Section 9; validators and bounds; drift check |
| **H1. Broker (Go)** | `internal/store/migrations/001_schema.sql`, `internal/store/auth.go`, `models.go` (+ tests) | Nullable grant; `RequestPairing` (prune, count without grant, unique code, insert); the redeem count gets `grant_id IS NOT NULL`; `ApproveRequested(code)`; `ApprovePendingPairing` refuses a row without a grant |
| | `internal/auth/service.go` (+ test) | `Listen(now)`, `Listening()`; `RequestChecked(label, app)`; `ApproveCode(code)`; the new rate window; constants |
| | `internal/tailnet/status.go` (+ test) | `Online` on `rawPeer` and `Node`; `Candidates()` (online, same owner, not tagged, sorted, max 16) |
| | `internal/broker/computers.go` (+ test) | `admitRequest(origin)` (P3, snapshot only); `requestBinding` shared with `resolveVia`; `foundComputers` (H1) |
| | **new** `internal/broker/found.go` (+ test) | The probe client (H2): dial guard, no redirects, limits, 30 s cache, single flight |
| | `internal/broker/gateway_rpc.go`, `admin_rpc.go` (+ tests), `internal/gatewayclient/*`, `internal/gatewayapi/*`, `internal/localclient/*` | The three gateway RPCs; `listen` on pending; `approve_code`; requested entries without code; error texts |
| **H2. Gateway and CLI (Go)** | `internal/gateway/handler.go`, `api.go`, `fake_broker_test.go`, `multicomputer_test.go`, `handler_test.go` | Routes; admission by `gateway.pair.admit` for `POST` and `OPTIONS` (no cache, no `pairing` set on this route); own limiter; probe refresh after a successful request; `/healthz` revision; `computers/found` handler (own origin, generic errors) |
| | `internal/cli/admin.go`, `confirm.go` (+ tests) | `Approve` wait loop (shared with `waitForPhone`); `Ask` (one bounded line); the texts of section 5; `(typed)` in `PairPending` |
| | `cmd/pterm/main.go`, `admin.go`, `lifecycle.go` (+ tests) | `pterm approve`; setup's wait for answer 2; usage text; a development-only way for the harness to route the probe (see T) |
| **W. Web** | `web/src/api/http.ts`, `web/src/pairing/pairing.ts`, `addComputer.ts`, `web/src/computers.ts`, `web/src/screens/Computers.tsx`, `AddComputer.tsx`, `App.tsx` (fragment without grant), styles (+ tests) | `pairRequest`, `foundComputers`; `AddComputerLink.grant` optional; `PairingFlow.request` with the 3 s retry for 2 minutes; the **Found on your network** block; texts of section 6; the one-time mark |
| **T. Harness and docs** (after H1, H2, W) | `scripts/e2e-multicomputer/*` | Section 11 |
| | `docs/multi-computer.md`, `docs/threat-model.md`, `docs/architecture.md`, `docs/pending-work.md`, `docs/progress.md`, `docs/owner-verification.md`, `CHANGELOG.md`, `README.md`, `site/src/content/docs/docs/**` (guide `computers`, `reference/cli.md`, `security.md`, `troubleshooting.md`, `release-notes.md`) | Rules 22, 23, 25; no internal detail on the public site |

Each helper brief must carry rules 21, 22, 24 and 26.

## 11. Test plan

### Unit tables

| Area | Cases |
| --- | --- |
| `tailnet` | `Candidates`: offline, tagged, other owner, shared-in, self, tagged self, more than 16, order |
| Probe (`found.go`, against `httptest`) | Termivoa shape with and without revision; wrong shape; non-JSON; 3xx never followed; body over 2 KiB; slow answer; non-tailnet address refused by the dial guard; cache for 30 s; one run for two callers |
| `admitRequest` | Window closed; no snapshot; own origin; other owner; tagged node; tagged self; shared-in; near-miss host; `null`; upper case; port; trailing dot; name not in status; same owner (yes) |
| `store` and `auth` | 4 requested rows then `limit_exceeded`; requested rows do not count for redeem and the reverse; expiry at 2 minutes; unique codes; rate window 12 per minute; `ApproveCode`: match, no match, expired, already approved, a code of a link pairing (no match); `Approve(id)` refuses a requested row; approved poll makes a device with the binding; 5 wrong poll secrets delete it; `RevokeVia` deletes requested rows; the window ends 15 s after the last `listen` and at restart |
| Gateway | Not admitted: 403 with no CORS header on `POST` and `OPTIONS`, and the body is the same as for an unknown origin; admitted: exact CORS, credentials true; two `Origin` headers; `no-cors`; missing `X-Pterm-Request`; the route's limiter does not use redeem tokens (flood the route, then one redeem passes); refresh after success so the poll preflight passes; a cookie on the request changes nothing; `/healthz` body; `computers/found` refuses a bound app origin |
| CLI | Prompt texts; one and several requests; code spellings; 3 wrong codes; Enter; timeout; a link pairing and a request in one setup wait; control characters in labels and owners are escaped |
| Web | Found block states; a row below revision 3; **Add** → sheet → page; the retry loop stops on Cancel and at 2 minutes; failure texts; no grant in the fragment; the mark is used once; a peer's error text is never shown |

### `scripts/e2e-multicomputer` must prove

The fake Tailscale CLI gets `Online` in its status files. A development-only setting routes the broker's probe through the harness front (the front stands in for MagicDNS and TLS, as it does for browsers); production refuses that setting.

| Check | What it proves |
| --- | --- |
| `F-list` | The mac app shows `vps` under **Found on your network**; not `evil` (owner mallory), not a tagged node, not an offline node, not a host of the front that is not Termivoa, not `vps` after it was added |
| `F-closed` | No command waits on vps: `POST /api/v1/pair/request` from the mac origin (browser and curl) gets 403 with no CORS header; `pterm pair pending` on vps is empty; the page keeps asking |
| `F-add` | Phone flow through the screens (Chrome and WebKit): **Add** → sheet → code; `pterm approve` on vps with that code; the computer is in the list; its sessions show; the device on vps has `Via` mac |
| `F-evil-origin` | In the window: a request with `Origin: https://evil.tailx.ts.net` (curl, and a real page on evil) is 403 with no CORS and stores nothing |
| `F-forged` | In the window: curl with a forged `Origin: https://mac.tailx.ts.net` gets its own code. The phone's request gets another. Typing the phone's code approves only the phone's: the forged poll secret never gets a cookie, and its row ends |
| `F-no-y` | `pterm pair approve <id>` of a requested pairing is refused; `pterm pair pending` shows no code for it |
| `F-flood` | In the window: 50 forged requests. At most 4 rows on vps; then a link pairing (`pterm pair --via mac`) on vps still completes; a paired phone's requests are not slowed |
| `F-wrong-poll` | The mac origin polls a requested pairing from evil's origin: refused (bound origin) |
| `F-expire` | No code typed for 2 minutes: the phone shows the expiry text; the row is gone |
| `F-node` | mac's node ID changes on vps between request and poll: the poll is refused |
| `F-setup` | `pterm setup` (answer 2) on a fresh vps waits; **Add** from the app; the code typed into setup completes it |
| `F-old` | A vps whose `/healthz` has no revision shows the update text and no **Add** |

### Real-phone checks (owner; `docs/owner-verification.md`)

- A real iPhone Home Screen app and Android: the whole flow over a real tailnet, two real computers, real Tailscale Serve.
- The broker's probe under launchd and systemd: MagicDNS names resolve and the Let's Encrypt certificate of a Serve host verifies from a background process.
- A real `tailscale status --json`: the `Online` field, a team tailnet with a second user (that user's machine is not listed and cannot ask), a tagged node.
- The typed code from a second device (SSH from a laptop) and inside a Termivoa session on home (`ssh vps`, then `pterm approve`, with the add running behind its banner).

## 12. Open risks

1. **Same-owner is the whole admission.** A hostile program on any computer of the user can ask. The typed code and the prompt text are the defence. A user who types whatever a screen tells them loses that computer.
2. **Probe under launchd/systemd** (DNS and certificate) is not verified; if it fails, discovery shows nothing and the link flow still works.
3. **Flood in the window** stops this flow (stated above).
4. **`Online` can be stale** in `tailscale status`; a stale "online" costs one 2 s probe.
5. **Fresh database** on update.
6. **Three parallel changes** touch `Computers.tsx`, `AddComputer.tsx` and `lifecycle.go`; this work must land after them.

## Reviews

(empty — the security review and the feasibility review with alternatives are still to run; record each finding and its resolution here)

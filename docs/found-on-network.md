# Found on your network: design

Status: **version 2 (2026-10-03). The owner approved the idea; v2 is the chosen design after two reviews of v1. The security re-check of the push shape is pending (AGENTS.md rule 24). No code exists yet.** ADR [0007](decisions/0007-found-on-network.md). Builds on [multi-computer.md](multi-computer.md) (ADR [0005](decisions/0005-multi-computer.md)).

Goal, in the owner's words: "login to Tailscale, see all devices, Add". A computer that waits to be added shows in **Settings → Computers** under **Found on your network** with an **Add** button. No link to move. The QR code, the link and the paste flow stay.

Words used here: **home** serves the app (for example `mac`). The **new computer** is the one to add (for example `vps`). An **offer** is a bound grant that the new computer sent to home.

**The idea in one line:** the new computer already makes a single-use grant bound to the home the user named (`pterm pair --via mac`). v2 only adds a way for that grant to reach home by itself, so the phone can take it from home instead of from a link. Everything after the phone has the grant is today's code.

## 1. What the code does today (evidence)

Paths and functions are on `origin/main` unless the row says otherwise.

| Fact | Where |
| --- | --- |
| `pterm pair --via` resolves home in a fresh status (`Tailnet.Fresh`: under 60 s cached, else `tailscale status`, 10 s), applies the owner rule and pins node ID, owner and tags. | `internal/broker/computers.go` `resolveVia`; `internal/tailnet/status.go` `Resolve`, `CheckOwner` |
| The grant is a 256-bit secret, 2 minutes, single use, stored only as a hash, bound to the app origin and pinned node. At most 8 unredeemed grants. | `internal/auth/service.go` `CreateBoundGrant`, `MaxOutstandingGrants`; `internal/auth/expiry.go` `PairingGrantLifetime`; `internal/store/auth.go` `CreatePairingGrant` |
| `admin.pair.grant` is the only delivery of a grant today: to the local CLI. | `internal/broker/admin_rpc.go` (`case protocol.AdminPairGrant`) |
| Redeem and poll pass only from the bound origin, and only while the snapshot still trusts the pinned node. | `internal/broker/computers.go` `pairingCheck`, `appTrust` |
| The CLI approves only after the owner compares the phone's code and types `y`. The prompt names the app computer, its origin and owner. | `internal/cli/admin.go` `pairingPrompt`, `waitForPhone`, `describeApp` |
| `waitForPhone` today waits until the grant expires (2 minutes) and offers only pending pairings bound to the named app. | `internal/cli/admin.go` `waitForPhone` |
| **Add this computer?** first records a pending entry on home (home checks the host in its own status and pins the node), then a full load of `/computers/add#grant=…&origin=…` redeems on the new computer. | `web/src/screens/AddComputer.tsx` `AddComputerSheet`; `web/src/pairing/addComputer.ts` `prepareAddComputer`, `addComputerUrl`, `takeAddComputerFragment`, `AddComputerFlow`; `internal/broker/computers.go` `addComputer`, `lookupComputer` |
| Every `/api` request needs an exact own or bound `Origin`, or no `Origin` with `Sec-Fetch-Site: same-origin`. `no-cors` is refused. | `internal/gateway/handler.go` `admitAPI`, `crossOrigin`, `sameOriginFetch` |
| State-changing routes share one boundary: exact `Origin`, `X-Pterm-Request: 1`, JSON, a body limit, strict decoding. | `internal/gateway/api.go` `mutation`, `mutationLimit`, `decodeStrict` |
| Gateway limits are token buckets keyed by client address; behind Tailscale Serve every client is `127.0.0.1`, so each is one bucket. | `internal/gateway/api.go` `newLimiter`, `clientKey`; `docs/threat-model.md` (pairing known limit) |
| `POST /api/v1/computers/offer` on a home without this feature hits `admitAPI` first: no `Origin` and no `Sec-Fetch-Site` gives `403 invalid_request "origin not allowed"` (not 404). | `internal/gateway/handler.go` `admitAPI` |
| The snapshot holds only own-tailnet nodes; shared-in nodes are dropped. `Node` has no addresses today. | `internal/tailnet/status.go` `ParseStatus`, `Node` |
| The broker's only outbound requests go to push services: exact hosts, no redirects, no proxy, public addresses only. | `internal/webpush/webpush.go` `NewHTTPClient`, `publicAddressOnly` |
| Setup's last step asks "first computer, or one more?" and runs `pterm pair --via <home>` for answer 2. | `feat/setup-ux` (merges first): `cmd/pterm/lifecycle.go` `offerPairing`; `internal/tailnet/status.go` `HomeCandidates`; `internal/cli/confirm.go` `Ask`. Its question text is changing to "Is Termivoa already on your phone? 1) No, this is my first computer 2) Yes, add this computer to it" and "Which computer did you first pair your phone with?" |
| `pterm pair --via` prints a QR code for the app's own scanner; **+ Add a computer** offers **Scan QR** and **Paste link**. | `feat/setup-ux`: `internal/cli/admin.go` `pairVia`; `feat/scan-add-computer` (merges first): `web/src/screens/Computers.tsx`, `web/src/pairing/scan.ts` |

## 2. The flow

### What the user does

1. On `vps`: `pterm setup`, answer 2 ("Yes, add this computer to it"), pick `mac`. Or, when `vps` is already set up: `pterm pair --via mac`.
2. On the phone: **Settings → Computers → Found on your network**: `vps` — **Add**.
3. **Add this computer?** shows address, machine and owner. Tap **Add vps**. The phone shows a code.
4. On `vps`: the prompt shows the same code and names the app at `mac`. Type `y`. `vps` is in the app.

### What the machines do

```
vps (new computer)                       mac (home)                         phone (app from mac)
pterm pair --via mac (waits, 10 min)
 admin.pair.offer -> broker:
   resolveVia(Fresh): pin mac's node
   CreateBoundGrant (2 min, bound to mac)
   POST https://mac…/api/v1/computers/offer
   dial mac's TailscaleIPs[0]:443, TLS name mac…, no Origin
   {origin: https://vps…, grant} ------->  gateway: no Origin, header, 1 KiB, bucket
                                           broker: vps is a peer, same owner, untagged
                                           offers[vps] = {grant, nodeID, +2 min}
   <------------------------------ 204
 (again every 30 s, fresh grant)                                     GET /api/v1/computers/found (every 5 s)
                                           cookie, own origin  <------------------
                                           [{origin, label, grant, expiresAt}] -->
                                                                     tap Add: {origin, grant}
                                                                     = a pasted link (today's code):
                                           POST /api/v1/computers {pending} <-----
                                                                     load /computers/add#grant=…
 POST /api/v1/pair/redeem  <------------------------------------------------------ (Origin mac)
 pending pairing bound to mac; code -------------------------------------------->  shows the code
 prompt: compare, y
 POST /api/v1/pair/poll    <------------------------------------------------------
 approved + cookie ------------------------------------------------------------->  confirm on mac
```

Everything from the tap on **Add** is the code of today.

## 3. Rules on home

### H1. `POST /api/v1/computers/offer`

Admission in the gateway (`admitAPI`, one exception for this exact path):

- `Origin` must be **absent**. Any `Origin` header: `403 invalid_request "origin not allowed"`, with no CORS headers. Browsers send `Origin` on every `POST`, so no web page can reach this route; and the custom header and JSON type would need a CORS preflight, which this route never grants.
- The `Sec-Fetch-Site: same-origin` rule of other `/api` routes does not apply here (the sender is a program). `Sec-Fetch-Mode: no-cors` is still refused.
- `crossOrigin` returns nothing for this path: no `Access-Control-*` header ever. `OPTIONS` without `Origin` gets `405` (`allowMethod`); a browser preflight has an `Origin` and so gets the `403` above.

The handler (`api.go`), in this order:

1. `POST` only (`405` else).
2. Today's boundary with "no `Origin`" in place of "exact `Origin`": `X-Pterm-Request: 1` (`403 "missing request header"` else), JSON (`415`), body at most **1 KiB** (`413`), strict decoding into `HTTPComputerOfferRequest {origin, grant}` (`400 "malformed request body"`). These are the answers every route gives today.
3. One token from a **new** bucket: 10, then 1 per second; its own, so it never takes tokens from redeem or poll. Empty: `429 limit_exceeded "too many offers"`.
4. `origin` must be a canonical computer origin (`protocol.ValidateComputerOrigin`) and `grant` a valid secret text (`protocol.ValidateSecretText`). Else `400 invalid_request "invalid offer"`.
5. `gateway.computers.offer {origin, grant}` to the broker. The answer:

| Broker answer | HTTP |
| --- | --- |
| `{}` | `204` |
| `invalid_request` | `403 invalid_request "that computer is not yours in this tailnet"` (the gateway already sent every shape error as `400`, so a broker `invalid_request` here is always the owner check) |
| `limit_exceeded` | `429 limit_exceeded "too many offers"` |
| `host_unavailable` | `503 host_unavailable "this computer cannot read its Tailscale status; check Tailscale and retry"` |

The gateway writes these fixed texts; nothing the sender sent is echoed.

### H2. The broker's offer book (`internal/broker/offers.go`, new)

- `gateway.computers.offer` is refused if the gateway forwarded any origin (defence in depth: this route has none).
- It reads only `Tailnet.Snapshot()` (never runs `tailscale`). No snapshot: `host_unavailable`.
- The origin's host must be in `status.Peer` (own tailnet, not shared-in, not home itself), the node and home must both be untagged, and the owner must match: `CheckOwner(node, false)`. The origin must not be home's own. Else `invalid_request`.
- Memory only: `offers[origin] = {grant, nodeID, expires = now + 2 min}`. The newest offer for an origin replaces the older one. At most **16** origins: expired entries are dropped first; a 17th origin gets `limit_exceeded`. A broker restart empties the book (the new computer sends again within 30 s).
- The grant is never logged, never written to SQLite, never sent anywhere but in the answer of H3.

### H3. `GET /api/v1/computers/found`

- Cookie required; own origin only (`authorizeOwn`, like the other computer routes). `gateway.computers.found {credential}`.
- No snapshot: `503 host_unavailable` (same text as above).
- Leaves out: expired offers; an offer whose origin's host now has another node ID than the one stored (or is gone from the snapshot); computers already **confirmed** for this device (`Store.ListComputers`).
- Answer `200 HTTPComputerFoundResponse {computers: [{origin, label, grant, expiresAt}]}`, at most 16, sorted by origin. `label` is the first DNS label of the origin, which home took from its own status; nothing the sender chose except the grant is shown.
- The gateway uses `respondGeneric` (no broker text to the page).

### H4. The rest is unchanged

**Add** runs today's path: `POST /api/v1/computers` with `state: pending` (home's own `lookupComputer` and node pin), the confirm sheet, a full load of `/computers/add`, redeem on the new computer, the code, the poll, `state: confirmed` after the first authenticated read. Only confirmed computers enter `frame-src`.

## 4. Rules on the new computer

### N1. `admin.pair.offer {via, allowTagged?}` (local CLI only)

The broker:

1. `resolveVia(via, allowTagged)` with a fresh status, exactly as for `admin.pair.grant`. It now also returns the resolved `tailnet.Node`, so the dial below uses the same pinned node. Its errors are RPC errors, as today.
2. `CreateBoundGrant(app)`: 2 minutes, unchanged; counts against `MaxOutstandingGrants` (8). A capacity error is an RPC error.
3. The push of N2.
4. Answer `AdminRPCPairOfferResultPayload {appOrigin, appNode, expiresAt, delivered, problem?}`. The grant is **not** in the answer: the CLI never sees the offered grant.

| Home's answer | `delivered` | `problem` |
| --- | --- | --- |
| `204` | true | – |
| no connection, TLS failure, timeout, a `3xx`, a `5xx` other than `503`, anything else | false | `unreachable` |
| `404` or `405`, or `403` whose error message is exactly `"origin not allowed"` | false | `outdated` |
| `400`, `413`, `415`, any other `403` | false | `refused` |
| `429` | false | `busy` |
| `503` | false | `home_unavailable` |

Why `403 "origin not allowed"` means "outdated": the push never sends `Origin`, so a home with this feature never answers it that way; a home without it refuses the request in `admitAPI` with exactly that text (section 1). A `404` is kept for a home whose routing changes later.

### N2. The dial

| Bound | Value |
| --- | --- |
| Target | `https://<home host>/api/v1/computers/offer`. `<home host>` is the pinned node's MagicDNS name from N1. No part of the URL comes from a network request. |
| Address | `TailscaleIPs[0]:443` of the pinned node, from the same status. `tailnet.Node` gains `TailscaleIPs`; `ParseStatus` keeps only addresses inside Tailscale's ranges (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`). No address: `unreachable`. DNS is never used. |
| TLS | `ServerName` = home host; system roots; TLS 1.2 or later. Tailscale Serve presents home's Let's Encrypt certificate for that name. |
| HTTP | `POST`, `Content-Type: application/json`, `X-Pterm-Request: 1`, body `{origin, grant}` where `origin` is this computer's own origin. No `Origin`, no cookies (no jar), no proxy (`Proxy: nil`), no redirects (`CheckRedirect` returns the 3xx as the answer), no keep-alive. |
| Time and size | 3 s for the whole request; the answer body is read up to 2 KiB (only the `403` message is used). |

This computer's own origin is the configured pairing origin (`pairingOrigin`), the same one the link uses. Without one (`protocol.ValidateComputerOrigin` fails), the RPC answers an error and the CLI prints today's text about `pterm setup` or `--origin`.

### N3. The CLI (`pterm pair --via`, and setup's answer 2)

Only with a confirmer (an interactive wait; setup's answer 2 is one). With `--no-wait` or without a terminal nothing is offered: nobody is there to press `y`.

1. Read the pending pairings that exist now (`seen`, as today).
2. Make the link grant with today's `admin.pair.grant` (it checks `--via` and fails as today). The QR code and the link show this grant.
3. Call `admin.pair.offer`. Check `appOrigin` with today's `boundAppOrigin`.
4. Print the screen (N4).
5. `waitForPhone` with a deadline of **10 minutes** (not the grant's expiry). Each 30 s it calls `admin.pair.offer` again with a fresh grant, so home always holds one that is valid. It stops offering after `outdated` or `refused`; after `unreachable`, `busy` or `home_unavailable` it keeps trying and prints one line when the state changes (`mac answers now: tap Add next to "vps" in the app.`). An RPC error on a later offer (for example 8 grants outstanding) prints nothing and is tried again 30 s later.
6. A new pending pairing bound to home's origin gets today's `pairingPrompt` and `y/N`. Nothing else changes: approve, refuse and the texts after it are today's.
7. Nothing for 10 minutes: `Nobody added this computer in 10 minutes. Run pterm pair --via mac again.` Exit 1.

At any moment at most 4 offered grants are alive (one per 30 s, 2 minutes each) plus the link grant: 5 of 8.

Setup (`offerPairing`, after `feat/setup-ux`): answer 2 calls the same `cli.Pair` with `Via`. Its own heading line ("Add this computer to the Termivoa app at …") goes, because the screen below prints it.

### N4. The screen

```
Add this computer to the Termivoa app at mac (https://mac.<tailnet>.ts.net, owner <owner>).
On your phone, open Termivoa → Settings → Computers and tap Add next to "vps".
Waiting for your phone (up to 10 minutes; Ctrl+C stops)…

No Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:
[QR]
<link>
```

The first line is `describeApp` (tags in place of the owner for a tagged home). `vps` is this computer's first DNS label. When the first push fails, the second line becomes:

| `problem` | Second line |
| --- | --- |
| `unreachable` | `mac did not answer. Use the QR code or the link below.` |
| `outdated` | `Termivoa on mac is too old for Add. Update it, or use the QR code or the link below.` |
| `refused` | `mac does not accept this computer for Add (it must be untagged and have the same Tailscale owner). Use the QR code or the link below.` |
| `busy` | `mac is busy. Use the QR code or the link below.` |
| `home_unavailable` | `mac cannot read its Tailscale status. Use the QR code or the link below.` |

The "do not share" sentence and the "Camera app will NOT work" sentence of `pairVia` stay under the link.

## 5. The phone

In **Settings → Computers**, between the list and **+ Add a computer**, a **Found on your network** block:

| State | What shows |
| --- | --- |
| Rows | `vps` · `vps.tailx.ts.net` · **Add** (one row per found computer) |
| Nothing found | `To add another computer, run pterm setup on it, or pterm pair --via mac if it is already set up. It shows here while it waits.` |
| Home cannot read Tailscale (`503`) | `mac cannot read its Tailscale status. Check Tailscale on mac.` |
| Any other failure | The block keeps its last rows; the next poll tries again. |

`mac` in these texts is home's label. The app reads `GET /api/v1/computers/found` when the screen opens and every 5 s while it is open and the page is visible; it stops when the screen closes.

**Add** builds `AddComputerLink {origin, grant}` from the row and opens today's **Add this computer?** sheet: exactly what a pasted or scanned link does. The link also carries `found: true`, which rides in the `/computers/add` fragment as `from=found` (not a secret). Its only use: when the redeem on the new computer fails with `pairing_expired` (`410`), the add page says

> vps stopped waiting. On vps run pterm pair --via mac, then tap Add again.

in place of today's "This add-computer link expired or was already used." Every other step and text is today's.

## 6. Trust: what the push changes and what it does not

**What a pairing needs is unchanged:** a grant made on the new computer, bound there to one app origin and pinned node, redeemed only from that origin while the node is trusted, then a person on the new computer who compares the code and presses `y`. v2 adds no way to start a pairing without a grant, and no new way to approve one.

**What changes:**

1. **The grant travels one more hop.** It is a single-use 2-minute secret. It now goes new computer → home over TLS, to the tailnet IP of the node the user named, checked against home's certificate name; then it sits in home's memory for at most 2 minutes; then it goes to the user's phones over home's own origin with their cookie. Who can read it on the way: home, and any device paired with home. A device paired with home already has a shell on home, where the grant is. A compromised home already sees every grant in today's link flow (the phone opens the link in home's app). **Nothing new there.**
2. **Home has a new unauthenticated write route.** Any tailnet machine that reaches home's 443 can post an offer for a computer of home's owner, with any grant text: a dead one, or one that overwrites the real offer. The result: **Add** fails ("vps stopped waiting…") until the new computer sends again (30 s), and the flood bucket can keep it failing while a flood runs. The QR code and the link still work; paired phones and other routes are not touched. The poster chooses only the grant text and which same-owner name it is for; home shows its own label and host. A dead grant redeemed on `vps` fails there and creates nothing.
3. **A hostile computer of the same user can offer itself** and show in **Found**. The user must still tap **Add**, check the sheet (machine and owner from home's status), and press `y` on that machine. This is the same as adding any hostile peer today with its link.
4. **Naming the wrong home.** A user who picks the wrong home in setup (or types `--via laptop`) sends the grant to that machine. The grant is bound to that machine's origin, so only that machine's app can use it. The defence is today's compare prompt, which names that app: "…will use this computer from the Termivoa app at laptop (https://laptop…, owner …)". The same as running `pterm pair --via laptop` today.
5. **The phone shows no code until a redeem happens**, and the code it shows comes from its own redeem on the new computer. Nothing on the network can make the phone show another computer's code.
6. **A new outbound request on the new computer.** One fixed path, to one node the user named and the broker pinned, by IP, only while a person waits in a terminal. It can carry only this computer's own origin and a fresh grant for that home.

**What v1's relay needs and v2 does not give:** an unauthenticated way to create a pending pairing on a waiting computer. In v2 a pending pairing still exists only after a redeem of a grant, and only the named home (and its phones) ever holds a grant.

Text for the "Honest trust statement" of `docs/multi-computer.md`:

> **Found on your network.** While `pterm pair --via mac` (or setup) waits on a new computer, it sends its single-use pairing secret to mac, the computer you named, and mac shows the new computer under **Found on your network** to your phones. Whoever controls mac could already use every secret you paste or scan into its app, so this adds no trust in mac. Any machine in your tailnet can make **Add** fail for a while; the QR code and the link still work. Another of your own computers can show itself in **Found**; add only a computer you meant to add, and press `y` only when the code and the app named on that computer match.

Also to update in track T: `docs/threat-model.md` (rows "New computer → home (offer)" and "Home app → found list"; the pairing known limit names the offer bucket) and `docs/architecture.md` section 6 (Pairing and Multi-computer rows).

## 7. How each v1 review finding is resolved

Security review of v1: "sound with changes".

| # | Finding (severity) | Resolution in v2 |
| --- | --- | --- |
| S1 | Code relay: hostile same-owner M forwards its own forged-`Origin` request to the real waiting computer and relays that code to the phone; typing it gives M a shell (high) | Moot. There is no unauthenticated pairing request. A pending pairing needs a grant; the grant goes only to the named home's pinned IP and certificate name. M never holds one unless M is that home (section 6, item 1) |
| S2 | The approval prompt cannot tell requests apart: the app origin is a forgeable header (high) | Moot. The binding comes from the grant (made on the new computer from its own status), not from a header. A forged `Origin` on redeem still needs the grant, as today |
| S3 | Grant-less `/computers/add` fragments (medium) | Moot. **Add** carries `{origin, grant}`; the fragment is today's |
| S4 | `waitForPhone` and requested rows (medium) | Moot. No requested rows. `waitForPhone` only gets a 10-minute deadline and a 30 s re-offer |
| S5 | Nullable `grant_id` breaks the `ListPendingPairings` scan (medium) | Moot. No schema change |
| S6 | Requested rows survive windows (medium) | Moot. No window and no rows. Offers are memory only, 2 minutes, and gone at a broker restart |
| S7 | The window opens only from a terminal (medium) | Kept on purpose. Offers are made only while a person waits in `pterm pair --via` or setup, because that person must press `y` |
| S8 | Dial `TailscaleIPs`, not DNS (low) | Adopted (N2) |
| S9 | Preflight `Max-Age` (low) | Moot. The offer route sends no CORS headers at all; `found` is own origin only |
| S10 | `MaxPairingOrigins` has no spare (low) | Moot. Offers never enter the gateway origin sets. On the new computer all offered grants carry one origin (home's), so the pairing origin set grows by at most one, as with today's link |

Feasibility and simplicity review of v1: "redesign".

| # | Finding | Resolution in v2 |
| --- | --- | --- |
| F1 | Vouching alone does not stop the relay: a compromised M vouches truthfully for itself, and admission is any same-owner node | Agreed. Vouching and the phone-initiated request are both dropped |
| F2 | v1 + vouching is about 1,300–1,500 non-test lines and a schema change (fresh database) | v2: about 600–700 non-test lines (estimate: protocol 100, new computer 250, home 150, web 150), no storage change, no fresh database |
| F3 | The simpler shape: the new computer pushes its own bound grant to the home the user named | Adopted |

## 8. Protocol and storage changes (rule 6)

Additive within v1; regenerate Go and TypeScript; `protocol/v1/revision.json` → `revision: 3`, `minRevision` stays 1.

| File | Change |
| --- | --- |
| `http-api.schema.json` | `HTTPComputerOfferRequest {origin, grant}`; `HTTPFoundComputer {origin, label, grant, expiresAt}`; `HTTPComputerFoundResponse {computers (max 16)}` |
| `http-api.md` | Routes `POST /api/v1/computers/offer` (auth: none; `Origin` must be absent; answers of H1) and `GET /api/v1/computers/found` (cookie; own origin). "Cross-origin requests": the offer route is the one `/api` route that refuses any `Origin` and never sends CORS. "Pairing flow": the offer path |
| `gateway-rpc.schema.json` | `gateway.computers.offer` (`HTTPComputerOfferRequest` → `{}`); `gateway.computers.found` (credential payload → `HTTPComputerFoundResponse`) |
| `admin-rpc.schema.json` | `admin.pair.offer` (`{via, allowTagged?}`, `via` required → `AdminRPCPairOfferResultPayload {appOrigin, appNode, expiresAt, delivered, problem?}`; `problem` ∈ `unreachable`, `outdated`, `refused`, `busy`, `home_unavailable`) |
| `authentication-rpc.md` | The RPCs above; limits table: "Offers: memory only, 2 minutes, at most 16 origins, newest per origin wins; gateway bucket 10, then 1 per second" |
| `internal/protocol/computers.go`, `auth_rpc.go` | Validators and bounds of the new payloads |

Storage: **none.** No table, no column, no fresh database. Offers are memory only on home; grants are today's rows on the new computer.

New constants: `MaxOffers = 16`, `OfferLifetime = 2 * time.Minute` (home); `OfferInterval = 30 * time.Second`, `OfferWait = 10 * time.Minute` (CLI); `offerTimeout = 3 * time.Second`, `maxOfferAnswer = 2 << 10` (new computer); `maxOfferBody = 1 << 10` (gateway).

Versions: an old home answers `outdated` (N1) and the CLI points to the QR code and the link. An old new computer never offers; home's **Found** stays empty for it and the link flow works. A new app on an old home: the app is served by home, so they are the same build.

## 9. Implementation split

Order: **P** first and alone. Then **N**, **H** and **W** in parallel (they use fakes of each other until P's types exist; no file is in two tracks). Then **T**. All start after `feat/setup-ux` and `feat/scan-add-computer` are merged (they change `cmd/pterm/lifecycle.go`, `internal/cli/admin.go`, `internal/tailnet/status.go`, `web/src/screens/Computers.tsx`, `AddComputer.tsx`).

| Track | Files | Work |
| --- | --- | --- |
| **P. Protocol** | `protocol/v1/http-api.schema.json`, `http-api.md`, `gateway-rpc.schema.json`, `admin-rpc.schema.json`, `authentication-rpc.md`, `revision.json`; generated `internal/protocol/generated.go`, `web/src/generated/protocol.ts`; `internal/protocol/computers.go`, `auth_rpc.go` (+ tests) | Section 8; drift check |
| **N. New computer** | `internal/tailnet/status.go` (+ test) | `TailscaleIPs` on `rawPeer` and `Node`, Tailscale ranges only |
| | `internal/broker/computers.go` (+ test) | `resolveVia` also returns the node. *Not in the brief's list; needed so the dial uses the node `resolveVia` pinned* |
| | **new** `internal/broker/offer.go` (+ `offer_test.go`) | N1 and N2: the offer, the dial (a package variable `dialHome`, production value only), the answer table |
| | **new** `internal/broker/peerdial_e2e.go` | `//go:build e2e`; section 10 |
| | `internal/broker/admin_rpc.go` | `case protocol.AdminPairOffer` |
| | `internal/localclient/admin.go` (+ test) | `OfferPairing` |
| | `internal/cli/admin.go` (+ test) | `Admin.OfferPairing`; N3 and N4 in `pairVia` and `waitForPhone` |
| | `cmd/pterm/lifecycle.go`, `cmd/pterm/management.go` | Setup's answer 2 drops its own heading; usage text of `pterm pair --via` |
| **H. Home** | **new** `internal/broker/offers.go` (+ `offers_test.go`) | H2 and H3 |
| | `internal/broker/rpc_server.go` | One field on `ManagementServer` for the offer book. *Not in the brief's list; the struct lives here* |
| | `internal/broker/gateway_rpc.go` (+ test) | `case protocol.GatewayComputersOffer`, `case protocol.GatewayComputersFound` |
| | `internal/gatewayapi/gatewayapi.go`, `internal/gatewayclient/client.go` (+ test) | `OfferComputer`, `FoundComputers` |
| | `internal/gateway/handler.go`, `api.go` (+ `handler_test.go`, `multicomputer_test.go`, `fake_broker_test.go`) | Routes; the admission exception; the offer bucket; H1 and H3 handlers |
| **W. Web** | `web/src/api/http.ts`, `web/src/computers.ts`, `web/src/screens/Computers.tsx`, styles (+ tests) | `foundComputers()`; the **Found on your network** block, its 5 s poll and texts |
| | `web/src/pairing/addComputer.ts`, `web/src/screens/AddComputer.tsx` (+ tests) | `found` on `AddComputerLink`, `from=found` in the fragment, the stale-offer text. *Not in the brief's list; the stale text lives on the add page* |
| **T. Harness and docs** (after N, H, W) | `scripts/e2e-multicomputer/*` | Section 10 |
| | `docs/multi-computer.md`, `docs/threat-model.md`, `docs/architecture.md`, `docs/pending-work.md`, `docs/progress.md`, `docs/owner-verification.md`, `CHANGELOG.md`, `README.md`, `site/src/content/docs/docs/**` (guide `computers`, `reference/cli.md`, `security.md`, `troubleshooting.md`, `release-notes.md`) | Rules 22, 23, 25; no internal detail on the public site |

Each helper brief must carry rules 21, 22, 24 and 26.

## 10. Tests

### Unit

| Area | Cases |
| --- | --- |
| `tailnet` | `TailscaleIPs` parsed; an address outside the Tailscale ranges dropped; a node with none |
| The dial (`offer.go`, against `httptest` TLS with a recording dialer) | Dials the node's IP and port 443, never resolves the host name; `ServerName` is the home host; a wrong certificate name fails (`unreachable`); a `307` is not followed (`unreachable`); an answer body over 2 KiB is cut at 2 KiB; a server that waits 5 s ends at 3 s (`unreachable`); no `Origin`, no `Cookie`, no proxy even with `HTTPS_PROXY` set; the body is `{origin, grant}` of this computer |
| Answer table | `204`; `404`; `405`; `403 "origin not allowed"` → `outdated`; `403` other → `refused`; `400`, `413`, `415`; `429`; `503`; `500` |
| `admin.pair.offer` | `resolveVia` errors pass through; the grant is bound and not in the answer; no own origin configured |
| Offer book (`offers.go`) | Peer, same owner, untagged: stored. Not a peer, other owner, tagged node, tagged home, own origin, no snapshot: refused. Newest wins; 16 then `limit_exceeded`; expired dropped; restart empty |
| `found` | Own origin only; expired, changed node ID, gone host and confirmed computers left out; label from home's status; at most 16 |
| Gateway | Any `Origin` (also home's own, a bound one, `null`): `403`, no CORS header; two `Origin` headers; `OPTIONS` with and without `Origin`; missing header; wrong type; 1 KiB + 1; unknown field; bad origin or grant: `400 "invalid offer"`; the bucket (10, then 1 per s) never takes redeem or poll tokens; `found` refuses a bound app origin |
| CLI | Screen texts for each `problem`; the 30 s re-offer with a fake clock; stop after `outdated`; the state-change line; 10-minute end; no offer with `--no-wait`; a pending pairing from the link and one from **Add** both get `pairingPrompt` |
| Web | Found states; the poll stops when the screen closes or hides; **Add** opens the sheet with `{origin, grant}`; `from=found` survives `takeAddComputerFragment`; the stale text only with it |

### `scripts/e2e-multicomputer`

The harness has no Tailscale IPs or real certificates: its front stands in for MagicDNS and TLS, as it does for browsers. So:

- **New** `internal/broker/peerdial_e2e.go` (`//go:build e2e`) sets `dialHome` in `init()`: when `PTERM_E2E_FRONT` is set, the dial opens `CONNECT <home host>:443` through that proxy and checks the server certificate by its SPKI hash `PTERM_E2E_SPKI` (the front prints it at start, for Chrome today). `ServerName`, the path, the headers and the limits stay the production code's.
- The harness builds `pterm` with `-tags webdist,e2e` (`lib.mjs`) and sets the two variables only for its stacks.
- Production binaries have no hook: the file is not compiled without the tag. The harness also builds once without `e2e` and checks that the binary does not contain the string `PTERM_E2E`.
- The fake status already has `TailscaleIPs` (`lib.mjs` `statusJSON`).

| Check | What it proves |
| --- | --- |
| `F-offer` | `pterm pair --via mac` on vps: mac's `found` lists vps with a grant. Direct `POST`s with curl: with `Origin` → `403` and no CORS header; from evil's name (owner mallory), a tagged node, mac's own origin → `403 "that computer is not yours…"`; bad body → `400`; nothing stored for them |
| `F-add` (Chrome and WebKit) | **Found** row → **Add** → sheet → code; `y` on vps; the computer is in the list; its sessions show; the device on vps has `Via` mac; the row is gone |
| `F-dead` | curl overwrites vps's offer with a dead grant: **Add** shows "vps stopped waiting…"; within 30 s vps's next offer replaces it and **Add** works; the QR link works all the time |
| `F-flood` | 50 offers at once: 10 pass, then `429`; a paired phone's requests and a link pairing still work |
| `F-gone` | Ctrl+C on vps: the row is gone within 2 minutes; vps's node ID changes on mac: the row is gone at the next poll |
| `F-setup` | `pterm setup` on a fresh vps, answer 2, pick mac: the screen of N4; **Add**; `y` in setup |
| `F-old` | mac runs `pterm` built from `v0.1.0-beta.2`: vps prints the "too old" line, and the QR link still adds vps. vps runs the old build: mac's **Found** stays empty and the link works |

### Real two-computer checks (owner; `docs/owner-verification.md`)

1. vps's broker under systemd dials mac's Tailscale IP:443 and verifies its Let's Encrypt certificate (system roots, background process, no user session).
2. mac's Tailscale Serve forwards a `POST` with no `Origin` to the gateway, with `Host` intact.
3. The reverse: mac's broker under launchd offers to vps as home.
4. The whole add, end to end, on a real iPhone Home Screen app and on Android.

## 11. Open risks

1. **Serve and certificates from a background process** (real check 1) are not verified yet. If the dial fails, the CLI says `mac did not answer` and the QR code and the link work.
2. **Flood on the offer bucket** stops **Add** while it runs (one bucket behind Serve, as for pairing). The QR code and the link still work.
3. **The user names the wrong home.** The compare prompt names the app; the user must read it.
4. **Two parallel changes** touch `lifecycle.go`, `cli/admin.go`, `status.go`, `Computers.tsx` and `AddComputer.tsx`; this work starts after they merge.

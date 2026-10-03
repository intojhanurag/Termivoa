# Found on your network: design

Status: **version 2 (2026-10-03). The owner approved the idea. v2 is the chosen design after two reviews of v1. The security re-check of the push shape is done ("sound with changes"), and its resolutions are in this text (section 7). No code exists yet (AGENTS.md rule 24).** ADR [0007](decisions/0007-found-on-network.md). Builds on [multi-computer.md](multi-computer.md) (ADR [0005](decisions/0005-multi-computer.md)).

Goal, in the owner's words: "login to Tailscale, see all devices, Add". A computer that waits to be added shows in **Settings → Computers** under **Found on your network** with an **Add** button. No link to move. The QR code, the link and the paste flow stay.

Words used here: **home** serves the app (for example `mac`). The **new computer** is the one to add (for example `vps`). An **offer** is a bound grant that the new computer sent to home.

**The idea in one line:** the new computer already makes a single-use grant bound to the home the user named (`pterm pair --via mac`). v2 only adds a way for that grant to reach home by itself, so the phone can take it from home instead of from a link. Everything after the phone has the grant is today's code.

## 1. What the code does today (evidence)

Paths and functions are on `origin/main` unless the row says otherwise.

| Fact | Where |
| --- | --- |
| `pterm pair --via` resolves home in a fresh status (`Tailnet.Fresh`: under 60 s cached, else `tailscale status`, 10 s), applies the owner rule and pins node ID, owner and tags. | `internal/broker/computers.go` `resolveVia`; `internal/tailnet/status.go` `Resolve`, `CheckOwner` |
| The grant is a 256-bit secret, 2 minutes, single use, stored only as a hash, bound to the app origin and pinned node. At most 8 unredeemed grants. Its value is "returned only to the restricted local CLI". There is no function that deletes one grant. | `internal/auth/service.go` `CreateBoundGrant` (and its comment), `MaxOutstandingGrants`; `internal/auth/expiry.go` `PairingGrantLifetime`; `internal/store/auth.go` `CreatePairingGrant` |
| A redeem finds the grant by its hash on the computer that made it. The pending pairing copies the app binding from the grant row in SQL, so the redeemer cannot choose it. At most 4 pending pairings in total. | `internal/store/auth.go` `RedeemPairingGrant` (`WHERE grant_hash = ?`; `INSERT … SELECT … FROM pairing_grants`); `internal/auth/service.go` `MaxPendingPairings` |
| Redeem and poll pass only when the `Origin` header equals the bound origin and the snapshot still trusts the pinned node. The header is the only origin check: a program can send any `Origin`. | `internal/broker/computers.go` `pairingCheck`, `appTrust` |
| The CLI approves only after the owner compares the phone's code and types `y`. The prompt names the app computer, its origin and owner, but not this computer. | `internal/cli/admin.go` `pairingPrompt`, `waitForPhone`, `describeApp` |
| `waitForPhone` today waits until the grant expires (2 minutes) and offers only pending pairings bound to the named app. | `internal/cli/admin.go` `waitForPhone` |
| The phone's confirm sheet says "Next, this phone shows a code. Check it on {label}."; its code screen says "If it matches, type y in the terminal on {label}." | `web/src/screens/AddComputer.tsx` `AddComputerConfirm`, `AddComputerPage` |
| **Add this computer?** first records a pending entry on home (home checks the host in its own status and pins the node). Then a full load of `/computers/add#grant=…&origin=…` redeems on the new computer. | `web/src/screens/AddComputer.tsx` `AddComputerSheet`; `web/src/pairing/addComputer.ts` `prepareAddComputer`, `addComputerUrl`, `takeAddComputerFragment`, `AddComputerFlow`; `internal/broker/computers.go` `addComputer`, `lookupComputer` |
| `admitAPI` runs before the router on every `/api` path. For an origin bound on this computer it sets CORS headers and answers a preflight on **any** `/api` path. A request with no `Origin` passes only with `Sec-Fetch-Site: same-origin`; else `403 "origin not allowed"`. `no-cors` is refused. | `internal/gateway/handler.go` `admitAPI`, `crossOrigin`, `sameOriginFetch` |
| So on a home without this feature, `POST /api/v1/computers/offer` with no `Origin` gets `403 "origin not allowed"`, not 404. | `internal/gateway/handler.go` `admitAPI` |
| State-changing routes share one boundary: exact `Origin`, `X-Pterm-Request: 1`, JSON, a body limit, strict decoding. | `internal/gateway/api.go` `mutation`, `mutationLimit`, `decodeStrict` |
| Gateway limits are token buckets keyed by client address. Behind Tailscale Serve every client is `127.0.0.1`, so each limit is one bucket. | `internal/gateway/api.go` `newLimiter`, `clientKey`; `docs/threat-model.md` (pairing known limit) |
| The computer routes on home admit only home's own origin (`authorizeOwn`). Another app gets `invalid_request`, which is HTTP 400. | `internal/broker/gateway_rpc.go` (`authorizeOwn`); `internal/broker/computers.go` `classifyComputerError`; `internal/gateway/api.go` `brokerStatus` |
| The snapshot holds only own-tailnet nodes; shared-in nodes are dropped. `Node` and `rawPeer` have no addresses. | `internal/tailnet/status.go` `ParseStatus`, `Node`, `rawPeer` |
| The broker's only outbound requests go to push services. Their dial guard refuses tailnet addresses on purpose (`100.64.0.0/10`). | `internal/webpush/webpush.go` `NewHTTPClient`, `publicAddressOnly` |
| Tailscale Serve probably sets `X-Forwarded-For` to the client's tailnet IP. Not verified. | `scripts/gate0/main.go` (`echo.Client` comment) |
| A tagged server is recommended as home. | `docs/multi-computer.md`, "Same tailnet is not same owner" |
| Today, `pterm pair --via` run inside a Termivoa session on home already sends the grant through home's broker to every viewer of that session. | `docs/multi-computer.md`, "Pairing a second computer" |
| Setup's last step asks "first computer, or one more?" and runs `pterm pair --via <home>` for answer 2. Its candidate list shows only first labels. | `feat/setup-ux` (merges first): `cmd/pterm/lifecycle.go` `offerPairing`; `internal/tailnet/status.go` `HomeCandidates`, `Node.OS`; `internal/cli/confirm.go` `Ask`. Its question text is changing to "Is Termivoa already on your phone? 1) No, this is my first computer 2) Yes, add this computer to it" and "Which computer did you first pair your phone with?" |
| `pterm pair --via` prints a QR code for the app's own scanner. **+ Add a computer** offers **Scan QR** and **Paste link**. | `feat/setup-ux`: `internal/cli/admin.go` `pairVia`; `feat/scan-add-computer` (merges first): `web/src/screens/Computers.tsx`, `web/src/pairing/scan.ts` |

## 2. The flow

### What the user does

1. On `vps`: `pterm setup`, answer 2 ("Yes, add this computer to it"), pick `mac` (the list shows each full address). Or, when `vps` is already set up: `pterm pair --via mac`.
2. On the phone: **Settings → Computers → Found on your network**: `vps` — **Add**.
3. **Add this computer?** shows address, machine and owner. Tap **Add vps**, then **Continue**. The phone shows a code and "type y in the terminal on vps".
4. On `vps`: the prompt names this computer (`vps`), the app at `mac`, and the same code. Type `y`. `vps` is in the app.

### What the machines do

```
vps (new computer)                       mac (home)                         phone (app from mac)
pterm pair --via mac (waits, 10 min)
 admin.pair.offer -> broker:
   1st call: resolveVia(Fresh), pin mac's node, mint grant G
   later: snapshot still shows the pinned node? same G (new G at 60 s left)
   POST https://mac…/api/v1/computers/offer
   dial pinned TailscaleIP:443 only, TLS name mac…, no Origin
   {origin: https://vps…, grant: G} ----> gateway (top of admitAPI): no Origin,
                                           no Sec-Fetch-*, header, 1 KiB, bucket
                                           broker (snapshot only): vps is a peer, same
                                           owner, untagged? store : drop silently
                                           offers[vps] = {G, nodeID, +60 s}
   <------------------------------ 204 (also when dropped)
 (again every 30 s)                                                  GET /api/v1/computers/found (every 5 s)
                                           cookie, own origin  <------------------
                                           [{origin, label, grant, expiresAt}] -->
                                                                     tap Add: {origin, grant}
                                                                     = a pasted link (today's code):
                                           POST /api/v1/computers {pending} <-----
                                                                     load /computers/add#grant=…
 POST /api/v1/pair/redeem  <------------------------------------------------------ (Origin mac)
 pending pairing bound to mac; code -------------------------------------------->  shows the code
 prompt: THIS computer (vps), app at mac, code; y
 POST /api/v1/pair/poll    <------------------------------------------------------
 approved + cookie ------------------------------------------------------------->  confirm on mac
 CLI exits: admin.pair.offer.end deletes unredeemed G and the link grant
```

Everything from the tap on **Add** is the code of today, plus one retry of a replaced grant (section 5).

## 3. Rules on home

### H1. `POST /api/v1/computers/offer`

**Admission: the first thing in `admitAPI`**, before `crossOrigin` and the preflight answer, for the exact path `/api/v1/computers/offer` and every method:

- No `Access-Control-*` header, ever. This holds also for an origin bound on home: the usual CORS and preflight code of `admitAPI` never runs for this path.
- Any `Origin` header, or any `Sec-Fetch-*` header: `403 invalid_request "origin not allowed"`. Browsers send `Origin` on every `POST` and `Sec-Fetch-*` on every request, so no web page can reach this route.
- The cookie is never read. No origin is passed to the broker (`WithForwardedOrigin` is not called).
- Then the router: methods other than `POST` get `405` (`allowMethod`).

This is for the exact `POST` in the review's words; it covers every method on that path, because a preflight from a bound origin must not get CORS headers either.

The handler (`api.go`), in this order:

1. `POST` only (`405` else).
2. Today's boundary, with "no `Origin`" in place of "exact `Origin`": `X-Pterm-Request: 1` (`403 "missing request header"` else), JSON (`415`), body at most **1 KiB** (`413`), strict decoding into `HTTPComputerOfferRequest {origin, grant}` (`400 "malformed request body"`).
3. One token from a **new** bucket: 10, then 1 per second. It is its own bucket, so it never takes tokens from redeem or poll. Empty: `429 limit_exceeded "too many offers"`.
4. `origin` must be a canonical computer origin (`protocol.ValidateComputerOrigin`) and `grant` a valid secret text (`protocol.ValidateSecretText`). Else `400 invalid_request "invalid offer"`.
5. `gateway.computers.offer {origin, grant}` to the broker. Any broker answer except a transport error is `204`. A transport error is today's `503`.

So a well-formed offer always gets `204`, whether home keeps it or not. The answer says nothing about which names are untagged nodes of home's owner. The gateway writes fixed texts; nothing the sender sent is echoed.

### H2. The broker's offer book (`internal/broker/offers.go`, new)

- `gateway.computers.offer` with a forwarded origin is dropped (defence in depth: this route never has one).
- It reads only `Tailnet.Snapshot()`. It never calls `Fresh` and never waits on `tailscale`. `Snapshot()` may start at most one background `tailscale status` refresh per 5 s when the last status is stale, as it does for every per-request check; the offer never waits for it.
- It keeps the offer only when all hold: there is a snapshot; the origin's host is in `status.Peer` (own tailnet, not shared-in, not home itself); the origin is not home's own; `CheckOwner(node, false)` passes (same owner by user ID, neither node tagged). Else it **drops the offer silently** and answers `{}`.
- Memory only: `offers[origin] = {grant, nodeID, expires = now + 60 s}`. The newest offer for an origin replaces the older one. At most **16** origins. Expired entries are dropped first; an offer for a 17th origin is dropped silently. A broker restart empties the book; the new computer sends again within 30 s.
- 60 s, not 2 minutes: the new computer sends every 30 s and replaces a grant when 60 s are left (N1). So an offer that home lists always has a grant that is valid for at least as long as home lists it.
- The grant is never logged, never written to SQLite, and never sent anywhere except in the answer of H3.

### H3. `GET /api/v1/computers/found`

- Cookie required. Own origin only: the broker runs it through `authorizeOwn` in `gateway_rpc.go`, like the other computer routes. A request from an app bound on home gets CORS headers from the gateway (as on every route), but the broker refuses it: `400 invalid_request`. A test proves this. `gateway.computers.found {credential}`.
- No snapshot: `503 host_unavailable`. The broker's text ("this computer cannot read its Tailscale status; check Tailscale and retry") does not reach the page: the gateway answers through `respondGeneric`, so the message is the generic status text "Service Unavailable".
- It leaves out: expired offers; an offer whose host now has another node ID than the one stored, or is gone from the snapshot; computers already **confirmed** for this device (`Store.ListComputers`).
- Answer: `200 HTTPComputerFoundResponse {computers: [{origin, label, grant, expiresAt}]}`, at most 16, sorted by origin. `label` is the first DNS label of the node's host from **home's own status**. The only thing from the sender that is shown is the grant.
- The gateway uses `respondGeneric` (no broker text goes to the page).

**Who can read offers:** every device paired with home, from home's own origin. Each such device is treated as the owner's. It already has a shell on home, where the offers are.

### H4. The rest is unchanged

**Add** runs today's path: `POST /api/v1/computers` with `state: pending` (home's own `lookupComputer` and node pin), the confirm sheet, a full load of `/computers/add`, the redeem on the new computer, the code, the poll, then `state: confirmed` after the first authenticated read. Only confirmed computers enter `frame-src`.

## 4. Rules on the new computer

### N1. The offer RPCs (local CLI only)

The broker keeps one **offer state** in memory: `{via, binding, node, grant value, expires}`. If a second `pterm pair --via` with another name runs on the same computer, its state replaces the first one, and the first grant is deleted.

**`admin.pair.offer {via}`**

1. With no state for this `via`: `resolveVia(via, false)` with a fresh status, exactly as for `admin.pair.grant`, and pin the node. `resolveVia` now also returns the resolved `tailnet.Node`. `allowTagged` is not accepted: with a tagged node on either side, home drops every offer (`CheckOwner(node, false)`), so a push could only fail. Then mint a grant with `CreateBoundGrant` (2 minutes, unchanged).
2. With a state for this `via`: read `Tailnet.Snapshot()`. If the snapshot shows another node ID for home's host, or home is gone, delete the grant, clear the state, and return the error `home_changed`. With no snapshot, skip this push and answer `unreachable`. If the grant has 60 s or less left, delete it (only if it was not redeemed) and mint a new one. Otherwise **re-send the same grant**.
3. The push of N2.
4. Answer `AdminRPCPairOfferResultPayload {appOrigin, appNode, expiresAt, delivered, problem?}`, where `problem` is `unreachable` (no HTTP answer) or `not_accepted` (any answer other than `204`). The grant is **not** in the answer: the CLI never sees the offered grant.

**`admin.pair.offer.end {grant?}`** deletes the offered grant and the given grant (the link's), each only when it was not redeemed, and clears the state. The broker deletes a grant by the hash of its value: there is a new `Service.DeleteGrant` in `internal/auth/service.go`, and a new `Store.DeleteUnredeemedGrant` (`DELETE … WHERE grant_hash = ? AND redeemed_at_ms IS NULL`) in `internal/store/auth.go`.

At most **one** live offered grant and one link grant exist at a time.

### N2. The dial (in the broker only)

| Bound | Value |
| --- | --- |
| Target | `https://<home host>/api/v1/computers/offer`. `<home host>` is the pinned node's MagicDNS name. No part of the URL comes from a network request. |
| Address | The pinned node's `TailscaleIPs[0]`. `tailnet.Node` and `rawPeer` gain `TailscaleIPs`. `ParseStatus` keeps only addresses inside Tailscale's ranges (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`). With no such address the result is `unreachable`. DNS is never used. |
| Dial guard | Its own `net.Dialer` with a `Control` function that allows only that one IP on port 443. It does not use the webpush guard, which refuses tailnet addresses on purpose. |
| Before each push | The pinned node ID must be unchanged in the latest snapshot (N1, step 2). |
| TLS | `ServerName` = home host; system roots; `MinVersion` TLS 1.2. TLS checks are never skipped, and there is never a fallback to `http`. Tailscale Serve presents home's Let's Encrypt certificate for that name. |
| HTTP | `POST`, `Content-Type: application/json`, `X-Pterm-Request: 1`, body `{origin, grant}`, where `origin` is this computer's own origin. No `Origin`, no `Sec-Fetch-*` header, no cookie jar, `Proxy: nil`, `CheckRedirect` returns `http.ErrUseLastResponse` (a 3xx is a non-204 answer), no keep-alive. |
| Time and size | 3 s for the whole request. At most 2 KiB of the answer is read, then dropped: only the status code is used, and home's text is never printed. |

This computer's own origin is the configured pairing origin (`pairingOrigin`), the one the link uses. Without one, the RPC returns an error, and the CLI prints today's text about `pterm setup` or `--origin`.

### N3. The CLI (`pterm pair --via`, and setup's answer 2)

The CLI offers only when there is a confirmer (an interactive wait; setup's answer 2 is one) and no `--allow-tagged`. With `--no-wait`, without a terminal, or with `--allow-tagged`, it does not offer, and its output is today's (with `--allow-tagged` the second line of N4 says why).

1. Read the pending pairings that exist now (`seen`, as today).
2. Make the link grant with today's `admin.pair.grant`. It checks `--via` and fails as today; for example, a tagged home without `--allow-tagged` fails here. The QR code and the link show this grant.
3. Call `admin.pair.offer`. Check `appOrigin` with today's `boundAppOrigin`.
4. Print the screen (N4).
5. `waitForPhone` with a deadline of **10 minutes** (not the grant's expiry). Every 30 s it calls `admin.pair.offer` again. It prints one line only when the result changes (`Sent to mac (https://…). Tap Add next to "vps" in the app.` or the failure line of N4). An RPC error on a later call prints nothing, and the CLI tries again 30 s later. The exception is `home_changed`: it ends the command with `mac is now a different Tailscale machine. Run pterm pair --via mac again.` (exit 1).
6. A new pending pairing bound to home's origin gets the prompt of N5. The prompt blocks the loop, so no offer is sent while it waits. After the answer the command ends, so one command run can produce at most one pending pairing from offers.
7. Nothing for 10 minutes: `Nobody added this computer in 10 minutes. Run pterm pair --via mac again.` (exit 1).
8. On **every** exit (`y`, `N`, Ctrl+C, timeout, error), the CLI calls `admin.pair.offer.end {grant: <link grant>}` with its own short context (Ctrl+C has already cancelled the main one).

Setup (`offerPairing`, after `feat/setup-ux`):

- Answer 2 calls the same `cli.Pair` with `Via`. Setup's own heading line ("Add this computer to the Termivoa app at …") goes, because the screen below prints it.
- The candidate list shows the full origin and the OS, and asks for the computer whose address the phone app opens:

```
Which computer did you first pair your phone with?
Pick the computer whose address your Termivoa app opens.
  1) mac — https://mac.tailx.ts.net (macOS)
  2) nas — https://nas.tailx.ts.net (linux)
Type a number, or the name of another computer (Enter skips pairing):
```

### N4. The screen

```
Add this computer (vps) to the Termivoa app at mac (https://mac.<tailnet>.ts.net, owner <owner>).
Sent to mac (https://mac.<tailnet>.ts.net).
On your phone, open Termivoa → Settings → Computers and tap Add next to "vps".
Waiting for your phone (up to 10 minutes; Ctrl+C stops)…

No Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:
[QR]
<link>
```

The first line uses `describeApp` (tags in place of the owner for a tagged home). `vps` is this computer's first DNS label. When the offer is not delivered, lines 2 and 3 become one line:

| Case | Line |
| --- | --- |
| `unreachable` | `mac did not answer. Use the QR code or the link below.` |
| `not_accepted` (any non-204 answer, for example an old home) | `Termivoa on mac cannot receive this yet. Update it, or use the QR code or the link below.` |
| `--allow-tagged` (no offer) | `Add does not work with tagged computers. Use the QR code or the link below.` |

The "do not share" sentence and the "Camera app will NOT work" sentence of `pairVia` stay under the link.

### N5. The approval prompt names this computer

`pairingPrompt` gets this computer's first label. For a pairing bound to another computer's app (both the link and Found):

```
Phone "iPhone" wants to use THIS computer (vps) from the Termivoa app at mac (https://mac.<tailnet>.ts.net, owner <owner>).
Approve only if your phone shows 123-456 and says "type y in the terminal on vps". If it names another computer, shows another code, or shows nothing, answer N.
Approve? [y/N]
```

The quoted words are the phone's code screen ("If it matches, type y in the terminal on {label}."). The review's draft quoted the confirm sheet ("Check it on vps"), but the code screen is what shows next to the code. This sentence is what stops the wrong-home relay (section 6, item 4): in that relay, the phone names the hostile computer, not `vps`.

**Typing the phone's code in place of `y`: not chosen.** In the relay, the phone shows the same code as the prompt, because the hostile computer passes it through. So typing it would add work and close nothing. What closes the relay is the name of the computer, which the prompt now asks the user to compare.

## 5. The phone

In **Settings → Computers**, between the list and **+ Add a computer**, a **Found on your network** block:

| State | What shows |
| --- | --- |
| Rows | `vps` · `vps.tailx.ts.net` · **Add** (one row per found computer) |
| Nothing found | `To add another computer, run pterm setup on it, or pterm pair --via mac if it is already set up. It shows here while it waits.` |
| Home cannot read Tailscale (`503`) | `mac cannot read its Tailscale status. Check Tailscale on mac.` |
| Any other failure | The block keeps its last rows; the next poll tries again. |

`mac` in these texts is home's label. The app reads `GET /api/v1/computers/found` when the screen opens, and every 5 s while the screen is open and the page is visible. It stops when the screen closes.

**Add** builds `AddComputerLink {origin, grant}` from the row and opens today's **Add this computer?** sheet: exactly what a pasted or scanned link does. The link also carries `found: true`, which goes in the `/computers/add` fragment as `from=found` (not a secret). It is used only when the redeem on the new computer fails with `pairing_expired` (`410`):

1. The page reads `/found` once. If the same origin now has another grant (the new computer replaced it at 60 s left), the page redeems that grant, once.
2. Otherwise it shows, in place of today's "This add-computer link expired or was already used.":

> vps stopped waiting. On vps run pterm pair --via mac, then tap Add again.

Every other step and text is today's.

## 6. Trust: what the push changes and what it does not

**What a pairing needs does not change:** a grant made on the new computer, bound there to one app origin and pinned node, redeemed only from that origin while the node is trusted, then a person on the new computer who compares the code and the computer name and presses `y`. v2 adds no way to start a pairing without a grant, and no new way to approve one.

**Checked sound in the re-check:**

- The v1 relay from any hostile same-owner node is closed. A grant is valid only on the computer that made it (lookup by hash). The new computer sends it only to the home the user named, resolved in its own fresh status with the owner rule and a node pin.
- The app origin in the new computer's prompt comes from its own grant row (copied in SQL). The redeemer cannot forge it.
- Browsers cannot reach the offer route. Every browser `POST` has `Origin`; `X-Pterm-Request` and JSON force a preflight; `no-cors` and every `Sec-Fetch-*` header are refused.
- A compromised home gains only three things: it learns that the new computer waits; it gets grants for up to 10 minutes (one live at a time); and it sends replies of at most 2 KiB, which the new computer's broker drops after reading the status code.

**What changes:**

1. **A written rule changes.** Until now a grant's value went "only to the restricted local CLI" (`CreateBoundGrant`), and `docs/threat-model.md` assumes this. Now the value also goes new computer → home over TLS (to the pinned tailnet IP, checked against home's certificate name). It stays in home's memory for at most 60 s per send. It goes to every device paired with home, over home's own origin, with that device's cookie. Every such device is treated as the owner's. This is not a new kind of exposure: the in-terminal path of today (`pterm pair --via` inside a Termivoa session on home) already sends the grant through home's broker to every viewer of that session, and a compromised home already sees every grant that is pasted or scanned into its app.
2. **Home has a new unauthenticated write route.** Anyone who reaches home's port 443 can post an offer. This includes another user in a team tailnet, and a user the home node is shared with. They can post junk grants under a name of home's owner, replace the real offer (the newest wins), or empty the shared bucket. They cannot compromise anything: a junk grant fails on the computer it is redeemed on. They learn nothing: every well-formed offer gets `204`. Home never waits on `tailscale` for them (snapshot only; at most one background refresh per 5 s, as for every per-request check). They cannot choose any text the phone shows (label and host come from home's status). The cost is denial of service: **Add** fails while it runs. The QR code and the link still work. **Accepted.**
3. **A hostile computer of the same user can offer itself** and show in **Found**. The user must still tap **Add**, check the sheet (machine and owner from home's status), and press `y` on that machine. This is the same as adding any hostile peer today with its link.
4. **Naming the wrong home (re-check finding 1).** Without a change, this would be a relay that today's link flow does not allow:
   1. The user picks a hostile computer M as home.
   2. The new computer X sends M a grant bound to M.
   3. M redeems it on X with a forged `Origin: M` (a program, not a browser) and gets code C.
   4. M posts an offer for itself to the real home, with a grant of its own.
   5. The user taps **Add** on "M". The phone redeems on M, and M answers with C.
   6. The phone shows C, and X's prompt shows C.

   With a wrong `--via` today, nothing happens. **Resolution:**
   - X's prompt names X ("THIS computer (vps)"), and it tells the user to answer `N` unless the phone says "type y in the terminal on vps". In this relay, the phone says "…on M".
   - Setup's list shows the full address and OS, and asks for the computer whose address the app opens.
   - The screen prints "Sent to mac (https://…)".
5. **The phone shows no code until a redeem happens.** The code it shows comes from its own redeem on the computer that the row and the code screen name.
6. **One live grant.** The new computer re-sends the same grant every 30 s, and replaces it only when 60 s are left (it deletes the old one if it was not redeemed). It deletes its grants when the command ends. Someone who holds an offered grant can make at most one pending pairing with it. While that pairing waits, the CLI sends no new grant.
7. **A new outbound request on the new computer.** It is in the broker only. It uses one fixed path, one pinned IP on port 443, and one certificate name. It runs only while a person waits in a terminal, and only while the pinned node is unchanged. It carries only this computer's origin and its current grant for that home.
8. **Tagged computers.** If home or the new computer is tagged, home drops every offer. So the CLI does not offer with `--allow-tagged`, and it says to use the QR code or the link. A tagged home (recommended in `docs/multi-computer.md`) therefore never shows **Found**. The link flow is unchanged for it.

**Rejected for now: identifying the sender from `X-Forwarded-For` set by Tailscale Serve.** If home could tell which tailnet node posted an offer, it could accept only the node that the offer names. That would close the junk offers, the replacement, and most of the flood. But it makes a Serve header a trust input: a new trust dependency, against the rule that forwarded identity headers are ignored (`docs/implementation-plan.md`, `docs/threat-model.md`). It needs its own ADR and a check on a real Serve. `scripts/gate0/main.go` suggests that Serve sets the header, but this is not verified. The DoS that it would remove is accepted, because the QR code and the link are the fallback.

Text for the "Honest trust statement" of `docs/multi-computer.md`:

> **Found on your network.** While `pterm pair --via mac` (or setup) waits on a new computer, it sends its single-use pairing secret to mac, the computer you named, and mac shows the new computer under **Found on your network** to every phone paired with mac. Whoever controls mac, or any device paired with it, could already use every secret you paste or scan into its app, so this adds no trust in mac. Anyone who can reach mac in your tailnet can make **Add** fail for a while; the QR code and the link still work. Another of your own computers can show itself in **Found**. Press `y` only when that computer's prompt names it as THIS computer, and the phone shows the same code and the same computer name.

Also to update in track T:

- `docs/threat-model.md`:
  - a row "New computer → home (offer)": no `Origin` or `Sec-Fetch-*`, own bucket, `204` always, snapshot only, memory only for 60 s, junk-offer DoS accepted;
  - a row "Home → paired devices (found list)": own origin, every paired device is the owner's;
  - the pairing known limit names the offer bucket;
  - the grant rule of item 1.
- `docs/architecture.md`, section 6: the Pairing and Multi-computer rows.

## 7. How each review finding is resolved

### v1 security review: "sound with changes"

| # | Finding (severity) | Resolution in v2 |
| --- | --- | --- |
| S1 | Code relay: a hostile same-owner M forwards its own forged-`Origin` request to the real waiting computer and relays that code to the phone; typing it gives M a shell (high) | Moot. There is no unauthenticated pairing request. A pending pairing needs a grant, and the grant goes only to the named home's pinned IP and certificate name (section 6) |
| S2 | The approval prompt cannot tell requests apart: the app origin is a forgeable header (high) | Moot. The binding comes from the grant row on the new computer, not from a header. A forged `Origin` on redeem still needs the grant, as today |
| S3 | Grant-less `/computers/add` fragments (medium) | Moot. **Add** carries `{origin, grant}`; the fragment is today's |
| S4 | `waitForPhone` and requested rows (medium) | Moot. There are no requested rows. `waitForPhone` gets a 10-minute deadline and a 30 s re-send |
| S5 | Nullable `grant_id` breaks the `ListPendingPairings` scan (medium) | Moot. No schema change |
| S6 | Requested rows survive windows (medium) | Moot. There is no window and there are no rows. Offers are memory only, 60 s, and gone after a broker restart |
| S7 | The window opens only from a terminal (medium) | Kept on purpose. Offers are made only while a person waits in `pterm pair --via` or setup, because that person must press `y` |
| S8 | Dial `TailscaleIPs`, not DNS (low) | Adopted (N2) |
| S9 | Preflight `Max-Age` (low) | Moot. The offer route never sends CORS headers; `found` is own origin only |
| S10 | `MaxPairingOrigins` has no spare (low) | Moot. Offers never enter the gateway origin sets. On the new computer the offered grant and the link grant carry one origin (home's) |

### v1 feasibility and simplicity review: "redesign"

| # | Finding | Resolution in v2 |
| --- | --- | --- |
| F1 | Vouching alone does not stop the relay: a compromised M vouches truthfully for itself, and admission is any same-owner node | Agreed. Vouching and the phone-initiated request are both dropped |
| F2 | v1 + vouching is about 1,300–1,500 non-test lines and a schema change (fresh database) | v2 is about 650–750 non-test lines (estimate). No storage change, no fresh database |
| F3 | The simpler shape: the new computer pushes its own bound grant to the home the user named | Adopted |

### Security re-check of v2: "sound with changes"

| # | Finding (severity) | Checked against | Resolution |
| --- | --- | --- | --- |
| R1 | Relay when the user names the wrong home: M redeems X's grant with a forged `Origin: M`, offers itself to the real home, and returns X's code to the phone; `y` on X gives M a shell. A wrong `--via` does nothing today (medium) | True: `pairingCheck` compares only the header; `pairingPrompt` names only the app | N5: the prompt names THIS computer and quotes the phone's code-screen sentence. N3: the setup list shows the full origin and OS, and the screen prints "Sent to mac (https://…)". Typing the code: not chosen (N5) |
| R2 | A new grant every 30 s leaves 4–5 live grants (of 8), and a holder can fill all 4 pending pairings (low–medium) | True: `MaxOutstandingGrants = 8`, `MaxPendingPairings = 4`, no grant delete | N1: one live offered grant, re-sent; replaced at 60 s left, with the old one deleted; `admin.pair.offer.end` on every exit. N3: the prompt blocks offers, so one run makes at most one pending pairing from offers |
| R3 | The unauthenticated route allows junk and replacement offers, empties the bucket, and is an oracle (204 vs 403) for untagged same-owner names (low) | True; no compromise: a grant is looked up by hash on its issuer (`RedeemPairingGrant`) | H1 and H2: `204` for every well-formed offer, with silent drops; `Snapshot()` only; the label comes from home's status. DoS accepted. The `X-Forwarded-For` sender check is rejected for now (section 6) |
| R4 | (a) A bound origin gets CORS and a preflight answer on every `/api` path, so "no CORS" was false; (b) an old home answers 403, not 404 (low) | True: `admitAPI` runs before the router; the no-`Origin` branch refuses with 403 | H1: the exception sits at the top of `admitAPI` for the exact path: no CORS, any `Origin` or `Sec-Fetch-*` refused, the cookie never read, no origin forwarded. N4: one fixed line for any non-204 answer; home's body is never printed |
| R5 | `/found` from another app bound on home (low) | True: the gateway sends CORS to bound apps on all routes | H3: `authorizeOwn` in the broker; a test shows that a bound origin gets 400 |
| R6 | Dial details: no IPs in `Node`; do not reuse the webpush guard (low) | True: `Node`/`rawPeer` have no addresses; `publicAddressOnly` refuses `100.64.0.0/10` | N2: checked `TailscaleIPs`, a `Control` that allows only the pinned IP on port 443, a node-ID check before each push, `Proxy: nil`, `ErrUseLastResponse`, no jar, TLS 1.2 or later, `ServerName`, system roots, no skip, no http, 2 KiB, broker only |
| R7 | A written rule changes: grants went only to the local CLI (info) | True: `CreateBoundGrant` comment; threat model | Section 6, item 1; ADR; the threat-model rows in track T; the `CreateBoundGrant` comment is updated in track N; "every device paired with home is the owner's" |
| R8 | Tagged: a tagged home or new computer has every offer refused, but the docs recommend a tagged home (info) | True: `CheckOwner(node, false)`; `docs/multi-computer.md` | N3 and N4: no push with `--allow-tagged`, and a line that points to the QR code and the link. Section 6, item 8 |

## 8. Protocol and storage changes (rule 6)

Additive within v1. Regenerate Go and TypeScript. `protocol/v1/revision.json` → `revision: 3`; `minRevision` stays 1.

| File | Change |
| --- | --- |
| `http-api.schema.json` | `HTTPComputerOfferRequest {origin, grant}`; `HTTPFoundComputer {origin, label, grant, expiresAt}`; `HTTPComputerFoundResponse {computers (max 16)}` |
| `http-api.md` | Route `POST /api/v1/computers/offer`: auth none; `Origin` and `Sec-Fetch-*` must be absent; `204` for every well-formed offer; the other answers of H1. Route `GET /api/v1/computers/found`: cookie; own origin. "Cross-origin requests": the offer route is the one `/api` path with no CORS for any origin. "Pairing flow": the offer path |
| `gateway-rpc.schema.json` | `gateway.computers.offer` (`HTTPComputerOfferRequest` → `{}`); `gateway.computers.found` (credential payload → `HTTPComputerFoundResponse`) |
| `admin-rpc.schema.json` | `admin.pair.offer` (`{via}` → `AdminRPCPairOfferResultPayload {appOrigin, appNode, expiresAt, delivered, problem?}`, where `problem` is `unreachable` or `not_accepted`); error `home_changed`; `admin.pair.offer.end` (`{grant?}` → `{}`) |
| `authentication-rpc.md` | The RPCs above. Limits table: "Offers on home: memory only, 60 s, at most 16 origins, newest per origin wins; gateway bucket 10, then 1 per second. On the new computer: one live offered grant, re-sent every 30 s, replaced at 60 s left, deleted at exit" |
| `internal/protocol/computers.go`, `auth_rpc.go` | Validators and bounds of the new payloads |

Storage: **no schema change.** There is no new table and no new column, so no fresh database is needed. The store gets one delete statement on the existing `pairing_grants` table (`DeleteUnredeemedGrant`). Offers are in memory only.

New constants:

| Constant | Value | Where |
| --- | --- | --- |
| `MaxOffers` | 16 | home |
| `OfferLifetime` | 60 s | home |
| `OfferInterval` | 30 s | CLI |
| `OfferWait` | 10 minutes | CLI |
| `offerRenewAt` | 60 s left | new computer |
| `offerTimeout` | 3 s | new computer |
| `maxOfferAnswer` | 2 KiB | new computer |
| `maxOfferBody` | 1 KiB | gateway |

Versions:

- An old home answers `403`, which counts as `not_accepted`. The CLI prints the line that tells the user to update, or to use the QR code or the link.
- An old new computer never offers. Home's **Found** stays empty for it, and the link flow works.
- The app is served by home, so the app and home are always the same build.

## 9. Implementation split

Order: **P** first and alone. Then **N**, **H** and **W** in parallel: they use fakes of each other until P's types exist, and no file is in two tracks. Then **T**. All tracks start after `feat/setup-ux` and `feat/scan-add-computer` are merged. Those branches change `cmd/pterm/lifecycle.go`, `internal/cli/admin.go`, `internal/cli/confirm.go`, `internal/tailnet/status.go`, `web/src/screens/Computers.tsx` and `AddComputer.tsx`.

| Track | Files | Work |
| --- | --- | --- |
| **P. Protocol** | `protocol/v1/http-api.schema.json`, `http-api.md`, `gateway-rpc.schema.json`, `admin-rpc.schema.json`, `authentication-rpc.md`, `revision.json`; generated `internal/protocol/generated.go`, `web/src/generated/protocol.ts`; `internal/protocol/computers.go`, `auth_rpc.go` (+ tests) | Section 8; drift check |
| **N. New computer** | `internal/tailnet/status.go` (+ test) | `TailscaleIPs` on `rawPeer` and `Node`, Tailscale ranges only |
| | `internal/broker/computers.go` (+ test) | `resolveVia` also returns the node |
| | `internal/auth/service.go`, `internal/store/auth.go` (+ tests) | `DeleteGrant` and `DeleteUnredeemedGrant`; the `CreateBoundGrant` comment states the new rule (R7) |
| | **new** `internal/broker/offer.go` (+ `offer_test.go`) | N1 and N2: the offer state, the two RPC bodies, the dial (a package variable `dialHome` with the production value only), the result |
| | **new** `internal/broker/peerdial_e2e.go` | `//go:build e2e`; section 10 |
| | `internal/broker/admin_rpc.go` | `case protocol.AdminPairOffer`, `case protocol.AdminPairOfferEnd` |
| | `internal/localclient/admin.go` (+ test) | `OfferPairing`, `EndOffer` |
| | `internal/cli/admin.go` (+ test) | The `Admin` interface; N3, N4 and N5 in `pairVia`, `waitForPhone` and `pairingPrompt` |
| | `cmd/pterm/lifecycle.go`, `cmd/pterm/management.go` | Setup's candidate list (full origin, OS, the hint); answer 2 drops its own heading; usage text of `pterm pair --via` |
| **H. Home** | **new** `internal/broker/offers.go` (+ `offers_test.go`) | H2 and H3 |
| | `internal/broker/rpc_server.go` | One field on `ManagementServer` for the offer book |
| | `internal/broker/gateway_rpc.go` (+ test) | `case protocol.GatewayComputersOffer`, `case protocol.GatewayComputersFound` (through `authorizeOwn`) |
| | `internal/gatewayapi/gatewayapi.go`, `internal/gatewayclient/client.go` (+ test) | `OfferComputer`, `FoundComputers` |
| | `internal/gateway/handler.go`, `api.go` (+ `handler_test.go`, `multicomputer_test.go`, `fake_broker_test.go`) | The exception at the top of `admitAPI`; the routes; the offer bucket; the H1 and H3 handlers |
| **W. Web** | `web/src/api/http.ts`, `web/src/computers.ts`, `web/src/screens/Computers.tsx`, styles (+ tests) | `foundComputers()`; the **Found on your network** block, its 5 s poll and its texts |
| | `web/src/pairing/addComputer.ts`, `web/src/screens/AddComputer.tsx` (+ tests) | `found` on `AddComputerLink`; `from=found` in the fragment; the one retry with a replaced grant; the stale-offer text |
| **T. Harness and docs** (after N, H, W) | `scripts/e2e-multicomputer/*` | Section 10 |
| | `docs/multi-computer.md`, `docs/threat-model.md`, `docs/architecture.md`, `docs/pending-work.md`, `docs/progress.md`, `docs/owner-verification.md`, `CHANGELOG.md`, `README.md`, `site/src/content/docs/docs/**` (the guide `computers`, `reference/cli.md`, `security.md`, `troubleshooting.md` (tagged home: no **Found**), `release-notes.md`) | Rules 22, 23 and 25; no internal detail on the public site |

Files that the first v2 brief did not list, and why they are needed:

- `computers.go`: the dial must use the node that `resolveVia` pinned.
- `auth/service.go` and `store/auth.go`: deleting a grant (R2).
- `rpc_server.go`: the `ManagementServer` struct is there.
- `addComputer.ts` and `AddComputer.tsx`: the stale text and the retry are on the add page.

Each helper brief must carry rules 21, 22, 24 and 26.

## 10. Tests

### Unit

| Area | Cases |
| --- | --- |
| `tailnet` | `TailscaleIPs` is parsed; an address outside the Tailscale ranges is dropped; a node with no address |
| The dial (`offer.go`, against an `httptest` TLS server with a recording dialer) | Dials the pinned IP on port 443 and never resolves the host name. `Control` refuses any other IP or port. `ServerName` is the home host. A wrong certificate name gives `unreachable`, with no http retry. A `307` is not followed (`not_accepted`). The answer body is read up to 2 KiB and never shown. A server that waits 5 s ends at 3 s (`unreachable`). No `Origin`, no `Sec-Fetch-*`, no `Cookie`. No proxy, also with `HTTPS_PROXY` set. The body is `{origin, grant}` of this computer |
| Offer state | The first call pins the node; later calls send the same grant; at 60 s left there is a new grant and the old unredeemed one is gone; a redeemed old grant is not deleted; a changed node ID in the snapshot gives `home_changed` and deletes the grant; no snapshot gives `unreachable` with no push; `end` deletes the offered grant and the link grant, not redeemed ones; a second `via` replaces the state; `allowTagged` is refused by the validator |
| Offer book (`offers.go`) | Peer, same owner, untagged: stored. Not a peer, other owner, tagged node, tagged home, own origin, or no snapshot: dropped, and the answer is still `{}`. `Fresh` is never called (a fake counts calls). Newest wins; a 17th origin is dropped; expired entries are dropped; a restart is empty |
| `found` | Own origin only (a bound origin gets 400); expired entries, a changed node ID, a gone host and confirmed computers are left out; the label comes from home's status; at most 16 |
| Gateway | Any `Origin` (home's own, a bound one, `null`), and any `Sec-Fetch-Site`, `-Mode` or `-Dest`: `403`, no CORS header. `OPTIONS` from a bound origin on this path: no CORS header. A cookie on the request changes nothing and is not read. Two `Origin` headers. Missing header; wrong type; a body of 1 KiB + 1 byte; an unknown field; a bad origin or grant: `400 "invalid offer"`. An offer that the broker drops: `204`. The bucket (10, then 1 per s) never takes redeem or poll tokens |
| CLI | The N4 lines for each case; `Sent to mac (https://…)`; the 30 s re-send with a fake clock; the state-change line; `home_changed` ends the command; the 10-minute end; `end` is called on `y`, `N`, Ctrl+C, timeout and error; no offer with `--no-wait` or `--allow-tagged`; the N5 prompt text for a pairing from the link and one from **Add**; control characters in labels are escaped |
| Setup | The candidate list shows the full origin and the OS, and the hint line |
| Web | The Found states; the poll stops when the screen closes or hides; **Add** opens the sheet with `{origin, grant}`; `from=found` survives `takeAddComputerFragment`; on `410`, one `/found` read and one redeem with a new grant; the stale text when there is no new grant; no retry for a pasted link |

### `scripts/e2e-multicomputer`

The harness has no Tailscale IPs and no real certificates: its front stands in for MagicDNS and TLS, as it does for browsers. So:

- **New** `internal/broker/peerdial_e2e.go` (`//go:build e2e`) sets `dialHome` in `init()`. When `PTERM_E2E_FRONT` is set, the dial opens `CONNECT <home host>:443` through that proxy and checks the server certificate by its SPKI hash `PTERM_E2E_SPKI` (the front prints it at start, for Chrome today). `ServerName`, the path, the headers, the node check and the limits stay the production code's. Only the IP guard of `Control` is replaced, and that guard is proven by unit tests and by the real checks.
- The harness builds `pterm` with `-tags webdist,e2e` (`lib.mjs`) and sets the two variables only for its stacks.
- Production binaries have no hook: the file is not compiled without the tag. The harness also builds once without `e2e` and checks that the binary does not contain the string `PTERM_E2E`.
- The fake status already has `TailscaleIPs` (`lib.mjs` `statusJSON`).

| Check | What it proves |
| --- | --- |
| `F-offer` | `pterm pair --via mac` on vps: mac's `found` lists vps with a grant, and the screen says "Sent to mac (https://mac.tailx.ts.net)". Direct `POST`s with curl: with `Origin` or `Sec-Fetch-Site` → `403`, no CORS header. An offer for evil's name (owner mallory), for a tagged node, or for mac's own origin → `204`, but nothing is listed. A bad body → `400` |
| `F-add` (Chrome and WebKit) | **Found** row → **Add** → sheet → code; vps's prompt says "THIS computer (vps)" and the phone says "type y in the terminal on vps"; `y` on vps; the computer is in the list; its sessions show; the device on vps has `Via` mac; the row is gone |
| `F-grants` | While vps waits, its database (sqlite3, read only) has at most 2 unredeemed live grants bound to mac over 3 minutes. The offered grant stays the same between sends and changes at 60 s left. After Ctrl+C, none are left |
| `F-dead` | curl overwrites vps's offer with a dead grant: **Add** shows "vps stopped waiting…". Within 30 s vps's next send replaces it and **Add** works. The QR link works the whole time |
| `F-flood` | 50 offers at once: 10 pass, then `429`; a paired phone's requests and a link pairing still work |
| `F-gone` | Ctrl+C on vps: the row is gone within 60 s. vps's node ID changes on mac: the row is gone at the next poll. mac's node ID changes on vps: the vps command ends with the `home_changed` text |
| `F-setup` | `pterm setup` on a fresh vps, answer 2: the list shows `mac — https://mac.tailx.ts.net (…)`; pick mac; the N4 screen; **Add**; `y` in setup |
| `F-old` | mac runs `pterm` built from `v0.1.0-beta.2`: vps prints "Termivoa on mac cannot receive this yet…", and the QR link still adds vps. vps runs the old build: mac's **Found** stays empty, and the link works |

### Real two-computer checks (owner; `docs/owner-verification.md`)

1. vps's broker under systemd dials mac's Tailscale IP on port 443 and verifies its Let's Encrypt certificate (system roots, a background process, no user session).
2. mac's Tailscale Serve forwards a `POST` with no `Origin` and no `Sec-Fetch-*` to the gateway, with `Host` intact. Also record whether Serve sets `X-Forwarded-For` (for the rejected-for-now option; it is not used).
3. The reverse: mac's broker under launchd offers to vps as home.
4. The whole add, end to end, on a real iPhone Home Screen app and on Android.

## 11. Open risks

1. **Serve and certificates from a background process** (real check 1) are not verified yet. If the dial fails, the CLI says `mac did not answer`, and the QR code and the link work.
2. **Junk offers and floods** stop **Add** while they run (accepted; section 6, item 2). The QR code and the link still work.
3. **The user must read the prompt.** The prompt names this computer and the app, and the phone names the computer. A user who presses `y` without reading can still be relayed through a wrong home.
4. **A tagged home never shows Found.** It uses the link, as today.
5. **Two parallel changes** touch `lifecycle.go`, `cli/admin.go`, `confirm.go`, `status.go`, `Computers.tsx` and `AddComputer.tsx`. This work starts after they merge.

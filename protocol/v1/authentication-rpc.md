# Gateway and local administration RPC v1

Two further correlated RPC schemas use the existing length-prefixed local IPC
after `ipc.hello` / `ipc.welcome`. Each message is
`{version, type, requestId, payload}`. A request type `X` has exactly one
successful result type `X.result`; any request may instead receive the schema's
error type (`gateway.error` / `admin.error`) with a canonical error code and a
bounded safe message. Requests on one connection are processed serially.

The broker selects the permitted schema from the IPC peer's declared role, not
from the message:

| IPC peer | Permitted after the handshake |
| --- | --- |
| `cli` | `broker-rpc.schema.json`, `admin-rpc.schema.json`, or a canonical `hello` with `clientKind: "desktop"` |
| `gateway` | `gateway-rpc.schema.json` only; after `gateway.live.open.result` the connection carries canonical envelopes starting with `hello` with `clientKind: "browser"` |

A gateway request may also carry `origin`: the browser request's exact
`Origin` header, forwarded by the gateway after its own Origin and
`Sec-Fetch-Site` checks. It is absent when the gateway admitted a request
without an `Origin` header as same-origin; the broker then treats it as the
computer's own origin. The broker accepts an origin other than its own only
when it is the app origin bound to that request's credential (or, on
`gateway.pair.redeem` and `gateway.pair.poll`, to that request's grant or
pending pairing). Results and local administration messages never carry
`origin`.

The IPC role is routing inside the current-user boundary, not cryptographic
process identity. It never grants browser authority: every gateway request that
reads or mutates state carries the raw browser credential, which the broker
parses, hashes (SHA-256) and re-authorizes for that request. The credential is
never logged, persisted in plaintext, or echoed.

## Gateway RPC (`gateway-rpc.schema.json`)

| Request | Payload | Result payload |
| --- | --- | --- |
| `gateway.pair.redeem` | `HTTPPairRedeemRequest` | `HTTPPairRedeemResponse` |
| `gateway.pair.poll` | `HTTPPairPollRequest` | `GatewayRPCPairPollResultPayload` |
| `gateway.auth.check` | `GatewayRPCCredentialPayload` | `GatewayRPCPrincipalPayload` |
| `gateway.auth.logout` | `GatewayRPCCredentialPayload` | empty |
| `gateway.settings` | `GatewayRPCCredentialPayload` | `HTTPSettingsResponse` |
| `gateway.session.list` | `GatewayRPCCredentialPayload` | `HTTPSessionListResponse` (newest 200) |
| `gateway.session.create` | credential + `HTTPSessionCreateRequest` | `HTTPSessionMutationResponse` |
| `gateway.session.rename` | credential + `sessionId` + `HTTPSessionRenameRequest` | `HTTPSessionMutationResponse` |
| `gateway.session.terminate` | credential + `sessionId` + `HTTPSessionTerminateRequest` | `HTTPSessionMutationResponse` |
| `gateway.live.open` | `GatewayRPCCredentialPayload` | empty; the connection switches to live mode |
| `gateway.device.rename` | credential + `HTTPDeviceRenameRequest` | `HTTPDeviceSummary` of the credential's own device |
| `gateway.origin` | empty | `GatewayRPCOriginResultPayload`: the configured origin, if any |
| `gateway.push.config` | `GatewayRPCCredentialPayload` | `HTTPPushConfigResponse` |
| `gateway.push.subscribe` | credential + `HTTPPushSubscribeRequest` | empty |
| `gateway.push.unsubscribe` | credential + `HTTPPushUnsubscribeRequest` | empty |
| `gateway.push.test` | `GatewayRPCCredentialPayload` | `HTTPPushTestResponse` |
| `gateway.upload.begin` | `GatewayRPCUploadBeginPayload`: credential, `sessionId`, `generation`, `size` | empty; the connection now carries this upload only |
| `gateway.upload.chunk` | `GatewayRPCUploadChunkPayload`: `data`, 1 byte to 128 KiB as base64 | empty |
| `gateway.upload.commit` | `GatewayRPCCredentialPayload` | `HTTPUploadResponse` |
| `gateway.preview.check` | credential + `sessionId` + `port` | `GatewayRPCPreviewTargetPayload` (`host`: `127.0.0.1` or `::1`), or `preview_missing` |
| `gateway.computers.list` | `GatewayRPCCredentialPayload` | `GatewayRPCComputerListResultPayload`: the device's confirmed `computers` and its unexpired `pendingOrigins` (CSP only) |
| `gateway.computers.add` | credential + `HTTPComputerAddRequest` | `HTTPComputerAddResponse` |
| `gateway.computers.remove` | credential + `origin` | empty |
| `gateway.computers.offer` | `HTTPComputerOfferRequest` (no credential) | empty, also when the offer is dropped |
| `gateway.computers.found` | `GatewayRPCCredentialPayload` | `HTTPComputerFoundResponse` |
| `gateway.providers.get` | `GatewayRPCProviderPayload`: credential + `provider` | `HTTPProviderSnapshotResponse` from the broker's cache |
| `gateway.providers.update` | credential + `provider` + `HTTPProviderUpdateRequest` | `HTTPProviderInfo` |
| `gateway.providers.open` | credential + `provider` + `HTTPProviderOpenRequest` | `HTTPProviderOpenResponse` |
| `gateway.providers.create` | credential + `provider` + `HTTPProviderCreateRequest` | `HTTPProviderCreateResponse`; sent on its own connection (up to 45 s) |
| `gateway.providers.manage` | credential + `provider` + `HTTPProviderManageRequest` | `HTTPProviderManageResponse`; sent on its own connection (revision 5; revision 6 adds `agent`, `prompt` and `openItem`) |

`gateway.origin` and `gateway.computers.offer` are the only requests
besides pairing that carry no credential: the configured origin is not a
secret. The gateway asks for it at
start and after every successful broker probe (once a second), and adds a
valid result to its exact Host and Origin allow-list next to its `--origin`
flags. A production gateway accepts only an `https` origin; a development
gateway only a loopback one. It keeps the last known value while the broker
does not answer. So `pterm config origin` takes effect without a restart.

The same result carries two sorted, unique origin sets, each absent when
empty. `boundOrigins` (at most 64) is the union of app origins bound to any
device, revoked and expired ones included, whose pinned Tailscale node the
broker still trusts: an app whose credential was revoked or expired must read
the 401 to offer pairing again. The gateway uses it only to emit CORS headers,
because the broker checks every request's `origin` against its own credential.
The gateway also probes right after an approved `gateway.pair.poll` from an
app origin, so the app's next request is admitted at once.
`pairingOrigins` (at most 16) are the app origins of unexpired bound grants
and pending pairings; the gateway admits them, and answers their preflight,
only on `/api/v1/pair/*`.

The `gateway.computers.*` requests act only on the list of the device that
owns the credential, and only from this computer's own origin (never a bound
app origin); see `http-api.md`, Computers.

`gateway.computers.offer` never carries an `origin`; the broker drops an
offer that does. The broker reads only its latest Tailscale snapshot (it
never waits on `tailscale` for an offer; the snapshot starts at most one
background refresh per 5 s, as for every per-request check) and keeps the offer only when the
origin's host is a peer in its own tailnet with the same owner, neither node
tagged, and not this computer. Otherwise it drops the offer silently. The
result is empty in both cases, so the answer says nothing about which names
exist. The grant is never logged or written to disk. `gateway.computers.found`
lists the kept offers for the credential's device (see `http-api.md`, Found
on your network); without a snapshot it is `host_unavailable`.

`gateway.device.rename` changes only the label of the device that owns the
credential. Labels stay untrusted display text, never identity.

The `gateway.push.*` requests act only on the device that owns the
credential; the request never names a device. The broker applies the push
host allow-list, stores the subscription, and sends the test notification
itself.

### Image uploads

An image (at most 5 MiB) does not fit in one 256 KiB IPC message, so the
gateway sends it on a new, dedicated connection:

1. `gateway.upload.begin` with the credential, session, generation and the
   exact `size`. The broker authorizes the credential, requires that the
   device holds the controller lease of that running generation, and takes an
   upload slot (one per device, four in total, 12 per minute per device).
2. `gateway.upload.chunk` messages in order, each at most 128 KiB decoded
   (about 171 KiB of base64, well under the 256 KiB message limit). Each waits
   for its empty result, so at most one chunk is in flight. More bytes than
   `size` fail at once.
3. `gateway.upload.commit` with the credential. The broker requires exactly
   `size` bytes, authorizes the credential and the controller again (control
   may have moved), checks the magic bytes, and writes the file. The result
   is `HTTPUploadResponse`.

The broker keeps the bytes in memory only, never on disk before commit. Any
error answers `gateway.error` and ends the connection, which drops the
buffer; so does 30 seconds without a message. Chunk and commit messages on
any other connection are `invalid_request`. The connection ends after the
commit result.

`gateway.preview.check` authorizes the credential, requires a running
session, and allows the port only when a process in that session's process
tree listens on it now (an OS scan at most 2 s old). The gateway sends it for
every preview request and again every 5 s for open preview sockets.

Only the selection (`/__termivoa/select`) from another computer's app carries
the envelope `origin` (the origin of the request's `Referer`); the broker
checks it like every forwarded origin, so an origin that is not the
credential's own bound app origin is `invalid_request`. Every other check
arrives without an origin. For a device that is bound to an app origin the
broker also requires, on every check and with or without an origin, that the
latest Tailscale snapshot still trusts the pinned node: a changed node ID is
`revoked`, a changed tag set, another owner or a missing name is
`invalid_request`, and a status that cannot be read after the grace period is
`host_unavailable`. A device that paired with this computer directly is not
affected. The preview listener answers `invalid_request` as `403`.

Authorization precedes every other decision, including replay of an existing
operation receipt. A poll result carries `credential` and
`credentialExpiresAt` exactly when `status` is `approved`; that one response is
the only delivery of the credential. If it is lost, the browser pairs again.

Live mode is bound to the credential that opened it. The broker assigns the
connection ID, uses the paired device ID as the viewer identity, and rechecks
the credential at every admitted action (`session.attach`, `snapshot.applied`,
`control.*`, `heartbeat`, `input.*`, `terminal.resize`, `history.read`,
`changes.*`, `tasks.list`, `branches.list`, `worktree.remove`), again inside
the session actor right before a `changes.commit`, `changes.push` or
`worktree.remove` starts git (with the controller lease), and again inside the
session actor immediately before queued input is encoded.
Revocation, logout, or expiry sends `access.revoked`, closes the viewer (which
releases its controller lease and drops its queued, not-yet-encoded input),
and closes the connection. Bytes already encoded for the child cannot be
retracted.

## Local administration RPC (`admin-rpc.schema.json`)

| Request | Payload | Result payload |
| --- | --- | --- |
| `admin.pair.grant` | optional `via`, optional `allowTagged` | grant (43-character base64url), `expiresAt`, optional configured `origin`; `appOrigin` and `appNode` when bound with `via` |
| `admin.pair.pending` | empty | at most 16 unexpired pending pairings: `id`, `verificationCode`, `deviceLabel`, `expiresAt`, optional `appOrigin` and `appNode` |
| `admin.pair.approve` | `pendingId` | `pendingId` |
| `admin.device.list` | empty | at most 200 devices: `id`, `label`, `revoked`, `status`, `createdAt`, `lastUsedAt`, optional `appOrigin` and `appNode` |
| `admin.device.revoke` | `deviceId` | `deviceId`, `closedStreams` |
| `admin.device.revoke.via` | `appOrigin` | `appOrigin`, revoked `deviceIds`, `closedStreams` |
| `admin.computers.list` | empty | at most 512 confirmed computers of all devices: `deviceId`, `origin`, `label`, `createdAt` |
| `admin.computers.remove` | `origin` | `origin`, `removed` (devices whose list held it) |
| `admin.pair.offer` | `via` (no `allowTagged`) | `appOrigin`, `appNode`, `expiresAt` of the offered grant, `delivered`; `problem` (`unreachable`, `busy` or `not_accepted`) exactly when not delivered. Never the grant. Errors `home_changed`, and `host_unavailable` when this computer has no Tailscale snapshot |
| `admin.pair.offer.end` | optional `grant` (the link's) | empty |
| `admin.device.rename` | `deviceId`, `label` | the saved `deviceId` and `label` |
| `admin.project.add` | `path`, optional `name` | the saved project |
| `admin.project.list` | empty | at most 200 projects |
| `admin.profile.list` | empty | at most 64 shell profiles |
| `admin.profile.detect` | empty | profiles after idempotent host shell discovery |
| `admin.config.origin` | optional `origin` | the configured origin, if any |
| `admin.shutdown` | empty | `terminatedSessions` (0–64) |
| `admin.gateway.stop` | empty | empty; served only by the gateway control endpoint |

`admin.shutdown` terminates every starting or running session through the same
durable path as `session.terminate` (each ends `exited`, not `lost`), waits a
bounded time for their process trees, writes the result, and then stops the
broker's serve loop; the process exits cleanly. The broker rejects
`admin.gateway.stop` with `invalid_request`.

A running gateway publishes a control endpoint in the `gateway` subdirectory
of the runtime directory, using the broker endpoint machinery: current-user
socket or pipe, single-instance OS lock, and a descriptor whose random token
the `ipc.welcome` returns as `brokerGeneration`. It accepts one
`admin.gateway.stop` from a `cli` peer per connection, answers, and then exits
cleanly; any other request receives `admin.error` `invalid_request`.

`admin.device.revoke` succeeds only after the revocation is durable and every
live stream bound to the device's credentials has been closed. If persistence
fails, the in-memory revocation stays in force (fail closed) and the caller
receives an error rather than a success.

`admin.pair.grant` with `via` (`pterm pair --via`) binds the grant to one app
origin. `via` is a full computer origin, or a short name that matches only
the first label of a node's DNS name in this computer's own tailnet (more
than one match is refused). The broker resolves it in its own tailscale
status and pins the node ID. A tagged app node needs `allowTagged: true`;
`allowTagged` without `via` is invalid. Redeem and poll then accept only that
origin, and the resulting device records it. `appOrigin` absent means "this
computer's own origin only". `appNode` (`TailnetNode`: stable `id`, `owner`
for user-owned nodes, `tags`) is display and pinning data, never
authentication. `admin.device.revoke.via` (`pterm revoke --via`) revokes
every device bound to `appOrigin`, with the same durability rule as
`admin.device.revoke`. `admin.computers.remove` (`pterm computers remove`)
removes the origin from every device's list on this home computer; it cannot
log the phone out on that peer.

`admin.pair.offer` (`pterm pair --via` while it waits in a terminal) offers
this computer to the home `via` names. The broker keeps one offer state in
memory; a call with another `via` replaces it and deletes the old grant.

- With no state for `via`, the broker resolves `via` exactly as
  `admin.pair.grant` does (fresh status, owner rule, pinned node ID), but
  never with `allowTagged`, and makes a bound two-minute grant.
- With a state, it reads its latest snapshot. If home's host now has another
  node ID, or is gone, it deletes the grant, clears the state and answers
  `home_changed`. With no snapshot it skips the push and answers the error
  `host_unavailable`. With 65 s or less left (home lists an offer for 60 s,
  plus the 3 s push limit) it makes a new grant and sends it; the replaced
  grant is deleted (only when not redeemed) after home answers `204`, or at
  the end. Otherwise it re-sends the same grant.
- It then sends `POST https://<home host>/api/v1/computers/offer` with
  `{origin, grant}` (this computer's configured origin) to the pinned node's
  Tailscale IP on port 443, with TLS for home's host name, no `Origin`, no
  proxy, no redirect, a 3 s limit and at most 2 KiB of the answer read.
  `delivered` is true exactly on `204`; no answer is `unreachable`, `429` is
  `busy`, any other answer `not_accepted`. Home's answer text is never
  returned.

`admin.pair.offer.end` deletes the offered grant (and a replaced one not yet
deleted) and the given `grant`, each only when it was not redeemed, and clears the offer state. The CLI calls it
on every exit.

A device's `status` is computed with the same rule the device cap uses:
`revoked` when the device is revoked; otherwise `active` when it holds a
credential that is unrevoked, before its absolute expiry, and used within the
7-day inactivity window; otherwise `expired`. `revoked` (boolean) is kept for
compatibility and is redundant with `status == "revoked"`.

## Pairing limits and lifetimes

| Item | Bound |
| --- | --- |
| Grant | 256-bit, two-minute lifetime, single use; at most 8 unredeemed |
| Pending pairing | independent 256-bit poll secret, five-minute lifetime, at most 4 at once, deleted after 5 wrong poll secrets |
| Verification code | `NNN-NNN`, uniform from `crypto/rand`; comparison only, never authorization |
| Redeem attempts | 30 per minute per broker; excess returns `limit_exceeded` before any row is allocated |
| Poll attempts | 120 per minute per broker; excess returns `limit_exceeded` |
| Browser credential | 256-bit; 30-day absolute and 7-day inactivity expiry; last use persisted at most once per minute |
| Devices | at most 64 rows; the oldest revoked devices are pruned first, active devices never |
| Offers on home | memory only, 60 s, at most 16 origins, the newest per origin wins; gateway bucket 10, then 1 per second; body at most 1 KiB |
| Offers on the new computer | one live offered grant, re-sent every 30 s, replaced at 60 s left, deleted at exit |

Error codes: an unknown, used or expired grant and an unknown or expired
pending pairing return `pairing_expired`; a wrong poll secret, or an unknown,
expired or invalid credential returns `unauthorized`; a revoked credential or
device returns `revoked`; capacity and rate limits return `limit_exceeded`;
`admin.pair.offer` returns `home_changed` when the home it offers to is now
another Tailscale node.

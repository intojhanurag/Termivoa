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

`gateway.origin` is the only request besides pairing that carries no
credential: the configured origin is not a secret. The gateway asks for it at
start and after every successful broker probe (once a second), and adds a
valid result to its exact Host and Origin allow-list next to its `--origin`
flags. A production gateway accepts only an `https` origin; a development
gateway only a loopback one. It keeps the last known value while the broker
does not answer. So `pterm config origin` takes effect without a restart.

The same result carries two sorted, unique origin sets, each absent when
empty. `boundOrigins` (at most 64) is the union of app origins bound to
unrevoked devices; the gateway uses it only to emit CORS headers, because the
broker checks every request's `origin` against its own credential.
`pairingOrigins` (at most 16) are the app origins of unexpired bound grants
and pending pairings; the gateway admits them, and answers their preflight,
only on `/api/v1/pair/*`.

The `gateway.computers.*` requests act only on the list of the device that
owns the credential; see `http-api.md`, Computers.

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

Error codes: an unknown, used or expired grant and an unknown or expired
pending pairing return `pairing_expired`; a wrong poll secret, or an unknown,
expired or invalid credential returns `unauthorized`; a revoked credential or
device returns `revoked`; capacity and rate limits return `limit_exceeded`.

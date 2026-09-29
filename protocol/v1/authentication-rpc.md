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

`gateway.origin` is the only request besides pairing that carries no
credential: the configured origin is not a secret. The gateway asks for it at
start and after every successful broker probe (once a second), and adds a
valid result to its exact Host and Origin allow-list next to its `--origin`
flags. A production gateway accepts only an `https` origin; a development
gateway only a loopback one. It keeps the last known value while the broker
does not answer. So `pterm config origin` takes effect without a restart.

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
`changes.*`), again inside the session actor right before a `changes.commit`
or `changes.push` starts git (with the controller lease), and again inside the
session actor immediately before queued input is encoded.
Revocation, logout, or expiry sends `access.revoked`, closes the viewer (which
releases its controller lease and drops its queued, not-yet-encoded input),
and closes the connection. Bytes already encoded for the child cannot be
retracted.

## Local administration RPC (`admin-rpc.schema.json`)

| Request | Payload | Result payload |
| --- | --- | --- |
| `admin.pair.grant` | empty | grant (43-character base64url), `expiresAt`, optional configured `origin` |
| `admin.pair.pending` | empty | at most 16 unexpired pending pairings: `id`, `verificationCode`, `deviceLabel`, `expiresAt` |
| `admin.pair.approve` | `pendingId` | `pendingId` |
| `admin.device.list` | empty | at most 200 devices: `id`, `label`, `revoked`, `status`, `createdAt`, `lastUsedAt` |
| `admin.device.revoke` | `deviceId` | `deviceId`, `closedStreams` |
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

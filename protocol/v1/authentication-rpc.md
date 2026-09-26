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

Authorization precedes every other decision, including replay of an existing
operation receipt. A poll result carries `credential` and
`credentialExpiresAt` exactly when `status` is `approved`; that one response is
the only delivery of the credential. If it is lost, the browser pairs again.

Live mode is bound to the credential that opened it. The broker assigns the
connection ID, uses the paired device ID as the viewer identity, and rechecks
the credential at every admitted action (`session.attach`, `snapshot.applied`,
`control.*`, `heartbeat`, `input.*`, `terminal.resize`, `history.read`) and
again inside the session actor immediately before queued input is encoded.
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
| `admin.device.list` | empty | at most 200 devices: `id`, `label`, `revoked`, `createdAt`, `lastUsedAt` |
| `admin.device.revoke` | `deviceId` | `deviceId`, `closedStreams` |
| `admin.project.add` | `path`, optional `name` | the saved project |
| `admin.project.list` | empty | at most 200 projects |
| `admin.profile.list` | empty | at most 64 shell profiles |
| `admin.profile.detect` | empty | profiles after idempotent host shell discovery |
| `admin.config.origin` | optional `origin` | the configured origin, if any |

`admin.device.revoke` succeeds only after the revocation is durable and every
live stream bound to the device's credentials has been closed. If persistence
fails, the in-memory revocation stays in force (fail closed) and the caller
receives an error rather than a success.

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

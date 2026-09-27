# Browser HTTP API

Bodies are defined in `http-api.schema.json`. The live terminal uses the
canonical envelopes in `envelope.schema.json` over one authenticated WebSocket.

## Common rules

- The gateway binds `127.0.0.1:8787`. Host must exactly match a configured
  private hostname or the bound loopback address. Forwarded identity headers
  are ignored.
- Every state-changing request (`POST`, `PATCH`) and the WebSocket upgrade
  require an exact `Origin` match. A missing `Origin` is rejected.
- HTTP mutations require `Content-Type: application/json` and the fixed header
  `X-Pterm-Request: 1`. Request bodies are limited to 64 KiB.
- Authenticated responses send `Cache-Control: no-store`.
- Errors return `HTTPErrorResponse` with a stable `errorCode` and a short safe
  message. Stack traces and secrets are never returned.
- Production cookie: `__Host-pterm_session`, `Secure`, `HttpOnly`,
  `SameSite=Strict`, `Path=/`, no `Domain`. The explicit `--dev` profile uses
  `pterm_dev_session` without `Secure` on loopback HTTP only.

## Routes

| Method and path | Auth | Request | Success |
| --- | --- | --- | --- |
| `GET /healthz` | none | – | minimal status |
| `POST /api/v1/pair/redeem` | none | `HTTPPairRedeemRequest` | `200 HTTPPairRedeemResponse` |
| `POST /api/v1/pair/poll` | poll secret | `HTTPPairPollRequest` | `200 HTTPPairPollResponse`; sets cookie only when `approved` |
| `GET /api/v1/settings` | cookie | – | `200 HTTPSettingsResponse` |
| `GET /api/v1/sessions` | cookie | – | `200 HTTPSessionListResponse` |
| `POST /api/v1/sessions` | cookie | `HTTPSessionCreateRequest` | `200 HTTPSessionMutationResponse` |
| `PATCH /api/v1/sessions/{id}` | cookie | `HTTPSessionRenameRequest` | `200 HTTPSessionMutationResponse` |
| `POST /api/v1/sessions/{id}/terminate` | cookie | `HTTPSessionTerminateRequest` | `200 HTTPSessionMutationResponse` |
| `PATCH /api/v1/device` | cookie | `HTTPDeviceRenameRequest` | `200 HTTPDeviceSummary`; renames the calling device only |
| `GET /api/v1/push` | cookie | – | `200 HTTPPushConfigResponse` |
| `POST /api/v1/push/subscribe` | cookie | `HTTPPushSubscribeRequest` | `204`; saves it for the calling device |
| `POST /api/v1/push/unsubscribe` | cookie | `HTTPPushUnsubscribeRequest` | `204`; removes it from the calling device only |
| `POST /api/v1/push/test` | cookie | `{}` | `200 HTTPPushTestResponse` |
| `POST /api/v1/logout` | cookie | `{}` | `204`; clears cookie |
| `GET /api/v1/ws` | cookie | WebSocket upgrade | canonical envelopes |

`GET /api/v1/sessions` records may carry live presence computed by the broker
at request time: `controlled` (some connection holds the controller lease),
`controllerIsYou` (the lease belongs to the requesting device) and
`viewerCount` (0..16 attached viewers). All three are absent when the session
is not live. Lease secrets, connection IDs and other devices' labels are never
included. Local CLI list and status results carry `controlled` and
`viewerCount` only.

Status mapping: `unauthorized`/`revoked` → 401, `pairing_expired` → 410,
`pairing_pending` is not an error (poll returns `pending`), `limit_exceeded` →
429, `session_missing` → 404, `generation_changed`/`session_lost` → 409,
`host_unavailable` → 503, malformed input → 400 with `internal_error` never
used for client mistakes. Only `unauthorized` and `revoked` clear the cookie.
A host clock step back behind the credential's creation or last use is
`host_unavailable` ("host clock changed; retry"): the request is rejected but
the cookie is kept, because the credential cannot be delivered again.

## Pairing flow

1. Local `pterm pair` prints `https://<host>/pair#<grant>` and a QR code.
2. The page reads the fragment, calls `history.replaceState` to remove it, and
   POSTs the grant to `/api/v1/pair/redeem`. It shows `verificationCode`.
3. The owner compares the code and runs `pterm pair approve <pendingId>`.
4. The page polls `/api/v1/pair/poll` every 2 seconds until `approved` or
   expiry. The approved response sets the cookie. The poll secret is kept only
   in memory and cleared afterward.

## WebSocket

After upgrade, the browser sends `hello` with `clientKind: "browser"`. The
gateway binds the connection to the broker-authorized credential; the browser
never supplies device or credential identity in messages. Every later message
follows `local-transport.md` live-mode semantics: observer first, explicit
`control.acquire`, lease and sequence on every input. Revocation or expiry sends
`access.revoked` and closes the socket.

## Session activity fields

Live session records on the gateway session list may include three optional,
non-authoritative display fields:

- `lastOutputAt`: when child output was last applied to the canonical screen
  (RFC 3339, millisecond precision). Any child output counts, even bytes that
  leave the screen unchanged, such as a bell or a title change. Clients compare it with the time
  they last viewed the session to show a "new output" dot.
- `preview`: the last non-blank line of the current screen as plain text, with
  control characters removed and at most 160 characters. It is terminal
  output: render it only as text, never as markup or a link.

- `agent`: the broker's agent state for the session (`AgentStatus` in
  `envelope.schema.json`):
  - `state`: `working` (output in the last 3 seconds, or an "esc to interrupt"
    status line), `waiting` (a recognized question has stayed on screen for
    1 second) or `idle` (quiet for 3 seconds with no question).
  - `stateSince`: when the state or the question last changed (RFC 3339,
    millisecond precision). Use it for "waiting 2 min".
  - `question`: present exactly while `waiting`. `text` is the question line
    (at most 160 characters). Optional `detail` says what is being approved,
    such as the tool and command: up to 6 lines joined with `\n`, each at
    most 120 characters, 600 in total. Up to 6 `options[].label` (at most 80
    each). `id` is a 32-hex hash of the session generation, text, the full
    dialog context behind `detail`, and the labels, so a different command
    gives a different id. All of these are terminal output: render them only
    as text.
  `agent` is absent until the broker's first screen scan (about 0.5 seconds
  after start). The live stream carries the same object on `session.state`.
  Answering uses the live WebSocket message `input.answer`; see
  `local-transport.md` and `docs/agent-state.md`.

All three are absent when the session is not live in this broker. They are not
shown on CLI management results.


## Push notifications

`GET /api/v1/push` returns the broker's VAPID public key (unpadded base64url
of an uncompressed P-256 point) for `PushManager.subscribe`. The subscribe
body is exactly `{endpoint, keys: {p256dh, auth}}`; other
`PushSubscription.toJSON()` fields such as `expirationTime` are rejected by
strict decoding. The gateway checks only the schema shape. The broker
accepts an endpoint only on a known push service host (see
`docs/notifications.md`); any other host is `invalid_request`. More than 64
subscriptions in total, or a test notification within 5 s of the last one,
is `limit_exceeded`. `sent` counts the calling device's subscriptions whose
push service accepted the test notification (0..5). Revocation and logout
delete the device's subscriptions.

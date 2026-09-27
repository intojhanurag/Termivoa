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

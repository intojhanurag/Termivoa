# Browser HTTP API

Bodies are defined in `http-api.schema.json`. The live terminal uses the
canonical envelopes in `envelope.schema.json` over one authenticated WebSocket.

## Common rules

- The gateway binds `127.0.0.1:8787`. Host must exactly match a configured
  private hostname or the bound loopback address. Forwarded identity headers
  are ignored.
- Every state-changing request (`POST`, `PATCH`) and the WebSocket upgrade
  require an exact `Origin` match. A missing `Origin` is rejected.
- Every `/api` request needs exactly one `Origin` that is this computer's
  own or an admitted app origin (see Cross-origin requests), or no `Origin`
  and exactly `Sec-Fetch-Site: same-origin`. `Sec-Fetch-Mode: no-cors` is
  refused with `403`. This applies to every client, with one computer too:
  Safari before iOS 16.4 sends neither header on `GET`, so iOS 16.4 is the
  minimum.
- HTTP mutations require `Content-Type: application/json` and the fixed header
  `X-Pterm-Request: 1`. Request bodies are limited to 64 KiB, except an image
  upload (about 7 MiB, see Image uploads).
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
| `POST /api/v1/sessions/{id}/uploads` | cookie, controller | `HTTPUploadRequest` (body up to 7 MiB) | `200 HTTPUploadResponse`; see Image uploads |
| `GET /api/v1/preview` | cookie | – | `200 HTTPPreviewConfigResponse`: the preview origin to frame, absent when previews are off |
| `POST /api/v1/logout` | cookie | `{}` | `204`; clears cookie |
| `GET /api/v1/computers` | cookie | – | `200 HTTPComputerListResponse`: the calling device's confirmed computers |
| `POST /api/v1/computers` | cookie | `HTTPComputerAddRequest` | `200 HTTPComputerAddResponse`; see Computers |
| `DELETE /api/v1/computers/{host}` | cookie | `{}` | `204`; removes `https://{host}` from the calling device's list |
| `GET /api/v1/ws` | cookie | WebSocket upgrade | canonical envelopes |

`GET /api/v1/sessions` records may carry live presence computed by the broker
at request time: `controlled` (some connection holds the controller lease),
`controllerIsYou` (the lease belongs to the requesting device) and
`viewerCount` (0..16 attached viewers). All three are absent when the session
is not live. Lease secrets, connection IDs and other devices' labels are never
included. Local CLI list and status results carry `controlled` and
`viewerCount` only.

`POST /api/v1/sessions` may carry `worktree: { sourceSessionId, name, base }`.
The broker then creates a git worktree from the live session's repository at
`<home>/termivoa/worktrees/<repo>/<name>` on the new branch `termivoa/<name>`
from `base` (a branch from a fresh listing) and starts the session there; see
`WorktreeCreate` in `broker-rpc.schema.json`. Records of live sessions that
started in such a worktree carry `worktree: { name, repo }`.

Status mapping: `unauthorized`/`revoked` → 401, `pairing_expired` → 410,
`pairing_pending` is not an error (poll returns `pending`), `limit_exceeded` →
429, `session_missing`/`preview_missing` → 404, `generation_changed`/`session_lost` → 409,
`worktree_exists`/`not_a_repository` → 409, `worktree_failed` → 422 (the message ends with what git printed),
`host_unavailable` → 503, malformed input → 400 with `internal_error` never
used for client mistakes. Only `unauthorized` and `revoked` clear the cookie.
A host clock step back behind the credential's creation or last use is
`host_unavailable` ("host clock changed; retry"): the request is rejected but
the cookie is kept, because the credential cannot be delivered again.

## Cross-origin requests

Multi-computer (`docs/multi-computer.md`, section 3) lets the app served by
another computer (its app origin) call this computer. The gateway learns two
origin sets from the broker's origin probe: **bound** origins (app origins of
devices whose pinned Tailscale node is still trusted, revoked and expired
devices included, so their app can read the 401; at most 64) and **pairing** origins (app origins of
unexpired bound grants and pending pairings, at most 16). Invalid entries are dropped.

- An `/api` request whose single `Origin` exactly matches a bound origin, or
  a pairing origin on `/api/v1/pair/*`, is admitted. The gateway forwards
  that `Origin` to the broker with the call, and the broker accepts it only
  if it is the app origin bound to that request's own credential, grant or
  pending pairing, and the pinned Tailscale node still holds it.
- Admitted requests get `Access-Control-Allow-Origin: <origin>`,
  `Access-Control-Allow-Credentials: true` and `Vary: Origin`. Others get no
  CORS headers.
- Preflight (`OPTIONS` with one `Access-Control-Request-Method`) is answered
  `204` only for an admitted origin, with
  `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE`,
  `Access-Control-Allow-Headers: Content-Type, X-Pterm-Request` and
  `Access-Control-Max-Age: 600`.
- The WebSocket upgrade checks the same origins per request.
- Matching is exact: `https`, lowercase host, default port, no trailing dot.
  `null`, near misses and other spellings never match.
- `GET /api/v1/settings` sends the same cookie again with `Max-Age` capped by
  the credential's remaining absolute lifetime, only when the request passed
  every check, so WebKit's 7-day cap on other computers' cookies does not end
  a session in regular use. The server never extends an expiry.
- The app document (`index.html`) adds the exact `https://` and `wss://`
  origins of the cookie device's confirmed and pending computers (at most 8)
  to its CSP `connect-src`. Confirmed computers also get their preview origin
  (see "Preview listener").

## Protocol revision

`HTTPSettingsResponse` carries `protocolRevision`, this computer's additive
revision of protocol v1, and `minProtocolRevision`, the oldest app revision
it still serves. The single source is `protocol/v1/revision.json`
(`revision`, `minRevision`); generation emits it as `ProtocolRevision` /
`MinProtocolRevision` in Go and `PROTOCOL_REVISION` /
`MIN_PROTOCOL_REVISION` in TypeScript. The app refuses a computer whose
`protocolRevision` is below the app's `MIN_PROTOCOL_REVISION` ("Update
Termivoa on vps"), and one whose `minProtocolRevision` is above the app's
`PROTOCOL_REVISION`.

Computers update one at a time, so every change within v1 follows these
rules:

- Bump `revision` by one for every change a peer can observe: a new message,
  field, route or enum value.
- Responses are read leniently (unknown fields are dropped), so a new
  response field needs only the bump.
- Go decodes requests strictly, so the app sends a new request field or
  message only to a computer whose `protocolRevision` is at least the
  revision that added it.
- A new enum value that older apps must parse either raises `minRevision`,
  or the app maps unknown values to `unknown` before validation.
- Raise `minRevision` only when an older app can no longer work with this
  computer.

`HTTPSettingsResponse.hostBuild` is the version of Termivoa the computer runs
(`version+commit`, for example `0.1.0-beta.2+1a2b3c4d5e6f`). The app shows it
in Settings → Computers, so a peer's value is untrusted text: 1 to 128 ASCII
letters, digits and `.` `_` `+` `-` (schema `pattern`, checked by Go and by
the app's validator), always rendered as plain text. Tightening the pattern
adds no field, message, route or enum value, so it needs no `revision` bump;
every build ID the packaging scripts produce already matches.

| Revision | Change |
| --- | --- |
| 1 | First revision. |
| 2 | Preview listener rules (see "Preview listener"): the selection needs a `Referer` of an app origin and answers `200`; every other request must come from the preview origin itself. No schema change. An app frames a peer's preview only when that peer's `protocolRevision` is 2 or higher. |

## Computers

Multi-computer (`docs/multi-computer.md`) keeps a per-device list of peer
computers on the home computer. Entries are keyed by `HTTPComputer.origin`,
a canonical origin: `https`, lowercase DNS host, default port, no trailing
dot. At most 8 per device, pending and confirmed together.

These routes accept only this computer's own origin, never a bound app
origin.

- `POST` with `state: "pending"` records or refreshes a short-lived pending
  entry before pairing, after the home computer finds the host in its own
  tailscale status (same tailnet; same owner unless either node is tagged)
  and pins that node's ID. Pending entries only widen the app's CSP
  `connect-src` for that device; they are never listed and expire.
- `POST` with `state: "confirmed"` confirms the entry after the app's first
  authenticated `GET /api/v1/settings` on the peer succeeded, or renames a
  confirmed entry. The host must still be the pinned node; otherwise the
  request fails with `invalid_request` and the computer must be added again.
- Once home has read its tailscale status, an entry whose pinned node no
  longer holds its name is left out of the list and of the CSP.
- The response echoes the saved entry and the peer's `TailnetNode` (`id`,
  `owner` when user-owned, `tags`) for the app's confirm sheet.
- `DELETE /api/v1/computers/{host}` with body `{}` removes the entry. It does not log the
  phone out on the peer: the app calls the peer's `POST /api/v1/logout`
  first.

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

Live session records on the gateway session list may include these optional,
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
  - `afterWork`: `true` only on an `idle` that ended a working period in this
    broker's lifetime (not the session's startup output); absent otherwise.
    With output the device has not viewed, show "Your turn".
  - `workedSeconds`: total seconds the session spent `working` in this
    broker's lifetime, up to when the status was sent. The difference from
    an earlier value is how long the agent worked in between.
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
- `previews`: TCP ports that processes of this running session listen on
  (`[{port, since}]`, sorted by port, at most 8), verified by the broker from
  the operating system. Also on the live `session.state` notice; absent there
  means none. See `docs/preview.md`.
- `cwd`: the session's live working folder, an absolute path of at most 512
  bytes without control characters. The broker reads it from the operating
  system (macOS, Linux) or from an OSC 7 / OSC 9;9 report in terminal output
  (see `docs/live-folder.md`). It is display text only: never open, link or
  run it. Absent when unknown. Also on the live `session.state` notice.

These fields are absent when the session is not live in this broker. They are
not shown on CLI management results.

Every session record may also carry `titleIsCustom: true` when a user renamed
the session. The default title (the project name) is not custom. Clients show
the last path segment of `cwd` as the session's name unless `titleIsCustom` is
true; the project name stays on the secondary line.


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

## Image uploads

`POST /api/v1/sessions/{id}/uploads` saves one image into the session's
project folder so a coding agent can read it by path. The body is
`{generation, data}`: `data` is the file as standard padded base64, 1 byte to
5 MiB (5,242,880 bytes) decoded, so the JSON body is at most about 7 MiB. The
client's file name is never sent.

- Only the device that holds the session's controller lease may upload
  (`not_controller`, 409, otherwise). The broker checks this at the start and
  again just before it writes, together with the credential and the running
  session `generation`.
- The broker detects the type from the file's magic bytes only: JPEG, PNG,
  WebP or GIF. Anything else is `invalid_request`.
- The broker names the file `.termivoa/uploads/<yyyyMMdd-HHmmss>-<8 hex>.<ext>`
  (local time) inside the project folder, creates it exclusively with mode
  0600 (a current-user-only ACL on Windows), and creates `.termivoa/.gitignore`
  containing `*` when it is missing. `.termivoa` and `uploads` must be plain
  directories; a symlink, junction or file there is refused with
  `invalid_request`.
- Limits: one running upload per device, four in total at the broker and two
  at the gateway, and 12 uploads per minute per device (`limit_exceeded`,
  429).
- The response `path` is relative to the project folder (the session's
  starting directory), for example
  `.termivoa/uploads/20260927-143012-a1b2c3d4.png`, with `size` in bytes and
  the detected `contentType`.

The phone re-encodes every photo through a canvas before upload, which drops
EXIF and GPS metadata and scales the long edge to at most 2048 px. The broker
does not re-encode; it stores the bytes it received. Removing an attachment on
the phone does not delete the file on the computer.

## Preview listener

A separate loopback listener (production 127.0.0.1:8789, exposed as
`https://<host>:8443` by Tailscale Serve) serves only dev-server previews:

| Route | Auth | Result |
| --- | --- | --- |
| `GET /__termivoa/select?session=<uuid>&port=<1..65535>` | app `Referer` + cookie | `200 text/html` with the preview-only cookie `__Host-pterm_preview=<session>.<port>` when the broker allows it; the page moves the frame to `/`. `403` (no app `Referer`, or this app may not use this computer), `401`, `404` (`preview_missing`, `session_missing`) or `400` otherwise |
| any other path | preview origin + cookie + preview cookie | proxied to the selected dev server (HTTP and WebSocket), re-authorized per request |

Rules, checked in this order (revision 2; `docs/peer-preview.md`):

1. Unknown Host is `421`.
2. The selection must carry no `Origin` header and exactly one `Referer`
   header: an app origin followed by one `/` (the app's frame has
   `referrerpolicy="origin"`). The app origin is this computer's own origin,
   or the exact app origin of another computer that a device here is bound
   to; the broker then checks that origin against the request's own
   credential, as for every `/api` call. Matching is exact, as for CORS: a
   path, no trailing `/`, an explicit `:443`, a preview origin, `null`, a
   missing or a doubled `Referer` are `403`.
3. Every other request must carry exactly one `Origin` equal to the preview
   origin itself (form posts, WebSocket handshakes), or no `Origin` and
   exactly one `Sec-Fetch-Site: same-origin`. Everything else is `403`, so no
   other page can load from, frame or open a socket to a dev server. A typed
   URL or `curl` is `403` too.
4. Then the cookie (`401`), the preview cookie (`400`) and the broker check.

The selection page has `Content-Security-Policy: default-src 'none';
frame-ancestors <the app origin of the Referer>`, `Referrer-Policy:
no-referrer` and `Cache-Control: no-store`. It is not a redirect: the request
for `/` must be started by the preview origin to pass rule 3.

Other answers from the listener itself are short `text/plain` bodies that any
page may frame, so the app's frame can show them.

A request to the selection path without credentials is the app's reachability
probe (`fetch` with `mode: "no-cors"`, `credentials: "omit"`): it answers
`403`, or `401` when it carries an app `Referer`. The app cannot read the
status; any answer means that the preview origin is served. `GET
/api/v1/preview` reports the origin whenever the listener runs; it does not
know whether Tailscale Serve exposes it.

The app document's CSP lists the own preview origin in `frame-src` and
`connect-src`, and `https://<host>:8443` of each confirmed computer of the
cookie's device (never a pending one) in both; never a `wss://` preview
origin.

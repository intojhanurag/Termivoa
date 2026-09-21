# P4 implementation boundary and acceptance map

Status: preparation, 2026-09-21. This document refines the authoritative plan;
it does not claim that pairing or authenticated browser access is implemented.
The current gateway still exposes only health checks. No browser route may use
the existing privileged local CLI identity as an authentication shortcut.

## Ownership and message boundaries

The broker owns the authentication service, its in-memory authorization state,
and all SQLite access. `internal/auth` owns secret generation, hashing, expiry,
pairing decisions and credential validation. `internal/store` owns transactional
persistence. The gateway transports bounded requests and sets cookies only after
broker authorization; it must never open the database or approve a device.

Before adding the gateway client, define the following exchanges as canonical
Draft 2020-12 schemas under `protocol/v1`, with generated Go/TypeScript types and
explicit semantic validation. They use the existing length-prefixed JSON IPC,
not an extra transport or an undocumented byte stream.

| Exchange | Allowed caller | Required broker decision |
| --- | --- | --- |
| Create pairing grant | Restricted local CLI | Generate a fresh two-minute grant; return its secret only to the requesting CLI |
| Inspect/approve pending pairing | Restricted local CLI | Show the verification code and bounded device label; approve an unexpired pending ID |
| Redeem grant / poll approval | Gateway | Atomically redeem the grant; require the independent poll secret, not the verification code |
| Authenticate browser request | Gateway | Resolve a valid credential to a broker-owned identity; never trust a caller-supplied device ID |
| Bind live browser stream | Gateway | Bind a fresh connection to that credential and broker generation; start as observer |
| List/revoke devices | Restricted local CLI | Authoritatively invalidate credentials, queued actions and leases; close affected viewers |
| Register project / discover shells | Restricted local CLI | Validate local paths/executables; network session creation still uses saved IDs only |

The current CLI-only management/live negotiation remains restricted. A gateway
cannot choose `desktop` to bypass browser authorization. A browser cannot send
local administration messages through HTTP or WebSocket. Any authenticated
management capability must carry or be bound to a broker-validated credential;
a successful gateway health handshake is not that capability.

The principal includes the broker-resolved credential/session ID, device ID and
expiry/revocation state. Authorize reads and mutations, including retrieval of an
already-deduplicated operation receipt, before returning their data. Logout targets
that credential and its connections; device revocation targets every credential
belonging to that device. A client-declared IPC role is routing, not cryptographic
proof of process identity. Current-user IPC is the intentional local trust
boundary, and no browser-controlled role can cross it as local administration.

## Pairing and credential lifecycle

1. Generate grants, poll secrets and browser credentials independently from
   `crypto/rand`, with 256 bits each. IDs and verification codes are not secrets
   that authorize terminal access. Persist only credential/secret SHA-256 hashes.
2. The local CLI prints the grant in an HTTPS URL fragment. The page removes the
   fragment immediately and submits it in a same-origin POST body. Do not include
   the URL or body in operational logs, diagnostics, browser persistence or referrers.
3. A single transaction checks grant expiry and unused state, consumes it and
   inserts the pending pairing. Concurrent redeemers cannot both succeed. A failed
   transaction leaves no partially consumed grant or partially authorized device.
4. Pending pairings have a bounded lifetime and capacity. Approval is local-only;
   the phone and CLI show the same code for human comparison. Wrong poll secrets,
   expired state and approval of a different pending ID do not grant access.
5. The approved poll atomically creates the device/browser session and consumes
   the pending approval. Only that successful response carries a fresh credential.
   Never persist plaintext to make a lost response replayable. If delivery is lost,
   show an explicit retry-pairing path; do not fabricate a recovered login token.
6. Authentication checks device revocation, credential revocation, 30-day absolute
   expiry and seven-day inactivity expiry. Use injected time in deterministic tests;
   reject clock-related invalid states rather than extending the absolute lifetime.
7. Expired and consumed grants/pending records are pruned with bounded retention.
   Apply explicit capacity/rate limits before untrusted attempts allocate persistent
   rows. Device labels remain untrusted bounded display text, never identity.

The exact pending-record limits, expiry and stable typed results must be recorded
in the schemas and tests when implemented. These details must not be inferred
from a UI timeout or silently depend on a gateway process remaining alive.

## Revocation and actor ordering

Checking a token at WebSocket upgrade is insufficient. The broker must recheck
authorization at action admission and immediately before queued actions can be
encoded. The session generation, connection-specific lease and sequence checks
continue to apply independently of browser authentication.

Revocation needs a broker-wide authorization barrier before per-session fan-out:

- Block new actions/attachments for the identity, persist the revocation and keep
  the in-memory state fail-closed if persistence/cleanup fails.
- Release its leases and remove queued, not-yet-encoded actions in each actor.
- Close affected streams and notify gateways. New subscriptions cannot slip past
  a precomputed connection list during revocation.
- Acknowledge local revocation only with an honest result; do not describe a failed
  database write or incomplete cleanup as successful durable revocation.

Do not hold an authorization lock while waiting on an actor that needs that lock.
Do not perform blocking SQLite work inside terminal parsing or the reply pump.
Tokens expiring during a connection must also stop further actions and viewing;
a heartbeat or open socket cannot extend absolute expiry. Logout revokes the
current browser credential and its bound connections without silently terminating
the managed child or revoking an unrelated device.

Bytes already encoded into the PTY writer or written to the child are in flight
and cannot be retracted. Tests must use barriers to distinguish those bytes from
actions still awaiting authorization; a closed WebSocket alone proves neither.

## HTTP boundary

Production remains `127.0.0.1:8787` behind the approved private HTTPS origin.
Require exact configured Host and exact Origin for mutations and WebSocket
upgrades, including missing-origin rejection. Ignore forwarded-user identity.
HTTP mutations require JSON and a fixed application request header. Validate
shape, UTF-8, size and bounds before invoking broker operations; return stable
public error codes without stack traces or secrets.

Production cookie is `__Host-pterm_session`, Secure, HttpOnly, SameSite=Strict,
Path=/, no Domain. Development is explicit `--dev` with distinct paths, cookie
name and credentials; it does not accept externally forwarded origins or activate
automatically on TLS/Tailscale failure. Authenticated responses are not cacheable.
Pairing grants, polling secrets and access tokens never enter a URL query,
localStorage, service-worker cache or diagnostic export.

The first phone UI must exercise real pairing; do not introduce a temporary
unauthenticated terminal route. Tailscale connectivity is supplemental network
protection, never application authentication. Inspecting/configuring Serve and
production real-phone access remain separately authorized owner gates.

## Checkpoints and required evidence

Implement reviewable checkpoints in dependency order:

1. Auth schemas, bounded secret/expiry primitives and atomic SQLite migrations.
2. Pairing/device service and local administration IPC, with replay/concurrency tests.
3. Broker-bound browser identity, action rechecks, expiry and revocation fan-out.
4. Strict HTTP cookies/Host/Origin/header/content-type/rate-limit boundary.
5. Authenticated WebSocket forwarding of existing canonical frames and inputs.
6. Owner-authorized private HTTPS setup/status integration and cellular evidence.

At each checkpoint test malformed input, failure rollback and restart behavior.
Required adversarial cases include simultaneous redemption, wrong poll secret,
approval replay, lost issuance response, expired/revoked credential, forged and
missing Origin, Host near-matches, cross-profile credentials, delayed gateway
revocation, queued-input revocation, revocation racing attachment, and expiry on
an otherwise-live connection. Capture only synthetic data. Keep unavailable
Windows/phone/network gates pending; Mac unit tests do not establish those gates.

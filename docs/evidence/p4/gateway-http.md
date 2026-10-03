# P4 gateway HTTP and WebSocket boundary (checkpoints 4 and 5, gateway side)

Date: 2026-09-27. Branch `feat/p4-gateway-http`, base `eb8421a`. Apple Silicon
macOS 26.5.2 (25F84), Go 1.27.1, Node 24.11.0. Only synthetic credentials,
UUIDs and fake broker streams were used. No real phone, Tailscale, or
production profile state was touched.

## Scope

- `internal/gateway`: all routes in `protocol/v1/http-api.md` over the frozen
  `gatewayapi.Broker` boundary, the authenticated `/api/v1/ws` relay, and the
  embedded static site.
- `internal/webassets`: committed placeholder site; the real build is embedded
  with `-tags webdist`.
- `cmd/pterm`: `__gateway --origin <origin>` (repeatable). The broker client is
  not wired yet: `parseGatewayConfig` has a `TODO(p4)` for
  `config.Broker = ...` once `internal/gatewayclient` exists. With a nil broker
  the gateway serves health and the static site; API routes return 404.

## Configuration

`gateway.Config` fields: `RuntimeDir`, `HostBuild`, `ListenAddress`, `Profile`,
`ProbeInterval`, `ShutdownTimeout`, `Origins []string`, `Broker
gatewayapi.Broker`, `Ready`. `AllowedHosts` was replaced by `Origins`: each
origin's host is the only extra accepted Host. The development cookie and
loopback origins follow the existing `Profile` (`--dev`); there is no second
dev flag that could disagree with it.

- Production: `127.0.0.1:8787` only; origins must be `https://host[:port]`.
- Development: its own `http://127.0.0.1:<port>` and `http://localhost:<port>`
  origins; extra origins must be loopback.

## Embedding the web build

```sh
pnpm build:web
./scripts/embed-web.sh            # copies web/dist -> internal/webassets/dist (gitignored by dist/)
GOTOOLCHAIN=go1.27.1 go build -tags webdist ./cmd/pterm
```

Without the tag, `go build` embeds `internal/webassets/placeholder/index.html`.
Windows packaging must copy `web\dist` to `internal\webassets\dist` the same
way before building with `-tags webdist`.

## Commands and results

| Command | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.27.1 go vet ./...` | pass |
| `GOTOOLCHAIN=go1.27.1 go test -race ./internal/gateway/... ./cmd/...` | pass (gateway also with `-count=2`) |
| `GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go vet ./...` | pass |
| `gofmt -l cmd internal` | no output |
| `pnpm build:web && ./scripts/embed-web.sh && go test -race -tags webdist -run 'Embedded\|Static' ./internal/gateway/` | pass |
| `go build -tags webdist ./cmd/pterm` then dev broker + `__gateway --dev --listen 127.0.0.1:9181` on temporary dirs; `curl` `/healthz`, `/`, `/pair`, `/assets/index-*.js`, `/assets/nope.js`, `/api/v1/sessions`, bad Host | 200 JSON no-store; 200 HTML no-store; 200 HTML fallback; 200 `text/javascript` immutable; 404; 404 JSON (nil broker); 421. CSP, nosniff and no-referrer on all |
| `./scripts/check` | protocol drift, plan copy, gofmt, vet pass; `go test ./...` fails only `tests/integration TestPtermDesktopAttachNativeOuterTTY` (see below). Remaining steps run by hand: vt, xpty, ultraviolet, `pnpm typecheck`, `pnpm test:web` (47 passed), `pnpm build:web` pass |

`TestPtermDesktopAttachNativeOuterTTY` ("attach executable did not exit:
context deadline exceeded") fails identically on the untouched base `eb8421a`
(worktree `p4-contract`), so it predates this change. It exercises desktop
attach in a native outer PTY and does not use the gateway.

## Test coverage (fake `gatewayapi.Broker`, race detector)

- Every route happy path: redeem, poll, settings, list, create, rename,
  terminate, logout, WebSocket relay round trip.
- Host: exact match, case-insensitive; rejects `mac.tailnet.ts.net.evil`,
  `evil.mac.tailnet.ts.net`, trailing dot, other ports, `X-Forwarded-Host`.
- Origin: missing, forged, near-match, `http` for an `https` origin, `null`,
  duplicate header; on POST, PATCH and the WebSocket upgrade.
- Mutations: missing/wrong `X-Pterm-Request`, wrong content type or charset,
  body over 64 KiB (413), unknown/duplicate fields, trailing values, non-object,
  invalid UTF-8, lone surrogate, bad secret, control characters, over-long
  label, missing field, uppercase UUID, bad path id. Rejected requests never
  reach the broker.
- Cookies: production `__Host-pterm_session` with Secure, HttpOnly,
  SameSite=Strict, Path=/, no Domain, Max-Age from `CredentialExpiresAt`;
  development `pterm_dev_session` without Secure; pending poll sets no cookie;
  approval sets it once; a replayed poll (410) sets none; broker contract
  violations (approved without credential, pending with credential, expired
  credential) return 500 without a cookie; cross-profile cookie name is
  ignored; logout clears the cookie (valid, revoked or absent credential).
- Revoked credential: 401 `revoked` and cookie cleared; error mapping for
  404/409/429/503/500 without leaking broker text for internal or transport
  errors.
- Rate limits: redeem (burst 5, 1 per 12 s), poll (burst 10, 1/s), failed
  auth (burst 20, 1 per 6 s) return 429 `limit_exceeded`; per-client isolation;
  bounded map with idle pruning.
- WebSocket: inbound over 256 KiB closes 1009; binary 1003; non-object, two
  values or invalid UTF-8 1007; broker message over 1 MiB closes 1011; broker
  stream end flushes `access.revoked` then closes 1008; a phone that never
  reads is dropped at the 2 MiB outgoing budget while the fake broker's sends
  never block; shutdown closes with 1001 and refuses later upgrades (503);
  ping keeps a responsive phone connected.
- Static: SPA fallback for `/`, `/pair`, deep links; 404 for missing files with
  an extension or under `/assets/`; content types including
  `application/manifest+json` and `font/woff2`; immutable `/assets/`, no-store
  elsewhere; security headers everywhere; `POST /` 405.

## Known limits

- Rate limits key on the peer IP. Behind Tailscale Serve every request comes
  from `127.0.0.1`, so the limits are effectively global for that origin.
  For that reason the failed-auth limit above was removed later (#149,
  `docs/threat-model.md`); the pairing limits remain.
- The gateway checks that inbound WebSocket messages are one JSON object; the
  broker validates envelope semantics.
- Vite source maps (`build.sourcemap: true`) stay in `web/dist` only; they are not embedded or served (see the review pass below).

## Owner verification needed

- Real Tailscale Serve HTTPS origin: confirm Serve forwards the original
  `Host` (for example `mac.tailnet.ts.net`) to `127.0.0.1:8787`, and run
  `pterm __gateway --origin https://<host>.ts.net`.
- Real iPhone Safari (and Android Chrome) over cellular: `__Host-` cookie
  set on the approved poll, sent on `/api/v1/*` and the WebSocket upgrade,
  cleared on logout; SameSite=Strict behavior when the pairing link is opened
  from the camera/QR app.
- Safari CSP: `connect-src 'self'` allows the same-origin `wss:` WebSocket.
- xterm.js rendering under the CSP (inline styles allowed, no inline script).
- End-to-end with the real broker client (`internal/gatewayclient`) once the
  lead wires `config.Broker`; the relay is tested only with a fake broker.
- Windows gateway runtime (only cross-compiled `go vet` ran here).

## Review pass 2026-09-27

Adversarial review of `git diff eb8421a..HEAD` by a second agent on the same
account (not an independent reviewer). Each fixed finding has a test that
failed before the fix.

| # | Severity | Finding | Status |
| --- | --- | --- | --- |
| 1 | Medium | `decodeStrict` used `encoding/json` v1, which matches field names case-insensitively. `{"GRANT":...}` was accepted, and `{"grant":A,...,"Grant":B}` slipped past the duplicate-key check and used `B`. | Fixed: `encoding/json/v2` `Unmarshal` with `RejectUnknownMembers(true)` (case-sensitive, rejects duplicates, invalid UTF-8, lone surrogates and trailing data). The hand-written token scan is deleted. |
| 2 | Low | Duplicate session cookies: `request.Cookie` took the first one, so a cookie tossed with a longer `Path` (possible for `pterm_dev_session` from another localhost port; `__Host-` blocks it in production) chose the credential. | Fixed: exactly one session cookie is required; duplicates are treated as malformed (401, counted as an auth failure, cookie cleared). |
| 3 | Medium | Source maps and dotfiles were served: `*.map` exposed the original web source, and `all:dist` would also serve `.vite/manifest.json` or `.DS_Store`. | Fixed: the static handler returns 404 for `*.map` and any path segment starting with `.`; `scripts/embed-web.sh` deletes `*.map` after copying (maps stay in `web/dist` for local debugging). |
| 4 | Low | An origin with a default or empty port (`https://host:443`, `http://localhost:80`, `https://host:`) was accepted but can never match, because browsers omit default ports in `Origin` and `Host`. It failed closed with a silent 421/403. | Fixed: rejected at configuration time. |
| 5 | Low | The exact origin was passed to coder/websocket `OriginPatterns`, which uses `path.Match`. IPv6 brackets (`http://[::1]:5173`, dev only) became a character class, so a valid cross-origin dev upgrade got 403 after the exact check passed. | Fixed: glob metacharacters are escaped; the exact check stays authoritative. |
| 6 | Low | `pairPoll` checked `time.Until(expiry) > 0` and `setCookie` computed Max-Age again. An expiry crossing between the two gives `Max-Age` 0, which Go omits, so the cookie would become a browser-session cookie with no expiry. | Fixed: Max-Age is computed once and must be ≥ 1. The race is not deterministic; a sub-second-expired case was added as a regression test. |

Checked with no defect found: every `/api/v1/*` route and the WebSocket
upgrade reach the broker only with the cookie credential (broker
re-authorizes; the upgrade also calls `Authenticate` before `OpenLive` and
before `Accept`); `/api/*` and `/assets/*` misses never fall back to HTML;
Host check (case, trailing dot, ports, absolute-form request URI, missing
Host); `null` and duplicate `Origin`; logout requires Origin, the request
header and `{}`; bodies are bounded by `MaxBytesReader` (chunked and
Content-Length lies included); no logging of secrets and no broker text on
internal or transport errors; limiter map bounded at 1024 clients; relay
read limit, one writer plus concurrent `Ping`, all goroutines joined, broker
and phone blocking bounded by context cancel and the write timeout;
shutdown stops live sockets with 1001 before `Run` returns.

Not fixed (notes for the lead):

- Any broker stream end closes the socket with 1008 "access ended", including
  a broker crash or restart. The `LiveStream` contract does not say how to
  tell revocation from transport failure; a client must use the
  `access.revoked` message, not the close code, to decide to re-pair.
- `http.ServeMux` 301 redirects for unclean paths such as `//api/v1/x` do not
  carry `Cache-Control: no-store` (path check runs before cleaning). No data
  is returned.
- The outgoing budget counts queued messages only; the one message being
  written (≤ 1 MiB) is extra.
- The built JS still ends with `//# sourceMappingURL=...`; the map now 404s.
  `build.sourcemap: 'hidden'` in `web/vite.config.ts` would drop the comment.

Commands after the fixes:

| Command | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.27.1 go test -race -count=2 ./internal/gateway/... ./internal/webassets/... ./cmd/...` | pass |
| `./scripts/embed-web.sh && GOTOOLCHAIN=go1.27.1 go test -race -tags webdist ./internal/gateway/` | pass; no `*.map` in `internal/webassets/dist` |
| `GOTOOLCHAIN=go1.27.1 GOOS=windows GOARCH=amd64 go vet ./...` | pass |
| `GOTOOLCHAIN=go1.27.1 go vet ./...` | pass |
| `gofmt -l cmd internal` | no output |

## PR #24 adversarial review 2026-09-27

Each fix has a regression test that failed before the fix.

| # | Severity | Finding | Fix | Test |
| --- | --- | --- | --- | --- |
| 1 | Medium-low | After the broker sent `access.revoked`, the next phone message made `stream.Send` return `ErrAccessRevoked`. The relay treated it as "host unavailable" and closed at once, so the queued `access.revoked` was never written and there was no 1008 close. | New `gatewayapi.ErrStreamEnded`; `gatewayclient.ErrAccessRevoked` wraps it. On that error the relay stops reading; `Receive` ends the outbox after the notice, and the writer flushes it and closes with 1008. The gateway still imports only `gatewayapi`. | `TestLiveSendAfterRevokeStillFlushesNotice` (16 MiB backlog + `access.revoked`, phone sends a heartbeat; before the fix: EOF after 0 messages) |
| 2 | Low | The shared RPC connection was dropped only after a failure, so the first call after a broker restart wrote to a dead socket (EPIPE): 503 `host_unavailable`, or `ErrDeliveryUncertain` for a mutation that was never delivered. | If the request write fails on a reused connection, the frame never reached the broker complete, so the client drops the connection and retries once on a new one. A fully written request is never retried. | `TestDeadIdleConnectionRedialsOnceBeforeDelivery` (real unix socket; server answers once and closes; next mutation succeeds with one redial; a written but unanswered mutation stays uncertain with no redial) |
| 3 | Low | A small clock step back made `Check` return `ErrInvalidLifetime`; `resolve` mapped it to `ErrUnauthorized`, and the gateway cleared the cookie. The credential is delivered only once, so the phone had to pair again. | `Check` returns `ErrClockRollback` (wraps `ErrInvalidLifetime`) when stored metadata is valid but the clock is behind last use. The broker maps it to `host_unavailable` "host clock changed; retry" (503, cookie kept). Expiry, revocation and corrupt metadata still clear the cookie. | `TestBrowserLifetimeRejectsInvalidMetadataAndClockRollback`, `TestCredentialLifetimeExpiryAndClockRollback` (auth), `TestGatewayClockRollbackIsHostUnavailable` (broker), `TestHostClockRollbackKeepsCookie` (gateway) |
| 4 | – | Expiry delayed during host sleep. | Fixed on a later branch; not changed here. | – |

Commands after the fixes:

| Command | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.27.1 go test -race -count=2 ./internal/auth/... ./internal/gateway/... ./internal/gatewayclient/... ./internal/broker/...` | pass |
| `GOTOOLCHAIN=go1.27.1 go test -race -count=20 -run 'TestLiveSendAfterRevoke\|TestDeadIdle' ./internal/gateway/ ./internal/gatewayclient/` | pass |
| `GOTOOLCHAIN=go1.27.1 go test -count=1 -run Pair ./tests/integration/` | pass |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...` | pass |
| `gofmt -l cmd internal tests` | no output |
| `./scripts/check` (Node 24.11.0) | pass |

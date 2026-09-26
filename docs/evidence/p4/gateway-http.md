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
- The gateway checks that inbound WebSocket messages are one JSON object; the
  broker validates envelope semantics.
- Vite source maps (`build.sourcemap: true`) are embedded and served.

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

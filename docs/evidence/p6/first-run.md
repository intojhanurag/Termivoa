# P6 first-run evidence (development profile)

Date: 2026-09-27. Host: macOS (Apple Silicon), Go 1.27.1. Branch
`feat/hu-setup`.

This run uses only the explicit `--dev` profile, temporary directories under
`/tmp/pterm-e2e`, and loopback port 8799. It registered no LaunchAgent, ran no
`launchctl`, and ran no `tailscale` command. The pairing grant is redacted.
The "phone" is `curl` against the real gateway HTTP API.

Script: build `go build -o /tmp/pterm-e2e/pterm ./cmd/pterm`, then:

1. `pterm up --dev --data-dir <tmp>/data --runtime-dir <tmp>/runtime --listen 127.0.0.1:8799`
2. `pterm project add <tmp> --name Evidence --dev --runtime-dir <tmp>/runtime`
   (flags after the path)
3. `pterm pair --dev --runtime-dir <tmp>/runtime --origin http://127.0.0.1:8799`
   with stdin from a FIFO; the phone redeems the printed link, then the
   owner writes `y` to the FIFO after the prompt shows the phone's code.
4. The phone polls, gets the cookie, and calls settings, sessions and
   `PATCH /api/v1/device` with it.
5. `pterm config origin http://localhost:4545` while the gateway runs; the
   gateway's Host check changes without a restart.

## Output

```text
## pterm up --dev
broker started (pid <pid>)
gateway started (pid <pid>)
Development gateway: http://127.0.0.1:8799/
Pair a browser: pterm pair --dev --runtime-dir /tmp/pterm-e2e/runtime --origin http://127.0.0.1:8799
Press Ctrl+C to stop both processes.

## pterm project add (flags after the path)
Saved project "Evidence" 40ee4503-725d-4811-95fa-9d26a4d2cf73 at "/private/tmp/pterm-e2e"

## phone: POST /api/v1/pair/redeem
phone shows code 620-241
## owner answers y
pair exit 0

## pterm pair output (grant redacted)
Pairing link (expires 2026-09-27T06:09:22.252Z):
http://127.0.0.1:8799/pair#<grant>
Open this link on the phone within 2 minutes. Waiting for it to open the link (Ctrl+C stops waiting).
The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.
Approve only if the phone shows exactly this code.
Phone "Evidence phone" shows code 620-241. Approve? [y/N] Approved a3d12138-9de3-4362-aa6e-67faa19c67ff. The phone finishes pairing on its next poll.

## phone: POST /api/v1/pair/poll
{"device":{"id":"0b0864a8-1bcb-48dc-b1e3-84c6fc9a389c","label":"Evidence phone"},"status":"approved"}

## GET /api/v1/settings with cookie
{'device': 'Evidence phone', 'projects': ['Evidence'], 'shells': 2}
## GET /api/v1/sessions with cookie
200
## PATCH /api/v1/device
{"id":"0b0864a8-1bcb-48dc-b1e3-84c6fc9a389c","label":"Renamed phone"}

## PATCH /api/v1/device with a foreign Origin
403

## pterm devices
DEVICE ID                             LABEL            STATUS  LAST USED
<device-id>  "Renamed phone"  active  2026-09-27T06:07:23.28Z

## origin pickup without restart
Host localhost:4545 before: 421
Pairing origin set to http://localhost:4545
Host localhost:4545 after: 200
Host attacker.example: 421
up exit 0
```

Notes:

- `421` is the gateway's answer for an unknown Host. `localhost:4545` became
  accepted about one probe interval (1 s) after `pterm config origin`, with the
  same gateway process. An unrelated Host stays rejected.
- The redeem, poll and rename used `Origin: http://127.0.0.1:8799`; a foreign
  Origin on the rename was rejected with `403`.

## Automated tests for the same flow

- `cmd/pterm/pair_native_test.go`: `pterm pair` against a real dev broker,
  phone redeem through `gatewayclient`, answer `y` → poll `approved`; answer
  `n` → poll `pending`. `--no-wait` + `pterm pair approve <id> --dev ...`,
  `pterm devices rename`. A running `__gateway` process mode picks up
  `pterm config origin` without restart.
- `cmd/pterm/setup_wizard_test.go`: interactive `pterm setup` with fake
  `launchctl` and fake `tailscale`, a real dev broker and gateway: one
  confirmation, origin from `Self.DNSName`, Serve only after its own `y`,
  "Home" project, QR/link, approval. `--apply --yes` never runs a Tailscale
  change and reports a 443 conflict.
- `internal/gateway/origin_test.go`: configured origin is exact, profile
  checked (production drops http), replaces the previous one, loopback page
  always allowed, and a running gateway picks it up.

## Owner verification needed

These need the real machine and the owner's authorization; they are **not**
covered above:

1. Real LaunchAgents: run `pterm setup` from `/Applications/Phone Terminal.app`
   and answer `y`. Check that both agents load (`launchctl print
   gui/$(id -u)/dev.phone-terminal.broker` and `.gateway`), that setup waits
   until both answer, and that they start again after a logout/login.
2. Real Tailscale: with Tailscale signed in, check that setup shows
   `https://<mac>.<tailnet>.ts.net` from `tailscale status --json` and saves it
   (`pterm config origin`).
3. Real Tailscale Serve: answer `y` to the Serve question only if you want it.
   Check `tailscale serve status` shows HTTPS 443 → `http://127.0.0.1:8787`,
   and that an existing different 443 handler is reported, not replaced.
   Funnel stays off.
4. Real phone over the tailnet (cellular if possible): scan the QR code,
   compare the code, answer `y`, and open a session. This confirms that Serve
   keeps the `Host` header so the gateway's exact Host check accepts it.
5. Tailscale missing or signed out: setup prints the exact next step and still
   pairs a browser on this computer through `http://127.0.0.1:8787`.
6. Windows 11: the same flow with Scheduled Tasks (not run on this Mac).

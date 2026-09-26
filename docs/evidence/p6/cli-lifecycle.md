# P6: pairing QR code, `pterm stop`, `pterm shutdown`

Date: 2026-09-27. Branch `feat/v0-cli-polish`, base `f139b49`. Apple Silicon
macOS 26.5, Go 1.27.1, Node 24.11.0.

No production broker or gateway was stopped or started. Every manual run used
the isolated development profile with temporary `--data-dir` / `--runtime-dir`
directories under `/tmp` and a development listener on `127.0.0.1:18791`. No
`launchctl`, `schtasks`, Tailscale, PATH or startup change was made. No pairing
secret is recorded here.

## Implemented

- `pterm pair [--no-qr]`: on a terminal, prints a QR code of the pairing link
  above the link. Unicode half blocks, 4-module quiet zone, explicit
  black-on-bright-white SGR (`30;107`) so it scans on dark and light themes,
  error correction M, at most 80 columns (a longer link prints the link only).
  Redirected output and `--no-qr` print only the link. Encoder errors never
  contain the link. Dependency: `rsc.io/qr v0.2.0` (BSD-3-Clause).
- `admin.shutdown` / `admin.shutdown.result` (`terminatedSessions` 0–64) and
  `admin.gateway.stop` / `admin.gateway.stop.result` in
  `protocol/v1/admin-rpc.schema.json`, regenerated Go and TypeScript types,
  strict validation in `internal/protocol/auth_rpc.go`.
- Broker: `Registry.TerminateAll` terminates every starting/running session
  through `Registry.Terminate` (durable receipt, state `exited`), waits up to 6 s
  for the process trees, then the admin handler writes the result and cancels
  the broker's serve context (`ManagementServerConfig.Shutdown`). The broker
  exits with status 0. The broker refuses `admin.gateway.stop`.
- Gateway control endpoint (`internal/host/gateway_control.go`): the gateway
  process publishes `<runtime>/gateway/` with the existing broker endpoint
  machinery (current-user socket or pipe, single-instance OS lock, descriptor
  with a random token that the handshake must return). It accepts one
  `admin.gateway.stop` from a `cli` peer, answers, and the gateway exits with
  status 0. No PID is trusted and no process is found or signaled by name. A
  second gateway for the same runtime directory is refused.
- `pterm stop`: stops only the gateway and reports the broker's live session
  count. `pterm shutdown`: shows the active session count, confirms like
  `pterm terminate`, sends `admin.shutdown`, waits for the broker endpoint to
  disappear, then stops the gateway. In the production profile both print, for
  each service registered for startup, that the supervisor may start it again
  and the exact unload command (`launchctl bootout gui/<uid>/<label>` or
  `schtasks /Change /TN "<task>" /Disable`); pterm never runs it.
- Help text, `docs/install.md`, `protocol/v1/authentication-rpc.md`,
  `docs/dependencies.md`, `THIRD_PARTY_NOTICES.md`.

## Automated checks

| Command | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.27.1 go vet ./...` | pass |
| `GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go vet ./...` | pass |
| `gofmt -l cmd internal` | no files |
| `GOTOOLCHAIN=go1.27.1 go test -race ./internal/... ./cmd/...` | pass |
| `./scripts/check` (Node 24.11.0) | pass |

New tests:

- `internal/cli/qr_test.go`: decodes every rendered half block back to the
  encoder's module matrix, checks the light quiet zone, explicit colors and
  width, and that an oversized link's error omits the secret.
- `internal/host/shutdown_darwin_test.go`: real broker, two `/bin/sh` sessions
  that each start a `sleep` descendant; `admin.shutdown` returns 2, the broker
  exits cleanly, both shells and both descendants are gone, SQLite records both
  sessions `exited`. The test fails if `TerminateAll` skips the sessions.
- `internal/host/gateway_control_test.go`: stop without a gateway, single
  instance, a non-stop request is refused without stopping, stop, second stop.
- `cmd/pterm/stop_native_test.go`: real broker and in-process gateway;
  `pterm stop` keeps the session running; declined `pterm shutdown` changes
  nothing; confirmed shutdown ends the session and stops broker and gateway.
  The supervisor note prints only the registered service's unload command and
  runs no startup mutation (fake `launchctl`).
- `internal/lifecycle`: exact unload commands on macOS and Windows.

A rendered terminal QR code (sample link, no real grant) was rasterized from
the half blocks and decoded with macOS CoreImage `CIDetector`; it returned the
exact link.

## Manual run (built binary, isolated development profile)

```sh
pterm __broker --dev --data-dir $R/data --runtime-dir $R/run &
pterm __gateway --dev --runtime-dir $R/run --listen 127.0.0.1:18791 &
pterm project add --dev --runtime-dir $R/run --name e2e $R/proj
pterm new ... (in a 100x30 pseudo-terminal, detached with Ctrl+])
pterm attach --control ... (typed `sleep 900 &`, detached)
```

| Step | Result |
| --- | --- |
| `pterm pair --dev ... --origin http://127.0.0.1:18791` on a pseudo-terminal | 23 QR rows, "Scan the QR code or open this link" |
| same with `--no-qr` | no QR rows, "Open this link" |
| same piped to `grep` | no escape sequences |
| `pterm stop --dev ...` | "Gateway stopped." and "The broker keeps running with 1 active session(s); nothing was ended."; gateway exit status 0; `/healthz` refused; `pterm list` still `running` |
| `pterm stop` again | "The gateway is not running." |
| `echo n \| pterm shutdown --dev ...` | prompt with 1 active session, "Shutdown cancelled." |
| `echo y \| pterm shutdown --dev ...` | "Ended 1 session(s). Broker stopped." "Gateway stopped."; broker and gateway exit status 0; both endpoint descriptors and sockets removed |
| session shell process after shutdown | gone |
| `sleep 900 &` started from the interactive zsh | **survived** (see defect) |

## Defect found (pre-existing, outside this change)

An interactive zsh puts each job in its own process group (job control). The
macOS termination boundary is the session shell's process group
(`internal/ptyhost/process_unix.go`: SIGTERM, then SIGKILL, to that group),
which the owner approved on 2026-09-20. A background job started at the
interactive prompt is therefore outside it and survives `pterm terminate` and
`pterm shutdown`; the shell is killed before it can send SIGHUP to its jobs.
Non-interactive descendants in the shell's group end (automated test above).
The leftover process from this run was killed by its exact PID. A fix belongs
in `internal/ptyhost` (for example a hangup to the shell before SIGTERM, so the
shell forwards it to its jobs) and needs an owner decision; it is not claimed
here.

## Owner verification needed

- `pterm stop` and `pterm shutdown` against gateways/brokers started by a real
  LaunchAgent: confirm launchd does not restart a clean exit
  (`KeepAlive{SuccessfulExit=false}`) and that the printed `launchctl bootout`
  line is correct for the account.
- Native Windows 11: the gateway control named pipe, `pterm stop`,
  `pterm shutdown`, Job Object cleanup at shutdown, Task Scheduler not
  restarting a clean exit, and the printed `schtasks /Change ... /Disable`
  line. Only cross-compiled `go vet` ran here.
- Scanning the QR code with a real phone camera over the private HTTPS origin.
- The interactive-shell background-job defect above.

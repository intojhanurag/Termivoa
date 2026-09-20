# P3 broker/gateway process-mode checkpoint

Date: 2026-09-21

Scope: construct the persistent broker and loopback gateway as independent
modes of the single Go `pterm` executable, and prove that ending/restarting the
gateway cannot end or replace a broker-owned terminal session. This is not the
P3 exit gate: live-session IPC, browser authentication/WebSocket routes, native
Windows 11 execution, startup registration, and packaging remain pending.

## Implemented behavior

- Hidden `pterm __broker` and `pterm __gateway` modes share the shipped Go
  binary. They use independent signal contexts. Production paths come from the
  OS per-user application-data/cache locations; any path or listener override
  requires an explicit `--dev` profile.
- Broker startup creates a random generation and acquires the restricted IPC
  listener plus the nonblocking OS broker lock before opening SQLite or calling
  `BeginBrokerGeneration`. A rejected second broker therefore cannot mutate
  live metadata. The broker then owns the sole Store, Registry, PTYs, terminal
  emulators, and management dispatcher for that generation.
- The data directory and database file reuse the platform current-user-only
  ACL/mode validation used by broker IPC. Persistent data and runtime endpoint
  descriptors remain in separate directories by default.
- Shutdown closes the accepted IPC connections/listener, closes the Registry
  and its managed sessions, waits for lifecycle cleanup, and closes SQLite
  last. No PID scan or matching-process kill is used.
- A new broker generation atomically marks prior `starting`, `running`, or
  `exiting` metadata `lost`. It does not rebuild a Registry entry or rerun a
  command for those rows.
- The gateway receives only a runtime-directory path and performs the
  current-user IPC handshake as client kind `gateway`. Its package has no
  Registry, Store, PTY, process specification, or shell-start capability.
- The initial HTTP surface is only `GET /healthz`, bound to IPv4 loopback. It
  requires an exact configured Host, ignores forwarded identity headers, emits
  restrictive response headers, and reports broker handshake availability.
  Production defaults to `127.0.0.1:8787`; an ephemeral listener is accepted
  only through explicit development configuration.

## Persistence evidence

The macOS integration fixture has two complementary layers:

1. It runs the real broker and gateway runners under independent cancellation
   scopes, creates a real PTY counter child through saved-ID broker RPC, captures
   its authoritative normalized frame through the broker-owned Registry test
   hook, stops the gateway, and observes the same PID/session generation plus a
   later frame revision and counter value. A restarted gateway handshakes with
   the same broker generation. No raw-output or diagnostic wire channel was
   introduced for this test.
2. It builds the actual `pterm` binary, starts `__broker` and `__gateway` as
   separate OS processes, creates the session through broker RPC, sends the
   gateway an interrupt, replays/status-checks the same session through the
   still-running broker, starts a second gateway process on the same loopback
   address, and terminates that exact session. The broker process remains the
   child lifetime owner throughout.

The same runner test attempts a second broker while the real session is live.
It receives `ipc.ErrBrokerAlreadyRunning`, after which the original session is
still `running`. A separate startup-recovery fixture seeds a prior-generation
`running` metadata row, starts a new broker, observes one recovered `lost` row,
and confirms the new Registry is empty. This proves the no-rerun boundary; it
does not claim a host-reboot restore.

## Commands and results

Development host: Apple Silicon macOS 26.5.2. Go 1.27.1 was selected through
the Go toolchain mechanism. The process checkpoint was rebased onto private
`main` at `9d212651efe331f35fdbb66038905e705125072f`; implementation commit:
`47abb44` (`feat(host): run broker and gateway independently`).

```text
GOTOOLCHAIN=go1.27.1 go test -race \
  ./cmd/pterm ./internal/gateway ./internal/host ./internal/ipc \
  ./tests/integration -count=1
passed

GOTOOLCHAIN=go1.27.1 go test -race \
  ./cmd/pterm ./internal/protocol ./internal/ipc ./internal/store \
  ./internal/broker ./internal/gateway ./internal/host ./tests/integration \
  -count=1
passed

GOTOOLCHAIN=go1.27.1 go test -race ./tests/integration \
  -run 'Test(PtermBrokerAndGatewayExecutableModesRestartIndependently|GatewayRestartPreservesBrokerSessionAndCanonicalOutput|BrokerStartupMarksPriorLiveMetadataLostWithoutRerun)' \
  -count=5
passed

GOTOOLCHAIN=go1.27.1 go vet \
  ./cmd/pterm ./internal/gateway ./internal/host ./internal/ipc \
  ./tests/integration
passed

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go build -o <temporary-directory>/pterm.exe ./cmd/pterm
produced a Windows x86-64 PE executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-directory>/gateway.test.exe ./internal/gateway
produced a Windows x86-64 PE test executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-directory>/pterm.test.exe ./cmd/pterm
produced a Windows x86-64 PE test executable

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
protocol generation drift, Go formatting/vet/tests, patched dependency tests,
TypeScript checking, 47 web tests, and the production Vite build passed

git diff --check
passed
```

The first post-rebase `scripts/check` attempt stopped before generation because
the new worktree had no `node_modules` and therefore no pinned `quicktype`
executable. `pnpm install --frozen-lockfile` reused all 160 packages from the
local store without changing the lockfile; the identical full check then
passed as recorded above. This setup failure is not reported as a source-test
pass or hidden as a product defect.

## Honest remaining gates

- The named-pipe, Windows signal, ACL, and process-mode sources cross-compile,
  but they have not run on native Windows 11. Cross-compilation is not a native
  platform pass.
- The gateway health bootstrap is not a browser authentication bypass. It does
  not expose session administration, frames, input, pairing, or local setup.
  Authenticated HTTP/WebSocket-to-live-session IPC remains a later checkpoint.
- Separate LaunchAgents/Scheduled Tasks, `pterm stop`/`shutdown`, Tailscale
  Serve, embedded production assets, signing, packaging, update, and uninstall
  behavior remain pending.

# P4 pairing, authentication and revocation (broker side)

Status: broker checkpoints 1–3 and the broker half of checkpoint 5 from
`docs/p4-authentication-design.md`. Date: 2026-09-27. Environment: macOS 26.5.2
(25F84), arm64, Go 1.27.1, Node 24.11.0. Branch `feat/p4-pairing-broker`, base
`eb8421a`, starting from the cherry-picked auth primitives (`a41d259` →
`6a6e715`). The HTTP gateway routes, cookies and web pairing page belong to the
separate gateway and web branches and are not part of this evidence.

## What is implemented

- `protocol/v1/gateway-rpc.schema.json` and `admin-rpc.schema.json` with
  generated Go/TypeScript types, strict Go decoders (direction, duplicate keys,
  exact fields, secret shape, labels, codes, origins, bounds) and
  `authentication-rpc.md`. Both schemas compile under Ajv 2020 strict mode.
- Migration `004_authentication.sql`: `devices`, `browser_sessions`,
  `pairing_grants`, `pending_pairings`, `settings`. Secret-derived columns are
  32-byte BLOB SHA-256 hashes; `STRICT` rejects plaintext text values.
- `internal/auth.Service`: two-minute grants (8 outstanding), atomic single-use
  redemption into five-minute pending pairings (4 at once) with an independent
  poll secret and a uniform `NNN-NNN` code, local approval, poll-secret check
  (record deleted after 5 wrong secrets), one-time credential issuance,
  authentication with in-memory and durable revocation, 30-day absolute and
  7-day inactivity expiry and clock-rollback rejection, logout, device
  revocation, fixed-window rate limits (30 redeems, 120 polls per minute) and
  live-stream bindings with expiry timers. Injected clock and random source.
- Broker: the IPC peer role selects the schema. Gateway peers reach only
  gateway RPC; every credential-carrying request is authorized before any read,
  mutation or receipt replay. CLI peers keep management RPC and gain admin RPC.
  `gateway.live.open` binds a connection; only a browser hello follows. Access
  is rechecked at every action, inside the session actor at admission, and
  again immediately before queued input is encoded. Revocation, logout and
  expiry send `access.revoked`, close the viewer (lease released, queued input
  dropped) and close the connection before the revocation is acknowledged.
- Shell-profile discovery at broker start and via `pterm profile detect`:
  macOS `/bin/zsh -l`, `/bin/bash -l` and an absolute `$SHELL`; Windows
  `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo` and an
  absolute `pwsh.exe`. Executables are checked for existence, never run.
- `internal/gatewayclient.Client` implements `gatewayapi.Broker` and
  `gatewayapi.LiveStream` over the gateway IPC role.
- CLI: `pterm pair [--origin URL]`, `pair pending`, `pair approve <id>`,
  `devices`, `revoke <device-id>` (confirmation), `project add [--name N] <path>`,
  `project list`, `profile list`, `profile detect`, `config origin [<url>]`.

## Commands and results

All commands were run from the worktree root on 2026-09-27.

```sh
GOTOOLCHAIN=go1.27.1 go vet ./...                                   # pass
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go vet ./...         # pass
GOTOOLCHAIN=go1.27.1 go test -race ./internal/... ./cmd/...          # pass, all packages
GOTOOLCHAIN=go1.27.1 go test -race -count=10 \
  -run 'Gateway|Peer|Browser|Revocation|Logout|Expires|Queued|Administration|Shell' \
  ./internal/broker/                                                # pass (24.5s)
GOTOOLCHAIN=go1.27.1 go test -race -count=10 \
  ./internal/auth/ ./internal/store/ ./internal/gatewayclient/      # pass
GOTOOLCHAIN=go1.27.1 go test -race -count=5 -run TestPairedBrowser ./tests/integration/  # pass
GOTOOLCHAIN=go1.27.1 go test -race -count=1 \
  -skip TestPtermDesktopAttachNativeOuterTTY ./tests/integration/... # pass
./scripts/check    # protocol generation has no drift; vet, gofmt and all Go
                   # unit packages pass; fails only at the pre-existing
                   # TestPtermDesktopAttachNativeOuterTTY (see defects)
(cd third_party/vt && GOTOOLCHAIN=go1.27.1 go test ./...)           # pass
(cd third_party/xpty && GOTOOLCHAIN=go1.27.1 go test ./...)         # pass
GOTOOLCHAIN=go1.27.1 go test github.com/charmbracelet/ultraviolet/...  # pass
pnpm typecheck && pnpm test:web && pnpm build:web                   # pass (47 web tests)
```

A manual smoke test against a real `--dev` broker in a disposable `/tmp`
directory ran `profile list` (zsh and bash login profiles detected), `project
add` / `project list`, `config origin` (rejected `http://example.com`, accepted
`http://127.0.0.1:8788`), `pair` (printed one link; the grant was redacted from
the captured output), `pair pending`, `devices`, `pair approve` of an unknown ID
(`pairing_expired`) and `revoke` of an unknown device (refused before any
prompt). The broker was stopped and the directory deleted afterwards.

## Required adversarial cases

| Case | Test |
| --- | --- |
| Simultaneous redemption | `store.TestPairingGrantIsSingleUseUnderConcurrentRedemption` (8 goroutines), `auth.TestConcurrentRedemptionHasOneWinner` |
| Wrong poll secret | `auth.TestPairingFlowIssuesCredentialExactlyOnce` (random secret and the verification code are rejected), `store.TestPendingPollBurnsAfterFailuresAndExpires` |
| Approval replay / different pending ID | `store.TestPendingPollRequiresSecretApprovalAndIssuesOnce`, `broker.TestGatewayPairingAndCredentialAuthorizedSessionRPC` |
| Lost issuance response | same tests: the second approved poll returns `pairing_expired`; no credential is replayable |
| Expired / revoked credential | `auth.TestCredentialLifetimeExpiryAndClockRollback`, `broker.TestGatewayPairingAndCredentialAuthorizedSessionRPC` (create/rename replay, auth check, list and live open all return `revoked`) |
| Cross-profile credentials | `auth.TestCrossProfileCredentialIsRejected` (a development-store credential is `unauthorized` in another store) |
| Delayed gateway revocation | every gateway request re-authorizes; the bound stream is closed by the broker, and actions check the binding (`broker.TestDeviceRevocationClosesLiveStreamAndReleasesControl`) |
| Queued-input revocation | `broker.TestQueuedInputRevokedAfterAdmissionIsNeverEncoded`: access valid at admission, revoked before encoding; the child receives nothing and the actor rechecked |
| Revocation racing attachment | `auth.TestRevocationRacingBindNeverLeavesAnAuthorizedStream`, `broker.TestRevocationRacingAttachmentLeavesNoLiveViewer` |
| Expiry on a live connection | `auth.TestBindingExpiresOnTheLiveConnection`, `broker.TestLiveStreamClosesWhenCredentialExpires` (`access.revoked` "expired") |
| Honest revocation result | `auth.TestRevokeDeviceClosesBindingsBeforeReportingSuccess` (waits for release; a stuck stream returns `ErrCleanup`), `auth.TestRevocationFailureStaysFailClosed` |
| Role separation | `broker.TestPeerRoleSelectsTheOnlyReachableRPCSchema`, `broker.TestBrowserLiveModeRequiresCredentialBoundGatewayPeer` |
| Migration failure rollback | `store.TestAuthenticationMigrationUpgradesAndRollsBackOnConflict` (v3 data and ledger intact; upgrade succeeds after the conflict is removed) |
| End to end | `integration.TestPairedBrowserControlsSessionUntilRevoked`: real broker, gateway client and admin client; grant → redeem → approve → poll → authenticate → create → live stream → hello/attach/snapshot → control.acquire → input.text + Enter reach the synthetic PTY child → revoke → `access.revoked`; the child keeps running |

## Defects found and fixed

1. Before this change, a `gateway` IPC peer could send unauthenticated
   `session.create` / `session.terminate` management RPC. Management and
   administration are now CLI-only; two existing tests that used the gateway
   role for management now use the CLI role.
2. The returned credential expiry had nanosecond precision while SQLite stores
   milliseconds, so the expiry reported at issuance differed from the one later
   resolved. The service clock is now truncated to milliseconds.
3. `host.RunBroker` returned `context canceled` when cancellation arrived
   between `Ready` and `Serve` (observed once under full parallel load in
   `TestBrokerDataLockRemainsHeldThroughRegistryAndStoreCleanup`). That window
   is now the same orderly shutdown Serve already reported as nil. 40 isolated
   repetitions passed afterwards.
4. Shell discovery for macOS used host `filepath` rules; tests on Windows would
   have rejected POSIX `$SHELL`. It now uses `path` for macOS paths.

## Known pre-existing failure

`TestPtermDesktopAttachNativeOuterTTY` times out waiting for the observer
`pterm attach` to exit after Ctrl+]. The identical failure reproduces on the
unmodified base commit `eb8421a` in a temporary detached worktree (removed
afterwards), so it is not caused by this branch. It is not counted as passing.

## Owner verification needed

- Native Windows execution of the broker, admin commands, PowerShell/pwsh
  discovery and the integration test (only cross-compiled `go vet` ran here).
- A real phone completing pairing through the gateway and web page over the
  private HTTPS origin; this branch provides only the broker side.
- The pre-existing `TestPtermDesktopAttachNativeOuterTTY` failure above.
- QR code rendering: deliberately not implemented; `pterm pair` prints the link
  only and says so.
  (Superseded in P6: `pterm pair` now also prints a terminal QR code; see
  `docs/evidence/p6/cli-lifecycle.md`.)
- Linux shell discovery: not implemented because Linux is not a supported v0
  broker host and the stored profile platform allows only darwin/windows/wsl.

## Security review pass 2026-09-27

Scope: `internal/auth`, the pairing/device/credential parts of `internal/store`,
`internal/broker/gateway_rpc.go`, `internal/broker/admin_rpc.go` and
`internal/gatewayclient`. Reviewer: an agent on the same account as the
author, so this is not an independent review. Branch `fix/v0-auth-review`,
base `f139b49`. macOS arm64, Go 1.27.1.

Defects found and fixed (failing test first, then the fix):

1. Low. Clock rollback extended pairing lifetimes. The store checked only
   `expires_at_ms <= now`, so a wall clock moved backward kept a grant or
   pending pairing redeemable, approvable and pollable for the size of the
   rollback. A creation time after now is now treated as expired in
   redemption, approval, polling and the pending list. The rejected attempt
   consumes nothing. Test: `auth.TestPairingRejectsClockRollback`.
2. Low. Host sleep extended a live binding. The expiry timer was one
   `time.AfterFunc` for the full remaining validity. Go timers use a
   monotonic clock that pauses while the host sleeps, so after a sleep across
   expiry, a passive browser viewer kept receiving output until the delayed
   timer fired or the browser sent a frame. The timer now rechecks the wall
   clock at least once a minute. Test:
   `auth.TestBindingExpiresWhenWallClockPassesDuringHostSleep`.

Checked and found sound: secret and poll-hash comparison (`subtle`;
lookups are by SHA-256 of the presented value); no secret or hash in
errors, results or logs; single-use grant redemption and single credential
issuance (one SQLite connection, `BEGIN IMMEDIATE`, UNIQUE hashes, delete in
the same transaction); `_foreign_keys` and `_txlock` are honored by
modernc.org/sqlite v1.59.0; approval of expired, consumed or unknown pending
IDs; rate limits before any row allocation; bounded pending, grant, failed
poll and device rows; authorization before receipt replay on every gateway
mutation; revocation barrier ordering against `Bind`; inactivity never
extends absolute expiry; separate development and production databases;
gateway peers cannot reach admin or management RPC or desktop live mode;
broker-generated device and credential IDs; parameterized SQL only. Device
labels reject C0/C1 controls but allow bidi and format characters; the CLI
prints them through `strconv.QuoteToGraphic`, which escapes those, so no
unsafe terminal output was found.

Not fixed, availability only: a device whose credential expired but was
never revoked still counts as active toward the 64-device limit and is never
pruned. After 64 such pairings, new pairing returns `limit_exceeded` until
the owner runs `pterm revoke`.

Commands (all pass):

```sh
GOTOOLCHAIN=go1.27.1 go test -race -count=3 ./internal/auth/... ./internal/store/... \
  ./internal/broker/... ./internal/gatewayclient/...
GOTOOLCHAIN=go1.27.1 go test -count=1 -run Pair ./tests/integration/   # -run Pairing matches no test
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go vet ./...
gofmt -l internal tests cmd                                             # no output
```

## Device cap and expired devices 2026-09-27

Fixes the availability gap recorded in the security review above.

Decision: a device counts toward the 64-device cap only while it is not
revoked and holds at least one credential that is unrevoked, before its
absolute expiry, and used within the 7-day inactivity window. The store
reads the same persisted fields (`expires_at_ms`, `last_used_at_ms`,
`revoked_at_ms`) that `auth` uses to authenticate, so a device that can no
longer sign in never blocks pairing. A device whose credentials were all
logged out is also inactive, because it can never authenticate again.

Revoked devices did not count before this change and still do not.

Pruning is capacity-driven and runs in the pairing-completion transaction.
When the table already holds 64 rows, the broker deletes inactive devices
(and, by cascade, their credentials) until one row is free: revoked devices
first, oldest revocation first, then expired devices, oldest last use first.
A live device is never pruned. Until its row is needed, an expired device
stays in `pterm devices`. The table stays bounded at 64 rows, so no separate
time-based retention prune was added.

Not changed: `pterm devices` still shows an expired, unrevoked device as
`active`. The status is computed in `internal/cli` from the admin RPC
`revoked` flag. An `expired` status needs an optional admin RPC device field
and a CLI change, which are outside this change.

Tests: `internal/store` `TestDeviceCapacityIgnoresExpiredDevices` (absolute
and inactivity expiry do not count, revoked pruned before expired, expired
stays listed, live devices never pruned) and the existing
`TestDeviceCapacityPrunesOnlyRevokedDevices`.

# P3 public management-command integration

Recorded 2026-09-21 on macOS 26.5.2 (25F84), arm64, Go 1.27.1.
Branch: `feat/p3-command-integration`, stacked locally on reviewed process modes
and the management client/owned I/O checkpoints. It is not pushed or merged.

## Implemented behavior

`pterm list` and `pterm terminate <session-id>` now use the restricted CLI IPC
client. They neither own SQLite nor create or replace the broker. Profile
resolution shares the validated production/development path rules. Overrides
are explicit; development commands reject production paths and their aliases.
Help and malformed command usage need no broker connection.

Termination opens an owned duplicate of stdin only when confirmation is needed.
It resolves the exact session generation, refreshes its state, and sends one
unique operation ID after explicit approval. Decline and already-exited cases
send no termination. Cancellation joins the active confirmation read before
returning, preserves the caller's stdin, and cannot consume a later answer.
Lost acknowledgements use the client's existing delivery-uncertain result;
mutations are not automatically retried.

## Actual native Mac evidence

`TestManagementCommandsAgainstNativeBroker` starts a broker using temporary
private directories and a SQLite database containing only synthetic saved
configuration. A disposable Go fixture is the real PTY child. The CLI goes
through the actual restricted socket and broker RPC dispatcher to prove:

1. An empty list needs no stdin.
2. A created live session appears with its exact ID and running state.
3. Answering no leaves that same generation running.
4. Answering yes terminates it and its state reaches exited.
5. Repeating terminate reports already exited without opening stdin.
6. Listing afterward reports the exited record, not a silently restarted child.

The focused cancellation fixture also proves the original input remains open
and a late `yes` remains unread after cancellation. No production project,
agent account, startup configuration, or installation is used by these tests.

## Commands

- `go test -race ./cmd/pterm ./internal/cli ./internal/localclient -count=1
  -timeout=60s`: passed.
- `go vet ./cmd/pterm ./internal/cli ./internal/localclient`: passed.
- Windows x64 pure-Go `go test -c ./cmd/pterm` and `go build ./cmd/pterm`:
  passed, producing PE32+ x86-64 binaries. Native Windows execution is pending.
- Node 24.11.0 / pnpm 10.33.0 frozen install: passed from the existing cache.
- Web TypeScript check, 47 web tests, and production build: passed.
- Initial `./scripts/check`: generation/format/vet and the listed Go packages
  passed, but `internal/ptyhost/TestStartReadWriteResizeAndWait` timed out after
  two seconds waiting for fixture READY. The full check did not pass. The same
  startup failure has occurred at prior checkpoints and is being investigated
  separately; it is not ignored or counted as passing. Web checks were then
  run explicitly because the full script stopped at that failure.

## Boundaries

This checkpoint does not expose an unauthenticated HTTP administration route,
wire desktop attachment, implement pairing, or claim a phone experience. Full
P3 and later milestone gates remain pending. Native Windows console UTF-8/VT
mode ownership and restoration must be handled during actual desktop wiring;
the byte I/O adapters alone do not establish Unicode console correctness.

GitHub DNS currently prevents private visibility re-verification, pushes, PR
creation, and remote review of these new commits. No alternate public remote or
external source upload is used to work around that failure.

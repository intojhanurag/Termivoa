# P3 broker-management RPC checkpoint

Date: 2026-09-20

Scope: canonical schema, generated Go/TypeScript types, strict Go validation,
and documentation for local session-management RPC after the IPC handshake.
This checkpoint does not implement broker dispatch, HTTP routes, durable
operation receipts, registry/store integration, or any session/viewer runtime.

## Implemented contract

- Separate Draft 2020-12 `broker-rpc.schema.json` using the existing v1
  version/type/requestId/session identity envelope conventions.
- Direction-specific request/result pairs for bounded session list, create,
  rename, terminate, and status operations, plus typed `broker.error` results.
- A request correlation UUID on every message and a separate canonical
  operation UUID on every create, rename, and terminate mutation, echoed by
  successful mutation results.
- Create requests contain only saved project and shell-profile UUIDs. The
  schema cannot represent executable paths, arguments, working directories, or
  environment values, and strict decoding rejects those unknown fields.
- Session-list pages are limited to 200 validated records with bounded opaque
  cursors. Titles, errors, timestamps, dimensions, UUIDs, lifecycle states,
  exit codes, and whole-message sizes all have explicit semantic bounds.
- Stable errors share the canonical protocol error enum, including management
  codes for invalid requests, operation conflicts, and missing saved project or
  shell-profile records.
- Generated Go and strict readonly TypeScript shapes are checked for drift.

Tests cover every request/result direction, wrong-direction and unknown types,
unknown/duplicate/malformed/trailing JSON, invalid UTF-8, required session
identity, mutation operation IDs, forbidden process-launch fields, message and
field bounds, duplicate list identities, lifecycle/timestamp consistency,
result/envelope identity agreement, and every stable management error code.

## Commands and results

Development host: Apple Silicon macOS 26.5.2. Go 1.27.1, Node 24.11.0, and
pnpm 10.33.0 were used. The isolated worktree dependencies were restored with
the committed frozen lockfile; no dependency manifest or lockfile changed.

```text
PATH="<Node 24>/bin:$PATH" pnpm install --frozen-lockfile
passed; reused the pinned local package store

PATH="<Node 24>/bin:$PATH" pnpm protocol:check
passed; generated Go and TypeScript files did not drift

GOTOOLCHAIN=go1.27.1 go test -race ./internal/protocol -count=20
passed

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
Go formatting, vet, repository tests, and patched dependency tests: passed
TypeScript checking: passed
Web tests: 32 passed
Production web build: passed

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-file> ./internal/protocol
produced a Windows x86-64 PE test executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go build -o <temporary-file> ./cmd/pterm
produced a Windows x86-64 PE executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go vet ./internal/protocol
passed

git diff --check
passed
```

The Windows commands are compilation and static analysis only. Native Windows
11 named-pipe and runtime execution remain pending and are not claimed by this
checkpoint. Broker request dispatch and durable operation-ID deduplication must
be implemented and tested before network mutation routes are exposed.

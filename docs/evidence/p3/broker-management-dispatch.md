# P3 broker-management dispatch checkpoint

Date: 2026-09-21

Scope: integrate the canonical broker-management RPC and durable mutation
receipts over accepted restricted local IPC connections. This is a broker-side
checkpoint. The loopback HTTP/WebSocket gateway, live viewer/input IPC, native
Windows 11 execution, and physical-phone gates remain pending.

## Implemented behavior

- The broker accepts connections only through `ipc.BrokerListener`, after the
  platform current-user peer check, then requires the versioned
  `ipc.hello`/`ipc.welcome` handshake before reading management frames.
- Each connection has one bounded 256 KiB frame reader and one serialized
  1 MiB frame writer. Requests execute serially on a connection, while separate
  accepted connections may execute concurrently.
- List, create, rename, terminate, and status dispatch return the exact request
  correlation ID and required result type/identity. Successful mutations echo
  the operation ID passed to the durable Registry methods.
- Create accepts only saved project/profile IDs. The broker loads enabled
  records from its sole SQLite Store, checks the shell profile against the host
  platform, and builds the process executable, argument array, project working
  directory, dimensions, and `TERM`/`COLORTERM` values locally. No remote path,
  executable, argument, environment, or raw command field exists.
- Operational failures map to bounded stable error codes and generic messages
  that contain no database paths, shell paths, environment, stack traces, or
  terminal data. Malformed JSON, a wrong-direction message, an empty/oversized
  frame, or an ambiguous envelope closes only that connection; it is not given
  a guessed request ID.
- Closing a gateway connection or canceling the management listener closes IPC
  resources and waits for handlers, but does not close the Registry, Store, or
  managed sessions. A retry on a new connection reaches the same durable
  operation receipt instead of starting or terminating twice.
- Session-list cursors are opaque fixed-width base64url offsets, validated again
  during dispatch, and pages remain bounded by the protocol's 200-record limit.

## Automated coverage

Tests exercise:

- broker-side saved project/profile resolution and the exact generated process
  spec;
- create replay after closing and replacing the gateway connection, including
  one process start and a typed conflicting-operation result;
- rename, status, terminate, and terminate replay with one termination side
  effect;
- missing saved configuration and invalid cursor error mapping;
- byte-fragmented frames and two coalesced frames on one connection;
- malformed JSON, host-to-client messages sent in the request direction, and
  an oversized length prefix without allocating its body;
- a real restricted platform listener and handshake, listener cancellation,
  handler shutdown, and proof that the Registry remains available;
- connection cancellation without broker-session shutdown.

## Commands and results

Development host: Apple Silicon macOS 26.5.2. Go 1.27.1 was selected through
the Go toolchain mechanism. Node 24.11.0 and pnpm 10.33.0 are used by the full
repository check.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -run 'TestManagement' -count=20
passed

GOTOOLCHAIN=go1.27.1 go vet ./internal/broker
passed

GOTOOLCHAIN=go1.27.1 go test -race ./internal/protocol ./internal/ipc ./internal/store ./internal/broker -count=1
passed

PATH="<Node 24>/bin:$PATH" pnpm install --frozen-lockfile
passed; reused the pinned package store and did not change a manifest or lockfile

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
protocol generation drift, Go format/vet/tests, patched dependencies,
TypeScript checking, 47 web tests, and the production web build passed

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-file> ./internal/broker
produced a Windows x86-64 PE test executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go build -o <temporary-file> ./cmd/pterm
produced a Windows x86-64 PE executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go vet ./internal/broker
passed

git diff --check
passed
```

The first full-check attempt had one unrelated two-second PTY fixture timeout
waiting for `READY`. The exact failing test passed ten consecutive race-enabled
runs, and the complete repository check then passed. This checkpoint does not
turn that transient timeout into a dispatch failure or conceal that it occurred.

## Honest remaining gates

- The actual macOS Unix-socket listener and peer/handshake path passed locally.
- Windows named-pipe code will be cross-compiled in this checkpoint, but native
  Windows 11 execution remains blocked until the owner performs the final
  physical-machine test. Cross-compilation is not reported as native evidence.
- This checkpoint does not claim gateway HTTP routes, WebSocket terminal
  streaming, authentication/pairing, Tailscale Serve, real-phone behavior,
  packaging, signing, stress, soak, or release readiness.

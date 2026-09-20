# P3 broker-owned session runtime checkpoint

Date: 2026-09-20

Scope: automated macOS development evidence for the first live P3 session
runtime. This is not the P3 exit gate and is not native Windows 11 evidence.

## Implemented behavior

- A broker-owned session lifetime, independent of request, gateway, and viewer
  contexts.
- One PTY reader feeding one authoritative Go terminal actor through a bounded
  output queue.
- One terminal-output consumer writing both structured user input and terminal
  query replies to the PTY in order, including zero-viewer operation.
- Structured text, paste, key, and mouse encoding through the authoritative
  emulator; clients never supply unrestricted terminal bytes.
- Explicit acquire, heartbeat, release, disconnect, resize, termination,
  snapshot, status, and device-revocation operations.
- Disconnect releases control without ending the child process. Explicit
  termination remains separate.
- Controller takeover purges pending input from older lease epochs, including
  an expired controller whose connection identity was already cleared.
- Input is rechecked against the current connection, device, epoch, and expiry
  immediately before terminal encoding.
- Natural process exit becomes visible only after process wait and ordered PTY
  output drain both complete, so an `exited` status cannot expose a truncated
  final screen.
- Shutdown terminates and reaps the process before closing PTY handles, then
  closes the terminal and joins all three runtime pumps.
- A PTY read failure without process exit has a bounded grace period, becomes
  `lost`, and terminates the child instead of remaining falsely `running`.

The final screen remains in memory after natural process exit until the broker
closes the session. This checkpoint does not persist terminal contents.

## Commands and results

Development host: Apple Silicon macOS 26.5.2.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -count=20
passed

GOTOOLCHAIN=go1.27.1 go test -race ./internal/terminal ./internal/ptyhost -count=5
passed

PATH=".../node/v24.11.0/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
Go formatting, vet and tests: passed
TypeScript checking: passed
Web tests: 32 passed
Production web build: passed

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-file> ./internal/broker
produced a Windows x86-64 PE test executable
```

The broker tests build and run a deterministic real PTY child on macOS. They
verify zero-viewer query responses, structured text and Enter input, final
screen capture, exit code, controller-only resize, disconnect survival, and
request-context independence. Instrumented runtime tests verify
terminate-before-wait-before-close ordering and read-failure cleanup.
They also verify bounded shutdown failure reporting and rejection of a request
that raced with shutdown instead of leaving its caller blocked.

The Windows cross-compile proves only that this code compiles for the target.
It is not a native Windows lifecycle pass.

## Independent review

The first source review found four blocking lifecycle gaps: PTY close before
confirmed wait, exit published before final output drain, ignored PTY read
failure, and expired-lease input surviving takeover. Each now has a code fix
and regression coverage. The final independent review found no remaining
blocking issue after repeated race tests and Windows/macOS cross-compilation.

## Pending P3 work

- Session registry, random IDs, and the eight-live-session limit.
- SQLite metadata and broker-restart recovery to `lost`.
- Platform IPC listeners, user-only peer checks, endpoint descriptor, and
  single-broker lock.
- Bounded viewer delivery, snapshot transactions, patch coalescing, and
  immutable history pagination.
- Separate broker/gateway process and gateway-restart identity evidence.
- Native Windows 11 execution on the owner's physical laptop.

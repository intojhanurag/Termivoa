# P3 session registry checkpoint

Date: 2026-09-20

Scope: broker-local session ownership and resource accounting. This is not the
complete P3 exit gate; SQLite recovery, platform listeners, viewer delivery,
and gateway restart evidence remain pending.

## Implemented behavior

- The registry assigns independent cryptographically random UUIDv4 session and
  generation identifiers.
- The default live-resource limit is eight. Concurrent starts reserve capacity
  before spawning, so the limit cannot be exceeded by racing requests.
- Session creation requires a bounded operation ID. Concurrent and later
  duplicate operations return the original result instead of spawning another
  shell. Reuse with different process arguments is rejected by a request
  fingerprint. The in-memory deduplication window retains only lightweight
  ID/generation results and is bounded to 1,024 completed operations; durable
  deduplication will move into the broker-owned store.
- A client-visible managed-session capability cannot close the broker-owned
  runtime. Explicit Terminate remains available and distinct from viewer
  disconnect.
- Process generations signal their terminal lifecycle to the registry. Exited
  sessions are closed and reduced to bounded status metadata; terminal content
  remains memory-only and is not presented as reboot recovery.
- Lost sessions release capacity only after runtime cleanup succeeds. A failed
  cleanup remains owned and counted against the live limit. If the process
  subsequently finishes, the registry observes definitive runtime completion
  and retires it; broker shutdown reports cleanup that remains incomplete.
- The registry retains at most 256 completed status records by default. Both
  this limit and the live-session limit are local resource controls.
- Broker shutdown cancels the registry lifetime, waits for in-progress starts,
  closes every owned session, waits for lifecycle cleanup, and gives all
  callers the same final result.

## Verification

The focused suite includes a real PTY process plus deterministic concurrency
fakes. It covers concurrent capacity reservation, colliding generated IDs,
concurrent duplicate operation IDs, late starts racing shutdown, cleanup-error
propagation, concurrent Close callers, bounded retired records, and a real
process naturally exiting into retained metadata.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -count=5
passed

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
Go formatting, vet and tests: passed
TypeScript checking: passed
Web tests: 32 passed
Production web build: passed

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-file> ./internal/broker
produced a Windows x86-64 PE test executable
```

The cross-compile is not native Windows 11 execution. Owner-device validation
remains a separate pending gate.

## Independent review

The first review identified colliding pending IDs, late-start cleanup loss,
non-idempotent shutdown, blocking capacity inspection, unbounded completed
runtimes, and missing create-operation deduplication. A second pass found that
temporary cleanup failures needed a definitive resource-completion signal and
that operation history must not retain closed runtimes. These paths now have
targeted race tests. The final review found no remaining merge blocker after
20 repeated broker race-test runs.

# P3 controller and input-ingress checkpoint

Date: 2026-09-20

Scope: automated macOS development evidence for the first P3 broker slice. This
is not the P3 exit gate and is not native Windows 11 evidence.

## Implemented behavior

- One explicit controller connection per session generation.
- A cryptographically random 256-bit lease returned only to its holder.
- Serial takeover with a monotonically increasing lease epoch.
- Immediate release on a matching disconnect and expiry after 15 seconds
  without a heartbeat.
- Broker-side generation, connection and constant-time lease checks before
  accepting input.
- Per-lease input sequence high-water tracking: duplicates acknowledge without
  enqueueing, gaps reject, and a replacement lease starts at sequence one.
- A bounded, ordered input inbox for the future session actor, capped by both
  item count and a 2 MiB aggregate payload budget. A full inbox rejects without
  advancing the high-water mark, so an explicit retry remains possible.
- Validation and defensive copying of structured key, text, paste and mouse
  actions before enqueueing.
- Device and connection provenance on pending actions. Device revocation clears
  the active lease and drops that device's queued actions while leaving another
  controller's accepted actions intact. Already-dequeued input is explicitly
  in flight and cannot be retracted.

An `input.accepted` result means the broker accepted the action. It does not
claim that the child process executed it, and this checkpoint adds no automatic
retransmission behavior.

## Commands and results

Development host: Apple Silicon macOS 26.5.2.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/broker -count=1
ok github.com/intojhanurag/phone-terminal/internal/broker

GOTOOLCHAIN=go1.27.1 go vet ./internal/broker
passed

PATH="$NVM_DIR/versions/node/v24.11.0/bin:$PATH" ./scripts/check
Go tests: passed
TypeScript checking: passed
Web tests: 32 passed
Production web build: passed
```

The first `./scripts/check` invocation intentionally failed before running the
suite because the shell's default Node was not Node 24. The second invocation
selected the already-installed Node 24.11.0 toolchain and passed.

## Pending P3 work

- Session registry and live-session limit.
- Broker-owned PTY/terminal actor integration and writer ordering.
- Versioned local IPC, same-user peer checks and single-broker lock.
- SQLite migrations and restart-to-lost lifecycle persistence.
- Separate broker/gateway process integration and gateway-restart identity
  evidence.
- Bounded viewers, immutable history paging and revocation queue removal.
- Native Windows 11 named-pipe, ACL, process-lifecycle and persistence tests.

GitHub Windows CI is supplemental and cannot replace the pending physical
Windows 11 validation.

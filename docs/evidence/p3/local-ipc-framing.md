# P3 local IPC framing checkpoint

Date: 2026-09-20

Scope: platform-independent local IPC framing and handshake validation on the
macOS development host. Unix socket peer validation, Windows named-pipe ACLs,
the broker process lock, endpoint descriptor, and native Windows 11 execution
are later P3 work and remain pending.

## Implemented behavior

- A canonical Draft 2020-12 local IPC handshake schema under `protocol/v1`,
  separate from the browser/desktop WebSocket hello.
- Generated Go and TypeScript types for CLI/gateway hello, broker welcome and
  typed negotiation errors.
- Strict Go validation for direction, version, UUIDs, supported versions,
  payload bounds, unknown fields and duplicate JSON keys.
- Four-byte unsigned big-endian length-prefixed JSON framing.
- Reader limits checked before body allocation: 256 KiB client-to-broker and
  1 MiB broker-to-client when constructed for those directions.
- Serialized full writes so concurrent callers cannot interleave headers or
  bodies, including writers that return short successful writes. Allocation
  occurs inside that serialization boundary.
- Terminal reader/writer poisoning after partial, malformed, oversized or
  truncated framing, preventing another message from being appended to or
  parsed from a stream whose boundary is no longer trustworthy.
- A five-second handshake deadline, earlier context deadlines, active context
  cancellation, exact request/response ID matching and explicit broker
  generation confirmation.
- A typed `protocol_mismatch` response, bound to the initiating request ID,
  when an invalid hello still supplies a usable canonical request identity.

The descriptor PID planned for the next platform slice remains informational;
this handshake will be the liveness authority.

## Commands and results

Development host: Apple Silicon macOS 26.5.2.

```text
PATH="$NVM_DIR/versions/node/v24.11.0/bin:$PATH" pnpm protocol:generate
passed

GOTOOLCHAIN=go1.27.1 go test -race ./internal/ipc ./internal/protocol -count=20
passed

git diff --check
passed
```

## Pending P3 work

- User-private runtime/data path resolution and permissions.
- Process-lifetime single-broker lock.
- Atomic endpoint descriptor with broker generation and diagnostic PID.
- macOS Unix socket listener with same-user peer credential checks.
- Windows named pipe with an explicit current-user SID ACL.
- Broker request dispatch after the handshake.
- SQLite migrations and restart-to-lost lifecycle persistence.
- Separate broker/gateway process and gateway-restart integration evidence.

Native Windows CI will be supplemental. The target Windows 11 physical-machine
gate stays blocked until the owner's laptop is available.

# P3 platform IPC listener checkpoint

Date: 2026-09-20

Scope: broker endpoint ownership and platform transport below the existing
length-prefixed JSON framing and versioned handshake. This checkpoint does not
yet connect the listener to broker request dispatch or prove gateway restart.

## Implemented behavior

- A mode-0700, current-user-owned runtime directory on macOS/Linux development
  hosts, with mode-0600 lock, socket and descriptor files.
- A Unix-domain socket whose accepted peer UID is checked with
  `LOCAL_PEERCRED` on macOS and `SO_PEERCRED` on Linux before the connection is
  returned to the broker.
- A Windows named pipe created through `go-winio` with a protected DACL that
  grants access only to the current process-token SID. The runtime directory,
  lock and descriptor also receive protected current-user-only DACLs.
- A non-blocking OS file lock held for the broker-listener lifetime. A second
  broker receives a typed ownership error; no PID scanning or process killing
  is used.
- An atomically replaced, bounded JSON endpoint descriptor containing
  transport, address, broker generation and diagnostic PID. Descriptor paths,
  type, permissions/ACL and fields are validated before use.
- Client dialing performs the existing bounded protocol handshake and requires
  the live broker generation to match the descriptor. PID presence is never
  treated as liveness.
- Stale descriptor/socket recovery only after obtaining the broker lock. A
  successfully handshaken recorded endpoint is preserved and reported as
  already running. Cancellation during a probe stops recovery without deleting
  the recorded endpoint.
- Unix runtime paths are canonicalized before any lock, socket or descriptor
  operation. Root-owned system aliases such as macOS `/var` are resolved once;
  user-controlled symlink components, foreign-owned ancestors and unsafe
  writable ancestors are rejected before permissions are changed. The
  persistent lock itself is also opened with no-follow semantics.
- Listener close removes only its matching generation descriptor and endpoint,
  then releases the OS lock. The lock file itself remains to avoid inode-lock
  replacement races.
- Existing four-byte big-endian JSON frame limits remain unchanged: 256 KiB
  inbound and 1 MiB outbound.

## Commands and results

Development host: Apple Silicon macOS 26.5.2 (25F84), Go 1.27.1.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/ipc -count=50
passed

GOTOOLCHAIN=go1.27.1 go test -race ./internal/ipc -count=20
passed after the intermediate-symlink retarget fix

PATH="$NVM_DIR/versions/node/v24.11.0/bin:$PATH" ./scripts/check
passed after installing the frozen pnpm lockfile dependencies in this isolated
worktree

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go test -c ./internal/ipc -o <temporary>/ipc-windows.test.exe
passed (compile only)

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go vet ./internal/ipc
passed (static analysis only)

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 GOTOOLCHAIN=go1.27.1 go test -c ./internal/ipc -o <temporary>/ipc-darwin.test
passed

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go test -c ./internal/ipc -o <temporary>/ipc-linux.test
passed

GOTOOLCHAIN=go1.27.1 go vet ./...
passed

git diff --check
passed
```

The native macOS race suite covers endpoint publication, same-user peer
acceptance, private modes, handshake/generation validation, single ownership,
live-endpoint refusal, stale recovery, cancellation safety and lock reuse.
Shared Windows-compatible tests and an ACL-specific Windows test compile into
the Windows test binary.

An independent source review found that the original Unix permission repair
could follow a symlink at the runtime-directory or persistent-lock path before
validating ownership. The implementation now validates the directory with
`Lstat`, opens the lock with `O_NOFOLLOW`, checks the opened file's owner, type
and link count, and only then tightens permissions. A regression test proves
that rejected symlinks leave both external target modes unchanged.

A second independent review demonstrated that checking only the final path
component still allowed an intermediate symlink to be retargeted so two broker
processes acquired different lock inodes. Runtime paths are now canonicalized
through trusted system aliases, unsafe intermediate components are rejected,
and a regression starts broker A, replaces an intermediate directory with a
symlink, then proves broker B cannot create a second lock or endpoint.

One repository-wide `go test ./...` run initially timed out waiting for the
pre-existing PTY fixture's `READY` marker in
`TestStartReadWriteResizeAndWait`. The exact focused test then passed ten
consecutive runs and the full suite passed on immediate rerun. This checkpoint
did not change `internal/ptyhost`; the transient result is recorded rather than
treated as platform-IPC evidence.

## Pending evidence and work

- Native Windows 11 x64 execution of the named-pipe, DACL, lock, stale recovery
  and handshake tests. Cross-compilation is not a native pass.
- A different-user negative peer test. The current macOS host has only the
  owner's test context; mode/DACL restrictions and positive peer identity are
  verified, but cross-account rejection is not claimed as executed evidence.
- Broker request dispatch over accepted framed connections.
- Separate broker/gateway processes and gateway-restart identity evidence.
- SQLite lifecycle persistence and restart-to-lost recovery.

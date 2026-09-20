# P3 SQLite persistence checkpoint

Date: 2026-09-20

Scope: broker-owned local metadata storage. This is not the complete P3 exit
gate; registry integration, durable mutation receipts, platform listeners,
viewer delivery, process separation, and gateway-restart evidence remain
pending.

## Implemented behavior

- `modernc.org/sqlite` runs in the shipped Go process with no Node runtime or
  CGO requirement.
- Embedded, ordered migrations are recorded with names and SHA-256 checksums.
  A changed or future migration stops startup, and a failed migration rolls
  back without modifying the preceding schema.
- The connection enables foreign keys, WAL, a five-second busy timeout, and a
  single database connection. The database file is restricted to the current
  user on Unix; the enclosing Windows data-directory ACL remains part of the
  platform integration checkpoint.
- Projects and shell profiles persist validated display metadata, canonical
  absolute paths, argument arrays, timestamps, and disabled state. Argument
  arrays are encoded as JSON and are never concatenated into commands.
- Session metadata records opaque session, process-generation, and broker-
  generation UUIDs; project/profile IDs; dimensions; timestamps; lifecycle;
  and exit code. Terminal output, history, input, environment variables,
  controller leases, and credentials are not stored.
- Rename, lifecycle-transition, and broker-recovery transactions reject a
  caller time older than the stored session timeline, so a stale event or wall
  clock rollback cannot create negative lifecycle durations.
- Starting a fresh broker generation transactionally changes earlier
  `starting`, `running`, and `exiting` rows to `lost`. It never reruns their
  commands or describes them as live sessions.

## Verification

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/store -count=20
passed

GOTOOLCHAIN=go1.27.1 go vet ./internal/store
passed

git diff --check
passed

GOTOOLCHAIN=go1.27.1 go test ./...
passed on the final rerun

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-file> ./internal/store
produced a Windows x86-64 PE test executable
```

The first repository-wide run observed one timeout waiting for the existing
macOS PTY fixture's `READY` marker while packages were running concurrently.
The exact test then passed 20 consecutive executions, and the complete suite
passed on immediate rerun. This was not attributed to SQLite and remains a
signal to monitor rather than being reported as a new passing guarantee.
The cross-compile confirms the pure-Go build boundary only; it is not native
Windows 11 execution or ACL evidence.

The focused store suite covers reopen behavior, migration rollback and drift,
foreign-key enforcement, Unicode round trips, field-bound validation,
lifecycle transitions, broker-restart recovery, and schema inspection for
forbidden terminal or credential material.

## Explicitly pending

- The broker registry does not yet call this store; that integration must own
  the single broker generation and lifecycle transition mapping.
- Create, rename, and terminate operation receipts are not durable yet and
  must exist before those network mutations are exposed.
- Authentication, pairing, browser-session hashes, settings, and their tables
  belong to P4 and are not claimed by this checkpoint.
- Native Windows 11 execution and data-directory ACL evidence are pending.

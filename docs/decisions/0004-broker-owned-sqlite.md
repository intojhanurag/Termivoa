# ADR 0004: Broker-owned local SQLite persistence

- Status: Accepted by the authoritative plan; implemented in v0 (2026-09-27, verified on the macOS development profile)
- Date: 2026-09-20

## Context

The single-owner local product needs durable metadata and credential hashes without an external database. Concurrent database ownership across gateway and broker would weaken authorization ordering and complicate recovery.

## Decision

Use SQLite through `modernc.org/sqlite`, with the broker as the sole database owner. Persist migrations, devices, browser-session hashes, pairing state, project and shell-profile configuration, session metadata, and settings. Use foreign keys, transactions, prepared statements, WAL, and a bounded busy timeout.

Keep process handles, active controller leases, canonical emulator state, and terminal contents in memory. Store credential hashes rather than raw tokens. Migration failure prevents startup while leaving the previous database intact.

## Consequences

The gateway and CLI perform durable operations through restricted broker IPC. v0 cannot restore a live terminal after broker failure or host reboot, and it never claims to. Release/update tests must cover backups and migration compatibility; no Redis, Postgres, hosted database, or raw transcript store is introduced.

# Pairing secret and lifetime primitives

Status: isolated preparatory checkpoint, not an enabled authentication path.
Date: 2026-09-21. Base: `f306380`. Environment: macOS 26.5.2 (25F84), arm64,
Go 1.27.1. Browser routes, SQLite auth tables, pairing approval, broker principal
binding, cookies and revocation are **not** implemented by this checkpoint.
P3 lifecycle/desktop fixes remain separate prerequisites for integration.

## Implemented policy

- Generate an independent 32-byte secret with `crypto/rand`; fail without a usable
  or partial result on random-source failure. No fallback or configurable seed.
- Accept only the canonical 43-character unpadded base64url encoding. Reject
  aliases, padding, wrong length, invalid trailing bits, controls and non-ASCII.
- Store SHA-256 of the **decoded 32 secret bytes**. This convention must remain
  consistent in the later broker/store implementation. Digest conversion copies
  buffers; comparison uses `crypto/subtle`. Invalid zero values cannot authorize.
- Require an explicit `TransportValue` conversion. Ordinary formatting and slog
  values are redacted; automatic secret JSON/text serialization returns an error.
  This reduces accidental leaks, not a promise that callers cannot log an explicit
  transport string or that Go memory copies can be perfectly zeroized.
- Two-minute pairing grants; 30-day absolute browser lifetime; seven-day inactivity
  expiry. Equality at either expiry is expired. Updating last use cannot revive
  expired/revoked state or extend absolute expiry. Future creation/last-use times,
  malformed metadata and unrepresentable UTC timestamps fail closed.

The lifetime object is broker-owned policy data, never authorization supplied by
a browser. The service must serialize it with persistent credential revocation;
the pure `Used` method is not an atomic database update or a concurrency gate.
Single-use redemption/issuance and two-step approval still require transactions.

## Checks

```sh
go test -race ./internal/auth -count=10
go vet ./internal/auth
git diff --check
```

Actual result after review fixes: ten repetitions passed (1.519s); vet and whitespace
checks passed. Independent review repeated the race suite ten times (1.254s).
Tests cover deterministic hash vectors, storage-copy ownership, malformed formats,
zero values, partial random failure, logging/serialization, exact expiry edges,
non-revival and clock rollback. Generating 64 distinct test values is a smoke
check, not statistical proof of cryptographic entropy.

The initial timestamp-boundary test failed: checking a timestamp's local calendar
year accepted Unix -1 in the host's positive-offset timezone. Validation now uses
the actual Unix epoch boundary and UTC upper year. The failing case is retained.
No phone, network, pairing, token persistence, Windows runtime or release result
is claimed here.

Independent review also reproduced a logging defect in the initial implementation:
String/GoString hooks did not protect numeric formatting or an unexported Secret
field inside a containing struct. Ordinary `slog.TextHandler` could expose the
embedded byte array through that reflection fallback. Secrets and digests now use
opaque immutable backing pointers and explicit formatting hooks; no public method
returns the backing pointer. The unchanged direct/nested/slog reproduction passes
(0.505s), and its cases are retained in repository tests. This protection is for
ordinary formatting/logging, not bespoke recursive reflection or unsafe memory
inspection. The storage-copy and nil-zero-value tests remain passing.

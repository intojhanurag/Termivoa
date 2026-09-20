# Architecture

Status: planned architecture for private development. No runtime component described here has been implemented yet.

## Product boundary

Phone Terminal manages terminal sessions that it starts on one owner's Mac or Windows host. A session can be observed from a desktop attachment and a paired phone browser. Viewer disconnects and gateway restarts must not end a broker-owned shell. A broker crash, OS logout, or host reboot ends the live process boundary; v0 will report the session lost and will not silently rerun commands.

The host must remain awake, logged in, and running the broker. Registered project roots simplify session creation but do not sandbox the shell from the owner's filesystem.

## Process ownership

One Go executable will expose separate process modes:

```text
desktop CLI ───────┐
                   │ restricted user IPC
phone browser ── gateway ──────────────── broker ── PTY ── child shell
    private HTTPS  │                       │
                   └─ embedded web assets  ├─ terminal emulator
                                             └─ SQLite
```

- **Broker:** sole owner of PTYs, child lifetimes, canonical terminal emulators, controller leases, device authorization, and SQLite. It is started independently at user login.
- **Gateway:** loopback HTTP/WebSocket and embedded static assets. It authenticates browser traffic and translates requests to IPC, but does not spawn a shell or own process lifetime.
- **CLI/desktop attachment:** uses restricted local IPC. Its terminal UI consumes normalized frames and emits the same structured input model as the web client.

Local broker IPC uses a user-owned Unix-domain socket on macOS and a named
pipe protected by an explicit current-user SID ACL on Windows. A mode-0700
runtime directory contains a restricted endpoint descriptor and persistent
lock file; an OS lock on that file admits one broker without scanning or
killing processes. The descriptor's PID is diagnostic only. Clients must
complete the versioned handshake and confirm its broker generation before the
endpoint is considered live. A lock owner may replace an endpoint only after
that handshake probe fails. Separate LaunchAgents and per-user Scheduled Tasks
remain packaging work.

On Unix, the runtime path is canonicalized once through trusted root-owned
system aliases. User-controlled symlink components, foreign-owned ancestors
and unsafe writable ancestors are rejected before the lock, socket or
descriptor is touched, preventing one configured path from resolving to two
independent broker locks.

## Terminal data flow

PTY output is untrusted. The broker feeds it to one Go virtual terminal that remains alive without viewers. One actor per session serializes terminal mutation, resize, accepted input, mode changes, revision assignment, and immutable frame capture. Emulator-generated replies are drained concurrently and join accepted input at one PTY writer.

Clients never receive the child's ANSI stream. They receive complete normalized snapshots and changed-row patches containing grid cells, resolved style, cursor state, dimensions, active buffer, generation, revisions, and terminal-width metadata. A patch applies only to its exact base revision; otherwise the client requests a fresh snapshot.

Browser xterm is display-only. A small adapter paints validated rows using an escape-sequence allowlist. It does not forward xterm input or terminal-query responses. React owns navigation and settings, while a dedicated terminal controller applies frames outside React state.

Normal-screen history is bounded and paged as an immutable snapshot. Alternate-screen state remains part of the live emulator. No raw-output log or terminal content is persisted to disk in v0.

## Input and control

Remote and desktop clients send bounded `key`, `text`, `paste`, and `mouse` messages. The broker's emulator encodes them for active terminal modes. Control characters use named key events rather than raw bytes.

Each session has one controller connection. Acquiring control revokes the prior lease, advances a lease epoch, and produces a new random lease secret. Mutating messages carry session generation, lease, and a monotonically increasing sequence. Authorization and lease validity are rechecked at the broker immediately before acceptance.

Input is never replayed automatically after disconnect. A lost acknowledgement yields an uncertain-delivery warning, and a reconnect begins as an observer. Revocation prevents later acceptance but cannot retract bytes already written to a child.

## Protocol boundaries

JSON Schema Draft 2020-12 under `protocol/v1` will be canonical. quicktype will generate Go and TypeScript shapes; the browser will validate with Ajv, and Go will apply explicit semantic and bounds checks. WebSocket uses JSON text messages, while local IPC uses length-prefixed JSON with equivalent limits and chunking.

After the local IPC handshake, session list/create/rename/terminate/status use
the canonical bounded broker-management RPC. Request IDs correlate replies;
unique operation IDs separately deduplicate create, rename, and terminate.
Create requests carry saved project and shell-profile IDs only. Executable
paths, arguments, working directories, and environment values cannot cross the
gateway/CLI-to-broker protocol boundary. Protocol UUIDs use lowercase canonical
spelling so deduplication keys have one representation.

Screen/input counters travel as decimal strings. Session and broker generations distinguish a reconnect from a dead/replaced process. Full snapshots are transactional: chunks are validated and painted as one state before later patches become visible.

## Authentication and network

Production HTTP binds only `127.0.0.1:8787`. Tailscale Serve, when explicitly authorized and configured without overwriting another service, provides the approved private HTTPS origin. Tailscale identity headers are ignored; application pairing and authentication remain mandatory. Funnel and public tunnels are excluded.

Pairing uses an expiring, single-use random grant in a URL fragment, an atomic POST redemption, a separate pending poll secret, and local approval after comparing a verification code. Only credential hashes are stored. Browser credentials use strict secure cookies, exact Origin and Host checks, and broker-authoritative revocation.

The trust boundary excludes malware already running as the same OS user. The processes do not run as root or SYSTEM.

## Persistence

The broker is the sole SQLite owner. Persistent tables cover migrations, paired devices, browser sessions, pairing state, projects, shell profiles, session metadata, bounded mutation receipts, and settings. Session create, rename, and terminate requests use canonical UUID operation IDs and request fingerprints. Their metadata change and result receipt commit in one transaction; a retry returns that original result, while reuse for different request content is a conflict. Broker shutdown closes the mutation admission gate before receipt lookup, waits for already-admitted rename and terminate operations, and retains process-termination and cleanup failures as lifecycle diagnostics. A terminal event may precede the latest metadata mutation timestamp, so persistence keeps `updated_at` monotonic while recording the actual `ended_at`. Receipts for active sessions are never evicted. Terminal-session receipt history is pruned first, and the broker rejects a new unique mutation if active receipts alone reach the hard cap. WAL, foreign keys, transactions, prepared statements, and a bounded busy timeout are required. Raw terminal data, provider credentials, active process handles, and controller leases are not persisted.

Migration failure must prevent startup without damaging the prior database. Release updates retain a previous signed version and must test schema backup/rollback compatibility.

## Failure and resource boundaries

- Gateway failure: existing broker sessions continue; clients reconnect and resynchronize.
- Viewer failure or slowness: drop/coalesce screen updates and eventually disconnect the viewer; never discard bytes before emulator interpretation or block the child indefinitely.
- Broker failure: active sessions become lost; supervision starts a new broker generation without replaying commands.
- Explicit termination: broker ends the selected process tree. Detach only closes a viewer, and Ctrl+C remains ordinary terminal input.
- Default live-session limit: eight, with three busy sessions in validation.
- Screen, input, history, queue, payload, and dimension limits are specified by the implementation plan and enforced at protocol boundaries.

## Platform and release boundaries

The intended releases are a signed/notarized macOS app/DMG and a signed per-user Windows installer. Unsigned development artifacts remain visibly distinct. Node.js is never required on an installed user's machine because production web assets are embedded in the Go binary.

Technical completion never authorizes publication or a visibility change. The owner manually changes repository visibility only after the release checklist and history/artifact review are complete.

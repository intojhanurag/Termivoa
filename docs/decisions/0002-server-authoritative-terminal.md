# ADR 0002: Server-authoritative terminal replication

- Status: Accepted by the authoritative plan; implementation pending
- Date: 2026-09-20

## Context

A newly attached browser cannot safely reconstruct hidden parser, mode, cursor, and alternate-buffer state from a tail of raw terminal bytes. Multiple clients must also avoid producing duplicate device-query replies or competing input encodings.

## Decision

The broker maintains one authoritative Go virtual terminal per live session. PTY output is interpreted there even with no viewers. Clients receive normalized full-screen snapshots and changed-row patches; they never receive the child's original ANSI stream. Structured input is encoded by the same emulator, and the broker is the only terminal-query responder.

Browser xterm is a display surface with stdin and independent scrollback disabled. The desktop client consumes the same frame and input protocols. Snapshot revision, frame capture, resize, and emulator mutation are serialized by a session actor.

## Consequences

Reconnection uses a current-state snapshot instead of raw replay and preserves live normal/alternate-buffer behavior. The protocol must carry cell width/style, cursor, dimensions, generation, revision, history, and Unicode-width metadata. The project must prove emulator/browser width agreement and query uniqueness with fixtures. If the pinned emulator cannot meet a required case, the milestone stops for an evidenced adapter patch or owner-approved architecture amendment.

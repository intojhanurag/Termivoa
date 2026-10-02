# ADR 0002: Server-authoritative terminal replication

- Status: Accepted; implemented in v0 (2026-09-27); still current (2026-10-02)
- Date: 2026-09-20

## Context

A newly attached browser cannot safely reconstruct hidden parser, mode, cursor, and alternate-buffer state from a tail of raw terminal bytes. Multiple clients must also avoid producing duplicate device-query replies or competing input encodings.

## Decision

The broker maintains one authoritative Go virtual terminal per live session. PTY output is interpreted there even with no viewers. Clients receive normalized full-screen snapshots and changed-row patches; they never receive the child's original ANSI stream. Structured input is encoded by the same emulator, and the broker is the only application-level terminal-query responder.

On 2026-09-20, the owner approved two demonstrated platform boundaries. Broker-owned Windows ConPTY may consume and answer DSR/CPR queries before Go vt sees them; tests must still prove exactly one response reaches the child and no viewer may respond. On macOS, the managed termination boundary is the isolated PTY process group. A descendant that deliberately detaches with `setsid` is outside that boundary and must not be described as cleaned up.

Browser xterm is a display surface with stdin and independent scrollback disabled. The desktop client consumes the same frame and input protocols. Snapshot revision, frame capture, resize, and emulator mutation are serialized by a session actor.

## Consequences

Reconnection uses a current-state snapshot instead of raw replay and preserves live normal/alternate-buffer behavior. The protocol must carry cell width/style, cursor, dimensions, generation, revision, history, and Unicode-width metadata. The project must prove emulator/browser width agreement and query uniqueness with fixtures. ConPTY remains inside the broker-owned Windows boundary, while browser and desktop clients remain display/input clients only. If the pinned emulator cannot meet a required case, the milestone stops for an evidenced adapter patch or owner-approved architecture amendment.

Open decision (2026-09-27): on Unix, terminate and shutdown now signal every process in the managed PTY session, not only the shell's process group. This change waits for owner approval; see [owner-verification.md](../owner-verification.md) section 6 and [cli-lifecycle evidence](../evidence/p6/cli-lifecycle.md).

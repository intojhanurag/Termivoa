# ADR 0014: Chat view, read from the agent's own transcript

- Status: **accepted** (2026-10-10). Critical under AGENTS.md rule 24. The owner approved the design and the recommended answers to all questions in section 13 of the design in writing ("Go shipppp").
- Date: 2026-10-10
- Design: [`docs/chat-view.md`](../chat-view.md)
- Issue: [#285](https://github.com/intojhanurag/Termivoa/issues/285)

## Context

The phone shows a coding agent only as a terminal, plus answer cards and the away recap. A long agent run is hard to read on a small screen. Other herdr phone clients (for example herdr-web-ui) read the agent's own session files and show them as a chat; this is the main feature where they are ahead.

Claude Code and Codex write an append-only JSONL transcript of every message, tool call and tool result on the computer (`~/.claude/projects/…`, `~/.codex/sessions/…`). The terminal shows only part of that, and its bounded history drops older output.

## Decision

1. **The broker reads the agent's transcript, read-only, only while a phone has Chat open.** A polling tail (no new dependency) parses new lines into a small normalized block model in a new package `internal/transcript`. With no subscriber, no transcript is opened.
2. **Binding never guesses** (no "probable" state in v1; after `/clear` the link is `none(cleared)` until an opt-in hook, M1.5, owner decision). A session is linked to a file by the process tree, the agent's own pid/session record, its open file, or herdr's `agent_session`, then verified against the file's header. Every link has a state (`linked`, `none{reason}`, `unsupported`); the terminal is always the fallback. Newest-file and folder-only matching are rejected.
3. **Only bounded previews leave the computer.** Text ≤16 KiB per block, tool results ≤2 KiB / 40 lines, edits as counts plus one ≤40-line hunk, paths relative to the session folder. Never: thinking, images, attachments, sub-agent transcripts, system prompts, environment, token usage. A small fixed-pattern secret mask runs as defense in depth, documented as not a guarantee.
4. **Nothing is stored.** Parser state lives in broker memory and is freed 10 s after the last subscriber; the phone keeps chat in memory only (rule 19). Pushes never carry chat text.
5. **Off by default, turned on only at the computer.** `pterm chat on` (with confirmation) turns it on; any device can turn it off in **Settings → (computer) → Chat view**. With it off, the host never opens a transcript.
6. **Transport is the existing session stream, pull model.** The host sends only a tiny `chat.changed{historyId}`; the phone pulls `chat.page` (one request in flight, ≤256 KiB), so chat never delays terminal frames. Protocol revision 8, feature `chat`. Chat is read-only: answering stays on answer cards and `input.answer` under the control lease.
7. **v1 agents:** Claude Code on macOS and Linux (M1a host, M1b phone), Codex (M2), herdr panes (M3). Others show `unsupported` and the terminal.

## Consequences

- New local reads of files outside project roots (`~/.claude`, `~/.codex`), opened safely (fixed roots, `os.Root`, regular files only, owner check, exact filename patterns, size caps).
- Transcript content can contain secrets and attacker-influenced text (from repositories, web pages, tool output). It reaches paired phones when the owner turns Chat on; it is rendered as text or sanitized markdown only.
- Agent formats change between versions: parsers fail closed to the terminal (`none(format_unknown)`).

## Alternatives rejected

| Alternative | Why not |
| --- | --- |
| Parse the terminal screen into chat | Loses folded and scrolled-off content; fragile across agent UIs |
| Inject a `SessionStart` hook or `--session-id` into the user's agent | Changes the user's agent and config |
| Read Codex's SQLite state database | Another app's live WAL database |
| fsnotify | New dependency; 250 ms polling meets the latency budget |
| Store transcripts on the broker or phone | Rule 19; widens exposure |
| Send full tool output and images | Size and privacy; Files and the terminal remain for the rest |

# Chat view: design

- Status: **accepted** (2026-10-10); reviewed (section 14). The owner approved the design and answered **yes** to all questions in section 13. (critical under AGENTS.md rule 24). ADR [0014](decisions/0014-chat-view.md). Issue [#285](https://github.com/intojhanurag/Termivoa/issues/285).
- Pictures (Paper, demo data): [`evidence/chat-view/`](evidence/chat-view/): `design-1-working.png`, `design-2-asks-you.png`, `design-3-long-session.png`, `design-4-not-available.png`.
- Research for this design read only public docs and source; no real transcript and nothing under the real `~/.claude`, `~/.codex` or `~/.config` was opened, and no agent was started.

## 1. Summary

1. The broker reads the agent's own JSONL transcript, read-only, only while some phone has Chat open. It parses new lines into a small normalized block model and sends them over the session's existing live WebSocket.
2. Binding a session to a file never guesses: process tree, the agent's pid/session record, open file descriptors, herdr's `agent_session`, then a header check. Every link has a state; the terminal is always the fallback.
3. Only bounded previews leave the computer; nothing is stored on the broker or the phone. Off by default, one switch per computer.
4. v1: Claude Code and Codex in Termivoa sessions, then herdr panes.
5. The phone shows a virtualised chat with a **Chat | Terminal** switch; answering stays on the existing answer cards.

## 2. What exists to reuse (origin/main)

| Need | Exists | Reuse |
| --- | --- | --- |
| Per-session read request on the live stream | `files.list/read/search` (`internal/broker/session_dispatch.go`), off the actor, one request in flight per viewer (`files.go`) | Same shape for `chat.*` |
| Pushed notices with backpressure | `queueViewerNotice` (`viewer_notices.go`), `MaxViewerQueuedNotices = 64`, `MaxViewerQueueBytes = 2 MiB`, `ErrViewerSlow` (`viewer.go`) | Carries `chat.delta` |
| Envelope limits | outbound 1 MiB, inbound 256 KiB (`protocol/v1/envelope.schema.json`); `EncodeHost` validates (`docs/terminal-delivery.md`) | Same caps and validators |
| Agent state, answer cards | `internal/agentstate`, `broker/agent.go`; answers re-parse the screen (`docs/agent-state.md`) | Chat shows questions read-only and links to the answer card; keys still come from the screen |
| Agent kind | Termivoa sessions: screen heuristics only. herdr targets carry the agent kind; herdr's `agent_session {id, path}` is in the snapshot but dropped by `wireAgent` (`herdr/snapshot.go`) | Keep `agentSession` host-only in M3 |
| Process tree | `liveProcesses()` (`ptyhost/session_darwin.go`, `session_linux.go`), `descendants()`, lsof batching (`ptyhost/listen*.go`) | Add `comm`, start time, and `OpenFiles(pid)` |
| Safe open | `platform.OpenRegular(root *os.Root, …)` | Every transcript open |
| Per-computer settings | `settings` keys such as `provider.herdr.enabled` (`broker/providers.go`) | `chat.enabled` |
| Read-only thumbnails | `ErrViewerReadOnly` | Thumbnails refused for chat |

## 3. Research findings

**Claude Code.** Transcripts in `~/.claude/projects/<project>/<session>.jsonl` ("every message, tool call, and tool result"); superseded copies `<session>.jsonl.superseded-<ts>`; sub-agents in `<session>/subagents/`; spilled outputs and images in `<session>/tool-results/`; `CLAUDE_CONFIG_DIR` moves everything; not encrypted at rest ([claude-directory](https://code.claude.com/docs/en/claude-directory)). Lines carry `type`, `uuid`, `parentUuid`, `timestamp`, `sessionId`, `cwd`, `version`, `isSidechain`; one API turn is written as several `assistant` lines sharing `message.id`; tool results are `user` lines with `tool_result` blocks keyed by `tool_use_id` (community docs; types change across versions). Binding signals: `--session-id`, `--resume`, `--continue`, `--fork-session` ([cli-reference](https://code.claude.com/docs/en/cli-reference)); hooks get `session_id` and `transcript_path` ([hooks](https://code.claude.com/docs/en/hooks)); an undocumented `~/.claude/sessions/<pid>.json` `{pid, sessionId, cwd, startedAt}` that goes stale after `/clear`; no `CLAUDE_SESSION_ID` in the environment.

**Codex** (source `openai/codex`, `codex-rs/rollout`): `rollout-YYYY-MM-DDThh-mm-ss-<threadId>[_<rolloutId>].jsonl`; lines `{type, payload}` with `session_meta`, `response_item`, `event_msg`, `turn_context`, `compacted`, `token_usage_record`…; cold rollouts compressed to `.jsonl.zst`; the writer keeps the file open in append mode and creates it on the first item.

**herdr.** `agent_session` is integration-reported and can be missing or stale ([session-state](https://herdr.dev/docs/session-state/)).

**Others.** pi: versioned JSONL ([format](https://pi.dev/docs/latest/session-format)); Gemini CLI: JSON/JSONL with rewind ops; opencode 2: a database.

**herdr-web-ui** (MIT): binds by herdr `agent_session`, then files held open by the pane's process, then other sources; never "newest file" or folder-only guesses; caches by inode+size+mtime; `history_changed` invalidation. We take ideas only (binding order, no guessing, history id); no code is copied.

## 4. Architecture

### 4.1 Packages

`internal/transcript` is the only package that knows agent file formats (like `internal/herdr` for herdr). Concrete types, no interface; the parser is chosen by `switch kind`.

| File | Holds |
| --- | --- |
| `bind.go` | `Bind(ctx, Hint) Link`, states and reason codes |
| `tail.go` | Polling tail: offset, inode/dev, partial-line carry, reverse scan for backfill |
| `claude.go`, `codex.go` | Line parsers → `[]Op`, pending tool-call map (≤256) |
| `model.go` | Normalized blocks → `protocol.ChatBlock`, caps, masking |
| `guard.go` | In a test binary, any root outside a temp folder panics |

`internal/broker/chat.go`: dispatch, one tail per session shared by subscribers, the setting, at most 8 active tails per computer. `internal/ptyhost`: `comm`, start time, `OpenFiles(pid)` (lsof on macOS, `/proc/<pid>/fd` on Linux, bounded). `cmd/pterm`: production roots from the user's home.

### 4.2 Data flow

```
agent → JSONL ─(fstat every 250 ms, read new bytes)→ tail → parser → []Op
  → broker chat hub (tail, subscribers, historyId) → queueViewerNotice(chat.delta)
  → gateway (opaque relay) → phone (memory only, virtualised list)
```

The tail runs only while a non-thumbnail viewer has sent `chat.open`, and stops 10 s after the last `chat.close` or detach.

### 4.3 Binding

| Step | Termivoa session | herdr pane (M3) |
| --- | --- | --- |
| 1. Agent process | In the session's process tree: `comm` `claude`/`codex` (or `node` running the npm entry). None → `unsupported`; two or more → `none(several_agents)` | herdr's agent kind and `agent_session` |
| 2. Claude Code | `<claude>/sessions/<pid>.json` (≤4 KiB): `pid` matches, `startedAt` within ±5 s of the process start, `sessionId` a UUID; open `projects/*/<sessionId>.jsonl` (exact pattern); header `sessionId` and `cwd` match → `linked` | `agent_session.path` under the root, pattern match, header id match → `linked` |
| 3. Codex | `OpenFiles(pid)` shows exactly one rollout under `<codex>/sessions/`; `session_meta.payload.id` matches the name → `linked`; no open file (app-server mode) → `none(no_open_file)` | Same validation |
| 4. After `/clear` | Linked file stops growing and one new `*.jsonl` with the same `cwd` appears after its last write, with no other live agent in that folder → `probable` ("may not be this conversation"); else `none(ambiguous)` | — |
| 5. Recheck | Every 2 s while subscribed and on rotation (inode change or shrink) → new `historyId`, `reset` | On every snapshot poll |

States: `off`, `unsupported`, `searching`, `linked`, `probable`, `none{reason}`, `ended`.
Rejected: newest file in the folder; folder match alone; visible-text matching; injecting hooks or `--session-id`; reading Codex's SQLite state; reading an agent's environment. `CLAUDE_CONFIG_DIR`/`CODEX_HOME` → `none(custom_root)` in v1.

## 5. Normalized model (protocol/v1, revision 8, feature `chat`)

```json
ChatBlock = {
  "id": "≤64, opaque, derived from file offset and tool id (stable across reconnects)",
  "turn": "≤64, id of the user_text that opened the turn",
  "kind": "user_text|assistant_text|tool_call|edit|question|status|compaction|omitted",
  "at": "RFC 3339?",
  "text": "≤16 KiB (markdown for assistant_text)",
  "truncatedBytes": "int?",
  "tool": {"name": "≤64", "summary": "≤240", "status": "running|ok|error|unknown",
           "result": {"preview": "≤2 KiB, ≤40 lines", "bytes": "int", "truncated": "bool"}},
  "edit": {"path": "≤512, relative to the session folder or basename", "added": "int", "removed": "int", "hunk": "≤40 lines"},
  "question": {"text": "≤480"}
}
Op = {"op":"append","block"} | {"op":"update","block"} | {"op":"reset"}
```

Thinking text never goes on the wire; only a duration may (screen 3, "Thought for 14s", from timestamps). Images → `omitted`. Sub-agent files are not followed: the `Task` call shows its own summary and status only (screen 3 shows the summary line, not the sub-agent's steps). A tool result is an `update` of its call block. Cursor = opaque `{historyId, offset}`.

## 6. Transport

| Phone → computer | Computer → phone |
| --- | --- |
| `chat.open {limit ≤ 60 turns}` | `chat.state {link, reason?, agent?, historyId?, stale?}` |
| `chat.page {before, limit}` | `chat.page {historyId, blocks ≤200, before?, complete}` ≤ 512 KiB |
| `chat.close {}` | `chat.delta {historyId, ops ≤128}` ≤ 256 KiB |

- **Backfill:** read backward from the end in 64 KiB chunks; stop at `limit` turns, 8 MiB read or 512 KiB payload; pages break at turn starts. Never reads the whole file.
- **Live:** `fstat` every 250 ms; partial line carried up to 1 MiB; longer lines skipped as `omitted`; inode change or shrink → re-bind and `reset`; `.zst` → `none(compressed)`. Coalesced per tick: at most 4 deltas a second per session.
- **Backpressure:** the viewer byte budget; past 32 queued notices deltas are dropped, one `chat.state {stale:true}` is sent and the phone re-pages; then the existing slow-viewer drop. The agent is never slowed.
- **Reconnect:** `chat.open` again, fresh latest page, no replay. Chat carries no input.

## 7. Performance budgets

| Metric | Budget | Measured by |
| --- | --- | --- |
| File write → phone | p50 ≤ 400 ms, p95 ≤ 750 ms | e2e with a fake agent stamping lines |
| Idle | 0 work with no subscriber; 4 `fstat`/s per subscribed session | Test (no tail goroutine), soak CPU sample |
| Parse | ≥ 50 MB/s per core | `BenchmarkClaudeParse`, `BenchmarkCodexParse` |
| First page | ≤ 300 ms on a 200 MB synthetic file, ≤ 8 MiB read | `BenchmarkBackfill` + bytes-read counter |
| Broker memory per tail | ≤ 2 MiB | allocation tests, soak heap |
| Tails per computer | ≤ 8 | Unit test |
| Phone | ≤ 2,000 blocks in memory, virtualised by `id` | Web test |

## 8. Security and privacy

- **New exposure:** transcript content the terminal had folded or dropped (full command output, files the agent read, pasted secrets, older turns), and local reads outside project roots.
- **Defaults:** `chat.enabled` off per computer; **Settings → (computer) → Chat view**. Off → no transcript is opened.
- **Leaves the computer:** text ≤16 KiB/block with control characters removed; tool previews ≤2 KiB/40 lines; edits as counts + one hunk; relative paths. **Never:** thinking, images, attachments, sub-agent transcripts, raw tool structures, environment, system prompts, token usage, transcript paths, pids, session ids. Fixed-pattern masking (`[secret]` for PEM blocks, `ghp_`/`gho_`/`github_pat_`, `sk-…`, `AKIA…`, `xox[bp]-`, `Authorization: Bearer`) is defense in depth, **not a guarantee**.
- **Who reads:** any non-thumbnail viewer of the session (same rule as Files); re-authorized on stream open; revocation closes the stream; one audit log line per `chat.open` without content.
- **Storage (rule 19):** memory only on the broker and the phone; never `localStorage`, the service-worker cache, SQLite or logs.
- **Opening files:** fixed roots from `cmd/pterm`, never phone-supplied; `os.OpenRoot` + `platform.OpenRegular`; symlinks refused; owner uid must match, no group/other write; exact filename patterns; pid files ≤4 KiB; lines ≤1 MiB; reopen on inode change.
- **Rendering:** transcript text is attacker-influenced; text or sanitized markdown only (no raw HTML; http/https links opened by the user).
- **Threat model:** a new section "Chat view" (the above), added in the same PR as M1.

## 9. Phone design (Paper, `evidence/chat-view/`)

| Screen | Shows |
| --- | --- |
| 1. Working | **Chat \| Terminal** switch under the status line. Your prompt (right), the agent's text (left), tool rows in one quiet block (Read, Edit +18 −7, Ran · 14 passed), a cyan working line with **esc stop**, and the message box (compose path: paste + Enter under control) |
| 2. Asks you | An edit opened to a ≤40-line hunk with **Open in Changes ›**; the question card inline, with the answer card's options. While a question waits, the card replaces the message box |
| 3. Long session | **↑ N earlier messages** (paging), **Conversation compacted** marker, **Thought for 14s** (duration only), a folded **Task** row (summary and status), **N new ↓** jump |
| 4. Not available | When linking fails: "Chat isn't available for this session", the reason, **Show terminal**, **Why?** |

Frontend notes: blocks are keyed by `id` and rendered in a virtualised list; chat text never enters terminal state; markdown is sanitized; the switch is hidden for sessions with no agent; reduced motion respected; every row is a button with a label.

## 10. Agent support (v1)

| Agent | Termivoa session | herdr pane |
| --- | --- | --- |
| Claude Code | M1 (`linked` via pid file + header; `probable` after `/clear`) | M3 via `agent_session` |
| Codex | M2 (`linked` via open file) | M3 when `agent_session` is present |
| pi, Gemini, opencode | `unsupported` | `unsupported` |

Format drift fails closed: unknown record types are skipped and counted; over 20% unparsable in the last 200 lines, or a user/assistant record without required fields → `none(format_unknown)`. Parsers never panic (fuzzed).

## 11. Failure modes

| Case | Behavior |
| --- | --- |
| No agent, several agents, custom root | `unsupported`, `none(several_agents)`, `none(custom_root)` |
| File not created yet | `searching` 10 s, then `none(no_file)`, recheck every 2 s |
| Rotation, superseded copy, truncation | Re-bind, `reset`, new `historyId` |
| Agent exits | `ended`; last page stays while the session lives |
| Corrupt or oversized line | `omitted` |
| Slow phone | Deltas dropped, `stale`, re-page; then slow-viewer drop |
| Broker restart | Nothing to recover |

## 12. Tests and milestones

- Synthetic fixtures only (from public docs and source types); fuzz `FuzzClaudeLine`, `FuzzCodexLine`, `FuzzTail`; binding tests (fake/stale pid file, two agents, symlink, wrong owner, FIFO); `guard.go`; e2e with a fake agent binary installed as `claude`/`codex` in the harness sandbox; latency and bench evidence under `evidence/chat-view/`. One scripted capture with real agents in a throwaway `HOME` needs the owner (credits, rule 12).

| Step | Scope |
| --- | --- |
| D0 | This design, ADR 0014, reviews. Draft, owner review |
| M1 | Claude Code in Termivoa sessions: `internal/transcript`, broker hub, setting, protocol revision 8, phone Chat (read-only) + switch |
| M2 | Codex |
| M3 | herdr panes |
| M4 | pi and others; full output on demand only if the owner asks |

Each implementation PR updates `docs/architecture.md`, `docs/agent-state.md`, `docs/threat-model.md`, `docs/pending-work.md`, CHANGELOG, README and the docs site (a Chat view guide, `reference/security.md`, troubleshooting for each `none` reason, release notes).

## 13. Questions for the owner (recommendation first)

See also section 14: the reviews changed the transport, binding and the on switch.


1. Chat off on every computer until turned on in Settings? **Yes.**
2. Never send thinking text in v1 (duration only)? **Yes.**
3. Tool output as previews only in v1 (Files or terminal for the rest)? **Yes.**
4. Add the fixed-pattern secret masking, documented as not a guarantee? **Yes.**
5. Chat turned on only from the computer (`pterm chat on`), off from any device? **Yes.**
6. Custom agent folders (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`) unsupported in v1, with a clear reason? **Yes.**
7. One scripted capture with real Claude Code and Codex in a throwaway folder before M1 merges (costs credits)? **Yes.**

## 14. Reviews and resolutions (2026-10-10)

Two independent reviews ran on this design: **security** (no blockers; 3 major, 7 minor) and **feasibility with alternatives** (no blockers; 6 major, 5 minor). Where this table and the sections above differ, **this table wins**; the sections are updated in M1.

| # | Finding | Resolution |
| --- | --- | --- |
| S1 | Any paired device can turn Chat on (a compromised home could enable it on every peer and pull transcripts silently) | **Turning Chat on happens only on the computer itself** (`pterm chat on`, with a `y/N` confirmation). Any device can turn it off (**Settings → (computer) → Chat view**); off closes every open chat stream at once |
| S2 / F2 | `probable` after `/clear` can show another conversation (for example a `claude` started in an editor in the same folder) | **`probable` is removed from v1**: `/clear` gives `none(cleared)` and the terminal. **M1.5 (owner decision, rule 12):** an opt-in Claude Code `SessionStart` hook that writes a breadcrumb file owned by Termivoa with `session_id`, `transcript_path` and `source=clear`, giving an exact link again |
| S3 | Markdown and links are an injection path (fake re-pair links, faked file names) | No images, no raw HTML. Links show their full address and open only after a confirm step, with `noopener noreferrer`. Add-computer link detection never runs on chat text. Bidi and zero-width characters (U+202A–202E, U+2066–2069, U+200B–200F) are removed or shown in paths, summaries and question text. The CSP (`img-src 'self' data:`) stays as a second wall |
| S4 | Binding checks too weak | Compare the header `cwd` with the OS-reported cwd of the agent pid; require exactly one `projects/*/<sessionId>.jsonl` whose folder is Claude's encoding of that cwd; herdr paths: absolute, `filepath.Rel` without `..`, opened through `os.Root` |
| S5 | `os.Root` follows in-root symlinks; hardlinks | `Lstat` the final name; refuse `nlink > 1`; send nothing before the header check passes; read only through the held handle; compare path and handle inode on each recheck |
| S6 / F4 | Phone requests can force large reads; big pages can close the connection | One backfill in flight per viewer and per session (like `filesBusy`); rate-limit open/close; validate `before`/`after` cursors against `historyId` and issued offsets; pages ≤ 256 KiB and wait for the writer budget; one real per-tail memory cap |
| S7 | Masking details | Mask the full line before cutting; mask every text field; add `sk-ant-`, JWT-shaped strings and `AIza`; keep "not a guarantee"; state plainly that the new data is mainly what the agent read outside the project |
| S8 | Revoked device mid-stream | Remove the subscriber in step with revocation; check revocation before each send; the 10 s linger never sends to a removed device; test |
| S9 | Process inspection privacy | lsof only on the agent pid, filtered to the agent's sessions folder, bounded; argv and open-file lists in memory only, never logged |
| S10 | No audit log exists | The `chat.open` line goes to the broker log: device ID, session ID (Termivoa's), time; never transcript path or agent session id |
| F1 | Matching on the process name misses the native Claude install (version-named binary) | No `comm` matching: for each pid in the session tree, look for `<claude>/sessions/<pid>.json`; for Codex, look for rollout files in the existing lsof batch |
| F3 | `chat.delta` as a viewer notice would delay terminal frames (notices go first) and a 65th notice drops the whole viewer | **Pull model:** the host sends a tiny coalesced `chat.changed{historyId}`; the phone pulls `chat.page{after}` with one request in flight, off the actor (the `files.go` pattern). No chat content in the notice queue |
| F5 | Lines over 1 MiB are normal (big file reads, base64 images); skipping them leaves calls `running` | Stream-scan big lines for `tool_use_id` and `is_error` without buffering; status falls back to `unknown` |
| F6 | herdr `agent_session` is probably an id, not a path (fixture is synthetic) | M3 maps the id to a file by exact name pattern under the root |
| F7 | Parser details | Claude: group by `message.id`; drop `isMeta`, command and hook lines from turn starts; hide `isCompactSummary` text; ignore abandoned rewind branches. Codex: text from `event_msg`, tools from `response_item` (`function_call`, `custom_tool_call`, apply_patch); drop `environment_context`. Drift: record versions and the unknown-record share in `pterm doctor` |
| F8 | Screens promised data the transcript lacks | The working line comes from agentstate (the terminal spinner), not the transcript. The question card **is the existing AnswerCard** (permission prompts are not in the JSONL); the `question` block kind is dropped. "14 passed" and "searched 214 files" are not shown unless the tool summary carries them |
| F9 | "No input" vs the message box and **esc stop** on the screens | Chat itself carries no input; the message box is the existing **Composer** (same draft, same input path, needs control) and **esc stop** is the existing agent key |
| F10 | Web fit | Lazy-load Chat (`@tanstack/react-virtual` already used, kept out of the main bundle); terminal gestures off in Chat mode; Windows hosts `unsupported` in v1 |
| F11 | Cost of lsof on macOS | lsof only at bind time and when the file stops growing, not every 2 s |

**v1 scope after review:** Claude Code in Termivoa sessions, macOS and Linux; binding by pid file (no `probable`); pull transport; read-only chat with the existing Composer and AnswerCard; edit counts and one hunk. **Slicing:** M1a host (protocol, `internal/transcript` with fuzz tests, hub, `pterm chat on`), M1b phone (Chat screen, docs). The owner's scripted capture with a real Claude Code (question 6) gates M1a, because format drift has no real-agent CI.

## 15. Build notes (M1a, host side)

M1a (branch `feat/chat-view-host`) builds the host side of section 14. Where it differs from the sections above:

| Topic | As built | Why |
| --- | --- | --- |
| Page request name | The client asks with `chat.read {before \| after, limit}`; the host answers `chat.page` | The web validator's discriminator needs one schema per message type; it mirrors `history.read` → `history.page` |
| Enum words | `ChatState.link`, `.reason` and `ChatTool.status` are schema patterns, with Go constants in `internal/protocol/chat.go` | As enums, quicktype renamed older generated names (`Running`, `Error`, `Off`, …) across the code |
| Reasons | Two more: `file_refused` (the file failed a safety check) and `busy` (8 chats already followed) | Each `none` has an exact reason |
| `chat.changed` path | One sender goroutine per viewer writes it through the stream writer, waiting for the byte budget; never through the actor's notice queue. One waits at most per viewer, spaced 250 ms | Coalescing means nothing is dropped, so `stale` is defined but not sent in M1a |
| Cursors | A random 16-byte token the host maps to a file offset (at most 1024 per history); a new history drops them | Validation against `historyId` and issued offsets is a map lookup |
| Masking window | The text up to the cut plus 16 KiB is masked before it is cut; a PEM block that begins there is masked to the end | Masking a whole multi-MiB tool output for a 2 KiB preview is not needed |
| Lines over 1 MiB | Stream-scanned for `tool_use_id` and `is_error` (status `ok`, `error`, or `unknown` when several results share one `is_error`); any other long line shows `omitted` ("Large message not shown") | F5 |
| `/clear` | Only with evidence tied to our agent (review C-2): the linked file was idle 2 s (checked at most every 10 s), the agent's own pid file was written after it, and a newer session file in the same folder is named by no pid file → `none(cleared)`; the hub then remembers that conversation so the stale pid file does not link it again. Another `claude` or `claude -p` in the same folder alone never ends a link. A pid file that names a new session links it with a new `historyId` | S2/F2 without guessing |
| Rewind | A new prompt whose parent is not the line Poll read just before it starts a new `historyId` (review C-3; no decision right after a line over 1 MiB, whose uuid is not read). At bind the last line (within 1 MiB) gives that uuid. Pages drop lines that are not on the parent chain (walk from the newest message line, or from the older page's anchor; `logicalParentUuid` crosses a compaction) | F7 |
| Very long lines | Poll keeps the scan state of an unfinished line over 1 MiB, so its bytes are read once across ticks (review C-1). An after page reads to `live` (always a line end) and stops after 8 MiB at a line end; a before page that would fall inside one line starts at that line. A long prompt line still opens a turn ("Message too large to show") and counts in the turn search | Paging always moves |
| Drift window | Fed only by lines read live (Poll) and, at bind, the last 1 MiB; old lines read for a page never count (review S-8) | A phone cannot change the drift check by paging |
| Folder limit | At most 10,000 folders in `projects/` are read; with more, the bind fails closed as `none(no_file)` | S-7 |
| Thinking | Only "Thought for Ns" (a `status` block), from the timestamp of the line before the thinking line, 1 s to 1 h | Design section 5 |
| Custom folder | `CLAUDE_CONFIG_DIR` is read from the environment of the `pterm __broker` process (`cmd/pterm/chat.go`); the agent's environment is never read. Under launchd or systemd a shell export does not reach the broker, so such an agent shows `unsupported`, not `custom_root` | Design rule: never read an agent's environment |
| Process facts | `ptyhost.TreeProcesses` adds start times (macOS `kinfo_proc`, Linux `/proc/<pid>/stat` with USER_HZ 100, no overflow); a recheck allows 2 s between start times (Linux `btime` moves); the OS working folder comes from lsof (macOS) or `/proc` (Linux) at bind time only. After Chat is off no bind runs (the tail's context is cancelled and checked) | S4, F11, C-4 |
| Masking order | Text is cleaned first (escape sequences, control, bidi and zero-width characters), then masked; a token prefix matches at the start or after any character that is not a letter or digit. Tool results with an invalid `tool_use_id` are dropped | Review S-4, S-5 |
| Folder name | Every character other than an ASCII letter or digit becomes `-`; a long path that Claude Code shortens does not match and fails closed (`none(no_file)`) | No guessing |
| Drift | `format_unknown` when over 20% of the last 200 lines are unknown, after at least 20 lines; `pterm doctor` and `pterm chat status` show the versions seen and the unknown share | Section 10 |
| Commands | `pterm chat status` next to `on` and `off` | The doctor line and a quick check |
| Close | `chat.close` has no reply; opens are limited to 10 a minute per device | Close only frees |

Measured (synthetic, Apple M5, `go test -bench`, warm file cache): parse about 280 MB/s; first page of a 200 MB transcript about 7 ms with 1.7 MiB read. See [evidence/chat-view/m1a-bench.md](evidence/chat-view/m1a-bench.md).

Still open before M1a merges: the owner's scripted capture with a real Claude Code (`scripts/chat-capture.sh`, question 7) to check the parser against a real transcript.

# P7 coding agents in a managed session

Date: 2026-09-29. Branch `test/hu-agents`, base `12b8dc7`. Product code was
not changed.

## Environment

| Item | Value |
| --- | --- |
| OS | macOS 26.5.2 (25F84), Apple Silicon |
| Go / Node | go1.27.1 darwin/arm64, Node v24.11.0 |
| Build | `pnpm build:web && ./scripts/embed-web.sh && go build -tags webdist -o bin/pterm ./cmd/pterm` |
| Profile | `pterm up --dev --data-dir /tmp/pt-agents/data --runtime-dir /tmp/pt-agents/run --listen 127.0.0.1:8797` |
| Session | Saved project `/tmp/pt-agents/project` (empty), profile `zsh (login)` (owner's zsh config) |
| Claude Code | 2.1.283, started as `command claude` |
| Codex CLI | codex-cli 0.157.0, started as `command codex` |

## Harness

- `tests/agents/driver.mjs` plays the browser over the real gateway:
  `pterm pair` grant, `POST /api/v1/pair/redeem`, `pterm pair approve`,
  `POST /api/v1/pair/poll` (cookie), `POST /api/v1/sessions`, then one
  WebSocket: `hello`, `session.attach`, snapshot assembly, `snapshot.applied`,
  `control.acquire`, 5 s heartbeat with lease, `input.text`, `input.key`,
  `input.paste`, `input.mouse`, `terminal.resize`, `history.read`,
  reconnect. It validates every host message against
  `protocol/v1/envelope.schema.json` (Ajv 2020) and serves the reconstructed
  screen, styles, frame statistics and a log on `127.0.0.1:8798`.
- `tests/agents/replay` (build tag `agents`) replays the `script` typescript of
  `pterm attach` through the project's vt emulator to get the desktop screen.
- The desktop observer ran as
  `script -q -t 0 attach.typescript sh -c 'stty rows 40 cols 120; exec pterm attach --dev --runtime-dir /tmp/pt-agents/run <id>'`.
  After every step the browser screen and the desktop screen were compared
  row by row.

Neither file runs in `go test ./...` or CI.

## Credits used

4 prompts in total, 2 per agent:

1. `Reply with only the word OK.` (Claude, Codex)
2. `Print the numbers 1 to 60, one per line, with no tools.` (Claude, Codex)

No tool calls ran. Claude was switched to manual mode with Shift+Tab before the
first prompt. Codex started in "YOLO mode" from the owner's
`~/.codex/config.toml`; it was switched to "Ask for approval" with
`/permissions` before the first prompt. Both folder-trust screens were accepted
for the empty `/tmp/pt-agents/project` only (Codex saved a `trusted` entry for
that path in `~/.codex/config.toml`).

## What passed

| Check | Claude Code | Codex |
| --- | --- | --- |
| TUI renders, no garbage in the agent screen | Pass | Pass |
| Box drawing, block logo (`▐▛███▛█`), dingbats | Pass | Pass (`╭─╮`) |
| Wide cells: emoji and CJK in pasted text (`😀`, `日本語`) at the correct columns, continuation cells after them | Pass | Pass |
| Truecolor in cell styles (for example `255,193,7`, `177,185,249`; Codex `bg=30,30,30`) | Pass | Pass |
| Cursor inside the prompt box, visible/hidden follows the app | Pass | Pass |
| Alternate screen: both agents run in the alternate buffer; normal buffer and zsh prompt come back on exit | Pass | Pass |
| Typing with `input.text`, then `enter` | Pass | Pass |
| Arrow keys in trust/permission pickers, Shift+Tab | Pass | Pass |
| Esc (Claude: second Esc clears the draft; Codex: closes the `/` popup) | Pass | Pass |
| Ctrl+C (Claude: "Press Ctrl-C again to exit", then exit; Codex: while the answer ran it opened "Task is still running · 1. Cancel task", Enter cancelled; at an empty draft it quit) | Pass | Pass |
| `input.paste` of 4 lines is one bracketed paste (Claude: `[Pasted text #1 +3 lines]`; Codex: 4 lines in the composer, not submitted) | Pass | Pass |
| `input.mouse` wheel-up scrolls the agent's own transcript | Pass ("Jump to bottom") | Pass ("Back to bottom") |
| `terminal.resize` to 46x30 while idle and while streaming: full snapshot, the app reflows, nothing is left over. Back to 80x24 is also clean | Pass | Pass |
| Reconnect mid-stream (close socket, 300 ms, new socket): new snapshot equals the old screen except the live spinner; `control.acquire` gives a new lease | Pass | Pass |
| `history.read` during and after streaming: consistent `history.page`, `complete: true` | Pass | Pass |
| Desktop observer equals the browser screen at every comparison (80x24, 46x30, idle, after streaming) | Pass | Pass |
| All host messages valid against the envelope schema | 0 invalid | 0 invalid |
| Shell `exit`: `session.state` `exiting` then `exited` with the code; desktop attach ends | Pass | Pass |

History note: both agents draw in the alternate buffer, so their answers never
go into pterm history. `history.read` returns only the normal-buffer shell
lines. On a phone, scroll back with the mouse wheel (it works, see above), not
with pterm history.

## Bandwidth and frame rate

Uncompressed WebSocket text bytes, measured in the driver.

| Run | Time | screen.patch | Bytes | Max patches/s | Average |
| --- | --- | --- | --- | --- | --- |
| Claude, "OK" (80x24) | 5.5 s | 36 | 279 KB | 8 | 51 KB/s |
| Claude, 1..60 (resize and reconnect inside) | 11.1 s | 73 | 1.25 MB | 9 | 112 KB/s |
| Codex, "OK" (80x24) | 6.7 s | 182 | 1.83 MB | 30 | 274 KB/s |
| Codex, 1..60 (resize and reconnect inside) | 13.1 s | 184 | 5.07 MB | 39 | 386 KB/s |

One 80-column Codex status row ("• Working (1s • esc to interrupt)", 10 style
runs) is 8.9 KB of JSON. One full 80x24 snapshot is about 125 KB. The same rows
compress about 60 times with gzip (166 KB to 2 KB). See defect 1.

## Defects (ranked by user impact)

### 1. Agent output costs 100 to 400 KB/s on the phone link (Medium-High)

Repro: start `command codex` in a session, attach with the browser, send one
short prompt. Count `screen.patch` bytes.

Expected: a spinner and a 60-line answer cost tens of KB/s at most, because the
phone is on Tailscale or a mobile network.

Actual: 274 KB/s for "OK" and 386 KB/s for 60 lines (table above). Codex
animates its "Working" shimmer at up to 39 patches/s, and every patch resends
full rows. Every cell is a JSON object
(`{"text":"x","width":1,"continuation":false,"contentClipped":false}`) and
every run repeats the full `CellStyle`. The socket has no compression.

Suspected: `internal/protocol/frame.go` `terminalRow`/`cellStyle` (no compact
encoding, no style table), `internal/gateway/live.go:68`
`websocket.Accept(... &websocket.AcceptOptions{OriginPatterns: ...})` (no
`CompressionMode`), `internal/broker/viewer.go` `framePublishInterval =
time.Second / 60`. Options: permessage-deflate (about 60 times smaller on these
rows), a lower publish rate for phone viewers, or a compact cell encoding.

### 2. OSC title with U+2700 to U+273F or curly quotes leaks as visible text (Medium)

Repro: in a session, run `command claude`, then exit it (Ctrl+C twice). A line
` Claude Code%` stays in the shell screen and in `history.read`. The owner sees
this every time Claude starts. Minimal repro with the replay tool:
`printf 'abc\r\n\033]0;say \342\200\234hi\342\200\235\007def\r\n'` shows
`hi”def` on the screen.

Expected: the whole OSC 0 title `✳ Claude Code` is consumed and nothing is
printed (xterm, iTerm2 and Terminal.app do this).

Actual: Claude Code writes `ESC ] 0 ; ✳ Claude Code BEL` before it enters the
alternate screen. `✳` is `E2 9C B3`. The parser takes the continuation byte
`0x9C` as the C1 String Terminator, ends the OSC, and prints the rest (` Claude Code`) as
text. `“` (`E2 80 9C`) breaks the same way. `é`, `😀` and braille `⠐` are not
affected. The broker model is wrong, so the phone, the desktop attach and
history all show the garbage.

Suspected: `github.com/charmbracelet/x/ansi@v0.11.7/parser/transition_table.go`
line 269 `table.AddOne(0x9C, OscStringState, DispatchAction, GroundState)`
(also lines 167 and 220 for DCS/SOS/PM/APC). It is used by
`third_party/vt/emulator.go` `NewEmulator` (`ansi.NewParser()`). Fix: in
UTF-8 mode, do not take 8-bit C1 bytes as string terminators (or only when they
are not UTF-8 continuation bytes). Add it to the third_party vt patch set with a
regression test.

### 3. Sessions inherit the whole environment of the process that started the broker (Medium-Low, depends on setup)

Repro: start `pterm up --dev ...` (or `pterm launch`) from a terminal that is
itself inside Claude Code. Create a session and run
`env | cut -d= -f1 | grep -i claude`.

Expected: a managed session gets a clean login environment, plus `TERM` and
`COLORTERM`.

Actual: `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_SESSION_ID`,
`CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN` and more are in
every session. Claude Code in the session then reports "Transcript saving is
off — inherited CLAUDE_CODE_CHILD_SESSION marker", so these sessions cannot be
resumed. A messaging token from another tool leaks into a phone-controlled
shell. The same applies to `TMUX`, `VSCODE_*` and similar variables when the
broker starts from those terminals.

Suspected: `internal/ptyhost/process.go:87` `cmd.Env = append(os.Environ(),
spec.Env...)`. Launchd-started brokers are less affected.

### 4. Desktop attach does not report a normal session exit (Low)

Repro: `pterm attach <id>` as observer, then `exit` in the session.

Expected: "Session exited (code 0)" or similar.

Actual: the status line shows `Disconnected | No input sent`, then it prints
"Attachment closed; the managed session was not terminated." The session did
end, so this text is wrong for this case.

Suspected: `internal/cli/desktop.go:153` prints the text unconditionally. It
does not use the `session.state` exited notice.

### 5. The WebSocket closes with 1008 "access ended" after a normal session exit (Low)

Repro: the browser is attached, `exit` in the shell. The driver sees
`session.state exited` and then close code 1008 `access ended`.

Expected: 1000 (normal) when the stream ends because the session ended. Keep
1008 for revocation.

Actual: 1008 in every case. The web client uses `session.state`, so the UI is
correct today, but other clients and logs see a policy violation.

Suspected: `internal/gateway/live.go` writer loop
`finish(websocket.StatusPolicyViolation, "access ended")` when the outbox ends.

### 6. The desktop renderer sends one cursor move per cell (Low)

The attach output positions each cell with CUP (`ESC[1;2Hp ESC[1;3Hr ...`),
also for adjacent cells in the same style. The session made 1.39 MB of
desktop output. This is local only, so it has little effect. Suspected:
`internal/desktop` painter.

## Observations (not pterm defects)

- Codex 0.157 installs and starts a background `codex app-server
  --managed-daemon` (plus `pid-update-loop` and `codex-code-mode-host`). They
  are reparented to launchd and live after the session and the broker. The
  test stopped them by exact PID. `pterm terminate` cannot end them because
  they leave the process tree on purpose.
- The owner's configs start both agents with elevated permissions (Claude
  "auto mode"; Codex "YOLO mode" from `config.toml`), even with `command`.
  On the phone, the user must change the mode in the agent, as done here.

## Cleanup

Agents exited from their own UI. Both shells exited (`exited`, codes 130 and
0). `pterm shutdown --dev --runtime-dir /tmp/pt-agents/run` stopped the broker
and gateway. The driver, `tail`, and the three Codex daemon processes started by
this run were stopped by PID. After the test, `ps` showed no `pterm`,
`codex`, `script` or driver process and ports 8797/8798 were free. The only
`claude` process was one that was already running before the test started.

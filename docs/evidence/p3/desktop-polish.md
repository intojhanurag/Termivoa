# Desktop attach polish: exit message and row-run painting

Date: 2026-09-27. Branch `fix/hu-desktop`. Environment: Apple Silicon,
macOS 26.5.2, Go 1.27.1 darwin/arm64. Source: defects found by the P7 coding
agent test (`test/hu-agents:docs/evidence/p7/coding-agents.md`).

## 1. Exit message shows the real session outcome

Before: when the managed session ended (shell `exit`, or `pterm terminate`),
desktop attach printed "Attachment closed; the managed session was not
terminated." That was wrong.

Now `internal/cli/desktop.go` reads the last `session.state` the host sent:

| Host state at close | Message |
| --- | --- |
| `exited`, exit code N >= 0 | `Session ended (exit code N).` |
| `exited`, no code or -1 (ended by a signal) | `Session ended.` |
| `lost` | `Session was lost; start a new session explicitly.` |
| anything else (Ctrl+] detach, connection loss, `exiting`) | `Attachment closed; the managed session was not terminated.` |

Test: `TestDesktopClosedMessageReportsSessionOutcome`.

Real run (isolated `pterm up --dev` in `/tmp`, bash login profile, 120x40
`script` PTY):

- `pterm new ...`, type `echo hi; exit 7`: `Session ended (exit code 7).`
- `pterm attach <id>`, press Ctrl+]: `Attachment closed; the managed session was not terminated.`
- `pterm new ...`, then `pterm terminate <id>` from another terminal: `Session ended.`

## 2. Renderer positions once per run, not once per cell

Before: `internal/desktop/renderer.go` wrote a cursor position before every
cell and an SGR before every style run.

Now each row is: position at column 1, SGR reset, erase row, then cells.
The cursor is positioned again only when the next cell is not where the outer
cursor already is. Only a printable ASCII cell has a width every terminal
agrees on, so after any other cell (wide, emoji, combining, ambiguous width)
the next cell is positioned explicitly. SGR is written only when the style
changes. Autowrap stays off while painting, cell text is still validated, and
the server cursor is restored at the end. No new escape sequences were added.

Full 120x40 snapshot, measured by the tests below:

| Frame | Before: bytes / CUP / ESC | After: bytes / CUP / ESC |
| --- | --- | --- |
| Styled ASCII (every cell printable, style change every 12 columns) | 55,793 / 4,841 / 5,326 | 17,993 / 41 / 526 |
| Mixed Unicode (CJK, emoji, flag, ZWJ, VS16, combining, box drawing, rest blank) | 44,885 / 4,833 / 4,968 | 7,385 / 77 / 212 |

CUP is `ESC [ row ; col H`. 41 = one per row plus the cursor restore.

Tests in `internal/desktop/renderer_replay_test.go`. Each one writes ANSI into
the project's vt emulator (`internal/terminal`), converts the frame with
`protocol.Snapshot`, renders it, replays the rendered bytes into a fresh
emulator, and compares every cell (text, width, continuation, colors,
attributes) and the cursor:

- `TestRendererPositionsOncePerRowForASCII`: exactly 41 positions, same screen.
- `TestRendererReplaysUnicodeAtExactColumns`: same screen; every non-ASCII cell
  is followed by an explicit position (for example `界` then `ESC[1;8H`).
- `TestRendererPatchReplaysChangedRows`: snapshot then `terminal.Diff` patch
  (wide text replaced by narrow text, a wide pair at the last columns) gives
  the new screen.

The same three replay comparisons also passed against the old renderer before
the change.

## Commands run

- `GOTOOLCHAIN=go1.27.1 go test -race -count=2 ./internal/desktop/ ./internal/cli/ ./internal/liveclient/`: passed.
- `GOTOOLCHAIN=go1.27.1 go test -count=1 -run 'Desktop|Attach' ./tests/integration/`: passed (`TestPtermDesktopAttachNativeOuterTTY`, `TestLiveClientAttachRejectedAtViewerLimit`; `TestDesktopOuterTTYGuardian` skips as before).
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...`: passed.
- `gofmt -l`: clean.
- `./scripts/check`: passed.

# Agent state and safe answers

The broker tells a phone whether the program in a session is working, waiting
for an answer, or idle. When it waits on a recognized question, the
controller can answer with one tap. This note describes the rules. The wire
format is in `protocol/v1` (`AgentStatus`, `input.answer`).

## States

| State | Rule |
| --- | --- |
| `working` | An "esc to interrupt" or "ctrl+c to interrupt" status line is in the last 8 non-blank rows, or child output arrived in the last 3 s and no question has settled. |
| `waiting` | A recognized question has stayed the same for 1 s (`Settle`) and no busy line is visible. |
| `idle` | No output for 3 s (`IdleAfter`), no question, no busy line. For an agent this means "your turn" or "done". |

`exiting`, `exited` and `lost` stay session lifecycle states. `agent` is
present only while the session is `running`.

`stateSince` is the time of the last change of state or question. A new
question while already waiting resets it.

Waiting uses "the same question for 1 s", not "no output for 1 s". Claude
Code blinks a dot about every 0.6 s while its permission dialog is open, so
its output never goes quiet (seen in the real capture).

## Recognized questions

All rules read only the broker's canonical screen. Concealed (SGR 8) cells are
blank. Every returned string has control characters removed, space runs
collapsed, other non-printable characters replaced by U+FFFD, and a length
cap: question 160 characters / 480 bytes, label 80 / 240.

**Numbered menu** (Claude Code permission dialogs, Codex approvals and trust
prompts, and other list prompts):

- Options `1.` to `n.` (or `1)`), 2 ≤ n ≤ 9, in order from the bottom of the
  screen. At most 2 footer lines may follow the last option. Up to 2
  continuation lines may sit between options; blank lines may not.
- Exactly one option has a cursor marker: `❯ › > ▸ ▶ ➤`.
- The question is the nearest line with `?` within 6 rows above option 1, cut
  after its first `?`. A horizontal rule stops the search. Without one, the
  nearest non-blank line within 4 rows is the title.
- If the cursor is visible, it must be between the question and the last
  option. A visible cursor below the menu is a shell prompt after printed
  output, not an open dialog.
- Box borders (`│ ┃ ║`) are ignored, so older boxed Claude Code dialogs match.
- Only the first 6 options are offered.

**y/n prompt**: the visible cursor is on the last non-blank row, and that row
ends with `[y/N]`, `[Y/n]`, `(y/n)` or `(yes/no)` (case-insensitive, optional
`:` or `?`). Options: Yes and No.

## Keys the broker sends

The phone sends only the option index. The broker derives the keys from the
screen at the moment it writes:

1. A trailing shortcut in the label wins: `(y)` sends `y`, `(p)` sends `p`,
   `(esc)` sends Escape. The shortcut is removed from the label.
2. Else, the highlighted option sends Enter.
3. Else, the option sends its digit.
4. y/n prompts send `y` or `n` (or `yes`/`no`) and then Enter.

Real captures showed why: a digit confirms at once in Claude Code, but in the
Codex trust dialog it only moves the highlight. Digit plus Enter would confirm
twice in Claude Code. With these rules the worst case is a second tap: the
first one moves the highlight, the second sends Enter on that same option.

## Answer safety

`input.answer` carries the lease, the next input sequence, `questionId` and
`optionIndex`.

1. The actor authorizes the lease first (same rule as any input). A viewer
   that is not the controller gets `not_controller` or `stale_lease` and learns
   nothing about the screen.
2. The published state must be `waiting` with that `questionId`, and a fresh
   parse of the screen must produce the same id. Otherwise `question_changed`.
   An option index that is not offered is `invalid_request`.
3. The answer then enters the normal input queue with its sequence, so queue
   bounds, duplicate handling and lease checks apply as for all input. A
   repeated sequence is acknowledged without a second write.
4. Just before writing, the actor parses the screen again. If the question
   changed in between, it drops the answer instead of writing keys for a
   different prompt.

Rejections do not consume the sequence. The broker never answers on its own,
and a phone must never send `input.answer` without an explicit user action.

`questionId` is `sha256(generation, text, labels)` cut to 32 hex characters.
It does not include the keys, because they follow the highlight.

## Performance

The actor never scans on each output chunk. Output arms one timer: the screen
is scanned at most every 0.5 s while output continues, then once when a new
question can settle (1 s) and once when the session can go idle (3 s). With no
output and nothing pending, no scan runs. A scan is one screen copy (the same
copy the session-list preview uses) and a pass over its rows.

## Fixtures

`internal/agentstate/testdata` holds screens as text with a
`# cursor=ROW,COL visible=BOOL` header.

- Real captures, 100×30, from an empty temporary directory through the
  broker's own session and emulator: Claude Code 2.1.283 (working, permission
  dialog, idle after approve and after reject, unnumbered trust dialog) and
  Codex CLI 0.157.0 (trust dialog, command approval, background-server menu,
  idle after reject). Temporary paths are replaced by `/tmp/project`.
- Hand-written: a boxed Claude Code dialog with an `(esc)` option, an apt
  `[Y/n]` prompt, an already-answered `(y/n)` prompt, and a numbered list
  printed by a shell (negative case).

## Known limits

- Menus without numbers (Claude Code's folder-trust dialog) are not offered:
  they need arrow keys. The session shows `idle` or `working` instead.
- An identical question asked twice in a row has the same id. An answer meant
  for the first one can answer the second. Both show the same text and options.
- The question text is one line. For Claude Code it is "Do you want to
  proceed?"; the command it asks about is visible on the live screen, not in
  the question.
- A busy line left on screen by a crashed agent keeps the state `working`.
- Any program that shows text matching the rules is detected. The rules are
  conservative; a missed question is preferred to a wrong `waiting`.
- New agent UIs, or changed wording or markers, need new fixtures and rules.

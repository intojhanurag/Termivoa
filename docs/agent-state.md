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

`afterWork: true` is set only on an `idle` that ended a busy period in this
broker's lifetime: the first `working` after the last `idle` started it, and
any `waiting` in between is part of it. It stays until the next state change
(new work clears it). The first `working` of a new session is its startup
output and starts no busy period, so a fresh shell is plain `idle`. The phone
shows **Your turn** for `idle` with `afterWork` and output it has not viewed;
`afterWork` survives a page reload because the broker keeps it.

`workedSeconds` is the total time the session spent `working` in this
broker's lifetime, the current stretch included, rounded down to seconds. It
is computed when a status is sent; a growing total never sends a
`session.state` by itself. A phone compares it with the value it saw last to
say how long the agent worked while it was away.

Push notifications follow the same transitions: `waiting` once per new
question id, and `done` when an idle ends a busy period of at least 20 s. See
`docs/notifications.md`.

Waiting uses "the same question for 1 s", not "no output for 1 s". Claude
Code blinks a dot about every 0.6 s while its permission dialog is open, so
its output never goes quiet (seen in the real capture).

## herdr provider sessions

A herdr terminal that the phone opens (`docs/herdr-provider.md`, section 6) is
a **provider session**: a broker session that runs `herdr agent attach <pane>`
(or `terminal attach`). It follows the same state rules above, with these
differences (`Registry.CreateProvider`, `internal/broker/providers.go`):

| Topic | Provider session |
| --- | --- |
| Storage | An in-memory registry entry only. No `session_metadata` row, no receipt, no final status. Not in `GET /api/v1/sessions`, `pterm list`, the Wall or New session. The entry is dropped when it ends. |
| Termivoa pushes | `Notify` is nil, so the `waiting` and `done` pushes of `docs/notifications.md` are off for it. herdr's own state (a separate poll) pushes instead. |
| Screen parser | Still runs: `agent` status and answer cards work. The attach client draws a full screen, and the parser reads that screen. |
| Input | The single PTY writer doubles every `ctrl+b` byte (`0x02`), for key, text, paste, compose, snippets and answer cards alike. The attach client then passes one literal `ctrl+b` to the agent, and the phone can never send herdr's prefix `ctrl+b q`. |
| Refused | History, tasks, branches and worktree remove (`providerRefused`), dev-server preview (`PreviewTarget`). Rename, terminate and photo upload find no stored session and fail. Changes reads the checkout folder only when it passed the project-path checks. |
| Cap | At most 4 (`MaxProviderSessions`), outside the 8-session limit. |
| End | 10 s after the last viewer left, when the target leaves herdr's snapshot, or when herdr is turned off. |

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

## Detail: what is being approved

`question.detail` shows what the answer approves, so the answer card can show
the command, not only "Do you want to proceed?".

- **Menus**: the dialog lines above option 1, top-down. The search skips the
  blank rows right above option 1, then reads upward and stops at a
  horizontal rule, a box top corner (`╭ ┌ ╔`), two blank rows in a row, or 16
  rows. Blank rows are left out. The question row adds only the text after
  its `?`; a title row without `?` is left out. Option continuation lines
  belong to the labels, not the detail.
  - Claude Code: `Bash command` / tip / `touch x.txt` / `Create empty file x.txt`.
  - Codex approval: `Would you like to run the following command?` /
    `Environment: local` / `$ touch x.txt` (the `Reason: …?` line is the text).
- **y/n prompts**: up to 2 non-blank rows directly above the prompt row
  (apt: the package list line and the disk-space line).
- Each line is cleaned like the question text and cut to 120 characters. At
  most 6 lines and 600 characters in total, joined with `\n`. `detail` is absent
  when there are no such lines.

## Question id

`questionId` is the first 32 hex characters of SHA-256 over length-prefixed
fields: session generation, question text, displayed detail, the **full**
uncapped context lines (so a change below the 6-line display cap, such as
line 8 of a diff, still changes the id), and the option labels. It does not
include the keys, because they follow the highlight. Two Claude Code prompts
with the same "Do you want to proceed?" and the same options but different
commands therefore have different ids, and an answer sent for the first is
rejected with `question_changed` once the second is on screen.

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

If step 4 drops an answer, the phone has already seen `input.accepted`. The
actor then rescans the screen at once and sends a fresh `session.state` even
if the state looks unchanged, so the phone shows the new question (or the new
state) and the user can decide again. An accepted answer is proof of receipt,
not proof that keys were written.

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
- An identical prompt asked twice in a row (same command, text and options)
  has the same id. An answer meant for the first one can answer the second;
  both approve exactly the same thing.
- Context is limited to 16 rows above the options. Two prompts that differ
  only further up share an id.
- A busy line left on screen by a crashed agent keeps the state `working`.
- Any program that shows text matching the rules is detected. The rules are
  conservative; a missed question is preferred to a wrong `waiting`.
- New agent UIs, or changed wording or markers, need new fixtures and rules.

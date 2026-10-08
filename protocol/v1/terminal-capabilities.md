# Terminal capability profile v1

Status: implementation checkpoint; physical-device validation remains pending.

The v1 capability profile is `phone-terminal-v1`. Supported Unix shells receive
`TERM=xterm-256color` and `COLORTERM=truecolor`. The Go terminal remains the
authority for parsing child output, input encoding, canonical cells, and each
cell's display width. Clients receive normalized cells and never receive the
child's original ANSI stream.

## Supported baseline

- ANSI/VT cursor movement, erase, insert/delete, scrolling regions, normal and
  alternate screens, and cursor visibility/style.
- Resolved 24-bit foreground, background, and underline colors, plus bold,
  faint, italic, blink, reverse, conceal, strike, and supported underline
  styles.
- Bracketed paste, application cursor/keypad modes, the versioned key enum, and
  the selected mouse messages after broker authorization.
- `wcwidth` and `unicode-graphemes` server width metadata. A normalized cell's
  explicit width is authoritative for browser painting.
- Safe `http` and `https` link metadata. Opening a link always requires an
  explicit user action and is not part of terminal painting.

Clipboard commands, terminal images, automatic notifications, title changes,
device-control escapes, and client-generated terminal-query replies are not
supported. A continuation cell inherits the leading wide cell's presentation;
its own style fields are not rendered independently.

## Browser width adapter

The pinned `@xterm/addon-unicode-graphemes` 0.4.0 is loaded through the public
xterm addon and Unicode-provider APIs. Its published Unicode trie does not join
several required Extended Pictographic ZWJ fixtures consistently: unmodified
`15-graphemes` reports three columns for `🏳️‍🌈` and `👨‍👩‍👦`, while the Go
emulator reports two.

`ServerUnicodeAddon` retains the pinned provider's behavior, repairs the GB11
Extended Pictographic classification, and applies the width carried by each
validated server cell while that cell is painted. This also represents the
server's one-column `wcwidth` rainbow-flag case without changing the string or
overlaying DOM content. The adapter uses `allowProposedApi: true` because the
pinned xterm Unicode-provider API is experimental.

Automated xterm-buffer fixtures currently cover ASCII, CJK, combining accents,
variation selectors, rainbow flag, woman-technologist, and family ZWJ
sequences at their authoritative widths. These tests establish buffer-column
agreement in the pinned software stack; physical Safari/Chrome font and layout
checks remain required before the P2/P5 device gates can pass.

## Browser drawing allowlist

The display-only adapter may emit only client-generated cursor positioning,
whole-row erase, resolved SGR, cursor visibility/style, autowrap control, and
synchronized-output boundaries. Printable cell text is rejected if it contains
C0, DEL, C1, or escape characters. xterm input is disabled, local scrollback is
zero, and the adapter never installs an xterm data path to the child.

## Live screen row coordinates

`snapshot.rows.startRow` is a zero-based integer from 0 through 199. A chunk
contains 1 through 200 normalized rows, and its exclusive end must not exceed
200. Go validates the declared offset range before comparing the chunk length
with `MaxRows - startRow`; an untrusted offset is never added to a row count
before its upper bound is established. A chunk starting at 199 with one row
is valid; starting at 199 with two rows is not. This enforces the existing
schema without changing protocol fields or revision.

`screen.patch` row indexes are checked directly against validated screen
dimensions. History offsets are bounded to 0 through 4999 before any row
count addition; pages contain at most 200 rows and may not end beyond 5000.
Snapshot assembly additionally checks against the snapshot's own row count.

## Normal history paging

Normal-screen history is oldest-first and is never mixed with alternate-screen
application output. Each screen snapshot advertises a history epoch and
revision. A `history.read` request must present that exact token and may request
at most 200 rows. The broker returns an immutable `history.page` when the token
still matches; otherwise it returns `history.changed` with the current token
and no rows. An explicit normal-history clear advances the epoch and live
patches set `historyReset` whenever the viewer's acknowledged base belongs to
an older epoch.

The terminal actor retains at most 5,000 complete rows and a conservative
32 MiB normalized-payload estimate, evicting oldest rows first. Width changes
recalculate that estimate before another page is exposed. Alternate-screen
state is limited to its live grid and has no hidden emulator scrollback.
History pages may contain fewer than the requested row limit when the encoded
payload must stay inside the 1 MiB outbound-message limit.

When a width shrink leaves a retained width-2 grapheme lead in the new final
column without room for its continuation, normalization emits one `U+FFFD`
cell with width 1 and `contentClipped=true`. It does not serialize an orphaned
wide lead or invent a continuation outside the grid. The emulator's retained
row is not mutated, so expanding the grid can expose the original grapheme
again in the new history revision.

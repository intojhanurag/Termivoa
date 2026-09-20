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

# Native Mac shell, pager, and editor compatibility

Date: 2026-09-21. Source base: `42d622c` plus this test-only checkpoint.

| Component | Actually tested |
| --- | --- |
| Host | Apple Silicon; macOS 26.5.2, build 25F84 |
| Go | 1.27.1, darwin/arm64 |
| Shell | `/bin/zsh` 5.9, arm64-apple-darwin25.0 |
| Pager | `/usr/bin/less` 668, POSIX regular expressions |
| Editor | `/usr/bin/vim` 9.1, patches 1–1752; compiled 2026-04-18 |
| Terminal profile | 80 columns × 24 rows; `xterm-256color`, truecolor, UTF-8 |

## Explicit command and isolation

The installed-application suite is excluded from ordinary generic checks. Run
it explicitly on Mac:

```sh
go test -race -tags=nativecompat ./tests/integration -run '^TestNativeZsh' -count=1 -timeout=60s -v
```

The `darwin && nativecompat` build tag prevents this from being mistaken for
Windows coverage. Missing required executables fail the explicit suite rather
than silently skipping an acceptance case. Version checks have their own
five-second bounds.

Each fixture creates a new broker-owned PTY and authoritative terminal emulator.
The direct launcher is `/usr/bin/env -i`, which execs `/bin/zsh -f -i` with only
the fixed test environment and a disposable project/home/ZDOTDIR. A synthetic
`.zshrc` containing `exit 93` guards against accidentally enabling user startup
files. No owner's dotfiles, history, project, credentials, model account, or
outer terminal are used. No startup or network configuration changes occur.

Every action uses `broker.SubmitInput` with the current generation, controller
lease, and increasing sequence. Text, keys, and paste remain distinct; no raw
terminal-input bridge is used. The broker interprets all child output and
answers terminal queries. A bounded headless observer validates normalized
snapshots/patches with the existing desktop frame adapter, discarding drawing
bytes. Test assertions inspect canonical screen cells and lifecycle state.

## Demonstrated behavior

- Zsh ZLE editing: Left/Backspace replace a character; Ctrl+A/Ctrl+E edit the
  beginning and end; explicit Enter executes the resulting command.
- Ctrl+C abandons an unsubmitted draft without executing it, interrupts an
  actual foreground `/bin/sleep`, and leaves the same zsh PID/generation alive
  and accepting input.
- Multiline bracketed paste remains in ZLE until a separate structured Enter.
  Before Enter, its first command has not created the synthetic proof file;
  afterward, both its file output and second command's canonical row match.
  This proves this zsh configuration, not that all shell pastes are safe.
- CJK, a combining accent, and an emoji ZWJ sequence survive the actual shell
  input/output path as expected canonical cell text.
- Ctrl+D on the empty prompt naturally exits zsh with status zero. This is not
  implemented by calling the destructive session-termination operation.
- Less enters the alternate buffer, accepts PageDown and the final-page key,
  displays the expected synthetic rows, and restores the normal shell screen
  after `q`.
- Vim runs with vimrc/gvimrc/viminfo/plugins/swap disabled, enters its alternate
  buffer, accepts insert text and Escape, saves the exact synthetic file with
  `:wq`, and returns to the normal shell screen.

## Actual checks and boundaries

- Ordinary explicit suite: both tests passed (0.856 s package duration).
- Race-enabled explicit suite: five repeated passes for both tests; after
  tightening version-check bounds and frame-consumer cleanup assertions, the
  five-repeat race command passed again.
- `go vet -tags=nativecompat ./tests/integration`: passed.
- `git diff --check`: passed.
- Pinned Gitleaks 8.30.1 staged scan: passed, no leaks found.

Durations are test runtimes, not performance acceptance measurements. These
fixtures exercise real installed applications behind broker-owned PTYs, but
do not exercise the shipped `pterm attach` command, physical outer-terminal
glyph layout, the owner's login-shell configuration, browser rendering, phone
input, native Windows, coding-agent accounts, installers, or release readiness.
Those gates remain pending. This checkpoint changes tests/evidence only.

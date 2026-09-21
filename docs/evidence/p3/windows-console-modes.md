# Reversible Windows desktop console configuration

Recorded 2026-09-21. Implementation and injected-operation tests were run on
macOS 26.5.2 (25F84), arm64, Go 1.27.1. Native Windows execution is pending.

## Source-level prerequisite

The selected `x/term.MakeRaw` enables Windows VT input and adjusts input modes,
but does not select UTF-8 code pages or enable output VT processing. Our owned
byte I/O adapters use `ReadFile` / `WriteFile`. Microsoft documents that console
input and output have independent code pages and recommends CP_UTF8 for UTF-8
byte-oriented I/O: [console code pages](https://learn.microsoft.com/en-us/windows/console/console-code-pages).
Drawing escapes require the output screen buffer's VT processing mode:
[console VT sequences](https://learn.microsoft.com/en-us/windows/console/console-virtual-terminal-sequences).
Those documented prerequisites were missing from the desktop coordinator. A
successful compile or ASCII pipe test was not evidence of Unicode console support.

## Behavior

`NewOuterTTY(inputFD, outputFD, output)` now owns reversible native display
configuration in addition to raw input. Windows setup captures both original
code pages and output mode before changing any of them, selects UTF-8 for input
and output, and enables processed/VT output while preserving other mode bits.
The source descriptors remain caller-owned. Non-Windows hosts need no extra
native display-mode changes.

Setup errors attempt every necessary rollback. On normal shutdown the client
workers have already stopped, the outer restore escapes are written first,
and original raw/native settings are restored afterward. A native restoration
failure remains visible and retryable; successful restoration steps are not
repeated unnecessarily.

Independent review found a retry edge: a failed restore-sequence write still
restored native modes, so a subsequent retry would emit escapes with VT disabled.
Before retrying the sequence, `PrepareRestore` now temporarily enables output
UTF-8/VT without recapturing or losing the original values. Every attempt still
restores native settings even if output remains unavailable. No pending input
is replayed, and no terminal query response is sent to the managed child.

The captured output-mode bits are always restored after drawing, including when
VT was enabled before entry: the drawing escapes themselves can change the
native autowrap bit. Already-UTF-8 code pages are not rewritten unnecessarily.

## Evidence and limitations

- Injected-operation tests cover exact original values, reverse restoration,
  partial setup failure at every getter/setter, continuation after restoration
  failure, selective retry, already-configured consoles, write ordering, and
  retry after a failed restoration write.
- `go test -race ./internal/desktop -count=1 -timeout=30s` passed on Mac.
- `go vet ./internal/desktop` and Windows cross-vet passed.
- Windows x64 pure-Go test-binary compilation and target vet passed with the
  final native fixture included. These are compile checks, not native execution.
- The prepared native fixture starts a hidden `CREATE_NEW_CONSOLE` child and
  verifies it is the only process attached before changing any console state.
  It checks raw input, UTF-8/VT entry, partial setup failure, failed entry write,
  restore-write retry, failed native restoration retry, borrowed-handle survival,
  and an already-UTF-8/VT console whose original autowrap was disabled. It never
  mutates the parent test runner's console.

Injected operations prove coordinator policy, not Windows kernel behavior,
glyph widths, or native input correctness. The prepared isolated-console test
must run on native Windows, and the required real terminal/phone Unicode matrix
still applies. This checkpoint does not wire public `pterm attach` or claim a
complete desktop user flow.

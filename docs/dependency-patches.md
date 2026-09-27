# Dependency patches

Termivoa keeps narrow source patches for four pinned Go dependencies under
`third_party/`. Their original versions remain in `go.mod`; local `replace`
directives make the reviewed copies authoritative for builds. All four directories
preserve the upstream license and test suite.

## `github.com/charmbracelet/x/vt`

Upstream baseline: `v0.0.0-20260920004010-53e2afe73ae5`.

The pinned `Emulator.Write` flushes a pending grapheme at the end of every input
slice. PTY reads have arbitrary boundaries, so a base character and combining
mark, or parts of a ZWJ emoji, can become different canonical cells depending
only on read chunking. The pinned implementation also always stores
`GraphemeWidth` cell widths while publicly advertising `WcWidth` unless DEC mode
2027 is enabled.

The local patch:

- adds `WriteStream`, which preserves the parser's pending grapheme across
  arbitrary PTY chunks;
- adds an explicit `Flush` publication boundary used immediately before a
  Termivoa snapshot;
- keeps upstream `Write` behavior for compatibility with other callers and the
  upstream test suite;
- calculates stored cell widths with the emulator's advertised width method;
- bounds pending and retained grapheme content per cell;
- adds normalized-width byte-budget enforcement to the existing line-bounded
  normal scrollback, with synchronous mutation/reset notification;
- disables the otherwise hidden alternate-screen scrollback so alternate
  applications retain only their bounded live grid;
- exposes the active cursor state so save/restore and alternate-buffer frames
  cannot drift from callback shadows;
- returns the ANSI ready response `CSI 0 n` to an ANSI operating-status query
  instead of incorrectly adding the DEC-private marker;
- bounds parser sequence data and retained OSC 8 URLs while discarding unused
  hyperlink parameters.

- enables the patched parser's UTF-8 string mode (see `ansi` below), so a
  C1 continuation byte in an OSC, DCS, or APC string is not ST. Its
  `go.mod` has `replace github.com/charmbracelet/x/ansi => ../ansi`, so the
  retained suite runs against the same parser as the root module.

Remove this patch only after an upstream version provides equivalent streaming
and width behavior and all split-byte, combining-mark, variation-selector, ZWJ,
and width-profile fixtures pass unchanged.

## `github.com/charmbracelet/x/ansi`

Upstream baseline: `v0.11.7`, copied from the verified Go module cache with its
original MIT license, `go.mod`, `go.sum`, fixtures, and complete test suite.

The pinned parser takes the 8-bit C1 String Terminator `0x9C` as the end of an
OSC, DCS, SOS, PM, or APC string. In UTF-8 output `0x9C` is also a continuation
byte: `✳` is `E2 9C B3` and `“` is `E2 80 9C`. Claude Code writes the title
`ESC ] 0 ; ✳ Claude Code BEL` at every start, so the OSC ended early and
` Claude Code` printed into the shell screen and history. SOS, PM, and APC
strings also dropped out of the string state on any byte `0x80`-`0xFF`.

The local patch adds opt-in `Parser.SetUTF8Strings`. When enabled, every byte
`0x80`-`0xFF` inside those strings is string data. The string ends only with
`ESC \` (7-bit ST) or, for OSC, BEL; CAN and SUB still cancel it. This is the
behavior of xterm and VTE in UTF-8 mode, which decode UTF-8 before they look
for controls, so a raw `0x9C` byte is never ST. One difference: they accept
the encoded C1 character U+009C (`C2 9C`) as ST, and this patch treats it as
data. Programs use `ESC \` or BEL. Raw C1 bytes in the ground state keep the
upstream behavior.

The default stays off, so upstream `Parser`, `DecodeSequence`, width, and
truncation behavior and the upstream suite are unchanged. `vt.NewEmulator` is
the only caller that enables it. `parser_phone_terminal_test.go` and the vt and
`internal/terminal` regression tests cover it.

Remove this patch only after an upstream parser stops treating C1 bytes inside
UTF-8 control strings as controls and those tests pass unchanged.

## `github.com/charmbracelet/x/xpty`

Upstream baseline: `v0.1.4`.

On Windows, xpty's ConPTY start path receives a process handle from `Spawn`,
then opens another handle with `os.FindProcess` and previously discarded the
original without closing it. The local patch closes that original handle after
the replacement succeeds and fails safely if closing it fails.

Termivoa separately starts the ConPTY process suspended, assigns it to a
kill-on-close Job Object, and then resumes its initial thread. That process
policy belongs to `internal/ptyhost`, not this dependency patch.

Remove the xpty patch only after the pinned upstream start path demonstrably
closes the original process handle and the native Windows lifecycle suite passes.

## `github.com/charmbracelet/ultraviolet`

Upstream baseline: `v0.0.0-20260303162955-0b88c25f3fff`, retained from the
verified Go module cache with its original MIT license and complete test suite.

The upstream reader accumulates bracketed paste in `eventScanner.paste` before
emitting a paste event. It also retains undecoded input until a sequence is
complete or its escape timeout expires. Neither accumulation has a byte limit;
validating a completed protocol message is therefore too late to bound memory.

The local patch adds opt-in `MaxPasteBytes` and `MaxPendingBytes` limits and
stable `ErrPasteLimit` / `ErrPendingInputLimit` errors. It checks sizes before
appending content, caps paste backing allocations, and permanently rejects
further use of a decoder that exceeded a limit. An oversized paste produces no
partial paste or subsequent key events. Zero limits retain upstream behavior,
including a deterministic fixture demonstrating the original unbounded state.

Bounded mode requires `ContextReader.ReadContext`, provided by the new
`NewContextTerminalReader` constructor. Its contract requires cancellation to
finish the actual read; wrapping a blocking reader in an unjoined goroutine is
insufficient. The reader cancels and joins its pump on a limit failure, stops
blocked event delivery on cancellation, and preserves independent read errors
that race shutdown. The byte pump also preserves data returned together with
EOF, following the Go reader contract.

In bounded mode, only bare or singly wrapped EOF/cancel-reader sentinels count
as clean termination. Joined source failures remain observable even when EOF,
data delivery cancellation, or final buffered-event cancellation occurs at the
same time. Zero-limit upstream error normalization remains unchanged.

`internal/desktop.NewBoundedInput` configures both bounds to 64 KiB, using the
shared protocol paste limit. The adapter does not own or close the source. The
eventual command owns native input and terminal modes; it must join decoding
before closing input or restoring the TTY. Native Windows console encoding and
console cancellation require separate platform evidence.

Remove this patch only when an upstream version provides equivalent early
limits, error semantics, and joined cancellation, with these fixtures passing.

## Validation

Run all retained upstream suites as well as the repository adapter suites:

```sh
(cd third_party/ansi && go test ./...)
(cd third_party/vt && go test ./...)
(cd third_party/xpty && go test ./...)
go test -race github.com/charmbracelet/ultraviolet/...
go test -race ./internal/terminal ./internal/ptyhost
go test -race ./internal/desktop
```

Native Windows execution remains a separate required gate; cross-compilation is
only a compile check.

The Ultraviolet suite runs through the root module's exact dependency graph,
which includes the plan's pinned ANSI version with the opt-in patch above
(off for Ultraviolet, so its behavior is upstream). Its retained upstream `go.mod`
and `go.sum` are unchanged provenance records.

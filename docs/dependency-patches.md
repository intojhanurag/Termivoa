# Dependency patches

Phone Terminal keeps narrow source patches for three pinned Go dependencies under
`third_party/`. Their original versions remain in `go.mod`; local `replace`
directives make the reviewed copies authoritative for builds. All three directories
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
  Phone Terminal snapshot;
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

Remove this patch only after an upstream version provides equivalent streaming
and width behavior and all split-byte, combining-mark, variation-selector, ZWJ,
and width-profile fixtures pass unchanged.

## `github.com/charmbracelet/x/xpty`

Upstream baseline: `v0.1.4`.

On Windows, xpty's ConPTY start path receives a process handle from `Spawn`,
then opens another handle with `os.FindProcess` and previously discarded the
original without closing it. The local patch closes that original handle after
the replacement succeeds and fails safely if closing it fails.

Phone Terminal separately starts the ConPTY process suspended, assigns it to a
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
(cd third_party/vt && go test ./...)
(cd third_party/xpty && go test ./...)
go test -race github.com/charmbracelet/ultraviolet/...
go test -race ./internal/terminal ./internal/ptyhost
go test -race ./internal/desktop
```

Native Windows execution remains a separate required gate; cross-compilation is
only a compile check.

The Ultraviolet suite runs through the root module's exact dependency graph,
which includes the plan's pinned ANSI version. Its retained upstream `go.mod`
and `go.sum` are unchanged provenance records.

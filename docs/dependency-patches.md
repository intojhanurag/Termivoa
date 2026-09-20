# Dependency patches

Phone Terminal keeps narrow source patches for two pinned Go dependencies under
`third_party/`. Their original versions remain in `go.mod`; local `replace`
directives make the reviewed copies authoritative for builds. Both directories
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
- adds byte-budget enforcement to the existing line-bounded scrollback;
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

## Validation

Run both retained upstream suites as well as the repository adapter suites:

```sh
(cd third_party/vt && go test ./...)
(cd third_party/xpty && go test ./...)
go test -race ./internal/terminal ./internal/ptyhost
```

Native Windows execution remains a separate required gate; cross-compilation is
only a compile check.

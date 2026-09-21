# P3 desktop decoder bounds

Date: 2026-09-21. Host: Apple Silicon macOS 26.5.2 (25F84).
Toolchain: Go 1.27.1. Baseline: private main `9d21265`.

## Reproduction and change

The exact pinned Ultraviolet module was copied from the existing Go module
cache without a dependency version change:
`v0.0.0-20260303162955-0b88c25f3fff`, module checksum
`h1:uY7A6hTokHPJBHfq7rj9Y/wm+IAjOghZTxKfVW6QLvw=`.
Its source, upstream tests, module manifests, and MIT license are retained in
`third_party/ultraviolet`. The original license matches byte-for-byte.

Before patching the reader, `TestPhoneTerminalReproduceUnboundedDefaults`
demonstrated 69,632 bytes retained in the private paste accumulator and 69,639
bytes of pending unterminated OSC input with no emitted event. A completed
protocol-paste size check cannot bound either earlier accumulation. This
fixture remains as compatibility evidence for zero-valued upstream defaults.

The local patch adds optional byte limits and a context-aware source API.
Phone Terminal's `desktop.NewBoundedInput` supplies the shared 64 KiB paste
limit and a separate 64 KiB pending-sequence limit. Overflow returns a stable
error before appending excess content. It never emits a partial paste or
reinterprets rejected content as key events; the failed decoder cannot restart.
The pump stops and joins its source read, including when overflow races a
blocked input read or event delivery. The caller continues to own its source.

Only `terminal_reader.go` differs from upstream; `input_limits.go` and
`phone_terminal_limits_test.go` are added. See `docs/dependency-patches.md` for
the patch contract and removal criteria. The Go version pin and upstream
Ultraviolet module manifests remain unchanged.

## Executed checks

```text
GOTOOLCHAIN=go1.27.1 go test github.com/charmbracelet/ultraviolet \
  -run TestPhoneTerminalReproduceUnboundedDefaults -count=1
Passed before the reader patch; reproduced both excessive accumulations.

GOTOOLCHAIN=go1.27.1 go test -race \
  github.com/charmbracelet/ultraviolet/... \
  ./internal/desktop ./internal/terminal -count=1
Passed all retained upstream packages and repository adapter tests.

GOTOOLCHAIN=go1.27.1 go test -race github.com/charmbracelet/ultraviolet \
  -run TestPhoneTerminalReadPumpPreservesDataReturnedWithEOF -count=1
Passed the additional data-plus-EOF source regression.

GOTOOLCHAIN=go1.27.1 go test ./...
Passed.

GOTOOLCHAIN=go1.27.1 go vet ./...
GOTOOLCHAIN=go1.27.1 go vet github.com/charmbracelet/ultraviolet/...
Passed.

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-directory>/desktop.test.exe ./internal/desktop
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c -o <temporary-directory>/ultraviolet.test.exe \
  github.com/charmbracelet/ultraviolet
Both produced x86-64 PE32+ test executables. No Windows execution claimed.

git diff --check
Passed.
```

Fixtures cover one-byte and 4096-byte paste chunks, rejection just beyond
64 KiB, exact-limit UTF-8 content, unterminated sequences, failed-reader reuse,
read-pump cancellation, stalled event delivery, arbitrary blocking-source
rejection, and the Go `Read` contract returning data together with EOF.

Repository Unix/PowerShell checks and CI now include the retained Ultraviolet
suite using the root module's exact dependency graph. CI has not run for this
local checkpoint. No frontend changes were made; the Node/web suite was not
rerun for this isolated decoder change.

## Remaining gates

- The new constructor must be used by the actual desktop command integration;
  this checkpoint does not modify command routing or attachment lifecycle code.
- Native Windows console UTF-8 setup, cancellation, and restoration remain
  separate execution gates. Byte-source Unicode fixtures and cross-compilation
  do not establish those results.
- Physical phone behavior, authentication, packaging, and release evidence
  are outside this decoder checkpoint and remain pending.

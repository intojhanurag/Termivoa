# P3 cancellable local terminal output

Date: 2026-09-21. Host: macOS 26.5.2 / build 25F84 / arm64.

## Behavior

`cli.NewFileTerminalWriter(*os.File)` owns a non-inheritable duplicate of a
terminal or pipe output handle. Its `WriteContext` completes the bytes or
returns the partial count and an error. Cancellation interrupts a stalled
write. `Close` cancels and joins active I/O before releasing the owned
duplicate, so the caller can restore its outer terminal after renderer writes
have finished. The caller's original output file remains open. This adapter
deliberately exposes no context-free `Write` method.

Input and output share the owned-handle lifecycle implementation. On Unix,
both use one mutex around the short nonblocking syscall and flag-restoration
window. No lock or temporary flag change spans a readiness wait. Thus aliased
stdin/stdout descriptors cannot capture each other's temporary flags. On
Windows, the read and write paths share the locked-thread
`CancelSynchronousIo` mechanism, whose cancellation callback is joined before
releasing the thread handle and unlocking the OS thread. Neither path changes
console mode or closes the caller's descriptor.
Windows pipes are checked using
[FileModeInformation](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_mode_information).
Overlapped pipes are rejected before I/O because synchronous cancellation
cannot safely be assumed for them. A native test for this rejection is
included, alongside pipe cancellation and console-mode preservation tests.

## Evidence

Actual macOS tests cover:

- A 1 MiB write to a pipe whose peer consumes nothing returns promptly when
  its context expires, with a bounded partial count.
- `Close` interrupts a blocked pipe write and returns after the write exits.
- Closing the owned writer leaves the original output handle valid; closing
  the original first still permits the duplicate to write exact bytes.
- The peer reaches EOF after both intended handles close, with no background
  writer or duplicate retaining the pipe.
- Concurrent blocked input and flooded output through duplicates of one PTY
  both cancel, and the original descriptor's flags are restored exactly.

The initial alias test compared flags before the PTY's first write and saw
Darwin add `FWASWRITTEN` (`0x10000`). This is an internal kernel bookkeeping
bit, documented in
[Apple XNU's fcntl definitions](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/fcntl.h).
The corrected fixture primes one synthetic byte before taking the baseline,
then explicitly starts in blocking mode. It still compares every flag bit
after both operations, exercising actual temporary nonblocking changes.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/cli -count=1
GOTOOLCHAIN=go1.27.1 go vet ./internal/cli
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go vet ./internal/cli
```

These checks passed after the fixture correction. Windows cross-compilation
with `go test -c ./internal/cli` also produced a PE32+ x86-64 executable
containing the shared stalled-pipe tests and the isolated console helper's
output/handle/mode checks. Native Windows execution remains pending;
cross-compilation is not evidence of actual Windows cancellation behavior.

## Remaining integration

The desktop live client must receive this context-aware output, cancel its
paint operation, and join painting before restoring the outer TTY. This
checkpoint does not wire `cmd/pterm`, prove that integration, or claim a
complete desktop attach flow. Native Windows output cancellation remains an
explicit gate.

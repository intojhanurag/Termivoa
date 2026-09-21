# Desktop attachment shutdown ordering

## Environment and scope

- Recorded 2026-09-21, macOS 26.5.2 (25F84), arm64, Go 1.27.1.
- Branch: `fix/p3-desktop-shutdown`, based on `9d21265`.
- Unit fixtures use synthetic events and terminal mode operations. They do not
  claim physical terminal, native Windows, or phone validation.

## Reproduction and fix

`TestAttachmentJoinsWorkersBeforeRestoring` failed against the prior coordinator:
`terminal restored while decoder was still running`. Canceling the event context
did not wait for the decoder to leave its read loop, and the attachment did not
own shutdown of its display transport. A final read or paint could therefore
race restoration of the enclosing terminal.

The coordinator now connects only after entering the alternate screen/raw mode.
Its transport contract includes cancellation/join through `Close` and connection
loss notification. Every connected exit cancels input, closes and joins the
display transport, waits for the decoder to finish, then restores terminal modes.
Connection failure still restores the terminal without starting input. A closed
event channel exits without a busy loop. Read, close, and restore errors remain
available through the returned joined error.

The supplied event streamer must actually honor context cancellation; the live
transport must use cancellable output and join its workers. Those adapters and
the complete public CLI integration are separate checkpoints, not proven merely
by this interface. No timeout that abandons a still-running reader was added.

## Commands and results

- Before the fix: `go test ./internal/desktop -run
  TestAttachmentJoinsWorkersBeforeRestoring -count=1` failed as expected.
- After the fix: `go test -race ./internal/desktop -count=1 -timeout=30s` passed.
- `go vet ./internal/desktop` passed.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o <temporary-output>
  ./internal/desktop` passed. This is compilation only, not native execution.

Regression coverage includes worker join ordering, connect-after-terminal-entry,
disconnect, failed connection setup, closed event channel, and simultaneous
decoder/close/restore errors. Existing detach, input failure, and retryable raw
mode restoration tests remain passing. No session termination is sent on detach.

## Independent review

An independent review found that the pinned Ultraviolet reader flushes decoded
events with unconditional channel sends, including during cancellation. Waiting
without draining could hang when one read contains Ctrl+] followed by other keys.
Shutdown now drains and discards those events until the reader has joined. A
real `uv.NewTerminalReader` fixture in a disposable helper process covers that
case and proves the trailing keys are not relayed. Cancellation joined with a
substantive decoder error is preserved rather than silently suppressed.

Review also identified unbounded bracketed-paste accumulation inside the pinned
reader before a `PasteEvent` reaches protocol validation. A bounded decoder
adapter or documented source patch remains required before live CLI input is
wired. This shutdown checkpoint does not claim that separate bound is fixed.

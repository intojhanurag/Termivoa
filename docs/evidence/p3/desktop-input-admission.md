# Desktop input admission

Date: 2026-09-27. Apple Silicon macOS 26.5.2 (25F84), Go 1.27.1.
Base commit: `14c81d9` (`feat/p3-checkpoint`), with uncommitted working-tree
changes. This follows the startup handoff failure in `desktop-native-resize.md`.
Only disposable terminals, broker state, and synthetic child receipts are used.

## What was wrong

- `Connect` displayed the `Control` footer before the startup decoder was
  joined and before native input was flushed. A user who typed after seeing
  `Control` could lose the input. Also, a key decoded before the desktop was
  the ready controller could be sent after a readiness or lease change.
- The native handoff flush (`TIOCFLUSH`/`TCIFLUSH`) also discarded a Ctrl+]
  typed in that window. An exact-fit observer has no footer, so its only
  ready signal is the painted screen. Typing Ctrl+] right after it appeared
  did not detach. The native fixture failed the same way on 4 of 4 runs, both
  on clean `14c81d9` and with the first admission diff:
  `attach executable did not exit: context deadline exceeded` (observer detach).
- A failed `Activate` returned its error twice (bare, and joined again as
  `desktop: activate input: ...`).

## The fix

- `liveclient.State.InputEpoch` changes each time Connected, Ready, Controller,
  or the lease epoch changes. It is never reused. It changes to 0 if the
  counter is exhausted.
- `desktop.NewAdmittedInput` tags each owned read with the current epoch. The
  epoch is 0 until `Attachment.Activate` writes the final footer under the
  display lock. Mixed-epoch fragments and split pastes are tagged 0.
- `InputController`, `desktopTransport.SendAdmitted`, and the liveclient write
  check the epoch again. An old or zero epoch is dropped. It is never queued
  or retried. Ctrl+] detaches for all epochs. A Ctrl+V quote expires when
  the epoch changes.
- Review fix: before the flush, `BeforeInput` drains queued handoff bytes
  without blocking. It does not send them. If they contain `0x1d`, it returns
  `desktop.ErrDetached`, and the attachment detaches normally
  (`internal/cli/desktop_input_unix.go`, `internal/desktop/attach_startup.go`).
- Review fix: `Run` reports an activation failure once.

## Commands and results

```sh
GOTOOLCHAIN=go1.27.1 go test -race -count=3 ./internal/cli/ ./internal/desktop/ ./internal/liveclient/
```

PASS: cli 8.609 s, desktop 5.175 s, liveclient 6.036 s. A `-json` rerun
counted 534 test/subtest passes, 0 failures, 0 skips.

```sh
GOOS=windows GOARCH=amd64 go vet ./internal/... ./cmd/...
CGO_ENABLED=0 GOOS=linux go vet ./internal/cli/
```

PASS for both.

```sh
GOTOOLCHAIN=go1.27.1 go test -count=1 -run 'Desktop|Attach' ./tests/integration/
GOTOOLCHAIN=go1.27.1 go test -race -count=5 -timeout=180s -v -run '^TestPtermDesktopAttachNativeOuterTTY$' ./tests/integration/
```

Before the drain fix: FAIL on 4 of 4 runs at the observer detach (35 s deadline).
With a temporary 1 s delay before the observer typed, the fixture passed. This
isolated the handoff window, and the delay was not kept. After the fix: 4 of 4
non-race runs PASS (1.3 to 2.3 s). The race run PASS 5 of 5: 6.85, 6.90, 6.90,
6.89, 6.89 s (package 36.057 s). `TestDesktopOuterTTYGuardian` skips because it
is a helper subprocess. `gofmt -l` and `git diff --check` are clean. The Ultraviolet
module tests (`third_party/ultraviolet`, `go test ./...`) pass.

New focused tests: `TestAttachmentStartupHandoffDetachClosesTransport` and
`TestAttachmentReportsActivationFailureOnce`. The second test failed before
its fix.

## Limitations

- Native Windows was not executed. Windows only compiles and vets. Its handoff
  still uses `FlushConsoleInputBuffer` with no detach scan, so a Ctrl+] typed
  at that instant must be pressed again.
- The drain looks for a raw `0x1d`. It does not decode kitty/modifyOtherKeys
  encodings of Ctrl+], which the desktop does not enable. It reads a maximum
  of 16 x 4 KiB, and the flush then discards any remaining bytes.
- Admission is sampled after each OS read returns. A byte that was typed while
  `Syncing` showed, but read after activation (a small scheduling window), is
  admitted.
- The real-terminal and phone UX gates remain pending.

## Review pass 2026-09-27 (PR #23 adversarial review)

Each fix has a test that failed before the fix and passes after it.

1. MEDIUM: one invalid decoded input ended `pterm attach`. Examples: Alt + a
   non-ASCII letter (`KeyAction{Character:"é", Alt}`), a paste with a form feed
   (`"a\fb"`), and an empty bracketed paste. Fix: `sendValidated` returns a typed
   `*desktop.InvalidInputError`. `Attachment.Run` drops that action and calls
   `InputRejected`. The CLI shows `Input not sent: invalid <kind>` in the
   controller footer for 3 s. An empty paste is now a no-op. Transport and I/O
   errors are still fatal. Protocol validation is unchanged. Tests:
   `TestAttachmentDropsInvalidLocalInputAndContinues`,
   `TestInputControllerEmptyPasteIsNoOp`,
   `TestDesktopRejectedInputShowsBriefFooterNotice`.
2. LOW: restore did not reset the cursor shape that the renderer sets with
   DECSCUSR. Fix: `restoreOuterTerminal` now sends `\e[0 q`. Test:
   `TestRestoreOuterTerminalResetsCursorShape`.
3. LOW: Ctrl+V followed by a paste or mouse event kept the quote armed, so
   the next Ctrl+] did not detach. The Ctrl+V was also lost. Fix: a paste or
   mouse event first sends the pending Ctrl+V as an ordinary key, the same as
   any other key. Test: `TestInputControllerQuoteBeforePasteOrMouseSendsCtrlV`.
4. MEDIUM: a rejected `session.attach` (for example `limit_exceeded` at 16
   viewers) only set `LastError`. `WaitReady` waited 15 s and reported
   `context deadline exceeded`. Fix: an error for the attach request closes
   the client with the typed `*liveclient.RemoteError`. `WaitReady` returns it
   at once, and `pterm attach` prints the broker reason one time. Tests:
   `TestAttachRejectionEndsWaitReadyWithRemoteReason`, plus the real-broker test
   `TestLiveClientAttachRejectedAtViewerLimit` (17th viewer). Before the fix,
   this test failed with `context deadline exceeded` after 3 s.
5. LOW: `json.Marshal` HTML-escaped the payload. A valid 64 KiB paste of `<`
   became more than 256 KiB and was rejected, which ended the attachment. Fix:
   the live client encodes with `SetEscapeHTML(false)` and removes the trailing
   newline. Test: `TestMaximumPasteOfHTMLCharactersFitsMessageLimit`.
6. LOW (documentation only): desktop mouse reporting is not enabled in the outer
   terminal. `docs/architecture.md` and `desktop-attach-command.md` now say
   that desktop mouse forwarding is not enabled in v0.

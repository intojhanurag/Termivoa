# Native macOS executable desktop attachment

Date: 2026-09-21. Apple Silicon macOS 26.5.2 (25F84), Go 1.27.1.
Command baseline: `006671c`; viewer epoch cancellation fix: `7f50d44`.
The fixture runs the actual built `pterm attach` executable, a disposable
restricted-IPC broker/SQLite database, and a synthetic managed child. It never
uses the owner's interactive terminal, projects, agent accounts, or credentials.

## Verified completed-attachment flow

`TestPtermDesktopAttachNativeOuterTTY` builds `cmd/pterm` using ordinary
`go build`. The enclosing test, broker, and synthetic child use the race-enabled
test executable; this does not claim that the separately built CLI is itself
race-instrumented.

- The broker answers the child's initial query before any viewer exists.
- An exact-fit 80x24 observer paints the current canonical screen, enters real
  raw mode, ignores typing, and detaches with Ctrl+] without sending input.
- Explicit `--control` changes the canonical grid to 120x39 in a 120x40 outer
  terminal. Input starts after the post-resize screen and Control footer appear.
- An injected outer cursor-position reply is discarded, while ordinary text
  reaches the child exactly once. Synthetic numbered SHA-256 receipts detect
  added, duplicated, missing, or reordered bytes.
- Unicode bracketed paste, the separate Enter action, child query replies, and
  Ctrl+V followed by literal Ctrl+] reach the child with the expected bytes.
- A second explicit controller displaces the first; displaced typing is ignored.
  Detaching that observer does not revoke the new controller or end the child.
- Both Ctrl+] detach and SIGTERM cancellation restore the complete original
  native termios structure and leave the outer alternate screen.
- A fresh read-only executable restores current content with no automatic
  control acquisition. Child PID, generation, and running state remain unchanged.
- Display-side query replies are counted as failures and never fed back into
  either terminal. Input and display adapters are canceled and joined in cleanup.

The outer PTY has an isolated guardian acting as its controlling-session leader,
like the desktop shell that normally surrounds `pterm`. It remains alive after
the attached command exits so the parent can inspect restored termios. Guardian
control pipes carry only fixture process status, never terminal content. Cleanup
targets only the exact fixture child or its newly created process group.

## Actual results and investigation

```sh
go test -race ./tests/integration -run '^TestPtermDesktopAttachNativeOuterTTY$' -count=5 -timeout=90s -v
```

PASS: 34.649 s, with individual runs 7.02 / 6.63 / 6.58 / 6.43 / 6.34 s.
After helper cleanup and explicit status-row refactoring, another single run
passed in 7.23 s (package 9.015 s). `go vet ./tests/integration` and
`git diff --check` also passed. Final process-handle cleanup ordering passed
again in 6.94 s (package 8.828 s). Independent source review found no remaining
fixture ownership/cleanup or assertion blocker. Staged Gitleaks scanning found
no leaks; only the fixture and this synthetic evidence are included.

Earlier failed runs are not hidden:

1. The first harness made `pterm` the outer controlling-session leader. After
   its successful exit, Darwin invalidated that terminal, so checking termios
   returned `ENOTTY` and reading returned EOF. The guardian fixes test ownership;
   that failure did not demonstrate a runtime restoration defect.
2. Several runs accepted any visible Control footer, including the old 80x24
   screen before initial resizing completed. Typed text could arrive before the
   replacement snapshot, but subsequent paste/Enter arrived during `Ready=false`
   and produced no receipt. One run lost the initial typed command as well.
   The attachment remained live; there was no demonstrated Unicode decoder
   deadlock. A temporary bounded-decoder probe produced the correct Paste/Enter
   events at chunk sizes 1–25. Native sampling recorded an input reader polling
   but did not independently establish the cause; diagnostic probes were removed
   afterward. Source review identified the pre-resize readiness transition.
3. The current fixture explicitly waits for Control on the final physical row,
   establishing that the replacement grid was painted and acknowledged. This
   verifies the completed flow but does **not** fix the user-facing readiness
   ambiguity: an old Control indicator briefly precedes a disabled-input resize
   transition. That issue and controlled shrink are tracked in the separate
   resize-transition change. Do not count this fixture as evidence that early
   input during initial resize is safe or queued.

No runtime workaround, larger input queue, input retry, deadline inflation, or
terminal transcript persistence was added. Native Windows outer-console
execution, real terminal application/manual UX checks, physical phones,
authentication/private HTTPS, and release readiness remain separate gates.

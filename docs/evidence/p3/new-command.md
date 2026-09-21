# Create and attach command checkpoint

Date: 2026-09-21. Apple Silicon macOS 26.5.2, Go 1.27.1.
Base: integrated desktop attach checkpoint `547ab35`.

`pterm new --project <saved-id> --shell <saved-id>` explicitly creates and takes
control of a new managed session. It first checks interactive input/output and
an initial viewport of at least 80x24, matching the fixed initial canonical grid.
Failed preflight does not change terminal modes, read input, or create a process.
Project paths and executables remain broker-resolved; the command sends IDs only.

Creation uses exactly one operation ID. After the correlated reply, the short-lived
management connection closes, the new session ID is displayed, and the canonical
desktop connection paints/acknowledges its first snapshot before taking control.
No creation, input, attachment, or reconnect is automatically retried. If output,
attachment, or cancellation fails after successful creation, the error identifies
the created session and tells the user to inspect it before creating another.
The command never terminates the new session as rollback. Naturally exited/lost
creation results are reported without starting a replacement process.

## Actual verification

- `go test -race ./cmd/pterm ./internal/cli -count=1 -timeout=90s`: passed before
  adding the native new-command regressions (2.716 s / 4.407 s).
- Final native new-command tests: `go test -race ./cmd/pterm -run TestNewCommand
  -count=5 -timeout=90s`: passed, 8.744 s.
- `go test -race ./cmd/pterm -run 'TestNewCommand|TestCommandHelp' -count=2
  -timeout=60s`: passed, 5.198 s. This includes saturated-pipe cancellation of
  `new --help` alongside existing help routes.
- `go vet ./cmd/pterm ./internal/cli`: passed.
- Windows x64 `CGO_ENABLED=0` command build and command-test cross-compilation:
  passed; no Windows executable was run.

The native fixtures run the actual command dispatcher against a real broker,
restricted IPC, SQLite and disposable managed child. A separate synthetic outer
PTY verifies creation from saved configuration, explicit control, canonical
81x24 resize inside an 81x25 window, Ctrl+] detach and cancellation, restored outer
termios, preserved caller handles, and the same running session generation afterward.
A second read-only live connection checks canonical dimensions, never inferring
them from persisted metadata. Small-window/non-terminal preflight leaves the
broker session list empty. Synthetic display capture is capped at 128 KiB.

## Fixture corrections and boundaries

The first draft of the native fixture incorrectly required persisted management
list dimensions to reflect live PTY resize. It timed out while the attachment was
already running. Its capture cleanup then waited for a PTY read after closing only
the master, leaving its own slave open; the overall test timed out after 60 seconds.
The fixture now observes canonical screen metadata through a live observer and
closes both its owned outer handles before a bounded reader join. No production
timeout, assertion about live dimensions, or process-lifetime behavior was weakened.

These are native in-process command/PTY tests, not the separately built executable
outer-terminal integration or a physical terminal glyph-layout test. Project
registration/setup UI, native Windows 11, real phone access, owner login profiles,
controller shrink recovery, stress/soak and signed release gates remain separate.
No provider account, user project, startup registration, network settings or
installation outside the repo were used.

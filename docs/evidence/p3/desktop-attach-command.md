# Public desktop attach command checkpoint

Date: 2026-09-21. Environment: Apple Silicon, macOS 26.5.2 (25F84),
Go 1.27.1 darwin/arm64. Private development only; no user terminal, real project,
provider account, startup registration, or network configuration was changed.

## Implemented behavior

`pterm attach [--control] [--dev] [--runtime-dir <isolated-directory>] <session-id>`
resolves an exact session generation over restricted management IPC, closes that
connection, then opens a canonical live desktop connection. It does not create a
process or silently restart a missing/lost session. `--control` is an explicit
takeover request made only after a complete initial snapshot is painted and
acknowledged. Without that flag, typing/paste is discarded locally, not queued.

Ctrl+] detaches without terminating the managed child. Ctrl+V then Ctrl+] sends
literal Ctrl+] while controlling. Input remains structured; the bounded
Ultraviolet decoder does not relay outer-terminal query responses. Input never
retries or reconnects. A takeover while a single action waits for a bounded resize
write discards that action; concurrent input callers are rejected, not queued.

The coordinator owns duplicate, cancellable input/output descriptors, not the
caller's stdin/stdout. Raw/alternate-screen/native console modes are guarded by
`desktop.OuterTTY`. Teardown joins the live painter, local status worker and input
decoder before restoration. Restoration uses a fresh five-second context per
write, independent of command cancellation; substantive cleanup errors survive.

The outer grid is measured from the actual terminal. Every incoming screen and
actual paint verifies that the entire canonical grid fits. An exact 80x24 observer
in an 80x24 outer terminal is valid and has no in-grid footer. Instructions appear
before entering and after leaving the alternate screen. When space exists, a
static ASCII footer occupies the first spare row and restores the canonical
cursor. No title, controller label, URL or child escape is inserted into it.
Controllers request the physical grid (bounded by protocol maxima), reserving one
status row when at least five child rows remain. Observers never resize the PTY.

## Commands actually run

- `go test -race ./internal/cli ./cmd/pterm ./internal/liveclient ./internal/desktop -count=1 -timeout=60s`: passed (3.804s / 3.220s / 3.641s / 3.007s).
- `go test -race ./internal/cli -run 'TestDesktop|TestAttachDesktop|TestRestorationWriter' -count=20 -timeout=60s`: passed, 13.860s.
- `go test -race ./internal/cli -run 'TestDesktop' -count=30 -timeout=60s`: passed, 1.562s, including cancellation and late status-writer cleanup.
- `go vet ./internal/cli ./cmd/pterm`: passed.
- `go test -race ./cmd/pterm -run 'TestCommandHelp|TestAttachUsage' -count=5 -timeout=30s`: passed, 3.021s. Attach help, top-level help and no-argument text honor cancellation with a saturated real pipe and do not close the caller's output.
- Windows x64 `CGO_ENABLED=0 go test -c` for CLI and command packages, plus `go build ./cmd/pterm`: passed compilation only; artifacts are outside Git.
- `git diff --check`: passed.
- Gitleaks 8.30.1 directory scans for `internal/cli` and `cmd/pterm`: no leaks found.

Focused tests cover observer discard/local detach, no concurrent-input queue,
lease change while waiting for resize, canceled pending input, uncertain delivery
not being mistaken for an observer transition, controller-only coalesced resize,
exact-fit/oversized grids, optional footer/cursor placement, and joined cleanup.
A disposable native Mac PTY test verifies a failed broker connection restores
termios and descriptor flags and leaves caller-owned handles usable.

The initial PTY fixture's exact flag comparison failed because its first write
changed Darwin's kernel-maintained `FWASWRITTEN` bit (0x10000), while termios and
mutable flags were unchanged. The test now writes a synthetic marker before
capturing the baseline; it still compares flags exactly afterward. The bit and
settable flags are documented in Apple's
[XNU fcntl header](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/fcntl.h).

## Pending gates and limitations

- The independent executable/real-broker outer-PTY integration suite is being
  implemented separately. These focused tests alone do not prove end-to-end
  control handoff or real-shell behavior.
- Window growth is measured, but shrinking below the previously rendered
  canonical grid can deliberately close the viewer before the resized snapshot
  arrives. The session continues; enlarge and re-attach. Smooth controlled shrink
  remains a concrete desktop UX gate, not a claimed pass.
- During resnapshot, local input is disabled/discarded and the optional footer
  says Syncing. Exact-fit observers cannot show a status row without sacrificing
  canonical cells; the pre-attach instructions document read-only behavior.
- Native Windows execution, real desktop terminals/Unicode width, native Windows
  console restoration, real phone tests, stress/soak, signing and release gates
  remain pending. Cross-compilation is not Windows runtime evidence.
- Public `new`, project registration and remaining CLI commands are not enabled
  by this checkpoint. The reusable coordinator is ready for creation wiring once
  it can create and attach as one tested user workflow.

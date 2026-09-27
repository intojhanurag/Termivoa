# CLI session selection and creation checkpoint

Date: 2026-09-21. Environment: Apple Silicon macOS 26.5.2, Go 1.27.1.
Private development only; this checkpoint does not complete desktop attachment.

`cli.App.ResolveSession` centralizes exact canonical UUID selection, bounded
pagination, rejection of multiple generations, and a fresh generation-correlated
status lookup. Termination reuses it without changing the confirmation flow.
An exited/lost state is returned honestly rather than silently starting a child.

`cli.App.Create` sends one operation ID and saved project/shell-profile IDs through
the management client. It neither constructs shell arguments nor owns processes.
It never retries uncertain delivery, checks acknowledgement configuration and
identity, and returns the broker's state. The future `new` command must attach
separately and preserve a successfully created session if attachment fails.

Verification actually run:

- `go test -race ./internal/cli ./cmd/pterm -count=1 -timeout=120s`: passed.
- `go vet ./internal/cli ./cmd/pterm`: passed.
- Unit cases cover missing/ambiguous/changed generations, refreshed exited state,
  exact saved configuration, invalid IDs, unavailable randomness, cancellation
  before/during ID allocation, uncertain delivery without retry, explicit remote
  rejection, and mismatched creation acknowledgements.
- Existing native Mac command tests still pass, including confirmed termination
  and cancellation of blocked command output.

Native Windows execution and public `new`/`attach` command integration are pending.
Unit tests are not evidence of interactive desktop or real-phone behavior.

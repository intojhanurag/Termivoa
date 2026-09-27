# Gateway shutdown fixture synchronization

Recorded 2026-09-21 on macOS 26.5.2 (25F84), arm64, Go 1.27.1.

The concurrent repository suite observed
`TestShutdownTimeoutClosesIncompleteHTTPConnectionAndReturnsTimeout` returning
nil instead of the expected deadline. The fixture opened a TCP connection,
wrote incomplete headers, and immediately canceled the server. TCP connect
and write can complete in the kernel before `http.Server` has accepted the
connection. In that case there is no tracked connection to drain, so a clean
shutdown is correct; the fixture had not established the condition it asserted.

An internal package-test `ConnState` observer now establishes `StateNew` before
the fixture cancels the server. The production observer remains nil. The test
still requires a shutdown deadline and verifies that forced close ends the
incomplete connection. Its 50 ms shutdown bound is unchanged. Cleanup now
cancels and joins the fixture gateway even after a failed intermediate assertion.

Validation:

- `go test -race ./internal/gateway -run
  TestShutdownTimeoutClosesIncompleteHTTPConnectionAndReturnsTimeout -count=50
  -timeout=45s` passed (5.281 s).
- `go vet ./internal/gateway` passed.
- Windows x64 `CGO_ENABLED=0 go test -c ./internal/gateway` passed; native
  execution is pending and not implied by compilation.

This is a fixture ordering correction, not a change to gateway shutdown policy.

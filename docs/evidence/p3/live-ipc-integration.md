# Native desktop client / broker integration

Recorded: 2026-09-21T07:22:08Z. macOS 26.5.2 (25F84), Apple Silicon,
Go 1.27.1. Private integration checkpoint parent:
`6e8ca106f3f0211ce48228143e7ef58f0909e236`.

The checkpoint combines the live-session server `4aee667`, independent broker
process ownership work through `248b043`, desktop live client `bb33c33`, and
attachment shutdown coordination `f775aa1`. This evidence concerns their actual
in-process broker + native child PTY + restricted IPC integration; it does not
substitute for the separate executable gateway-restart evidence.

## Fixture and assertions

`tests/integration/live_client_test.go` runs a deterministic child copy of the
test executable as a saved shell profile through the real PTY adapter. A
temporary, explicit development profile contains SQLite and the restricted
broker endpoint. Project/profile records are seeded before the broker starts;
the broker is the sole SQLite owner while running. Session creation travels
through authenticated-current-user CLI IPC and the canonical management RPC.
No user terminal, project, shell history, credentials, or coding-agent account
is involved.

The fixture reads raw terminal input, answers each submitted synthetic command
with a numbered SHA-256 receipt, and requests cursor position. Desktop viewers
use the real `liveclient` and constrained renderer. A context-aware synthetic
display interprets only the renderer's drawing instructions; its output stream
is drained into a failing assertion if any query response is generated, never
forwarded to the child.

The passing native test proves:

- An initial query completes with zero viewers. First attach is read-only, and
  complete snapshot painting precedes the acknowledgement needed for control.
- Every displayed cell, including resolved style, Unicode width/continuations,
  and the cursor, matches the canonical screen for the synthetic fixture.
- Observer input and resize are rejected. Explicit takeover updates both
  viewers, rejects input from the displaced controller, and does not silently
  grant control to a new observer.
- Structured text, Tab, bracketed multiline paste, and Enter reach the real
  child exactly as expected. Numbered receipt hashes also detect duplicates,
  dropped input, unexpected terminal replies, or replayed offline actions.
- A cursor query with two live viewers receives the expected response. A
  following clean command receipt detects an extra response in the input
  stream; neither synthetic client display responds to the child's query.
- Controller resize reaches 100x30 canonical dimensions and both viewers
  converge to the exact resized cells and cursor.
- Explicit release, observer detach, and active-controller Close do not end the
  process. Reconnect restores the current screen with the same child PID and
  session generation. It remains read-only until explicit acquisition.
- An action attempted after Close is rejected, not queued: a fresh connection's
  next command receives the exact next receipt with no stale prefix or replay.
- Closing the controlling connection relinquishes ownership for a subsequent
  observer; all client workers, native child, and broker are cleaned up.

## Commands and results

| Command | Actual result |
| --- | --- |
| `go test ./tests/integration -run '^TestLiveClientNative' -count=1 -timeout=45s -v` | PASS; test 0.30 s, package 0.889 s |
| `go test -race ./tests/integration -run '^TestLiveClientNative' -count=10 -timeout=90s` | PASS; ten native runs, package 23.307 s |
| `go test -race ./tests/integration -run '^TestLiveClientNative' -count=1 -timeout=45s -v` | PASS after helper-signature cleanup; test 2.19 s, package 3.824 s |
| `go test -race ./tests/integration -run '^TestLiveClientNative' -count=3 -timeout=45s` | PASS after hardening convergence polling across split trailing CRLF; package 8.325 s |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o tmp/live-integration-windows.test.exe ./tests/integration` | PASS; Windows x64 test executable compiled only, not run |
| `go vet ./tests/integration` | PASS |
| `git diff --check` | PASS |
| `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --staged --redact --no-banner` | PASS; no leaks found in approximately 20 KiB of staged synthetic tests/evidence |

The Windows binary is an ignored local build artifact, not a signed installer
or a runtime-validation result. The test source is enabled for native macOS and
Windows only. Linux is not substituted for either host.

## Remaining gates

Native Windows execution remains pending. Real outer-TTY entry/restoration,
signal behavior through the final CLI command, real phone display/input,
Tailscale HTTPS, authentication/revocation, multi-session stress, performance,
soak, packaging, signing, and release readiness are not claimed by this test.
The fixture shares the pinned Go emulator with the synthetic display, so this
is not independent proof of browser/physical-terminal Unicode width agreement.
No startup registration, account changes, network configuration, publication,
or provider-credit use occurred.

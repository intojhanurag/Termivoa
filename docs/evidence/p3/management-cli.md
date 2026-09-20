# P3 local management CLI checkpoint

Date: 2026-09-21

Scope: implement the restricted local management client and the user-facing
orchestration for `pterm list` and explicitly confirmed
`pterm terminate <session-id>`. This checkpoint intentionally does not modify
`cmd/pterm`; its thin command routing is coordinated with the separate host
process-mode checkpoint. `pterm new` and `pterm attach` remain pending the
canonical live-session IPC stream.

## Implemented behavior

- `internal/localclient` authenticates as `protocol.CLI` through
  `ipc.DialBroker`, verifies the live broker generation through the existing
  endpoint handshake, then uses the canonical length-prefixed management RPC.
  Requests are limited to 256 KiB and responses to 1 MiB.
- A management client permits only one request/response exchange at a time.
  A waiting call observes its own cancellation without writing a second frame;
  cancellation of an active exchange interrupts the socket read. Transport or
  correlation failure closes that client rather than attempting to recover a
  partially consumed stream.
- Every response must match the request ID, message type, and optional complete
  session identity. Mutation acknowledgements must also echo the exact durable
  operation ID. A typed broker error is a known rejection; a write followed by
  a missing, malformed, or mismatched mutation acknowledgement is reported as
  delivery-uncertain and is never retried automatically.
- The internal create primitive accepts only operation, saved-project, and
  saved-shell-profile UUIDs. There is no path, executable, argument,
  environment, working-directory, or raw-command field.
- `internal/cli.App.List` follows bounded 200-record pages, detects duplicate
  session identities and repeated cursors, and caps the traversal at 32 pages,
  which exceeds the broker's current 4,096 retained-session bound. It assembles
  the result before writing output.
- `internal/cli.App.Terminate` accepts only an exact canonical lowercase UUID,
  resolves its generation from the bounded list, refreshes status, and prompts
  with the session title and destructive process-tree effect. A declined or
  already exiting/exited/lost session allocates no operation ID and sends no
  mutation. One confirmed live session receives exactly one operation ID and
  one terminate request.
- Terminal-facing stored titles use quoted graphic output. Legacy ESC, OSC,
  bell, newline, bidi-format, and other non-graphic characters appear as
  visible escapes and are never written as terminal control characters.
  Confirmation input is bounded and accepts only `y`/`yes` as approval.
- Output distinguishes acceptance from process completion. `exiting` is
  reported as an accepted process-tree termination; `exited` and `lost` are
  explicitly reported as having no live process.

## Automated coverage

Tests exercise:

- a real restricted local listener, endpoint descriptor, CLI handshake, and
  one management list exchange;
- byte-fragmented response framing and sequential list/status requests;
- exact request, response, session, and operation correlation;
- typed broker errors and an oversized response length without allocating its
  body;
- active-read cancellation and cancellation while waiting for the serialized
  connection slot;
- a lost termination acknowledgement producing one uncertain result and one
  request, with no retry;
- a create request containing only saved configuration IDs;
- list pagination loops and duplicate identities;
- exact full-ID termination, decline, already exiting/exited/lost handling,
  one operation ID, and ambiguous-delivery guidance;
- bounded confirmation input and malicious legacy-title output containing
  terminal and bidi controls.

## Commands and results

Development host: Apple Silicon macOS 26.5.2. Go 1.27.1 was selected through
the Go toolchain mechanism. Node 24.11.0 and pnpm 10.33.0 were used for the
complete repository check.

```text
GOTOOLCHAIN=go1.27.1 go test -race ./internal/localclient ./internal/cli -count=1
passed

GOTOOLCHAIN=go1.27.1 go vet ./internal/localclient ./internal/cli
passed

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c ./internal/localclient
produced a Windows x86-64 PE test executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go test -c ./internal/cli
produced a Windows x86-64 PE test executable

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 \
  go vet ./internal/localclient ./internal/cli
passed

PATH="<Node 24>/bin:$PATH" pnpm install --frozen-lockfile
passed; reused the pinned local package store without changing manifests or the lockfile

PATH="<Node 24>/bin:$PATH" GOTOOLCHAIN=go1.27.1 ./scripts/check
protocol generation drift, Go format/vet/tests, patched dependency tests,
TypeScript checking, 47 web tests, and the production web build passed

git diff --check
passed
```

The first complete-check invocation stopped before repository tests because a
fresh worktree did not yet contain `node_modules` and therefore could not find
the pinned `quicktype` executable. Installing the frozen lockfile resolved the
local tool availability; no source or lockfile changed, and the complete check
then passed.

## Honest remaining gates

- The internal command layer is tested, but `cmd/pterm` does not expose `list`
  or `terminate` in this checkpoint. That thin integration waits for the
  coordinated process-mode branch so neither checkpoint overwrites the other.
- `pterm new` is not presented as complete merely because the internal create
  RPC exists. The command contract requires it to create and attach locally.
- `pterm attach`, controller acquisition, heartbeat, structured live input,
  resize, snapshot acknowledgement, and resynchronization require the pending
  canonical live-session IPC dispatcher/client.
- The Unix-socket listener and handshake ran on macOS. Windows code compiled
  to x86-64 PE and passed cross-vet, but this is not native Windows 11 runtime
  evidence.
- This checkpoint does not claim gateway HTTP/WebSocket behavior, pairing,
  Tailscale access, physical-phone behavior, packaging, signing, stress, soak,
  or release readiness.

# Session activity fields (broker side)

Date: 2026-09-27. Environment: macOS arm64, Go 1.27.1, Node 24. Branch
`feat/ui-v2-backend`. Contract: `lastOutputAt` and `preview` on
`sessionRecord` (`protocol/v1/broker-rpc.schema.json`, `http-api.md`).

## Behavior

- `lastOutputAt`: the session actor stores the wall-clock time after each PTY
  output chunk is applied to the canonical emulator (`internal/broker/session.go`,
  PTY case of the actor loop). Resize, snapshots, viewer attach/resync and
  control changes do not touch it. The presence actor query returns it; the
  record carries it as millisecond RFC 3339 UTC. Any applied output counts,
  including output that leaves the visible grid unchanged.
- `preview`: on `gateway.session.list` only, the same presence actor call also
  copies the current screen (`ScreenSnapshot`); the text is extracted after the
  actor replies (`internal/broker/preview.go`). Last non-blank row; continuation
  cells skipped; C0/C1/DEL dropped; Unicode spaces collapsed to one space and
  trimmed; other non-printable runes (format/bidi such as U+202E and U+2066,
  zero-width, private-use, unassigned) replaced with U+FFFD, except U+200D so
  emoji ZWJ sequences stay whole. Truncated on a rune boundary to at most 160
  characters and 480 UTF-8 bytes including a trailing `…`. Absent for a blank
  screen.
- Both are filled only for live sessions on the browser gateway path. CLI
  `session.list` and `session.status` never carry them.
- `internal/protocol` validation: `lastOutputAt` is a 20..35-byte RFC 3339 time;
  `preview` passes `ValidatePreviewText` (safe display text, <=160 runes,
  <=480 bytes); both are rejected on exited or lost records.

## Tests

- `internal/protocol`: `TestBrokerRPCValidatesSessionActivity` (accepts edge
  values; rejects edge whitespace, NBSP/ideographic space, control bytes, C1,
  DEL, 161 runes, 484 bytes, bad timestamps, fields on exited/lost records).
- `internal/broker/preview_test.go`: last non-blank row, collapse/trim,
  controls, bidi/zero-width/BOM/private-use replacement, CJK, combining marks,
  wide continuation cells, rune and byte truncation, and 5000 seeded random
  screens whose previews all pass `protocol.ValidatePreviewText`.
- `internal/broker/gateway_rpc_test.go`: output moves `lastOutput` while
  resize/resync/snapshot/control do not; preview follows output and clears on
  a blank screen; exited/lost omit both; CLI list omits both; gateway list
  carries both through the validated RPC encoder.
- `tests/integration/pairing_test.go`: the real broker's gateway list shows the
  synthetic child's last row (`GRID 界 é 👩‍💻`, then the input receipt).

## Commands

- `GOTOOLCHAIN=go1.27.1 go test -race -count=2 ./internal/...`
- `GOTOOLCHAIN=go1.27.1 go test -count=1 ./tests/integration/`
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...`
- `./scripts/check`

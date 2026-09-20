# P1 core validation

Date: 2026-09-20

Milestone: P1 — Go terminal core

Status: in progress; not a completed milestone gate

## Environment

- Host: Apple Silicon macOS 26.5.2
- Go: 1.27.1 selected with `GOTOOLCHAIN=go1.27.1`
- Branch: `feat/p1-terminal-core`
- Core implementation commit: `dc7c04b4197252d8e88fce303e06b825f31cc19c`

No Windows 11 host or VM was available for this evidence. A Windows amd64 test
binary was compiled, but that is not native execution evidence.

## Implemented checkpoint

- `internal/ptyhost` starts executable/argument arrays under xpty, preserves the
  inherited environment, configures a controlling Unix PTY, serializes writes,
  separates viewer waits from session lifetime, and terminates a managed Unix
  process group.
- Windows code creates the ConPTY child suspended, enrolls it in a broker-owned
  kill-on-close Job Object, and resumes it only after enrollment.
- `internal/terminal` owns vt in one actor, continuously drains terminal replies
  without viewers, serializes encoded input and replies into one bounded stream,
  and captures immutable normalized screen/history frames.
- A local vt patch makes grapheme parsing independent of arbitrary PTY read
  and snapshot boundaries, aligns stored widths with the advertised width
  profile, exposes authoritative cursor state, and bounds retained cell,
  history, hyperlink, and parser-sequence data.
- The deterministic PTY child sends two terminal status queries and validates
  exactly one ordered response to each through the complete PTY → emulator →
  single writer path with no viewer on macOS.

## Windows CI observation

GitHub's native `windows-2025` runner exposed two real portability defects in
the first P1 checkpoint: ConPTY line input requires carriage return for Enter,
and ConPTY emits legitimate startup control sequences before child output.
Both fixtures now model those platform behaviors.

The same run showed that Windows ConPTY consumes child DSR/CPR queries and
returns its own console-state response before those query bytes reach the Go
emulator. The captured ConPTY output contained the cursor movement but not the
following query, while the Go terminal response stream remained empty. This is
incompatible with the plan's stricter statement that the Go emulator is the
only query responder on every host. The end-to-end no-viewer fixture still
checks that the Windows child gets one valid response, but that does not prove
the Go emulator generated it. P1 remains incomplete until the owner approves a
documented Windows exception or an implementation route that preserves the
single-responder requirement is demonstrated.

## Commands and outcomes

```text
GOTOOLCHAIN=go1.27.1 go test ./internal/terminal ./internal/ptyhost -count=1
PASS

GOTOOLCHAIN=go1.27.1 go test -race ./internal/terminal ./internal/ptyhost -count=1
PASS

GOTOOLCHAIN=go1.27.1 go vet ./internal/terminal ./internal/ptyhost ./tests/fixtures/ptychild
PASS

(cd third_party/vt && GOTOOLCHAIN=go1.27.1 go test ./...)
PASS

(cd third_party/xpty && GOTOOLCHAIN=go1.27.1 go test ./...)
PASS

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.27.1 \
  go test -c ./internal/ptyhost -o <temporary>/ptyhost.test.exe
PASS; output identified as PE32+ x86-64 Windows executable

git diff --check
PASS

source "$NVM_DIR/nvm.sh" && nvm use 24.11.0 && ./scripts/check
PASS; quicktype printed its existing empty-payload inference warning
```

## Remaining gate

- Run the PTY/process lifecycle suite on native Windows 11 x64 and record the
  ConPTY close/deadlock and descendant-cleanup results.
- Run the full P1 parser, query, resize, Unicode, alternate-buffer, and
  no-viewer fixture matrix through the private macOS and Windows CI jobs; native
  Windows 11 evidence remains a distinct target-host requirement.
- Resolve the demonstrated ConPTY query-responder mismatch with the plan before
  claiming the server-authoritative model complete on Windows.

These items are pending, not passed.

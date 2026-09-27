# P7 terminal fixes: OSC title leak and child environment leak

Date: 2026-09-27. Branch `fix/hu-env-parser`, base `7f58631`. Fixes defects 2
and 3 of `docs/evidence/p7/coding-agents.md` (branch `test/hu-agents`).

| Item | Value |
| --- | --- |
| OS | macOS 26.5.2 (25F84), Apple Silicon |
| Go / Node | go1.27.1 darwin/arm64, Node v24.11.0 |
| Parent process | The tests ran inside a Claude Code session, so the real `CLAUDE_CODE_*` variables were in the test process environment. |

## 1. OSC title with C1 continuation bytes leaks as text

Cause: `github.com/charmbracelet/x/ansi@v0.11.7` takes the raw byte `0x9C`
(8-bit C1 ST) as the end of an OSC/DCS/SOS/PM/APC string. In UTF-8 it is also a
continuation byte (`✳` = `E2 9C B3`, `“` = `E2 80 9C`). `third_party/vt` uses
this parser (`ansi.NewParser()` in `NewEmulator`); it has no parser of its own.

Fix: a patched copy of ansi in `third_party/ansi` (with a `replace` in
`go.mod`) adds opt-in `Parser.SetUTF8Strings`. `vt.NewEmulator` enables it.
Inside those strings, bytes `0x80`-`0xFF` are data. ST is `ESC \` or BEL (OSC).
This is what xterm and VTE do in UTF-8 mode. Details and the one known
difference (encoded U+009C) are in `docs/dependency-patches.md`.

Repro, with the bytes of
`printf 'abc\r\n\033]0;say \342\200\234hi\342\200\235\007def\r\n'` written
to the emulator core (`internal/terminal`), at every split point:

`go test -run TestOSCTitleWithC1ContinuationBytesDoesNotLeak ./internal/terminal/`

Before (upstream parser):

```text
--- FAIL: TestOSCTitleWithC1ContinuationBytesDoesNotLeak/claude-code
    core_test.go:943: split 0: screen rows = ["abc" " Claude Codedef" ""], want ["abc" "def" ""]
--- FAIL: TestOSCTitleWithC1ContinuationBytesDoesNotLeak/esc-st
    core_test.go:943: split 0: screen rows = ["abc" " xdef" ""], want ["abc" "def" ""]
--- FAIL: TestOSCTitleWithC1ContinuationBytesDoesNotLeak/quotes
    core_test.go:943: split 0: screen rows = ["abc" "hi”def" ""], want ["abc" "def" ""]
```

After: `ok github.com/intojhanurag/phone-terminal/internal/terminal`. The
screen shows `abc` and `def` only, for every split of the input.

The title is checked in `third_party/vt`
(`TestPhoneTerminalOSCTitleKeepsC1ContinuationBytes`): the Title callback
gets `say “hi”` one time. With `SetUTF8Strings(false)` this test fails. The
parser cases (OSC with BEL and `ESC \`, all C1 continuation values, DCS, APC,
CAN) are in `third_party/ansi/parser_phone_terminal_test.go`. The upstream
default stays the same and the upstream ansi suite passes unchanged.

## 2. Sessions inherit the broker's whole environment

Cause: `internal/ptyhost/process.go` set `cmd.Env = append(os.Environ(), ...)`.

Fix: `inheritedEnvironment` keeps only an allowlist, then the broker adds
`TERM` and `COLORTERM` (`internal/broker/rpc_server.go`, unchanged).

- Unix (case-sensitive): `HOME USER LOGNAME SHELL PATH TMPDIR SSH_AUTH_SOCK
  __CF_USER_TEXT_ENCODING LANG LANGUAGE TZ LC_*`. This is what launchd gives a
  user agent (without `XPC_*`), plus locale and time zone. The zsh login
  shell loads the rest from the user's profile.
- Windows (case-insensitive): `SystemRoot windir SystemDrive ComSpec Path
  PATHEXT TEMP TMP USERPROFILE APPDATA LOCALAPPDATA PUBLIC ALLUSERSPROFILE
  ProgramData ProgramFiles ProgramFiles(x86) ProgramW6432 CommonProgramFiles
  CommonProgramFiles(x86) CommonProgramW6432 HOMEDRIVE HOMEPATH USERNAME
  USERDOMAIN COMPUTERNAME OS NUMBER_OF_PROCESSORS PSModulePath PROCESSOR_*`.
  The hidden `=C:` entries are dropped.
- Dropped, for example: `CLAUDECODE`, `CLAUDE_CODE_*`, `TERM_PROGRAM`,
  `ITERM_SESSION_ID`, `VSCODE_*`, `TMUX`, `SHLVL`, `PWD`, the parent `TERM`.

Repro: `TestChildDoesNotInheritLaunchingProgramEnvironment` starts
`/bin/sh -c env` in a real PTY. The test sets `CLAUDE_CODE_X`, `CLAUDECODE`,
`TERM_PROGRAM`, `ITERM_SESSION_ID`, and `VSCODE_GIT_IPC_HANDLE`, and the real
Claude Code variables are also present.

Before (old `os.Environ()` line; values not recorded here):

```text
--- FAIL: TestChildDoesNotInheritLaunchingProgramEnvironment
    child inherited CLAUDE_CODE_MESSAGING_TOKEN
    child inherited TERM_PROGRAM
    child inherited CLAUDE_CODE_X
    child inherited ITERM_SESSION_ID
    child inherited CLAUDE_CODE_SESSION_ID
    child inherited CLAUDE_CODE_MESSAGING_SOCKET
    child inherited CLAUDE_CODE_EXECPATH
    ...
```

After: pass. `HOME`, `PATH`, and `LANG` pass through, `TERM=xterm-256color`
and `COLORTERM=truecolor` are set, and no `CLAUDE*`, `VSCODE_*`,
`TERM_PROGRAM`, or `ITERM_SESSION_ID` variable is present. Both lists are also
covered by pure-function tests (`internal/ptyhost/environment_test.go`).

Known limit: on Windows there is no login profile, so persistent user
variables from the registry (for example `JAVA_HOME`) are not in the session.
If they are needed, add the user's default environment block
(`windows.Token.Environment(false)`) before the allowlisted values.

## Checks

| Command | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.27.1 go test -race ./internal/... ./cmd/...` | Pass |
| `GOTOOLCHAIN=go1.27.1 go test -count=1 ./tests/integration/` | Pass |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...` | Pass |
| `./scripts/check` (gofmt, vet, `go test ./...`, ansi/vt/xpty/Ultraviolet suites, web typecheck/test/build) | Pass |

Native Windows execution was not run; the Windows allowlist has only the
pure-function test and a cross-compile check.

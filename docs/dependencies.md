# Dependency record

This is the P0 resolution record. Exact transitive versions are recorded in
`go.sum` and `pnpm-lock.yaml`. Inclusion here does not claim compatibility;
P1/P2 fixtures must establish terminal behavior.

## Runtime Go dependencies

| Module | Version | Purpose | License check |
| --- | --- | --- | --- |
| `github.com/coder/websocket` | `v1.8.15` | WebSocket transport | pending notice audit |
| `github.com/charmbracelet/x/xpty` | `v0.1.4` | PTY/ConPTY integration | pending notice audit |
| `github.com/charmbracelet/x/vt` | `v0.0.0-20260920004010-53e2afe73ae5` | Authoritative emulator | pending notice audit |
| `github.com/charmbracelet/ultraviolet` | `v0.0.0-20260303162955-0b88c25f3fff`, local bounded-input patch | Desktop input decoding; see `dependency-patches.md` | upstream MIT text retained; distribution notice audit pending |
| `github.com/charmbracelet/x/ansi` | `v0.11.7` | ANSI utilities | pending notice audit |
| `modernc.org/sqlite` | `v1.59.0` | Local metadata database | pending notice audit |
| `github.com/Microsoft/go-winio` | `v0.6.2` | Windows named-pipe IPC | pending notice audit |

## Build-time web dependencies

The exact direct and transitive dependency graph is locked in
`pnpm-lock.yaml`. The P0 baseline uses Node 24.11.0 and pnpm 10.33.0. The
runtime companion does not require Node.

## Security tooling

CI pins Gitleaks 8.30.1 and govulncheck 1.8.0. Scanner results require human
review and are not proof that the repository is free of secrets or vulnerable
code.

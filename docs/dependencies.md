# Dependency record

This started as the P0 resolution record and lists the direct dependencies in
`go.mod` and `web/package.json` as of 2026-09-27. Exact transitive versions are
recorded in `go.sum` and `pnpm-lock.yaml`. Terminal behavior is established by
the fixtures under `docs/evidence/`, not by inclusion here. Local patches are
described in [dependency-patches.md](dependency-patches.md); release notices are
tracked in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

## Runtime Go dependencies

| Module | Version | Purpose | License check |
| --- | --- | --- | --- |
| `github.com/coder/websocket` | `v1.8.15` | WebSocket transport | pending notice audit |
| `github.com/charmbracelet/x/xpty` | `v0.1.4`, local patch | PTY/ConPTY integration | pending notice audit |
| `github.com/charmbracelet/x/vt` | `v0.0.0-20260920004010-53e2afe73ae5`, local patch | Authoritative emulator | pending notice audit |
| `github.com/charmbracelet/ultraviolet` | `v0.0.0-20260303162955-0b88c25f3fff`, local bounded-input patch | Desktop input decoding; see `dependency-patches.md` | upstream MIT text retained; distribution notice audit pending |
| `github.com/charmbracelet/x/ansi` | `v0.11.7`, local patch | ANSI utilities | pending notice audit |
| `github.com/charmbracelet/x/term` | `v0.2.2` | Local terminal state and size for CLI commands | MIT per locked module `LICENSE`; distribution notice audit pending |
| `golang.org/x/sys` | `v0.47.0` | Platform system calls (Unix and Windows) | BSD-3-Clause per locked module `LICENSE`; distribution notice audit pending |
| `modernc.org/sqlite` | `v1.59.0` | Local metadata database | pending notice audit |
| `github.com/Microsoft/go-winio` | `v0.6.2` | Windows named-pipe IPC | pending notice audit |
| `rsc.io/qr` | `v0.2.0` | Terminal QR code for `pterm pair` (packages `qr`, `qr/coding`, `qr/gf256` only; never the cgo `libqrencode` subpackage) | BSD-3-Clause, checked against the locked module `LICENSE`; distribution notice audit pending |

## Build-time web dependencies

The exact direct and transitive dependency graph is locked in
`pnpm-lock.yaml`. The P0 baseline uses Node 24.11.0 and pnpm 10.33.0. The
runtime companion does not require Node.

## Website build dependencies

The website in `site/` (landing page and docs) is built with Astro 7.3.5 and
Starlight 0.42.4, locked in `site/pnpm-lock.yaml`. It is a separate static
site: nothing from `site/` ships in `pterm` or the phone app.

## Security tooling

CI pins Gitleaks 8.30.1 and govulncheck 1.8.0. Scanner results require human
review and are not proof that the repository is free of secrets or vulnerable
code.

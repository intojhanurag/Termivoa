# Phone Terminal

Phone Terminal is a planned self-hosted phone interface for terminal sessions running on an owner's Mac or Windows computer. The host-side runtime will be written in Go, and the phone interface will be a bundled React application. The server-authoritative terminal model is intended to let a phone and a desktop attachment observe the same managed session while keeping the shell alive across viewer disconnects.

> **Private development status:** this repository is private and at P0 (preflight and scaffold). No application runtime, terminal session management, phone UI, installer, or release artifact exists yet. Nothing here should be treated as a supported release.

## Intended v0 experience

The planned v0 supports one owner and one host computer, with multiple sessions started and owned by the Phone Terminal broker. Users can attach from their preferred desktop terminal through `pterm` and from a paired phone browser over private Tailscale HTTPS. Existing arbitrary terminal processes cannot be adopted.

The planned host platforms are Apple Silicon macOS and Windows 11 x64. The planned phone browsers are iPhone Safari/Home Screen mode and Android Chrome. These are targets, not current support claims; the tested matrix will be updated only when evidence exists.

## Support matrix

| Surface | v0 target | Current evidence |
| --- | --- | --- |
| Host | Apple Silicon macOS | Development host identified; application tests not started |
| Host | Windows 11 x64 | Access and native test arrangement not established |
| Phone | iPhone Safari and Home Screen mode | Physical-device arrangement not established |
| Phone | Android Chrome | Physical-device arrangement not established |
| Shell | zsh | Not tested |
| Shell | Windows PowerShell and available PowerShell 7 | Not tested |
| Optional profile | WSL bash when already installed | Not tested; never substitutes for native Windows coverage |

Linux may run generic CI and development checks, but it is not a claimed v0 host platform.

## Architecture

One Go executable will provide separate broker, gateway, and CLI/desktop-attach modes:

- The broker owns PTYs, authoritative virtual-terminal state, authentication, controller leases, and SQLite.
- The gateway serves the embedded web application and translates authenticated HTTP/WebSocket traffic to restricted local IPC. It never owns or spawns PTYs.
- Clients receive normalized screen snapshots and row patches, not the child's ANSI byte stream, and send structured input rather than arbitrary bytes.
- Production browser access binds to loopback and is exposed only through explicitly authorized Tailscale Serve configuration.

See [docs/architecture.md](docs/architecture.md), the versioned decision records in [docs/decisions](docs/decisions), and the authoritative implementation plan in [docs/implementation-plan.md](docs/implementation-plan.md).

## Repository layout

The P0 scaffold contains `cmd/pterm`, the planned `internal/*` ownership boundaries, `web`, `protocol/v1`, `tests`, `packaging`, and `docs/evidence`. Most packages contain only boundary documentation; they are not functional implementations yet.

## Development prerequisites

The locked baseline is Go 1.27.1, Node.js 24.11.0, and pnpm 10.33.0. Direct dependencies and complete lockfiles are pinned in the repository. Node.js and pnpm are development tools only; released companions will embed the web assets and will not require a Node runtime.

Contributors use:

```sh
./scripts/check
```

On Windows:

```powershell
./scripts/check.ps1
```

The check scripts cover generation drift, Go formatting/vet/tests, TypeScript checking, web unit tests, and the production web build. Platform integration suites and physical-phone tests remain separate explicit gates; a Linux or browser-emulation pass will not imply macOS, Windows, iPhone, or Android validation.

## Security and privacy

Phone Terminal will handle terminal control, so terminal output, input, tokens, and pairing data are sensitive. Never include credentials, raw personal terminal transcripts, signing material, runtime databases, or private test data in issues, pull requests, fixtures, screenshots, or logs. See [SECURITY.md](SECURITY.md) for the current private-development reporting process.

## Current limitations

- Only a non-functional scaffold executable and placeholder web page exist; no terminal application has been implemented or benchmarked.
- No target-platform, browser, phone, pairing, reconnect, performance, packaging, signing, installation, or uninstall claim has passed its acceptance gate.
- Tailscale is not configured by this repository, and no public tunnel is supported.
- A future live session will require the host to stay awake, logged in, and running the broker. Host reboot, logout, or broker failure will not preserve a live process.
- Signing identities, owner-approved public contacts, final branding, and publication authorization have not been established.

Progress and blockers are tracked in [docs/progress.md](docs/progress.md).

## Screenshots

No interface exists yet, so there are no truthful product screenshots. Future screenshots must use synthetic data and identify the tested build; private terminal content must never be captured for documentation.

## Contributing

Development is currently private. See [CONTRIBUTING.md](CONTRIBUTING.md) for the expected workflow and validation standards. Contributions are licensed under Apache-2.0 unless explicitly stated otherwise.

## License

Original project code is licensed under the Apache License 2.0. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

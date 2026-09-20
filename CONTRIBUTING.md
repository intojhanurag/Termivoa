# Contributing to Phone Terminal

Phone Terminal is in private development. The implementation plan and `AGENTS.md` are authoritative. Do not widen scope, publish artifacts, or make support claims without evidence.

## Development setup

The intended toolchain is Go 1.27.1, Node.js 24 LTS, and pnpm. Exact setup commands will be added when the manifests and repository-owned check scripts exist. Do not install application dependencies globally when a repository-local tool is available.

The planned checks are:

```sh
./scripts/check
```

and on Windows:

```powershell
./scripts/check.ps1
```

Neither script exists yet. Platform integration commands will be documented separately because generic tests do not establish native macOS or Windows lifecycle behavior.

## Code organization

- `cmd/pterm`: CLI entry point and internal process modes
- `internal`: broker, PTY, terminal, protocol, IPC, gateway, auth, store, and platform boundaries
- `protocol/v1`: canonical JSON Schemas and capability documentation
- `web`: strict TypeScript/React phone interface
- `tests`: deterministic fixtures and integration tests
- `packaging`: macOS and Windows packaging/signing preparation
- `docs`: architecture, decisions, progress, evidence, and release material

## Branches, commits, and pull requests

Use one descriptive branch per milestone or reviewable subtask, such as `feat/p1-terminal-core` or `fix/p2-resize-state`. Keep `main` buildable. After the initial bootstrap, application changes enter `main` through pull requests.

Use Conventional Commit messages with meaningful scopes, for example `feat(terminal): add canonical row snapshots`. Before each commit:

1. Inspect `git status` and the staged diff.
2. Stage only explicit task files.
3. Run relevant checks.
4. Review staged content for secrets, runtime data, personal paths, terminal transcripts, generated binaries, and signing material.

Pull requests must explain the problem, behavior change, validation evidence, compatibility impact, limitations, and milestone. Resolve review conversations and rerun affected checks. Do not bypass CI, rewrite shared history, or invent a second reviewer.

## Testing and evidence

Add deterministic fixtures before relying on screenshots or a real coding-agent run. Record exact commands, environment, expected/actual outcomes, and defect links under `docs/evidence/<milestone>/`. Mark tests that could not run as blocked.

Native Windows coverage cannot be replaced by WSL. Emulator or responsive-browser tests cannot replace physical iPhone or Android evidence. Tests using a real coding CLI require owner approval before spending credits or touching a non-fixture repository.

## Protocol and dependency changes

Protocol changes begin in the canonical JSON Schemas and include regenerated Go/TypeScript types, validation, compatibility tests, and documentation. Never change protocol behavior silently.

Dependency changes must pin exact versions, explain the reason, update checksums/lockfiles and notices, and run the affected regression suite. Do not use floating `latest` references in release builds or workflow actions.

## Reporting issues

Use sanitized reproduction steps and synthetic data. Never post credentials, pairing links, tokens, environment variables, customer/employer code, raw personal terminal history, or sensitive screenshots. Security-sensitive reports follow `SECURITY.md`.

## Licensing

Contributions are licensed under the project's Apache License 2.0. No contributor-license-agreement service or fabricated sign-off is used. Preserve third-party license and notice obligations.

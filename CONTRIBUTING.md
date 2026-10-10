# Contributing to Termivoa

Thanks for your interest. Termivoa is free and open source, and it is still
in beta (`v0.1.0-beta.3`), so expect changes. For a large change, open an
issue first so we can agree on the shape before you write code.

[AGENTS.md](AGENTS.md) holds the design rules every change must keep (for
example: the broker owns all PTYs, clients get normalized frames, input is
never queued or resent). Read it, and [docs/architecture.md](docs/architecture.md)
for how the code fits together. AI coding agents must follow AGENTS.md too.

## Set up

You need Go 1.27.1, Node.js 24.11.0 and pnpm 10.33.0. Node and pnpm are build
tools only.

```sh
nvm use                     # reads .nvmrc (24.11.0)
corepack enable             # uses the pnpm version pinned in package.json
pnpm install --frozen-lockfile
```

Go's toolchain selection downloads Go 1.27.1 if your default Go is older
(`GOTOOLCHAIN=go1.27.1`). Do not install project tools globally.

## Check

```sh
./scripts/check             # Windows: ./scripts/check.ps1
```

This runs protocol generation drift, Go formatting,
`go vet` and tests (including the patched `third_party` modules), TypeScript
checks, web unit tests and the production web build.

Useful narrower commands:

```sh
GOTOOLCHAIN=go1.27.1 go test ./internal/broker/...
pnpm typecheck
pnpm test:web
pnpm protocol:generate      # after changing protocol/v1 schemas
```

To run the real stack locally without changing startup or Tailscale settings,
use the development harness in
[docs/install.md](docs/install.md#development-harness).

End-to-end tests run two isolated dev stacks in a headless browser and never
touch an installed `pterm` or Tailscale. See
[scripts/e2e-multicomputer/README.md](scripts/e2e-multicomputer/README.md).
The docs site has its own build, which fails on a broken link:

```sh
cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build
```

Platform integration and real-device tests are separate. A Linux or
browser-emulation pass does not prove macOS, Windows, iPhone or Android
behavior, and WSL does not replace native Windows.

## Code layout

| Path | Contents |
| --- | --- |
| `cmd/pterm` | CLI entry point and internal process modes |
| `internal/` | broker, PTY host, terminal, protocol, IPC, gateway, auth, store, lifecycle and platform code |
| `protocol/v1` | canonical JSON Schemas and capability docs |
| `web/` | strict TypeScript + React phone app |
| `tests/` | deterministic fixtures, integration and soak tests |
| `packaging/` | macOS, Windows and Linux packaging scripts; macOS and Windows signing scripts |
| `third_party/` | patched upstream Go modules ([docs/dependency-patches.md](docs/dependency-patches.md)) |
| `docs/` | architecture, decisions, progress, evidence and release material |
| `scripts/` | checks, protocol generation, Linux install, soak, docs screenshots, test harnesses |
| `site/` | landing page and public docs site (Astro Starlight) |

## Branches, commits and pull requests

- One branch per reviewable change, for example `feat/terminal-row-snapshots`
  or `fix/resize-state`. Keep `main` buildable; changes reach `main` through
  pull requests.
- Use [Conventional Commits](https://www.conventionalcommits.org/) with a
  meaningful scope, for example `feat(terminal): add canonical row snapshots`.
- Before each commit: check `git status` and the staged diff, stage explicit
  paths, run the relevant checks, and make sure nothing staged contains
  secrets, runtime data, personal paths, terminal transcripts, binaries or
  signing material.
- Fill in the [pull request template](.github/pull_request_template.md):
  problem, behavior change, validation actually run, compatibility impact and
  limitations. Do not mark a test as passed unless you ran it.
- Keep each pull request to its own task. If you find an unrelated bug, open
  an issue for it instead of fixing it in the same pull request.
- Resolve review conversations and rerun affected checks. Do not bypass CI or
  rewrite shared history.

## Where docs live

| What | Where |
| --- | --- |
| User docs (how to use it), published at termivoa.com/docs | `site/src/content/docs/docs/` |
| Landing page | `site/public/` |
| Internal design (how it works) | `docs/architecture.md`, `docs/*.md`, `docs/decisions/` |
| Roadmap and progress log | `docs/pending-work.md`, `docs/progress.md` |
| Release history | `CHANGELOG.md` (add to `Unreleased`) |

A feature or user-visible fix updates the docs in the same pull request:
`CHANGELOG.md`, the README feature list, the docs site guide and
`docs/architecture.md` (AGENTS.md rules 22, 23 and 25). Never put internal
package names or file paths on the public docs site.

## Tests and evidence

Add deterministic fixtures before you rely on screenshots or a real
coding-agent run. Milestone evidence (exact commands, environment, expected and
actual results) lives in [docs/evidence/](docs/evidence/README.md). Tests that
use a real coding CLI need the owner's approval before spending credits.

## Protocol and dependency changes

Protocol changes start in the JSON Schemas in `protocol/v1`, then regenerate
the Go and TypeScript types (`pnpm protocol:generate`) and add validation,
compatibility tests and docs. Never change protocol behavior silently.

Dependency changes pin exact versions, explain the reason, update lockfiles,
checksums and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), and run the
affected tests. No floating `latest` references in builds or workflow actions.

## Reporting issues

Use the issue templates with sanitized steps and synthetic data. Never post
credentials, pairing links or QR codes, tokens, environment variables, private
code, real terminal history or sensitive screenshots. Report security problems
privately as described in [SECURITY.md](SECURITY.md).

## Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Contributions are licensed under the [Apache License 2.0](LICENSE). There is no
contributor license agreement (CLA) and no sign-off requirement. Keep
third-party license and notice obligations intact.

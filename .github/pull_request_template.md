## What and why

<!-- What problem does this solve? Link the issue: "Closes #123". -->

## What changed

<!-- User-visible behavior and internal changes. Call out process ownership, trust-boundary, protocol, data or install changes. -->

## How I tested it

- Commands run:
- Platforms or devices tested:
- Evidence (path under `docs/evidence/`, if any):
- Not tested, and why:

## Checklist

- [ ] `./scripts/check` passes (and `cd site && npx astro build` if docs or the site changed).
- [ ] Commits use [Conventional Commits](https://www.conventionalcommits.org/) (`feat(web): ...`, `fix(broker): ...`).
- [ ] I checked `git status` and the staged diff: no credentials, `.env` files, signing material, personal paths, terminal transcripts, model logs or binaries.
- [ ] Screenshots and fixtures use made-up demo data only (no pairing links, QR codes or real terminal content).
- [ ] This pull request does only its task. Unrelated problems I found are filed as issues: <!-- list them -->
- [ ] Docs are updated in this pull request: `CHANGELOG.md` (Unreleased), README feature list, `docs/progress.md`, the docs site guide and release notes, and `docs/architecture.md` / `docs/pending-work.md` when the design or roadmap changes.
- [ ] Protocol changes start in `protocol/v1` and the generated types are regenerated (`pnpm protocol:generate`).

<!-- Do not tick a test you did not run. -->

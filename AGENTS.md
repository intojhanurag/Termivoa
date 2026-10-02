# Agent operating rules

These rules apply throughout this repository.

1. Before you work, read `docs/architecture.md` (how Termivoa works today) and `docs/pending-work.md` (the roadmap). `docs/implementation-plan.md` is the original v0 specification; follow it where it still applies. Preserve unrelated user work.
2. Use Go for all shipped host runtime code. Node.js and pnpm are build-time tools only; do not introduce a Node runtime, Rust backend, SSH-only gateway, cloud shell, or hosted service.
3. Keep the repository private. Never change repository visibility to public. The owner alone performs the manual private-to-public transition.
4. Never publish code, packages, installers, images, previews, logs, or artifacts outside the approved private repository without explicit authorization.
5. Complete milestone gates in order. Independent documentation, fixtures, and packaging preparation may proceed while owner-dependent access is pending, but do not claim blocked evidence as passed.
6. Never change a public protocol silently. Define protocol v1 messages in `protocol/v1`, generate Go and TypeScript types, and check generation drift.
7. The broker owns PTYs, terminal emulators, authentication state, controller leases, and SQLite. The gateway never owns or spawns a shell.
8. Send clients normalized terminal frames, never child ANSI output. Keep terminal bytes out of React state. Do not treat emulator rendering output as a serialized checkpoint.
9. The broker is the only application-level terminal-query responder. Broker-owned Windows ConPTY may answer DSR/CPR queries it consumes before Go vt sees them, but tests must prove exactly one response. Never send browser-, desktop-, or client-generated terminal-query responses to the child.
10. Route all PTY input through structured, authorized messages and one broker writer. Never queue offline user input or automatically retransmit it after reconnect.
11. Preserve strict bounds on dimensions, messages, paste, history, queues, and viewers. A slow viewer must not indefinitely backpressure the child.
12. Ask before using missing credentials, spending provider credits, installing outside the repository, changing Tailscale or startup configuration, adding collaborators/apps/webhooks, signing, or publishing.
13. Never request passwords, pasted private keys, or broad personal tokens. Use existing authenticated sessions first and name the minimum missing access and blocked gate.
14. Do not alter user files outside the approved workspace. Do not discard unrelated changes, rewrite shared history, force-push shared branches, disable checks, or weaken security to make a gate appear green.
15. Before every commit, inspect status and the staged diff, stage explicit task paths, run relevant checks, and review additions for credentials, `.env` files, signing material, personal paths, terminal transcripts, model logs, and binaries.
16. Use Conventional Commit messages and reviewable milestone branches. After the bootstrap commit, application changes reach `main` through private pull requests and required checks.
17. Preserve configured Git author and commit signing. If unavailable, ask instead of inventing identity, approval, attestation, or co-authorship.
18. Update `docs/progress.md` at every milestone with actual commands, evidence paths, defects, and owner-dependent blockers. Keep unexecuted tests pending.
19. Store non-sensitive evidence under `docs/evidence/<milestone>/`. Never store production terminal history, credentials, or private test data.
20. At the end of each work session, report the current branch, latest commit, push/PR status, checks run, uncommitted task work, and remaining blockers.
21. Keep every pull request to its own task. If you find a bug or problem that is not part of the task (a flaky test, a wrong test, a tooling or doc defect, a behavior bug elsewhere), do not fix it in the same pull request. Open a GitHub issue in this repository with what you saw, how to reproduce it, the evidence (command, output, file and line), and its impact, then continue the task. Fix it later in its own pull request that references the issue. Only fix it inside the task when the task cannot pass without it; then say so in the pull request. This rule also applies to helper agents: their briefs must include it, and the coordinating agent files the issues they report.
22. Every feature or user-visible fix updates all documentation in the same pull request: `CHANGELOG.md` (Unreleased; phone and host sides), the `README.md` feature list, `docs/progress.md` (rule 18), and the docs site in `site/src/content/docs/docs/` (the feature guide and its sidebar entry in `site/astro.config.mjs`, `reference/release-notes.md` under "Next beta", `reference/security.md` when access or data changes, `reference/troubleshooting.md` for new failure modes, `reference/cli.md` for new commands). Major features also update the landing page in `site/public/`. After merging, deploy the site with `cd site && vercel deploy --prod`. Helper-agent briefs must include this rule.
23. Write the docs site (`site/src/content/docs/docs/`) for people and AI tools at once:
    - Each page opens with a one-sentence answer that names Termivoa, and has a 120–160 character `description` that names Termivoa, a `lastUpdated` date, and question- or task-style headings whose sections stand alone.
    - Put facts in tables. Use the app's exact labels and one term for one thing (session, tab, control, session menu). Add "Common questions" only where users really ask.
    - Check every claim against the code before it ships, and never overstate security. Mark features that are not in the latest release tag as **Next beta** (`git log <tag>..HEAD`).
    - Add an image only where it shows that a step worked or explains an idea: real screenshots from `scripts/docs-screenshots/` (isolated dev stack, demo data only, never real names, hosts or paths), or a few diagrams and illustrations in the landing-page style (night palette, one cyan signal, amber hand notes; designed in Paper) whose words match the app and CLI exactly. Save them as small WebP files in `site/src/assets/docs/` (`phone-*.webp` for phone screens, `illo-*.webp` for illustrations), with alt text that says what is on screen and repeats any words in the image. Text in diagrams and illustrations must stay readable at 390 px (about 11 px or more on screen).
    - Keep `llms.txt`, the per-page `.md` copies and `robots.txt` working. Do not add FAQPage/HowTo schema, hidden text or bot-only content.
    - Before merging, `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` must pass; it fails on a broken docs link or anchor (starlight-links-validator).

24. Critical features need the owner's review before merge. A feature is critical when it changes authentication, pairing, trust between computers, the network surface, the protocol in a breaking way, or stored secrets (for example multi-computer). For such a feature: write the architecture first (an ADR in `docs/decisions/` and a design document) and open it as a draft pull request marked "DO NOT MERGE without owner review"; run independent adversarial reviews (at least security, and feasibility with alternatives) and record how each finding is resolved; then report to the owner in short, simple words and wait. Do not merge the design or any implementation pull request of a critical feature until the owner approves it, even when other rules allow autonomous merges. Pick the stack for performance and simplicity on evidence; a change from Go (rule 2) needs its own ADR and the owner's approval.

25. Keep the architecture and the docs up to date. The internal architecture lives only in this private repository: `docs/architecture.md` (the map) and the design documents in `docs/`. Every pull request that adds a feature or improves one updates, in the same pull request:
    - `docs/architecture.md` and the feature's design document (internal: how it works);
    - the docs site (rule 22; public: how to use it);
    - `docs/pending-work.md` (move the item to Done, or add new follow-ups).

    Never put internal architecture (package names, internal file paths, how security checks work inside) on the public docs site. When you find a doc that is out of date or repeats another, fix it or delete it.

When access is missing, use this format:

> I need [access/action] for [specific milestone]. It permits [scope] and will change/use [resource]. Please configure it through [secure location/action]. Until then, I can complete [independent work]; [specific gate] remains blocked.

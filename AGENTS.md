# Agent operating rules

These rules apply throughout this repository.

1. Read and follow `plan.md` and `docs/implementation-plan.md` completely. They are the authoritative implementation specification. Preserve unrelated user work.
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

When access is missing, use this format:

> I need [access/action] for [specific milestone]. It permits [scope] and will change/use [resource]. Please configure it through [secure location/action]. Until then, I can complete [independent work]; [specific gate] remains blocked.

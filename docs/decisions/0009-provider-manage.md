# ADR 0009: manage a provider from the phone (close, remove, new tab, new workspace, start, install command)

- Status: **accepted** (2026-10-04). The owner asked for the feature and delegated its approval in writing to the coordinating agent ("Go ahead, plan, verify, implement, test, raise pr, adversarial review then ship"). Under AGENTS.md rule 24 (new routes that change things on the computer) the coordinator approves after the independent reviews recorded in the design, section 12.
- Date: 2026-10-04
- Design: [`docs/herdr-manage.md`](../herdr-manage.md)
- Builds on: ADR [0008](0008-herdr-provider.md)

## Context

ADR 0008 let the phone see herdr, open one terminal and make a worktree. It left out "closing anything". On the first day of real use the owner had nine stale workspaces of a repository he had deleted, and no way to close them from the phone; two git projects had no **+**, because herdr reports a repository only after it made a worktree there; and a computer without herdr offered nothing. He wants to manage herdr from the app, and to install it on a computer from **Settings → Integrations**.

Lab facts (isolated herdr 0.8.2; design, section 1): `workspace.close` deletes no file, and closing a main checkout's workspace closes its linked worktrees' workspaces too; `worktree.remove` without force refuses a dirty worktree and keeps the branch of a clean one; `tab.create` and `workspace.create` answer the new pane; `workspace.create` silently uses another folder when `cwd` is missing; files that git ignores go with a removed worktree; herdr 0.9.3 refuses to close a main checkout while its worktrees are open; herdr's official installer writes `~/.local/bin/herdr`, one of Termivoa's lookup paths.

## Decision

1. **One new mutation route**, `POST /api/v1/providers/{provider}/manage`, with an `action` word (`closeItem`, `removeItem`, `newTab`, `newWorkspace`, `startServer`), an item ID with the name the user confirmed, or a project ID, and nothing else from the phone. Protocol revision 5.
2. **The phone never forces, and a close ends one workspace.** `removeItem` is `worktree.remove` without force: uncommitted work answers `problem: uncommitted`. `closeItem` deletes no file and is refused for a main checkout while its worktrees are open. The host checks the confirmed name on a fresh snapshot.
3. **The host finds git projects itself** (a `.git` entry at or above the workspace's folder, after an owner check, with a time limit), so **+** shows before herdr's first worktree.
4. **New workspace takes a Termivoa project ID**, never a path; the host checks the folder and its owner right before the call.
5. **The broker downloads nothing.** **Install herdr** opens a Termivoa session with herdr's official install command typed and not run; the user presses Enter.
6. **Start runs `herdr server`** with argv, in its own session, and on Linux in its own systemd scope when `systemd-run` is there.

## Rejected

- **A download by the broker** (version 1 of the design: a manifest from herdr.dev, a release asset from GitHub, a SHA-256 check). The security review cut it: a downloader and a new trust root inside the broker, a manifest that grows past any fixed limit, a version the lab had not tested, and a downgrade path, to save one key press. The official installer in a visible session does the same with no new code.
- **`force` removal, with a second confirmation.** Uncommitted work deleted from a phone screen is the one accident this feature must not allow; the user can open the terminal and commit or discard there.
- **Closing a main checkout together with its worktrees.** herdr 0.8.2 does it silently and 0.9.3 refuses; one rule for both is simpler and safer.
- **One route per action.** Five routes, five RPC messages and five limiters for bodies that differ by one field.
- **A path from the phone for a new workspace.** A second place where a phone names a folder; projects already are the list of folders the owner allowed.
- **Start at login; herdr updates.** A login item is a startup change (rule 12); `herdr update` runs in a terminal.

## Consequences

- A paired phone can close a herdr workspace and delete a linked worktree's folder when it has no uncommitted change (files that git ignores go with it). It could already do each by typing in a session; the app now does it in two taps, with a confirmation.
- `HTTPSettingsResponse.providers` lists herdr as `notInstalled` on macOS and Linux, so **Settings → Integrations** shows a card on every such computer.
- A herdr that Termivoa started keeps running when the phone leaves. On Linux without `systemd-run`, it ends when the Termivoa service stops.
- "Nothing downloads code on its own" stays true: Termivoa has no download code for herdr.

## Reviews

See [`docs/herdr-manage.md`](../herdr-manage.md), section 12.

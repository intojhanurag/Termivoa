# Manage a provider from the phone: design

Status: **accepted for build, version 2 (2026-10-04).** ADR [0009](decisions/0009-provider-manage.md). Builds on [herdr-provider.md](herdr-provider.md) (ADR 0008); names, the trust statement, the wire rules and the test tools of that design apply here unless this page says otherwise. The owner asked for this feature by name (manage herdr from the app; install herdr on a computer from **Settings → Integrations**) and delegated its approval in writing ("Go ahead, plan, verify, implement, test, raise pr, adversarial review then ship"). Under AGENTS.md rule 24 (new routes that change things on the computer) section 12 records the independent reviews and how each finding was resolved. Version 2 is the design after the two design reviews: the broker downloads nothing.

What it does. From the phone, for a computer's herdr:

| # | Action | Where in the app |
| --- | --- | --- |
| A1 | **+** on every git project, also one where herdr has made no worktree yet | project row (as today) |
| A2 | **New tab** in a workspace, opened at once | item menu |
| A3 | **Close workspace** | item menu, after a confirmation |
| A3b | **Close tab** (added on the owner's request during the build) | item menu, after a confirmation |
| A4 | **Remove worktree** (a linked worktree; never with force) | item menu, after a confirmation |
| A5 | **New workspace** in a Termivoa project folder, opened at once | herdr tab |
| A6 | **Start herdr** when it is installed and not running | Settings → Integrations, and the herdr tab's "not running" notice |
| A7 | **Install herdr** when it is not installed (macOS, Linux): opens a Termivoa session on that computer with herdr's official install command typed, not run | Settings → Integrations |

**Upgrade:** no SQLite schema change. Protocol revision 5. Pairing, devices and settings stay.

## 1. Facts this design rests on (lab evidence, 2026-10-04)

Run against an isolated herdr 0.8.2 (a copy of the binary, a temp `HOME`, its own named session, stopped and deleted after), and read-only calls on the owner's herdr (`herdr api schema --json`, `herdr api snapshot`, `session.json`).

| Question | Finding |
| --- | --- |
| Why a git project has no **+** | A workspace gets `worktree {…}` in the snapshot only after herdr made or opened a worktree for that repository (`worktree_space` in herdr's `session.json`). A workspace made with `workspace.create --cwd <git repo>` has `worktree: null`. The owner's `personal-website` and `pdf-editor` are git repositories with no **+**. |
| Create on such a workspace | `worktree.create {workspace_id, branch, focus:false}` works on a workspace with `worktree: null`; after it, the main workspace and the new one both have `worktree`. |
| `tab.create {workspace_id, focus:false}` | Makes a tab; answers `root_pane {pane_id, terminal_id, cwd}`. |
| `workspace.create {cwd, label, focus:false}` | Makes a workspace with one shell pane; answers `root_pane`. With a `cwd` that does not exist, herdr silently uses another folder. |
| `workspace.close {workspace_id}` | Ends the workspace's terminals. A linked worktree's folder stays. **Closing the main checkout's workspace also closes every linked-worktree workspace of that repository.** Unknown ID: `workspace_not_found`. |
| `worktree.remove {workspace_id, force:false}` | Clean worktree (also with commits that are not pushed): closes the workspace and deletes the folder; the branch stays. Modified or untracked files: `dirty_worktree_requires_force` (git's text, with the path), nothing changes. Main checkout: `not_linked_worktree`. |
| Install | The official installer (`https://herdr.dev/install.sh`) reads `https://herdr.dev/latest.json` (`version`, `assets {<os>-<arch>: URL}`, `sha256 {<os>-<arch>: hex}`), downloads the asset from `https://github.com/herdrdev/herdr/releases/download/…`, checks the SHA-256, and writes `~/.local/bin/herdr` (mode `+x`). Targets: `linux-x86_64`, `linux-aarch64`, `macos-x86_64`, `macos-aarch64`. `~/.local/bin/herdr` is already one of Termivoa's fixed lookup paths. |
| Start | `herdr server` runs the default session's server in the foreground with no terminal (the lab runs `herdr --session <name> server` this way). |
| `tab.close {tab_id}` | Ends the tab and its panes; the labels of later tabs are counted again (tab "3" becomes "2"), the IDs stay. **Closing the last tab closes the workspace.** Unknown ID: `tab_not_found`. Same on 0.8.2 and 0.9.3. |
| Files that git ignores | `worktree.remove` without force deletes a worktree that has only ignored files (a `.env`): they go with the folder (0.8.2 and 0.9.3). |
| herdr 0.9.3 | Same results, except: closing a main checkout's workspace while a linked worktree's workspace is open answers `workspace_group_close_required` and closes nothing. A second `herdr server` for a running session answers "herdr server is already running" and exits. |
| Service stop | Termivoa's systemd user unit has the default `KillMode` (control group): stopping the broker ends every process it started. |

## 2. Goals and non-goals

Goals: A1–A7 above; every destructive action asks first and says exactly what ends or is deleted; nothing new for a paired phone to reach that it could not already reach by typing in a session; Termivoa itself downloads nothing.

Non-goals: `force` removal (uncommitted work is never deleted from the phone); closing a main checkout together with its worktrees; renames; closing one pane; splits and layout; updating or uninstalling herdr; a download by the broker; Windows; named herdr sessions; starting herdr at login.

## 3. Wire contract (protocol revision 5)

Bodies in `protocol/v1/http-api.schema.json`, generated into Go and TypeScript. `protocol/v1/revision.json`: `revision` 5, `features.providerManage` 5. The app shows A2–A7 only for a computer at revision 5 or later. `minRevision` stays 1: an older app drops a provider entry whose state it does not know (its lenient check) and ignores unknown fields.

### 3.1 Changes to existing bodies

| Body | Change |
| --- | --- |
| `HTTPProviderState` | new value `notInstalled` |
| `HTTPProviderInfo` | `canInstall?: boolean` (true only with `notInstalled`, on macOS or Linux, when no herdr was found **or refused** at any lookup path), `canStart?: boolean` (true only with `notRunning`, a usable binary and no socket refusal) |
| `HTTPProviderItem` | `linked?: boolean` (a linked worktree: **Remove worktree** applies) |
| `HTTPSettingsResponse.providers` | lists herdr with `state: notInstalled` on macOS and Linux when it is not installed (before: absent). `notInstalled` wins over `off`. Such a provider has no tab, is not polled by the app, and its card has no switches. |

### 3.2 One new route

`POST /api/v1/providers/{provider}/manage`, under `admitAPI` and `handler.mutation` like `…/create` (cookie, exact own or bound `Origin`, `X-Pterm-Request: 1`, JSON, at most 64 KiB, unknown fields refused), on the gateway's own broker connection (like create). Gateway RPC: `gateway.providers.manage` with its own cases in the payload validators.

`HTTPProviderManageRequest`: `action` (`closeItem` | `removeItem` | `newTab` | `closeTab` | `newWorkspace` | `startServer`), `itemId?`, `name?`, `targetId?`, `projectId?`.

| Action | Required | Meaning |
| --- | --- | --- |
| `closeItem` | `itemId`, `name` | `workspace.close` |
| `removeItem` | `itemId`, `name` | `worktree.remove {force:false}` |
| `newTab` | `itemId` | `tab.create` |
| `closeTab` | `itemId`, `targetId` | `tab.close` for the tab of that terminal |
| `newWorkspace` | `projectId` | `workspace.create` in that project's folder |
| `startServer` | – | start `herdr server` |

A field that does not belong to the action is refused. `name` is the item name the user confirmed.

`HTTPProviderManageResponse`: `itemId?`, `targetId?` (the new terminal of `newTab` and `newWorkspace`; absent when this herdr cannot open a terminal without an agent), `problem?` (`uncommitted`: the worktree has changes that are not committed; nothing changed).

| Case | Code | Status |
| --- | --- | --- |
| Bad body, a field that does not belong to the action, `removeItem` on an item that is not `linked`, `startServer` when not `notRunning` or without `canStart`, `newTab` in an item that has 32 terminals, `newWorkspace` with 64 groups | `invalid_request` | 400 |
| Provider off or not installed; an item action while not `ready`; the server did not come up | `host_unavailable` | 503 |
| Item not in a fresh snapshot | `session_missing` | 404 |
| The item's name is not the confirmed `name`; `closeItem` on a main checkout while a linked worktree of its group is open; `closeTab` on the item's last tab | `operation_conflict` | 409 |
| Project unknown, or its folder is gone or not this user's | `project_missing` | 404 |
| The per-device limit (30 per minute), or another manage or create is running | `limit_exceeded` | 429 |
| herdr refused or failed in any other way | `worktree_failed` for `removeItem`; `host_unavailable` for the rest | as today |

Messages are fixed Termivoa text. herdr's and git's text never leaves the host and is never logged; the broker log gets the action and herdr's error code only.

## 4. Host

All in `internal/herdr` and `internal/broker/providers.go`; the gateway only routes.

### A1: **+** on every git project

The snapshot's panes carry `cwd`. In `apply` (not in `mapSnapshot`, which stays pure), for each workspace without `worktree`, the host looks for a repository at the workspace's panes' folders, first pane first: the folder, made absolute and resolved, must be an existing directory owned by this user, and it or one of its parents (at most 32 levels, stopping at a folder not owned by this user) must have a `.git` entry (`lstat`; no `git` process). Each workspace's check runs with a 200 ms limit in its own goroutine; one that does not answer counts as "not git" and is not tried again for 60 s, so a hung network mount cannot stop the poll loop. The result (a set of workspace IDs) goes into `mapSnapshot`, which sets `canCreate` and the group's `git` flag. Create still sends `worktree.create {workspace_id, …}`; herdr answers `not_git_worktree` if the host was wrong, which is already `not_a_repository`. After the first worktree the group's ID changes from `ws-<id>` to `g<hash>`: the create timeout path looks for the new item in every group.

### A2–A5: one `Manage` method

`func (p *Provider) Manage(ctx, request, project Project) (protocol.HTTPProviderManageResponse, error)`; `project` is the resolved folder and cleaned name for `newWorkspace`. Rules:

- One manage or create at a time per computer (the create lock, `TryLock`). Broker-owned context, 15 s per herdr call.
- Item actions read a **fresh** snapshot under the lock. The item must be there, and for `closeItem` and `removeItem` its name must equal `name` (workspace IDs start again after a herdr restart, and the phone's list can be 5 s old).
- `closeItem`: refused with `operation_conflict` when the item is a main checkout and a **linked worktree** of its group is in the fresh snapshot (herdr 0.8.2 would close it too; 0.9.3 refuses). Else `workspace.close`.
- `removeItem`: needs `linked`. `worktree.remove {force:false}`; `dirty_worktree_requires_force` → `problem: uncommitted`.
- `newTab`: refused when the item already has 32 terminals. `tab.create {workspace_id, focus:false}`.
- `closeTab`: `targetId` must be a terminal of the item in the fresh snapshot (the terminal ID is part of it, so an ID from before a herdr restart never matches); the host finds its tab there. The item's last tab is refused with `operation_conflict` (herdr would close the workspace: **Close workspace** says what that ends). `tab.close {tab_id}`; `tab_not_found` is `session_missing`. Tab labels are counted again by herdr, so the phone names the tab by its terminal, never by its label.
- `newWorkspace`: refused at 64 groups. The broker resolves `projectId` through the store, then the folder must pass `resolveProjectFolder` and `ipc.RequireCurrentOwner` right before the call (herdr falls back to another folder when `cwd` is missing). `workspace.create {cwd, label: <project name, cleaned>, focus:false}`.
- `newTab` and `newWorkspace` answer `targetId = pane_id.terminal_id` (each part and the whole checked by the handle pattern) only when this herdr can attach a terminal without an agent (`attachAny`), and wait for a poll turn that has it (`settle`, at most 3 s), so it opens at once.
- After every success the poll loop runs at once. Provider sessions of a closed or removed workspace end by the existing rule (their pane left the snapshot).

### A6: start

`startServer` runs `<herdr> server` with `exec.Command` and argv (never a shell, never tied to a request or serve context): the environment every Termivoa session gets (the ptyhost allowlist: `HOME`, `USER`, `LOGNAME`, `SHELL`, `PATH`, `TMPDIR`, `SSH_AUTH_SOCK`, `LANG`, `LC_*`, `TZ`, and on Linux the session-bus variables) plus `HERDR_SOCKET_PATH` and `TERM`; working folder `HOME`; standard input, output and error on the null device; its own session (`setsid`), so it has no controlling terminal and is not in the broker's process group. The broker keeps the child and reaps it in a goroutine; `startServer` is refused while a child it started is still alive and the provider is not `ready`. herdr itself refuses a second server for a running session. On Linux, when `systemd-run` is at `/usr/bin/systemd-run` or `/bin/systemd-run` and passes `tailnet.CheckBinary`, the argv is `systemd-run --user --scope --quiet --collect -- <herdr> server`, so stopping the Termivoa service does not end herdr and its agents; if that run ends without a ready provider, the host starts herdr plainly once. Without `systemd-run`, herdr ends with the Termivoa service, and the docs say so. The herdr binary is checked (`tailnet.CheckBinary`) before the run. After the start, a "look again now" flag clears the poll loop's retry and lookup timers; the host waits up to 5 s for the provider to be `ready`; else `host_unavailable` ("run `herdr server` in a terminal on <computer> to see why").

### A7: install

The broker installs nothing. `canInstall` tells the app that the official installer can work here. The app opens a new Termivoa session on that computer (its first project, as **New session** does) with the text `curl -fsSL https://herdr.dev/install.sh | sh` typed into the terminal and **not** sent: the user reads it and presses Enter. The official installer checks its own SHA-256 and writes `~/.local/bin/herdr`, one of Termivoa's fixed lookup paths. The same "look again now" flag is set when the app reads the provider's settings card again, so the card turns to "Not running" with **Start herdr** within about 2 s of a tap on **Check again**, and by itself within 30 s.

## 5. Security

Trust statement (unchanged): a paired phone can already run any command in a session, so it can already close herdr workspaces, delete folders, and run herdr's installer. This feature gives it no new authority. New risks are accidents and wrong targets.

| # | Threat | Mitigation |
| --- | --- | --- |
| M1 | A page or tailnet node calls manage | Same admission and authorization as create (ADR 0008 S1), mutation headers, device limiter: 30 per minute |
| M2 | Injection through `itemId`, `name`, `projectId`, the project name | Patterns in the schema and in Go; the item must be in a fresh snapshot; `projectId` is a UUID looked up in the store (the phone never sends a path); values go to herdr as JSON; the label is cleaned (Cc, Cf) and cut |
| M3 | A wrong tap, or an ID that now names another workspace, destroys work | The app asks first; the request carries the confirmed `name` and the host checks it on a fresh snapshot; `removeItem` never forces: uncommitted work answers `problem: uncommitted`; the branch stays, so commits are not lost; the confirmation says that files git ignores are deleted too; `closeItem` deletes no file and never closes more than one workspace |
| M4 | The broker downloads or runs a chosen program | It downloads nothing. `startServer` has no parameter from the phone: fixed argv, a binary that passed the checks of ADR 0008 S3 |
| M5 | The install command | It is typed into a normal session, visible, and runs only when the user presses Enter: the same as typing it. The app's text is a constant; no value from a computer or from herdr is in it |
| M6 | Starting herdr twice, or with a refused socket | `startServer` only in `notRunning` with `canStart`; one child at a time; herdr refuses a second server |
| M7 | Paths or herdr text in an answer or a log | Fixed texts; `projectId`, never a path; the new `targetId` is checked by pattern; the log gets codes only |
| M8 | A1 reads folders herdr names | Only `lstat` on `.git` after the folder and owner checks, with a time limit; the folder never leaves the host |
| M9 | Shells without bound (rule 11) | `newTab` stops at 32 terminals in an item, `newWorkspace` at 64 groups; one manage at a time; the device limiter |
| M10 | Tests reach the real herdr | The S16 guard; the lab test covers A1–A6 on an isolated herdr |

## 6. Phone

- **Item menu**: a `⋯` button on each item row opens a sheet with **New tab**, **Close workspace** and, for a linked worktree, **Remove worktree**. **Close workspace** on a main checkout with open worktrees is off, with the line "Close its worktrees first."
- **Close** confirmation: "Close silver-meadow? Its terminals end. Files stay."
- **Close tab**: the item menu lists the item's terminals by tab label; each has **Close tab**, with the confirmation "Close tab 2? Its terminals end." An item with one terminal has the line "Close the workspace to end its last tab." In every confirmation **Cancel** comes first, and a tap in the first 400 ms does nothing, so a double tap on the menu row cannot confirm.
- **Remove** confirmation: "Remove the worktree silver-meadow? Its folder is deleted, also files that git ignores (such as .env). The branch stays." `problem: uncommitted`: "silver-meadow has changes that are not committed. Commit or discard them first."
- **New workspace**: a button on the herdr tab (also when the list is empty) opens a sheet with that computer's Termivoa projects; a tap makes the workspace and opens its terminal. With no projects the sheet says how to add one, as New session does.
- **Integrations**: `notInstalled` shows "Not installed"; with `canInstall`, **Install herdr** and the line "Opens a terminal on mac with herdr's install command. Press Enter there to run it."; without it, "Run `pterm doctor` on mac." `notRunning` with `canStart` shows **Start herdr** (also on the herdr tab's "herdr is not running on mac" notice). A `notInstalled` card has no switches.
- The app does not poll a `notInstalled` provider and shows no tab for it.
- Every button shows that it works (busy, result, error), as the rest of the app. For a manage error the app shows the host's fixed message.

## 7. Failure modes

| Failure | What happens |
| --- | --- |
| Item gone, or renamed, between the list and the action | `session_missing` or `operation_conflict`; the list refreshes; nothing changed |
| Uncommitted changes | `problem: uncommitted`; nothing changes |
| herdr refuses or times out | Fixed error text; the next poll shows the truth |
| The phone leaves during an action | It finishes in the broker's context |
| The install command fails (no network, no `curl`) | The user sees the installer's own output in that session |
| A herdr was found but refused (owner, mode, folder) | `canInstall` is false; the card says to run `pterm doctor`, which names the fix |
| Start does not come up in 5 s | `host_unavailable` with the hint to run `herdr server` in a terminal |
| Linux without `systemd-run` | herdr started from the phone ends when the Termivoa service stops |

## 8. Tests

- `internal/herdr`: `Manage` against the in-process fake (each action, each refusal, the name check, the main-checkout rule, `uncommitted`, the caps, the lock with create, no `targetId` without `attachAny`); A1 detection (git, not git, a parent with `.git`, another user's folder, depth, the time limit); start with a script binary (argv, environment, second start refused, a start that never answers); `notInstalled`, `canInstall` (also false with a refused binary) and `canStart`.
- `internal/broker`, `internal/gateway`, `internal/protocol`: the route (authorize, origins, mutation headers, limiter, codes, project lookup and owner check, validators).
- `scripts/fakeherdr`: `workspace.close` (with the 0.9.3 refusal by scenario), `worktree.remove` (dirty by scenario, `not_linked_worktree`), `tab.create`, `workspace.create`, pane `cwd`, `worktree.create` on a workspace without `worktree`, a `server` command for the start check.
- Lab test (real herdr, isolated): A1–A6, that the server keeps running with no workspace, and `$PATH` in a pane of a server that the provider started.
- Phase B (`H-*`): the menu and both confirmations, close, remove (clean and uncommitted), new tab opens, new workspace opens, **+** on a git project without worktrees, **Start herdr**, and **Install herdr** opening a session with the command typed and not run (on a stack without the fake herdr).
- Web: vitest for each sheet, text and state.

## 9. Docs to update

`CHANGELOG.md`, `README.md`, `docs/progress.md`, `docs/architecture.md`, `docs/pending-work.md`, `docs/threat-model.md`, `docs/herdr-provider.md` (pointers), `protocol/v1/http-api.md` (route, revision 5); site: `providers.md`, `guides/herdr.md`, `reference/security.md`, `reference/troubleshooting.md`, `reference/release-notes.md`.

## 10. Work split

HOST owns `internal/`, `cmd/`, `scripts/fakeherdr/`, `protocol/v1/http-api.md`; WEB owns `web/`. Both start from the generated types (already in the branch). Integration: phase B, docs, screenshots.

## 11. Open questions (defaults chosen)

| # | Question | Default |
| --- | --- | --- |
| Q1 | Force-remove a worktree with uncommitted changes from the phone? | No |
| Q2 | Close a main checkout and its worktrees in one tap? | No (close the worktrees first) |
| Q3 | Close one tab? | Yes (A3b), but never the last one |
| Q4 | Start herdr at login? | No |
| Q5 | Should the broker download herdr itself? | No (version 1 had it; the security review cut it) |

## 12. Reviews: findings and resolutions

Two independent reviews ran on version 1 of this design: security, and feasibility with alternatives (rule 24). Each finding was checked against the code or the lab.

### Security review (no critical; six high)

| # | Finding | Resolution |
| --- | --- | --- |
| S-H1 | The push HTTP client cannot do the download (10 s limit, no redirects, dial check not exported); loosening it would weaken push | Moot: no download (S-cut) |
| S-H2 | The manifest is near the size limit, gives 0.9.3 while the lab used 0.8.2, and allows a downgrade | Moot: no download. The lab now also ran on 0.9.3 (section 1) |
| S-H3 | A workspace ID can name another workspace after a herdr restart; a worktree opened after the list was read is not in the confirmation | The request carries the confirmed `name`, checked on a fresh snapshot; a close never ends more than one workspace |
| S-H4 | `remove` without force can delete files that git ignores | Lab: confirmed on 0.8.2 and 0.9.3. The confirmation says so; the docs too |
| S-H5 | A1 puts file-system calls in the poll loop; a dead network mount would stop it; the cache has no bound | The check runs in `apply` with a 200 ms limit per workspace and a 60 s back-off; no cache beyond that |
| S-H6 | Rule 24: the owner's general "then ship" came before this design | The owner's request names the feature (manage, and install from Integrations) and the process. The riskiest part of version 1, a download by the broker, is cut. The pull request says what changed from his words (install opens a terminal with the command typed) so he can object |
| S-M1 | A refused file at the install path makes an install loop | `canInstall` is false when any herdr was found or refused |
| S-M2 | A second `start` can run a second server; nobody reaps the child | One child at a time, reaped; herdr refuses a second server (lab) |
| S-M3 | `systemd-run --user` needs the session-bus variables | The ptyhost allowlist; `CheckBinary` on `systemd-run`; a plain start as fallback |
| S-M4 | `herdr-server.log` grows without bound | Output goes to the null device; the error text says how to see why |
| S-M5–M7 | URL check, temp file, e2e override of the download | Moot: no download |
| S-M8 | `providerFor` refuses a provider that is not installed | `startServer` needs an installed herdr; nothing else runs when `notInstalled` |
| S-M9 | The post-check of `workspace` compares path strings and closes on a mismatch | The post-check and the automatic close are deleted; the folder is checked right before the call |
| S-M10 | `tab` and `workspace` have no count bound | 32 terminals per item, 64 groups |
| S-L1 | "Text goes to the broker log" contradicts "never logged" | Codes only |
| S-L4 | `resolveProjectFolder` has no owner check | `ipc.RequireCurrentOwner` is named |
| S-L5 | The busy text is about worktrees | Its own text |
| S-L6 | On macOS a started herdr also outlives the broker | In the docs |
| S-cut | Cut the download: open a session with the install command instead | Adopted (A7) |

### Feasibility review: "feasible with changes"

| # | Finding | Resolution |
| --- | --- | --- |
| F-H2 | "Not installed" is `NotRunning` or "no such provider" in three places | `notInstalled` at those places; it wins over `off` |
| F-H3 | After a start the poll loop keeps its 5 s and 30 s timers | A "look again now" flag |
| F-H4 | The start environment is too small (`XDG_RUNTIME_DIR`, `PATH`) | The ptyhost allowlist; the lab test checks `$PATH` in a pane |
| F-H5 | Create refuses a group that is not git; the group ID changes after the first worktree | The detected set sets the group's `git` flag; the timeout path looks in every group |
| F-M1 | Pane `cwd` is not decoded | Decoded; the lab test proves which folder herdr uses |
| F-M2 | `mapSnapshot` must stay pure | The check is in `apply` |
| F-M3 | A new tab cannot open when herdr cannot attach a terminal without an agent | No `targetId` then |
| F-M4 | The manage result would be checked as a create result | Its own cases in the validators |
| F-M5 | The app would poll and show a tab for `notInstalled` | Not polled, no tab, no switches |
| F-M7 | 10 per minute blocks closing nine stale workspaces | 30 per minute |
| F-M8 | The child must not be tied to the serve context | `exec.Command`, `Start`, a reaping goroutine |
| F-M10 | The fake herdr refuses `worktree.create` on a workspace without `worktree` | The fake follows real herdr |
| F-L1 | Generated names clash (`Remove`, `Tab`) | The actions are `closeItem`, `removeItem`, `newTab`, `newWorkspace`, `startServer` |
| F-missing | **Start herdr** only in Settings; an empty project list; an empty herdr tab | Start is also on the tab's notice; the sheet says how to add a project; **New workspace** shows on an empty list |
| F-split | Four pull requests | One pull request: with the download cut, the rest is one route and one screen, and the owner asked for it together |

### Review of the built branch

Two independent reviews of the code (host: security and correctness; phone app), and the end-to-end run of both halves.

| # | Finding | Resolution |
| --- | --- | --- |
| B-H1 | `canStart` was true, and `startServer` ran, for up to 5 s after the socket turned refused (the state and the refusal were two publishes) (medium) | One publish (`notRunning` takes the error) |
| B-H2 | herdr's `dirty_worktree_requires_force` became `problem: uncommitted` with status 200 for every action, also a close (low) | Only for `removeItem`; else a failure (`TestManageItemActions`) |
| B-H3 | A herdr that was removed while the provider was off stayed `off` (low) | The off branch looks for the binary again: `notInstalled` wins |
| B-H4 | The 64-group limit of `newWorkspace` did not hold when the body budget cut the list (low) | A cut list counts as full |
| B-H5 | A1 can leave more than one hung goroutine for a workspace on a hung mount that leaves and comes back (low, plausible) | Not changed; in `docs/pending-work.md` |
| B-H6 | No test for a `systemd-run` that lives without a ready server; the in-process fake never changes its snapshot | Not changed; the reviewer's own test showed the guard works; the e2e fake does change state |
| B-W1 | A double tap on a menu row could land on the danger button of the confirmation that took its place (medium) | **Cancel** is first; a confirm tap in the first 400 ms does nothing (`confirmReady`; phase B `H-close` taps at 11 ms and nothing closes) |
| B-W2 | The New workspace sheet went away without its message when its computer's read failed, and came back by itself (medium) | The sheet keeps the last good settings while it is open |
| B-W3 | Focus was lost when the menu became a confirmation; Escape stopped working (medium) | Focus goes to the confirmation container, never to the danger button |
| B-W4 | The card stayed "Not installed" after an install (a `notInstalled` provider is not polled) (low) | Settings reads those computers when it opens and once more 2.5 s later, quietly |
| B-W5 | One failed automatic settings read showed "can't reach" (low) | A quiet read |
| B-W6 | The app's close rule had no 64-item case (low) | Same rule as the host |
| B-W7 | Install could move the user to Live after they left Settings (low) | A mounted check |
| B-W8 | No test failed if Install lost `typeOnly` (the command would run) | `installView` with its test; phase B `H-install-herdr` checks that nothing ran |
| B-E1 | Phase B, first run of both halves together: no product defect. The harness hid the real herdr as "not permitted", which made `canInstall` false on this Mac | The sandbox now answers "no such file" for the real herdr paths |

## 13. Implementation notes (differences from the text above)

| Where | Built | Why |
| --- | --- | --- |
| `closeItem` on a main checkout (section 4) | Refused only when another item of the group is a linked worktree (or the group is at its 64 items, where the list may be cut) | The lab showed two main checkouts in one group (a second workspace whose shell moved into the same repository); "another item" would make both impossible to close. The app uses the same rule |
| Start failure text | "herdr did not start; run `herdr server` in a terminal on this computer to see why" | The broker's fixed texts say "this computer" |
| A second `startServer` while the first child lives | `host_unavailable` with the start-failure text | That text is the right hint |
| Wait for ready | 5 s for each start try, and 500 ms more after a child ends; on Linux the systemd try and the plain try can add up to about 10 s | Two tries |
| `canInstall` | Also needs x86_64 or aarch64 | herdr's installer has builds only for those |
| `PATCH` on a provider that is not installed | `503 host_unavailable` | The same answer as every other route |
| `canInstall`, `canStart` | The validator refuses `canInstall` outside `notInstalled` and `canStart` outside `notRunning` | The schema text |
| A1 bounds | At most 64 workspaces without a worktree record and 32 pane folders each are checked in one poll | Rule 11 |
| The log | A herdr error code that is not `[a-z0-9_]{1,64}` is logged as `other`, no answer as `no_answer` | No herdr text in the log |
| The busy text | Manage: "herdr is busy with another change; try again"; create keeps its own | Each route's own words |
| "Look again now" | Set by a settings read while the provider is `notInstalled` or `notRunning`, at most once per 2 s; the app's **Check again** on the card reads the settings | The card turns within about 2 s |
| Phone: a peer's manage error | The app shows "That computer could not do this." for every peer error (its rule against a peer's own words), so the start hint shows for the home computer only | An existing rule of the app |
| Phone: settings while herdr changes | The app reads a computer's settings again when its provider read shows another state than the settings | `canStart` comes from the settings, which the app keeps for a visit |
| Phone: no `targetId` | "The tab is made. Update herdr to open it from the phone." / "The workspace is made. Update herdr to open it from the phone." | The design had no text for it |
| Phone: install | `runFirst` with `typeOnly`: the existing run-first path sends the text and skips Enter; control rules are unchanged (it types only when this phone has control, never after a reconnect) | One input path (rules 9 and 10) |
| Two workspaces at one repository's main checkout, herdr 0.9.3 | Neither can be closed from the phone: herdr answers `workspace_group_close_required` for each and wants `close_group`, which Termivoa never sends (a close ends one workspace). The answer is `operation_conflict` ("close this project's worktrees first"); the user closes them in herdr. herdr 0.8.2 closes each alone | Lab, 2026-10-04 (0.9.3 release binary, isolated). Rare: it needs a second workspace whose shell moved into a repository that already has a workspace, and a worktree made from it |
| Add a folder (after the merge, on the owner's request) | The **New workspace** sheet has **Add a folder**: a new session on that computer with `pterm project add ` typed and not run (`runFirst` with `typeOnly`, the Install herdr path), and **Check again** | The Mac had one project, "Home folder", and the phone sends no path (M2). The user types the folder in a terminal on that computer |
| `closeTab` (A3b) | Added during the build on the owner's request; the lab ran `tab.close` on 0.8.2 and 0.9.3 (section 1) | The owner asked to close tabs too |

## 14. Second round (proposal, protocol revision 6): an agent in a new tab, closed worktrees, the menu in the terminal

Status: **proposed; not merged.** The owner asked for the herdr features that were still missing, each drawn, built on a branch and opened as a pull request for his review. No new route: the manage route gets two optional fields for `newTab`, one new action and one new list. `features.providerAgentTab` is 6; the app shows these controls only for a computer at revision 6.

### Lab facts (herdr 0.8.2, isolated, 2026-10-05)

| Question | Finding |
| --- | --- |
| `worktree.list {workspace_id}` | Answers every worktree of that repository: `branch`, `path`, `is_linked_worktree`, `is_prunable`, `is_bare`, `is_detached`, and `open_workspace_id` only for one that has a workspace. A linked worktree whose workspace was closed is in the list without `open_workspace_id`. |
| `worktree.open {workspace_id, branch, focus:false}` | Opens a workspace for that existing worktree again and answers `root_pane` and `already_open` (true, with no new workspace, when it was open). |
| `agent.start {name, kind, pane_id, timeout_ms}` on the pane of a new tab | The same call that create uses on a new worktree's pane (ADR 0008, section 1). |

### B1. New tab with an agent

`newTab` may carry `agent` (one of the provider's `agentKinds`, as in create) and, with `agent`, `prompt` (the create request's rules: no ESC, C0 except tab and line feed, DEL or C1). The host makes the tab (`tab.create`), then starts the agent in its pane and sends the prompt with the code that create uses (`agent.start` 20 s, `agent.prompt` 5 s), and answers `agentStarted` and, when false, `agentProblem` (`agentNotReady`, `agentFailed`, `promptFailed`); the tab exists either way. A `newTab` with `agent` counts against the create limit of the device (3 per minute) as well as the manage limit. Without `agent` nothing changes.

Phone: **New tab** in the item menu shows the choices **Shell** and one for each agent kind of that computer. **Shell** makes the tab at once (as today). An agent shows **First prompt (optional)** (the create sheet's field and checks) and **Start**. What did not start is said once over the terminal, with create's texts.

### B2. Open a closed worktree again

**Close workspace** on a worktree keeps its folder, and until now nothing on the phone could open it again. The host asks herdr for each git group's worktrees (`worktree.list` with the group's main workspace; at most 4 groups in one poll turn, each group at most every 10 s, 1 s limit, results kept between polls) and puts into `HTTPProviderGroup.closed` (at most 16) the branch names of linked worktrees that have no workspace, are not prunable, bare or detached, and whose name passes the create name pattern (`providerWorktreeName`); any other is left out. Paths never leave the host.

New action `openItem` with `groupId` and `worktree`: under the create lock the host reads a fresh snapshot and a fresh `worktree.list` for that group; the name must be in the fresh closed list (so the phone can only name what herdr itself lists); the group must have fewer than 64 items; then `worktree.open {workspace_id: <the group's main workspace>, branch, focus:false}`. The answer is `itemId` and, when this herdr can attach a terminal without an agent, `targetId` (after `settle`). `already_open` is a success. Errors: `session_missing` (the group or the name is gone), `invalid_request` (the group is full), else `host_unavailable`.

Phone: under a project's items, each closed worktree is a dimmed row with its name, the word **CLOSED** and **Open**. A tap opens it and goes to its terminal. Closed rows are not counted in the status line or the tab count.

### B3. The menu inside a herdr terminal

Live for a herdr terminal gets the same **⋯** button (in its chip row) that opens the item menu of that terminal's workspace: **New tab**, the tabs with **Close tab**, **Close workspace**, **Remove worktree**. After an action that ends the terminal on screen (its tab or its workspace), the app goes back to the herdr tab. Phone only.

### Security

Nothing new in kind. B1 is create's agent start in an existing workspace: the same kind check, prompt filter and limits. B2 names a worktree by a branch name from herdr's own list, checked again under the lock, and never a path; it deletes nothing. B3 reuses reviewed actions. `worktree.list` runs in the poll loop with a 1 s limit and at most 4 calls per turn (rule 11); its text and paths never leave the host.

### Tests

Unit tests for each rule above (host and phone); the fake herdr learns `worktree.list` and `worktree.open`; the lab test covers B1 without a real agent program (the start fails cleanly: `agentFailed`), and B2 on real herdr; Phase B: `H-agent-tab`, `H-closed`, `H-live-menu`.

### Build notes (differences from the text of section 14)

| Where | Built | Why |
| --- | --- | --- |
| Agent name (also changes create) | An agent's herdr name is its kind and pane, such as `claude-w3-p2`, cut to 32 characters. Create sent the kind alone | herdr takes each name once: a second agent of one kind answered `agent_name_taken` (lab, herdr 0.8.2) |
| "The start fails cleanly" (Tests) | With no agent program, herdr 0.8.2 answers "started" at once (`launch_pending`) and the prompt then fails: the lab sees `agentStarted: true`, `agentProblem: promptFailed` | Lab |
| A second `openItem` for one name | `session_missing`: the name is then not in the fresh closed list. `already_open` is a success only when another client opened it between the list and the open | The spec's own rule |
| When lists are asked again | Also when the set of workspaces changes (still at most 4 in one turn), so an opened worktree does not show as an item and as closed for 10 s. A group seen for the first time gets its list one poll later; a list that fails is empty until the next try | The 10 s rule alone left a stale row |
| The 4 calls of one turn | Run at the same time, so a hung herdr costs a turn about 1 s | Rule 11 |
| `openItem`'s own `worktree.list` | The 15 s manage limit; it checks the whole filtered list, not the 16 names of the wire | It runs under the create lock, not in the poll |
| The name rule | Create's Go rule (`ValidateProviderBranch`): the pattern, at most 4 parts, no `.lock` ending | One rule for names the phone sends |
| `targetId` of an agent tab | Given when herdr can attach a terminal without an agent, or the agent started | A pane with an agent can always be attached |
| A git worktree made by hand | Counts as closed when it passes the filter | herdr lists it |
| Phone: the wait for an agent tab | 45 s (create's wait), else 30 s | The host needs up to 20 s and 5 s after the tab |
| Phone: **Shell** in the chooser | Takes no tap in the first 400 ms | A double tap on **New tab** would make a tab |
| Phone: "the tab on screen" | Known by its tab label; two tabs with one label count as one | The list has no tab ID |
| Phone: texts | "The tab is ready, but the agent did not start."; the prompt problem shows also when herdr said the agent started | A tab is not a worktree; herdr can say "started" and then refuse the prompt |
| Phone: the **⋯** in a herdr terminal | Shows for a computer at revision 5 (it reuses those actions); the chooser in it needs revision 6 | The gates of each part |

### Review of the built branch (second round)

One independent review of both commits (security and correctness) and the end-to-end run of both halves. No critical or high finding; no security finding; the end-to-end run found no product defect.

| # | Finding | Resolution |
| --- | --- | --- |
| R2-1 | The phone's wait for an agent tab (45 s) equals the host's worst case, and `openItem` could take 34 s against a 30 s wait: the timeout showed as a network failure (the Offline screen with one computer), and a second tap could make a second tab and agent (medium) | A wait that ran out says "This takes longer than the phone waits. Look at the list before you try again." and reads the list; `openItem`'s own `worktree.list` has a 5 s limit, so its worst case is 24 s |
| R2-2 | On a herdr that cannot attach a terminal without an agent, an agent that waits at a question gave no `targetId`, and the phone dropped the agent problem (low to medium) | `targetId` also for `agentNotReady` (as in create); an agent problem with no terminal is said in the sheet |
| R2-3 | A workspace that comes and goes every turn kept the same four groups in front and the others never got a list (low) | Lists are marked old, not dropped; the longest wait goes first; an open item wins over an old closed name |
| R2-4 | A long kind or pane ID cut the pane part from the agent name, so two panes could share a name (low) | The cut takes from the kind and keeps the pane's end (`TestAgentNameKeepsThePane`) |
| R2-5 | Two tabs with one label: closing the other one from a terminal sends the user to the herdr tab though their terminal lives (low) | Not changed; the wire has no tab ID |
| R2-6 | `open_workspace_id` is proven on herdr 0.8.2 only (low) | An open item now wins over a closed name of the same name; the lab on 0.9.3 is open |
| R2-7 | Limit order: an agent tab that the create limit refuses still takes a manage unit (low) | As create does; written here |

### Found in this build, not part of it (rule 21)

- A first prompt may always fail on herdr 0.8.2, in create too: `agent.start` answers before the agent is ready (`launch_pending: true`), and `agent.prompt` right after answers `agent_not_ready`. Seen with a stand-in program, not with a real agent.
- `agentStarted: true` does not prove that the agent runs: herdr 0.8.2 says "started" when the program is missing.
- A herdr terminal outside git shows "this herdr terminal's folder cannot be read" right after it opens, though nobody tapped Changes: the away recap in Live asks for the changes, and the host refuses a folder that is not a checkout. It is on `main`; an agent tab's notice sits over it.
- The app offers the agent kinds of its last settings read; the host offers a kind only while an agent of it runs. A kind that left still shows, and **Start** answers "herdr cannot start that agent here". The **+** sheet has the same rule.
- The `terminal attach --help` probe has a 3 s limit and its result is kept for that herdr version: one slow first probe (seen once under a full parallel test run) turns off tabs without an agent until herdr's version changes or the broker restarts.

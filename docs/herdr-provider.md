# herdr on your phone: design

Status: **accepted for build, version 2 (2026-10-04).** ADR [0008](decisions/0008-herdr-provider.md). The owner approved the product in Paper (file `termivoa`, artboards H1–H6) and delegated the approval of this feature in writing to the coordinating agent ("approve if all looks good, then ship"). Under AGENTS.md rule 24 the coordinator approves after the independent security and feasibility reviews; section 20 records every finding and its resolution. The protocol types are in `protocol/v1` and generated; no route serves them yet.

What it does: a **herdr** tab in Sessions shows each computer's herdr projects (fold and unfold), their worktrees, and agent state words with a roll-up; a tap opens that one real terminal full screen in the existing Live screen; the workspace's tabs are chips on top; the phone buzzes when an agent is blocked or finished; **Settings → Integrations** turns it on or off per computer. M2 adds **+** on a project row: a new worktree and an agent in it.

Names below: a **provider** is a terminal workspace tool on a computer (herdr now; maybe tmux or zellij later). A **group** is a project row, an **item** is a row under it (a herdr workspace: the main checkout or a linked worktree), a **target** is one terminal the phone can open (a herdr pane). A **provider session** is the in-memory broker session that shows a target. Demo names (`shop`, `silver-meadow`) stand for real ones.

**Upgrade:** no SQLite schema change. An upgrade keeps pairing, devices, computers and push subscriptions; nothing is paired again.

Code is named by function, not by line number.

## 1. Facts this design rests on (lab evidence)

Every row was run on 2026-10-04 against an **isolated** herdr: `HOME=<tmp> herdr --session termivoa-lab server` (0.8.2, the installed version) and the 0.9.3 release binary in `/tmp` (never installed, never on `PATH`) with its own `HOME` and session. The owner's default session was only read (`herdr api snapshot`, `herdr status server --json`, `herdr api schema --json`, help, `herdr integration status`). Lab sessions were stopped with `herdr session stop` and their folders deleted. Agents were faked with `herdr pane report-agent`; one `pi` agent started in a lab worktree with no prompt.

| Question | Finding |
| --- | --- |
| Detect herdr | `herdr status server --json` gives `running`, `version`, `protocol`, `socket`, `compatible`. The socket's `ping` also returns `version` and `protocol`. 0.8.2 = protocol 20, 0.9.3 = protocol 22. Homebrew installs `/opt/homebrew/bin/herdr`, a symlink into `/opt/homebrew/Cellar/herdr/<version>/bin/herdr`. |
| Socket | Newline-delimited JSON: `{"id","method","params"}` → `{"id","result"}` or `{"id","error":{"code","message"}}`, **one request per connection** (herdr closes the socket after each reply). Default `~/.config/herdr/herdr.sock`, mode `0600`, folder `0755`, owned by the user; named sessions use `~/.config/herdr/sessions/<name>/herdr.sock`. An unknown method gives `invalid_request` with the list of methods. |
| Snapshot | `session.snapshot` → `result.snapshot` with `protocol`, `version`, `workspaces`, `tabs`, `panes`, `layouts`, `agents`, `focused_*`. Same field set in 0.8.2 and 0.9.3 (titles and `agent_session` optional). IDs: workspace `w2`, tab `w2:t1`, pane `w2:p4`, terminal `term_…`. |
| Worktree grouping | A workspace in git has `worktree {repo_key, repo_name, repo_root, checkout_path, is_linked_worktree}`. The main checkout has `is_linked_worktree: false`; linked worktrees share its `repo_key`. A workspace outside git has no `worktree`. Two workspaces can have the same label. |
| States | Agents and panes: `idle`, `working`, `blocked`, `done`, `unknown`; panes without an agent say `unknown`. `idle` reported to a headless server became `done` (unseen); once a herdr UI focused the tab it was `idle`. `state_change_seq` grows with each change. Terminal titles often hold the task text a person typed. |
| Cost | Socket `session.snapshot` of the owner's 12 workspaces (32 KB): p50 2.0 ms, p95 5.4 ms. `herdr api snapshot` as a process: about 6 ms. |
| Events | `pane.agent_status_changed` needs a `pane_id` (one subscription per pane); a status change fires no workspace, tab or pane event; 0.8.2 replays old lifecycle events on subscribe, 0.9.3 does not; the docs add `events_lost`. |
| Attach and size | `herdr agent attach <pane>` in a 45×20 PTY made the pane's real PTY 45×20 (`stty size` in the pane), also while a herdr UI (150×40) showed that tab; laptop typing reached the pane but did not take the size back. When the attach ended (`ctrl+b q`, exit 0, or killed), the pane went back to the herdr UI's size (123×39). With no herdr UI the pane kept the phone size until a UI showed it. 0.9.3: the same. |
| `layouts` rect vs real size | **Not equal.** With a herdr UI, the real PTY is the rect minus borders: one pane 124×39 → 123×39; two panes 62×39 → 59×37, 37×29 → 34×27, 93×51 → 90×49 (0.8.2 and 0.9.3 alike). Headless, the rect (47×39) has nothing to do with the PTY (93×39), and after the UI left, the PTY kept the UI's last size while the rect showed the headless value. herdr exposes no real size (`pane get` has only `viewport_rows`). |
| Zero size | An attach started or resized to 0×0: 0.8.2 shrank the pane to a few columns; 0.9.3 kept the UI size at start, but the attach exited (status 1 after a later resize, 0 after a resize to 0×0). Not usable. |
| Attach prefix | The attach client's only prefix command is `ctrl+b q` (detach). `ctrl+b` followed by any other key (tried `? h d w o s x z n p c`) drops the `ctrl+b` and sends the key; `ctrl+b ctrl+b` sends one literal `ctrl+b`. The prefix is configurable in herdr's config (default `ctrl+b`). The attach output starts with `ESC[?1049h` (alternate screen). |
| Attach rules | Attach to a pane without an agent on 0.8.2: `agent_not_found`, exit 1. On 0.9.3 `herdr terminal attach <terminal_id>` attaches any pane. A second attach to the same terminal: "already has an attached client; retry with --takeover", exit 1. The attach outlives the agent, and ends with exit 1 when the pane closes. A phone attach does not mark `done` as seen. |
| Create (M2) | `worktree.create {workspace_id, branch, focus:false}` made `~/.herdr/worktrees/<repo>/<branch>`, a new workspace (label = branch) and its shell pane (`result.root_pane.pane_id`). Existing path: `worktree_create_failed` with git's text (it names local paths). `agent.start {name, kind, pane_id, timeout_ms}` took 3.9 s; a bad name: `invalid_agent_name`. |
| Agent kinds | `agent start --kind` accepts 22 kinds on 0.8.2 and 24 on 0.9.3 (`[possible values: …]` in its help). `server.agent_manifests` lists detection manifests, a different set (no `mastracode`, `omp`). `herdr integration status` is text only. |

## 2. Goals and non-goals

Goals: herdr tab with groups, items, state words, roll-up and a count; the All tab section of H1; open one target in Live with tab chips; push on blocked and finished; Settings → Integrations; nothing changes for a computer without herdr; no re-pairing. M2: create a worktree and start an agent with an optional first prompt.

Non-goals: split panes, the mouse, prefix keys (never); herdr layout changes, renames, closing anything; named herdr sessions (default session only); Windows; events; terminal titles or screen text in the list or in pushes; other providers (the wire is generic; none is built).

## 3. The provider seam

The seam is the **wire contract** (section 4): the phone sees providers, groups, items, targets and state words, never herdr names it must understand. On the host, all herdr code sits in one package, and the broker calls a concrete `*herdr.Provider` through five methods. There is no Go interface until a second provider exists (one implementation; extract it then).

```go
// internal/herdr
func New(config Config, onChange func(Change)) *Provider // no defaults: config names every path
func (p *Provider) Run(ctx context.Context)                              // discovery + 2 s poll loop
func (p *Provider) Info() protocol.HTTPProviderInfo                      // detection + settings
func (p *Provider) Snapshot() protocol.HTTPProviderSnapshotResponse      // cached; never blocks
func (p *Provider) Attach(targetID string) (Attach, error)               // checked against the cache
func (p *Provider) Create(ctx context.Context, request protocol.HTTPProviderCreateRequest) (protocol.HTTPProviderCreateResponse, error) // M2

type Config struct {
	BinaryPaths []string // fixed paths tried before PathEnv; set by cmd/pterm, empty in tests
	PathEnv     string   // the PATH searched after them, and PATH of every herdr child
	Home        string   // for ~/.config/herdr; set by cmd/pterm, a temp dir in tests
	Session     string   // empty (the default session); only the opt-in lab test names one
	Enabled     func() bool
}

type Attach struct {
	Argv      []string // absolute herdr path first; never a shell
	Env       []string // fixed list, section 11
	Directory string   // the workspace checkout path (host only; Changes and the recap use it)
	Title     string   // "herdr · shop · silver-meadow"
	Key       string   // pane ID + terminal ID: one provider session per key
}

type Change struct{ Key, Group, Item, Agent, From, To string; Worked time.Duration }
```

| Package | Holds |
| --- | --- |
| `internal/herdr` (new) | finding the binary, `status server --json`, the socket client (`ping`, `session.snapshot`; M2 `worktree.create`, `agent.start`, `agent.prompt`), socket checks, the poll loop, mapping (section 5), changes, attach argv, capability probes, Unicode cleaning of every herdr string |
| `internal/broker/providers.go` (new) | wiring, settings (`settings` table), `providers` in the settings response, gateway RPC handlers, the per-device limiter, provider sessions (section 6), push from changes |
| `internal/gateway` | the routes; no herdr knowledge |
| `cmd/pterm` | the production `Config` (fixed paths, the user's home) |

## 4. Wire contract

The bodies are in `protocol/v1/http-api.schema.json` (`HTTPProvider*`), generated into `internal/protocol/generated.go` and `web/src/generated/protocol.ts`. The stored session schema and `sessionRecord` do not change.

### 4.1 Routes

All under `admitAPI` (exact own or bound `Origin`, CORS only for bound origins, `no-cors` refused), cookie authentication through the broker's `authorize` (own or bound origin, the same as `/api/v1/sessions`), `Cache-Control: no-store`. Mutations go through `handler.mutation` (`X-Pterm-Request: 1`, JSON, at most 64 KiB, unknown fields refused).

| Method and path | Request | Success | Notes |
| --- | --- | --- | --- |
| `GET /api/v1/settings` | – | `HTTPSettingsResponse` with `providers?: HTTPProviderInfo[]` | Absent or empty: no provider here, no tab |
| `GET /api/v1/providers/{provider}` | – | `200 HTTPProviderSnapshotResponse` | From the cache; body at most 256 KiB |
| `POST /api/v1/providers/{provider}/open` | `HTTPProviderOpenRequest {targetId, columns, rows}` | `200 HTTPProviderOpenResponse {session, reused}` | Opens or reuses the provider session |
| `PATCH /api/v1/providers/{provider}` | `HTTPProviderUpdateRequest {enabled?, notify?}` | `200 HTTPProviderInfo` | Settings → Integrations; this computer, all devices |
| `POST /api/v1/providers/{provider}/create` (M2) | `HTTPProviderCreateRequest {groupId, name, agent?, prompt?}` | `200 HTTPProviderCreateResponse {itemId, targetId, agentStarted, problem?}` | The phone waits up to 45 s |

`{provider}` must match `^[a-z][a-z0-9-]{0,31}$` and name an installed provider. Gateway RPC messages (host-internal, `gateway-rpc.schema.json`): `gateway.providers.get`, `.open`, `.update`, `.create`, each with the credential payload and forwarded origin, like `gateway.sessions.list`. The provider session is attached over `/api/v1/ws` with today's `session.attach` (by session ID and generation).

### 4.2 Bodies

`HTTPProviderInfo`: `id`, `name` (display, ≤32), `state` (`ready` | `notRunning` | `unsupported` | `off`), `version?`, `enabled`, `notify` (subset of `blocked`, `done`), `canCreate`, `agentKinds` (≤16; kinds herdr can start that already run in this herdr, most used first).

`HTTPProviderSnapshotResponse`: `provider`, `state`, `groups` (≤64), `truncated?` (item limits or the 256 KiB body budget hit: the host drops whole groups from the end, least urgent first, until it fits).

`HTTPProviderGroup`: `id`, `name`, `state`, `canCreate`, `items` (≤64).

`HTTPProviderItem`: `id`, `name`, `state`, `stateSince?` (RFC 3339, when the host first saw this state), `openTarget?`, `targets` (≤32).

`HTTPProviderTarget`: `id`, `label` (tab label), `state`, `agent?` (kind), `canOpen`. No title (section 11, S5).

`HTTPProviderOpenRequest`: `targetId`, `columns` (20–400), `rows` (5–200): the phone's fitted grid.

`HTTPProviderSession`: `id`, `generation` (UUIDs, for `session.attach`), `title`, `state` (session lifecycle), `columns`, `rows`, `provider`, `targetId`. Never in `GET /api/v1/sessions`.

State words (`providerItemState`) are a pattern, not an enum: known words `blocked`, `done`, `working`, `idle`, `unknown`, `none`. The app shows any other word as `unknown`, so a later provider can add a word without a `minRevision` raise.

IDs are opaque, issued by the host, never paths:

| ID | herdr value | Pattern |
| --- | --- | --- |
| target | `<pane ID>.<terminal ID>`, for example `w8:p1.term_65d0031602b777` | `^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$` |
| item | workspace ID (`w8`) | same |
| group | `g` + the first 12 hex digits of SHA-256 of `repo_key`, or `ws-` + workspace ID outside git | same |

The terminal ID in the target means a target from before a herdr restart never names a different terminal: pane IDs start again at `w1:p1` in a new server.

Example (`GET /api/v1/providers/herdr`):

```json
{"provider":"herdr","state":"ready","groups":[
 {"id":"g3f9a0c2b17e4","name":"shop","state":"blocked","canCreate":true,"items":[
  {"id":"w8","name":"silver-meadow","state":"blocked","stateSince":"2026-10-04T09:51:02Z","openTarget":"w8:p1.term_65d0031602b777",
   "targets":[{"id":"w8:p1.term_65d0031602b777","label":"1","state":"blocked","agent":"claude","canOpen":true},
              {"id":"w8:p3.term_65d0031602b7a1","label":"logs","state":"none","canOpen":false}]}]}]}
```

### 4.3 Errors

Existing codes only (no enum widens); HTTP status from the gateway's code mapping (`brokerStatus`). Messages are fixed Termivoa text, **never herdr's or git's text**.

| Case | Code | Status |
| --- | --- | --- |
| Unknown `{provider}`, bad body, bad name, kind not in `agentKinds`, control characters in a prompt | `invalid_request` | 400 |
| Provider off, not running or unsupported (open, create) | `host_unavailable` | 503 |
| Target or group not in the latest snapshot, or `canOpen` false | `session_missing` | 404 |
| 4 provider sessions in use, or the per-device limit | `limit_exceeded` | 429 |
| Create: group not in git | `not_a_repository` | as today |
| Create: worktree or branch path exists | `worktree_exists` | as today |
| Create: any other herdr or git failure | `worktree_failed` ("herdr could not make the worktree.") | as today |

An agent that does not start is not an error: create returns `agentStarted: false` with `problem` (`agentNotReady`, `agentFailed`, `promptFailed`); the worktree exists.

### 4.4 Push payload

```json
{"title":"herdr · shop · silver-meadow","body":"claude needs you. Tap to answer.","kind":"waiting","provider":"herdr","target":"w8:p1.term_65d0031602b777"}
{"title":"herdr · shop · lucky-valley","body":"claude finished after 12 min.","kind":"done","provider":"herdr","target":"w9:p1.term_65d0031602b7c4"}
```

- Fixed format. Title: `herdr · <group> · <item>`, one name when they are equal (a workspace outside git). Body: `<agent> needs you. Tap to answer.` or `<agent> finished after <N> min.` (whole minutes of observed work; `<agent> finished.` under one minute). Names: Unicode categories Cc and Cf removed (bidi controls and invisible characters), spaces collapsed, cut to 40 characters. Never a title, screen text, path or prompt.
- `kind` keeps today's words (`waiting`, `done`). Exactly one of `sessionId` or (`provider`, `target`).
- `sw.js` accepts this shape (provider and target checked by pattern), sets tag `<peer>p:<provider>:<target>:<kind>`, and on a tap opens `/p/<provider>/<encodeURIComponent(target)>` (home) or `/c/<host>/p/<provider>/<…>` (peer), and marks the open as a tap (section 6, rule 9).

### 4.5 Protocol revision

Revision 4 when the routes ship: the routes, `HTTPSettingsResponse.providers`, the `HTTPProvider*` bodies. `minRevision` stays 1. The app calls provider routes only on a computer with `protocolRevision ≥ 4`; it reads `state` from the snapshot (not from the cached settings). `http-api.md` gets the routes and the revision row with the routes, not before.

## 5. State mapping and grouping (herdr)

| herdr | Phone word | Shown as |
| --- | --- | --- |
| agent `blocked` | `blocked` | BLOCKED (amber) |
| agent `done` | `done` | DONE |
| agent `working` | `working` | WORKING |
| agent `idle` | `idle` | IDLE |
| agent `unknown` | `unknown` | no word |
| no agent in the pane or tab | `none` | nothing |
| any other word (newer herdr) | `unknown` | no word |

Roll-up (item from its targets, group from its items): most urgent wins, in the order `blocked`, `done`, `working`, `idle`, `unknown`, `none`. The tab count is the number of targets that are `blocked` or `done`, over all computers. The host computes roll-ups from agents itself (one rule).

Grouping:
- A workspace with `worktree` belongs to the group of its `repo_key`; the group name is `repo_name`. Items: the main checkout first, then linked worktrees by herdr's `number`.
- A workspace without `worktree` is its own group with one item. The phone shows such a group as one row; when two rows have the same name, it adds herdr's workspace number ("notes · 5").
- Targets of an item, by tab number: each agent pane of the tab (label = tab label), and for a tab with no agent one target for the tab's focused pane (`layouts[].focused_pane_id`), `state: none`, `canOpen` only when this herdr can attach any pane (section 13).
- `openTarget`: the most urgent agent target (ties: lowest tab number), else the active tab's target when it can open, else absent.
- Every herdr string (labels, names, kinds) is cleaned on the host: Cc and Cf removed, cut to the schema length. Paths (`cwd`, `repo_root`, `checkout_path`, `repo_key`) never leave the host.

## 6. Opening a terminal

### Options

| | A. Broker session running herdr's attach client (**chosen**) | B. `pane read` + `send-keys` | C. Full herdr UI client in a broker PTY |
| --- | --- | --- | --- |
| Real terminal on the phone | Yes: Live, keys, compose, answer cards | No: text snapshots, no cursor or colour | Yes, but the whole herdr UI in 45 columns |
| Input path | The broker's one writer (rule 10) | A second input path | One writer |
| Prefix keys, mouse | Not needed | Not needed | Needed (ruled out) |
| Laptop pane size | Phone size while the session lives | Untouched | Phone size; 0.8.2 focus is shared, so it moves the laptop's view |
| herdr 0.8.2 / 0.9+ | Agent panes / any pane | Yes / yes | Yes / yes |

Also not chosen: 0.9's `terminal session control` JSON stream (per-viewer size, but absent in 0.8.2 and a second terminal pipeline). Revisit when 0.8 support ends; it is also the only way to watch without resizing.

### Size: what is possible

herdr gives the pane the attach client's size from the moment it attaches, gives it back to the herdr UI when the attach ends, and exposes no real PTY size (section 1). So the broker cannot "restore" a size: the `layouts` rect is not the real size (borders, and stale when headless), and a 0×0 size breaks the pane or ends the attach. The design therefore **ties the pane's phone size to the life of the provider session** and keeps that life short.

### Rules for provider sessions

1. **In memory only.** A registry entry flagged as a provider session: not written to `session_metadata` or `mutation_receipts`, no final status persisted, not in `GET /api/v1/sessions`, `pterm ls`, the Wall or New session. `Notify: nil`, so Termivoa's own `waiting` and `done` pushes are off; the screen parser still runs, so answer cards work. Ended entries are dropped.
2. **Argv**: `[<herdr>, "agent", "attach", <paneId>]`; for a target without an agent when herdr can attach any pane: `[<herdr>, "terminal", "attach", <terminalId>]`. Never `--takeover`. Directory: the workspace checkout path, so Changes and the away recap work. Env: section 11.
3. **Start size**: the phone's `columns` × `rows` from the open request. herdr makes the pane that size on the laptop until the session ends. Only the controller resizes after that (unchanged lease rules).
4. **One per target**: key = pane ID + terminal ID. `open` runs under one lock per key: two taps or two phones get the same session (`reused: true`).
5. **Cap**: at most 4 provider sessions, **not** counted in `DefaultMaxLiveSessions` (8), so herdr never blocks a normal session. No eviction: a 5th open gets `limit_exceeded`.
6. **End**: a provider session with no viewer for 10 s ends (SIGHUP to the attach client), and herdr gives the pane back to the herdr UI's size. A just-opened session counts as viewed for its first 10 s (the phone may not have attached yet). It also ends when its key leaves the snapshot or herdr is rediscovered. The phone closes its stream when the page is hidden and opens again when it comes back, so a phone in a pocket does not keep the laptop squeezed.
7. **Input**: the broker doubles each `ctrl+b` byte (`0x02`) in input to a provider session, so the attach client passes one literal `ctrl+b` to the agent and the phone can never send `ctrl+b q` or any prefix command (proven with herdr's default prefix; a custom prefix is Q1).
8. **Live features**: History (the attach client uses the alternate screen), rename, terminate, uploads, tasks, preview, worktree and "start like this" are hidden in Live and refused by the host for provider sessions. Keys, type, compose, snippets, answer cards, Changes and take control stay.
9. **Address**: Live for a provider session is `/p/<provider>/<encodeURIComponent(target)>` (peer: `/c/<host>/p/…`). A tap on a row or chip in the app, or a notification tap (the service worker tells the page by `postMessage`, never by the URL), calls `open` at once. Any other arrival (reload, a typed or pasted address) shows the target's names and an **Open** button first. Opening never takes control: the phone starts read-only with today's **Take control**. A target that is gone lands on the herdr tab with "That terminal is gone."
10. **Exit**: the attach client ends when the pane closes (exit 1) or when another client holds the terminal (exit 1; herdr's reason shows in the last frame). Live shows the ended state with **Open again**; the herdr row stays.

Honest consequence, shown in Live under the chips: "mac shows this terminal at phone size while it is open here." The laptop user can still type in it.

## 7. Notifications

Per agent target, from each 2 s poll (previous → new state), keyed by pane ID + terminal ID:

- **Blocked**: `blocked` in two polls in a row after it was not → one push. Not again until it leaves `blocked`.
- **Finished**: after the host saw the agent `working` for at least 20 s in this turn (`agentDoneAfter`), it turns `done` **or** `idle` → one push. (`idle` counts too: herdr says `idle` when a herdr UI shows that tab, also when nobody sits at the laptop.)
- **Baseline**: the first snapshot after the broker starts, or after herdr is rediscovered (all per-target state dropped), sets states without pushes.
- Respect the provider's `enabled` and `notify`, existing subscriptions, today's TTL and urgency of `waiting` and `done`. Open viewers do not suppress pushes (today's rule).
- **Own budget**: provider pushes have their own per-minute limit (6) and coalesce key (`provider:<target>:<kind>`, 1 minute), separate from session pushes, so herdr can never use up the budget of a normal session's push.
- Each computer pushes its own herdr changes through its own subscriptions (peer push as today).

## 8. Settings → Integrations

- One section, one row per computer that lists a provider: "herdr on mac · 0.8.2", a switch (**On**), and two switches **Buzz when blocked** and **Buzz when finished**. State words: "Running", "herdr is not running", "Update herdr" (`unsupported`), "Off".
- Stored on that computer in the existing key/value `settings` table (`key`, `value`, `updated_at_ms`; `Store.GetSetting`, `Store.PutSetting`): `provider.herdr.enabled` (`1`/`0`), `provider.herdr.notify` (`blocked,done`). Absent means on, both. No schema change.
- Off: the broker stops polling, ends provider sessions, reports `state: off`; the herdr tab hides that computer.

## 9. Multi-computer

- The phone reads `providers` from each computer's settings. A computer without a provider shows nothing.
- The herdr list is one more read per computer (`GET /api/v1/providers/herdr`) through the existing scheduler, every 5 s while Sessions is visible, separate from the sessions read: its failure shows only on the herdr part ("herdr on vps · can't reach").
- Groups show the computer label ("shop · on mac"). `open` and `create` go to that computer's origin.
- Peer data is untrusted: lenient validation per item, names as text, IDs by pattern; the app never builds a URL from a name.

## 10. Failure modes

| Failure | What happens |
| --- | --- |
| herdr not installed | No `providers` entry; no tab; the binary lookup runs again every 30 s |
| Installed, server not running | `state: notRunning`; "herdr is not running on mac"; discovery every 30 s |
| Server stops or restarts (update, handoff) | Socket error → `notRunning` within one poll; rediscovery (at most once per 5 s); per-target state dropped; provider sessions ended; new baseline, no push burst |
| Socket hangs | 1 s deadline per call; after 3 misses in a row `notRunning` |
| Protocol below 20, or a snapshot without its arrays | `state: unsupported`, "Update herdr on mac" |
| Newer herdr, unknown fields or words | Ignored; unknown words → `unknown`; a record without a required ID is dropped |
| Target gone between list and open | `session_missing`; the list refreshes |
| Another direct attach holds the terminal | The session ends at once with herdr's reason in the frame |
| Pane closed while viewing | Attach exits; Live shows ended |
| Phone leaves | Stream closed on hide; 10 s later the session ends and the laptop gets its size back |
| Broker restarts | Provider sessions are gone (memory only); phones open again |
| Cap reached | `limit_exceeded` |
| Create: git or herdr error | `worktree_exists` or `worktree_failed`, fixed text |
| Create: phone disconnects or a step times out | Create runs in a broker-owned context, not the request's; after a timeout the host reads the snapshot again and answers with what exists (worktree made, agent started or not) |
| State flapping | Two-poll rule for blocked, once per change, own push budget |
| Very large herdr session | Item caps, 4 MiB socket line, 256 KiB body budget, `truncated: true` |
| Named herdr sessions, Windows | Not shown in M1 |

## 11. Security

Trust statement: a paired phone can already run any command in a Termivoa session, including `herdr agent send-keys`. This feature gives it no new authority on the computer. New are: herdr names must not reach a caller that is not authorized; terminal text must not reach a push; herdr being slow must not hurt Termivoa; nothing a phone sends may reach a shell; and tests must never reach the owner's real herdr.

| # | Threat | Mitigation |
| --- | --- | --- |
| S1 | A page or tailnet node reads herdr names | Same `admitAPI` and `authorize` as `/api/v1/sessions`: cookie, exact own or bound `Origin`, CORS only for bound origins, `no-cors` refused, `no-store` |
| S2 | Injection through `targetId`, `groupId`, `name`, `agent`, `prompt` | Patterns in the schema and in Go; target and group must be in the latest snapshot (pane ID and terminal ID both match); `agent` must be in `agentKinds` (herdr's own start kinds); prompt refuses ESC, C0 (except tab, line feed), DEL and C1; exec with argv, never a shell; values go to herdr as JSON over the socket; the only CLI arguments are a pane or terminal ID checked against the snapshot |
| S3 | A fake herdr binary or socket | Binary: `Config.BinaryPaths` first, then `PATH`, with the ownership and mode checks of `tailnet.FindBinary` (one shared exported check). Socket: `lstat` shows a socket owned by the current user, no group or other access, in a folder owned by the user and not group- or other-writable; after connect, the **peer credential** (`LOCAL_PEERCRED` on macOS, `SO_PEERCRED` on Linux) must be the current user. Same-user malware is out of scope (`docs/threat-model.md`) |
| S4 | Terminal text in a notification | The payload is built only from the group and item names, the agent kind, the state and a duration (section 4.4). Go test: titles and screens of the fake herdr carry a marker that no payload may contain |
| S5 | Hostile text from herdr (a program in a pane can set titles and workspace names) | Titles are never sent at all. Names: Cc and Cf removed on the host, schema `safeDisplayText` and lengths, shown as text; in pushes the fixed `herdr · group · item` format, 40 characters per name |
| S6 | herdr slow or hung slows Termivoa | Reads come from the cache; 1 s socket deadline per poll; CLI calls with time limits, `WaitDelay` and 64 KiB output caps; the Sessions list never waits on herdr; M2 steps 15 s (`worktree.create`), 20 s (`agent.start`), 5 s (`agent.prompt`) in a broker-owned context |
| S7 | Too many processes | 4 provider sessions; 10 s end rule; one create at a time per computer |
| S8 | Kicking the owner's own attach | Never `--takeover` |
| S9 | Path or private text disclosure | No paths and no terminal titles in any body; group IDs are hashes |
| S10 | Abuse of routes | A limiter in the broker keyed by device ID: `open` 20 per minute, `create` 3 per minute, `PATCH` 10 per minute (`limit_exceeded`) |
| S11 | Environment leak to herdr children | Fixed env: `HOME`, `USER`, `LOGNAME`, `LANG`, `PATH` (the broker's lookup path), `TERM=xterm-256color`, `COLORTERM=truecolor`, `HERDR_SOCKET_PATH` |
| S12 | A peer computer lies in its herdr data | Same as any peer response: lenient per-item validation, generic peer errors, text only |
| S13 | Prefix commands from the phone | `0x02` doubled in provider-session input (section 6, rule 7) |
| S14 | A link opens a herdr terminal (and resizes the laptop pane) without intent | `/p/…` opens at once only after an in-app tap or a notification tap reported by the service worker; else an **Open** button; never takes control |
| S15 | Stale target after a herdr restart | Target = pane ID + terminal ID; state dropped on rediscovery |
| S16 | Tests or the e2e harness reach the owner's live herdr | No provider unless `cmd/pterm` builds one: `herdr.New` has no default paths or home; Go tests pass a temp `Home` and empty `BinaryPaths` with a fake; a guard test fails if any test config names the real home or a real herdr path; the harness sandbox denies the real binary and its resolved paths (`/opt/homebrew/bin/herdr`, `/opt/homebrew/Cellar/herdr`, `/usr/local/bin/herdr`, `/usr/local/Cellar/herdr`, `~/.local/bin/herdr`, `~/.cargo/bin/herdr`) and the real `~/.config/herdr`, and gives each stack its own `HOME`; `pterm doctor` in the harness must report the fake's version |

## 12. Performance budget

| Item | Budget | Basis |
| --- | --- | --- |
| Poll | Every 2 s while on and running; p95 ≤ 10 ms per poll including mapping | Measured 2.0 ms p50 / 5.4 ms p95 socket time at 32 KB |
| Host CPU | < 0.5 % of one core for 12 workspaces | Measure in M1 (`pprof`) |
| `GET /providers/herdr` | Broker ≤ 1 ms (cache); body ≤ 256 KiB | Cached |
| `open` | ≤ 300 ms to the session when the target is in the cache | One PTY spawn |
| Phone | One read per computer per 5 s only while Sessions is visible; no change to the Sessions read | Scheduler |
| Live through attach | Key-to-frame p95 within the release gate (30 ms) plus herdr's hop; measured in M1b | `docs/implementation-plan.md` §13 |

## 13. herdr versions

- Supported: protocol 20 (0.8.2) and later. Below: `unsupported`.
- Read leniently: unknown fields ignored, optional fields optional, records without a required ID dropped.
- Probed once per discovered version: `herdr terminal attach --help` exit 0 → targets without an agent can open (0.9+; the host lab found that 0.8.2 also has `terminal attach`, and it attached a pane without an agent, so the probe enables those tabs on 0.8.2 too — section 21); `herdr agent start --help` → the start kinds from its `[possible values: …]` line (if that line is missing, `agentKinds` is empty and M2 offers **No agent** only).
- The opt-in lab test runs against every herdr version the owner installs; CI uses the fake.

## 14. What the drawings need that herdr cannot give

| Drawing | Problem | Closest that works |
| --- | --- | --- |
| H1: the question line under a blocked agent | herdr gives no question text, only the terminal | No question line; the answer card shows it after the tap |
| H1, H3: the task title ("Predicate-based sampling") | The terminal title is set by programs and often holds the task text a person typed; it is not sent | The row shows the agent kind and the tab ("claude · tab 4") |
| H3: "<project> · 2 worktrees" (two workspaces outside git with one name) | Workspaces outside git are not linked in herdr | Two rows, each with that name, and herdr's workspace number when names repeat |
| H2: every tab as a chip | A tab with several panes shows one | One terminal per agent, else the tab's focused pane. Tabs without an agent open with `terminal attach`, which 0.8.2 has too (section 21); only a herdr whose `terminal attach --help` fails gets them as disabled chips ("Update herdr to open this tab") |
| H2: opens fitted to the phone, in **CONTROL** | Fitted: yes (the attach starts at the phone's size). Control: opening never takes control | Opens read-only at the phone's size with **Take control**; one line under the chips: "mac shows this terminal at phone size while it is open here." |
| H4: "<project> · silver-meadow needs you" | Fixed push format | "herdr · shop · silver-meadow" / "claude needs you. Tap to answer." |
| H3, H1: DONE | `done` clears only when seen in the herdr UI; the phone must not move the laptop's focus | DONE stays; the phone dims a DONE row it has opened since (memory only) |
| H6: "from main" | herdr branches from the checkout's current `HEAD` | "herdr makes the folder and a new branch from where shop is now." |
| H6: agent chips | Kinds come from herdr's start kinds that already run there | The same chips for the owner (claude, codex, mastracode); a new user may see only **No agent** |

## 15. Tests

### Unit (Go, `go test ./...`)

- `internal/herdr`: an in-process fake socket server drives the client. Mapping from **synthetic** fixtures (`testdata/`, demo names only) in 0.8.2 and 0.9.3 shapes to the exact generic JSON; unknown fields and words; caps, body budget and `truncated`; Cc/Cf cleaning; socket checks (owner, mode, folder, peer credential); deadlines and the 4 MiB cap; state changes and push rules (two-poll blocked, finished after 20 s via `done` or `idle`, baseline, drop on rediscovery); target IDs with terminal IDs; attach argv per capability; start-kind parsing; no shell (argv tests). The guard test of S16.
- `internal/broker`: routes through `startRPCTestClient` with an injected provider: authorize, bound and wrong origins, patterns, unknown target, the open lock (two parallel opens → one session), cap, 10 s end rule and the 10 s first grace, not in `ListSessions`, `Notify` nil, `0x02` doubling, refused features, settings storage, the per-device limiter, error codes and fixed messages; the push payload (marker test) and the separate push budget.
- `internal/gateway`: the routes with `fakeBroker`: mutation headers, body limits, CORS.

### Fake herdr

`scripts/fakeherdr/main.go`, built by the harness (the herdr twin of `scripts/e2e-multicomputer/tailscale`). One binary:
- `server`: serves the socket (`ping`, `session.snapshot`, M2 `worktree.create`, `agent.start`, `agent.prompt`) from a scenario file (`$FAKE_HERDR_SCENARIO`: workspaces, tabs, panes, agents, a timeline), plus a control socket the test uses to change states, close panes or restart.
- `status server --json`, `terminal attach --help`, `agent start --help`, by scenario.
- `agent attach` / `terminal attach`: a TTY program that draws a marker and the size it sees, echoes input, logs sizes, input and exit to `$FAKE_HERDR_LOG`, exits on `ctrl+b q`, on pane close, or with "already has an attached client".
- It reads only its own `HOME`. The harness sandbox and HOME rules of S16 apply.

### Opt-in lab test (real herdr)

`PTERM_HERDR_LAB=1 go test ./internal/herdr -run Lab`. It makes a temp `HOME` and refuses to run otherwise, starts `herdr --session termivoa-lab-<random> server` there, makes a temp git repo, a workspace and a worktree, fakes agents with `herdr pane report-agent`, and checks: the mapping, `done` vs `idle`, attach size and size back after the attach ends, the second-attach refusal, `0x02` doubling, start-kind parsing, and M2 create. It stops only its own session (`herdr session stop`) and deletes its folders.

### New phase-B checks (`scripts/e2e-multicomputer/phase-b.mjs`, ids `H-*`, `--only H`)

Fake herdr on mac (two git groups, one workspace outside git, three agents), none on vps.

| ID | Check |
| --- | --- |
| H-tab | The herdr tab shows with the count for mac; nothing for vps |
| H-group | A project row folds and unfolds; worktrees under it; roll-up word |
| H-state | The fake turns an agent `blocked`: BLOCKED and the count within 10 s |
| H-open | Tap the blocked worktree: Live shows the fake's marker at the phone's size, read-only; the Sessions list and the Wall do not show it |
| H-chips | Chips switch terminals; a tab without an agent is disabled when the fake has no `terminal attach` |
| H-type | Take control, type (also `ctrl+b`): the fake logs the text and one literal `0x02` |
| H-end | Leave Live (hide the page): the fake attach exits within 15 s |
| H-link | Load `/p/herdr/<target>` directly: an **Open** button, no attach until it is tapped; a simulated service-worker tap message opens at once |
| H-peer | Fake herdr on vps opened from mac's app; `GET /api/v1/providers/herdr` on vps from `evil.tailx.ts.net` is refused |
| H-auth | No cookie: 401; bad `targetId`: 400; unknown target: 404 |
| H-down | Fake stopped: "herdr is not running on mac"; the Sessions tab loads as fast as without herdr |
| H-hang | Fake socket accepts and never answers: Sessions unaffected; herdr shows not running |
| H-settings | Integrations off hides mac's herdr; notify switches survive a reload |
| H-doctor | `pterm doctor` reports the fake's version (proof the real herdr is not used) |
| H-create (M2) | **+**, a name, the fake agent: a new worktree row, Live opens it; a bad name and a prompt with ESC are refused by the app and the host |
| H-fit | Nothing wider than 390 px on the herdr screens |

Push is tested in Go only (headless Chrome has no push service, and the push dialer refuses local addresses).

### Real checks (owner; `docs/owner-verification.md`)

On the Mac with his live herdr: open a blocked agent, answer it, see the laptop pane go phone-sized and come back within about 10 s after the phone locks; a push on a real iPhone.

## 16. Milestones

All behind detection: on a computer without herdr nothing changes (no tab, no polling, no setting). No milestone changes the database.

- **M1a.** Detection, list, roll-up, count, push (a tap lands on the herdr tab, scrolled to that item), Integrations, `pterm doctor`. Protocol revision 4.
- **M1b.** Open, provider sessions, chips, the `/p/…` address, a push tap opens the agent.
- **M2.** Create, the **+** sheet (H6), `H-create`; `canCreate` true.

## 17. Work split

Two engineers in parallel, no shared files. Both start from the generated types in this pull request (`HTTPProvider*`). AGENTS.md rules 21 and 22 apply to both (file issues for out-of-task bugs; docs in the same pull request).

### HOST (owns `internal/`, `cmd/`, `protocol/` and generated files, `scripts/fakeherdr/`, Go tests)

1. **H1 `internal/herdr` read side (M1a).** `Config` without defaults, binary lookup (export the `tailnet` binary check), `status server --json`, socket client and all S3 checks, poll loop, mapping, cleaning, body budget, capability probes, changes; fixtures and unit tests; the S16 guard test.
2. **H2 routes (M1a).** `internal/broker/providers.go`, `gateway.providers.get` and `.update` in `gateway-rpc.schema.json`, `gatewayapi`, `gatewayclient`, gateway handlers and mux, `providers` in settings, settings keys, the per-device limiter; `http-api.md` routes and revision 4.
3. **H3 push (M1a).** Changes → `webpush` with `provider` and `target`, optional `sessionId`, the rules and own budget of section 7.
4. **H4 doctor (M1a).** `pterm doctor`: herdr found, version, protocol, socket state, `canCreate`.
5. **H5 fake herdr and lab test (M1a, before H6).** `scripts/fakeherdr`, the sandbox and HOME rules of S16, the opt-in lab test.
6. **H6 provider sessions (M1b).** In-memory registry entries (rule 1), `gateway.providers.open`, the open lock, cap, end rules, `0x02` doubling, refused features, directory.
7. **H7 create (M2).** Validation, broker-owned context, `worktree.create`, `agent.start`, `agent.prompt`, snapshot re-check after a timeout, one at a time.

### WEB (owns `web/`: `web/src/**` and `web/vite.config.ts`)

1. **W1 client (M1a).** `HTTP_NAMES` (`vite.config.ts`), `HttpBodies` and `ITEMS` (groups per item) in `httpBody.ts`; `client(origin)` methods `provider`, `providerOpen`, `providerUpdate`, `providerCreate` (45 s); state-word normalising.
2. **W2 data (M1a).** A `providers` module beside `allSessions.ts`: per computer with `protocolRevision ≥ 4` and a listed provider, one scheduler read, its own status; roll-up and count helpers.
3. **W3 Sessions (M1a).** herdr tab with count (H3), fold and unfold (memory only), the All tab section (H1), repeated names with numbers.
4. **W4 Settings → Integrations (M1a).**
5. **W5 push tap (M1a, then M1b).** `sw.js` provider payload, tag and path; the tap message; in M1a the path shows the herdr tab at that item.
6. **W6 Live for provider sessions (M1b).** `/p/…` and `/c/<host>/p/…` in `parseRoute` and `notificationPath`; open on tap, **Open** otherwise; Live from `HTTPProviderSession` (no project or profile); chips; hidden features (rule 8); close the stream when hidden and open again on return; the phone-size line; **Open again**.
7. **W7 create (M2).** The **+** sheet (H6), name and prompt checks before sending.
8. Vitest for each.

Until H2 lands, the web side tests against a stubbed `client`.

### Integration (after both)

- Phase-B `H-*` checks with the fake (section 15); the harness README counts.
- Docs (section 18); screenshots with `scripts/docs-screenshots` and the fake herdr (demo names only).
- The opt-in lab test against the owner's herdr version; the owner verification items.

## 18. Docs to update (rules 22 and 25)

`CHANGELOG.md` (Unreleased: phone and host), `README.md` feature list, `docs/progress.md`, `docs/architecture.md` (Features row, code map), `docs/pending-work.md`, `docs/notifications.md` (provider pushes, own budget), `docs/agent-state.md` (provider sessions), `docs/threat-model.md` (provider routes, herdr socket), `protocol/v1/http-api.md` (routes, revision 4), `docs/owner-verification.md`; site: new guide `site/src/content/docs/docs/herdr.md` ("Termivoa + herdr") and its sidebar entry in `site/astro.config.mjs`, `reference/release-notes.md` (Next beta), `reference/security.md` (what a phone sees and can do), `reference/troubleshooting.md` (not running, update herdr, phone-sized pane), `reference/cli.md` (doctor line); the landing page in `site/public/`. No internal package names on the site (rule 25).

## 19. Open questions (defaults chosen)

| # | Question | Default |
| --- | --- | --- |
| Q1 | A custom herdr prefix (not `ctrl+b`)? | Double `0x02` only (default prefix). A custom prefix is not read from herdr's config in M1; `pterm doctor` warns when herdr's config sets `prefix` |
| Q2 | Which agent kinds does M2 offer? | herdr's start kinds that already run in this herdr, most used first, plus **No agent** |
| Q3 | Named herdr sessions? | Default session only |
| Q4 | Windows (named pipe)? | Not detected in M1 |
| Q5 | Clear DONE when opened on the phone? | No (it would move the laptop's focus); the phone dims it locally |
| Q6 | Offer **Take over** when another client holds the terminal? | No |
| Q7 | Poll even when no phone looks? | Yes while on and running (2 ms per 2 s), for push |
| Q8 | Provider sessions on the Wall? | No |
| Q9 | Question text or task titles in the list? | No |
| Q10 | Wording of the phone-size line | Section 6; the coordinator approves |
| Q11 | Branch base in M2 | herdr's default (the checkout's `HEAD`) |
| Q12 | Watch without resizing the laptop? | Not possible on 0.8.2; revisit with 0.9's `terminal session observe` when 0.8 support ends |

## 20. Reviews: findings and resolutions

Two independent reviews ran on version 1: security, and feasibility with alternatives (rule 24). The coordinator added decisions on top. Each finding was checked against the code or the lab.

### Coordinator decisions

| # | Decision | Resolution |
| --- | --- | --- |
| C1 | No fresh database: the owner paired his phone and VPS today | Provider sessions live in broker memory only (section 6, rule 1); `sessionRecord` and `session_metadata` unchanged; a separate `HTTPProviderSession` wire shape; provider settings in the existing key/value `settings` table. An upgrade keeps pairing, devices and push subscriptions |
| C2 | Accept the security findings | Section 20, security review |
| C3 | The coordinator approves after the reviews (owner's written delegation) | Status line |

### Feasibility review: "feasible with changes"

| # | Finding (severity) | Resolution |
| --- | --- | --- |
| F1 | The fresh database deletes devices, computers and push subscriptions on every computer (high) | Adopted: in-memory provider sessions (C1) |
| F2 | Coming back to Live breaks: reload looks the session up in the list; "start new" needs a project (high) | Adopted: the `/p/<provider>/<target>` address calls `open` (rule 9); **Open again** instead of "start new" |
| F3 | `done` push misses the case where a herdr UI shows the tab (it becomes `idle`) (high) | Adopted: push on `done` or `idle` after 20 s of work (section 7) |
| F4 | Optional `projectId` couples HOST and WEB and breaks the Go validator (medium) | Moot: `sessionRecord` unchanged (C1) |
| F5 | Live features assume a project (Changes, tasks, preview, upload, worktree, start new) (medium) | Adopted: directory = checkout path; the rest hidden and refused (rule 8) |
| F6 | Size restore on lease end is fragile; is the rect the real size? (medium) | Lab: the rect is **not** the real size, and herdr has no size API (section 1). Replaced by ending the attach 10 s after the last viewer, which gives the laptop its size back (proven), and closing the stream when the page hides |
| F7 | Open races and reaping before the phone attaches (medium) | Adopted: one lock per key; 10 s first grace (rules 4, 6) |
| F8 | The sandbox would not hide the real herdr (symlink to the Cellar) (medium) | Adopted: S16 deny list includes the Cellar paths and `~/.config/herdr`; H-doctor proves the fake is used |
| F9 | Push cannot run in phase B (low) | Adopted: Go only |
| F10 | herdr installed later is never found; settings cached by the app (low) | Adopted: lookup every 30 s; the app reads `state` from the snapshot |
| F11 | Delete: the one-implementation interface, `revision`, eviction (low) | Adopted: concrete `*herdr.Provider`; `revision` removed from the schema; no eviction |
| F12 | H-state's 7 s is tight (low) | Adopted: 10 s |
| F13 | The attach client may redraw instead of passing bytes, so History is wrong (low) | Lab: it uses the alternate screen; History hidden for provider sessions |
| F14 | M1 joins low- and high-risk work (low) | Adopted: M1a and M1b |

### Security review

| # | Finding | Resolution |
| --- | --- | --- |
| R1 | No response budget for a large herdr session | 256 KiB body budget, whole groups dropped from the end, `truncated` (4.2) |
| R2 | Size restore from the start or current rect | Lab disproves rect = real size on 0.8.2 and 0.9.3; replaced by ending the attach (see F6) — the one point where size changes back is the end of the attach |
| R3 | Tests could reach the real herdr | S16: no provider unless injected; paths and HOME are config; guard test; sandbox deny list |
| R4 | Push text from herdr-controlled names; session pushes could be starved | Fixed `herdr · group · item` format, Cc/Cf removed, own budget and coalesce key (4.4, 7) |
| R5 | Rate limits keyed by IP behind Serve | Broker limiter keyed by device (S10) |
| R6 | A link can open a terminal (and squeeze the laptop) | `/p/…` opens at once only on an in-app or notification tap; **Open** button otherwise; never takes control (S14) |
| R7 | `ProjectID` sites break | Moot (C1) |
| R8 | Pane IDs repeat after a herdr restart | Target = pane ID + terminal ID; state dropped on rediscovery (S15) |
| R9 | Terminal titles carry private text | `title` removed from the contract; S9 corrected |
| R10 | Cf characters in provider text | Cleaned on the host for every herdr string (section 5) |
| R11 | Socket spoofing beyond file checks | Peer-credential check (S3) |
| R12 | Prefix commands; agent kinds from the snapshot only | Lab list of prefix commands (section 1); `0x02` doubling (S13); kinds only from herdr's start kinds (S2) |
| R13 | M2 create tied to the request context; prompt control characters | Broker-owned context and snapshot re-check after a timeout; ESC, C0 (except tab and line feed), DEL and C1 refused in the schema and in Go |
| R14 | Reaping before the phone attaches | First 10 s count as viewed (rule 6) |

### Final security pass (resolved in the host build)

| # | Finding | Resolution |
| --- | --- | --- |
| N1 | (web) | Web task; no host work |
| N2 | `0x02` doubling must cover every input kind, not only keys | The broker's single PTY writer doubles every `0x02` after the emulator built the input (`SessionConfig.DoublePrefix`, provider sessions only), so key, text, paste, compose, snippets and answer cards are all covered, and any later kind. Tests: one per input kind and one at the writer. A custom herdr prefix is an accepted, documented risk (at worst a phone detaches its own attach client); `pterm doctor` warns when herdr's `config.toml` sets `[keys] prefix` to anything but `ctrl+b` |
| N3 | The checkout path from herdr's snapshot became a session folder unchecked | It must pass the project-path checks (`resolveProjectFolder`, shared with `pterm project add`: absolute, resolved, an existing directory) and be owned by this user. Else the session starts in the home folder and Changes and files are refused for it |
| F11 (remainder) | The socket's parent chain was not checked | Before every dial the socket's folder must be the user's and not group- or other-writable, and its whole parent chain up to `/` passes the folder check of the binary lookup (`tailnet.CheckFolders`) |

### Rejected

- **Restore the laptop's size from the snapshot when control changes** (R2, F6): the lab shows the `layouts` rect is not the real PTY size (borders; stale when headless) and herdr has no other size field; a 0×0 size breaks the pane (0.8.2) or ends the attach (0.9.3). Resizing to the rect would leave the laptop pane wrong. Ending the attach is the only proven restore.

## 21. Host implementation notes (differences from version 2)

Each difference and why. Everything else is built as written above.

| Where | Built | Why |
| --- | --- | --- |
| Detection (sections 1, 10, 13) | The broker detects with the socket's `ping` (version, protocol), not `herdr status server --json`; `pterm doctor` uses the same pass (`herdr.Inspect`) | Same fields, no process per discovery, and the binary never runs unless the server answers. The probes run once per binary and server version |
| Retry timing (section 10) | Binary lookup every 30 s; a server that does not answer is tried again every 5 s; an unsupported one every 30 s | A not-running server costs one `lstat` and a refused dial; 5 s finds a restarted herdr sooner |
| `Config` (section 3) | `PathEnv` replaces the process PATH lookup; `Session` exists for the lab test; `Notify` is gone (the broker filters pushes by its own setting) | No test can find a herdr through the test runner's PATH; the lab needs its own session |
| S16 guard | In a test binary `herdr.New` panics when any `Config` path is outside a temp folder (`testing.Testing()`); `TestGuardRefusesRealHerdr` proves it. The dev-stack lifecycle test runs with its own HOME and PATH | Enforced for every test, not only by review |
| Environment (S11) | Provider sessions get the ptyhost allowlist every Termivoa session gets (HOME, USER, LOGNAME, SHELL, PATH, TMPDIR, SSH_AUTH_SOCK, LANG, LANGUAGE, TZ, `LC_*`; Linux adds the session-bus variables), then `HOME`, `PATH`, `HERDR_SOCKET_PATH`, `TERM`, `COLORTERM`. Probes get only `HOME`, `PATH`, `HERDR_SOCKET_PATH` | One environment rule for every broker session |
| Socket checks (S3, F11) | The folder chain is checked up to `/`, not only up to the config folder | A superset; it reuses the binary lookup's check |
| 0.8.2 attach (sections 1, 13) | `terminal attach` exists in 0.8.2 too; the probe decides | Lab, 2026-10-04 |
| Open (section 6) | A reused session answers its live size, and a reuse counts as a new view (another 10 s grace). The reaper runs every second | The phone attaches right after the answer |
| Create (sections 4.3, 10) | The agent's herdr name is its kind. After a successful create the poll loop refreshes at once and create waits up to 3 s for the new terminal, so **Open** works at once. Create runs in the broker's serve context and the gateway sends it on its own connection | A 45 s create never holds the gateway's shared broker connection |
| Limiter (S10) | A fixed one-minute window per route and device | The same shape as the upload limiter |
| Push (section 7) | Names are cleaned twice (herdr, then the push builder); a cut name is 39 characters and "…" | Reuses the push builder's cleaning |
| `pterm doctor` | A herdr line only when herdr is installed; never counts as a problem | Nothing changes on a computer without herdr |
| Create answer (integration) | Create answers once a poll turn that **started after its last step** has the new terminal (at most 3 s), not as soon as any cached snapshot has it | A poll between `worktree.create` and `agent.start` cached the shell pane without its agent; on a herdr without `terminal attach` that target could not open yet (`TestCreateAnswersAfterAReadThatSawTheAgent`) |
| Integrations on (integration) | Turning herdr on wakes the poll loop at once; the `PATCH` never waits for herdr (it shares the gateway's broker connection with the Sessions list). The app's Integrations card shows the state of its latest provider read (F10), which it reads while Settings is open and again right after a save | The `PATCH` answer still says `off` for the moment before that poll |
| `/p/…` addresses (integration) | The gateway serves the app for `/p/<provider>/<target>` and `/c/<host>/p/…` although the target ID has a dot | Its "a path with an extension is a file" rule answered 404, so a reload, a link or a notification tap with the app closed showed "404 page not found" (`TestStaticSite`) |
| Revision gates (integration) | `protocol/v1/revision.json` names the revision that added each gated feature (`features`: `peerPreview` 2, `providers` 4); generation emits `FEATURE_REVISION` and the app gates on it | One source for the gates; `PROTOCOL_REVISION` grows with every bump and must not be a gate |
| One-row git project (integration) | A git project with only its main checkout shows as one row (like a workspace outside git) and keeps its **+** | It had no **+**, so such a project could not get a worktree from the phone |
| Project row (integration) | The small labels ("ON MAC", "2 WORKTREES") wrap under a long project name instead of cutting it | The name is cut only when it alone is wider than the row (H-fit at 375 and 390 px) |

How to run the fake herdr: `go build -o /tmp/fh/bin/herdr ./scripts/fakeherdr`, write `scenario.json` (`{"version","protocol","attachAny","kinds","snapshot"}`, see the package comment) into `$HOME/.config/herdr`, then `HOME=<that home> /tmp/fh/bin/herdr server`. A broker finds it with that `HOME` and the fake's folder on `PATH` (or in `BinaryPaths`). `control.sock` in the same folder changes states (`{"op":"status","pane":"w1:p1","status":"blocked"}`), closes panes, hangs or stops the server; `fake.log` records attach sizes, input and exits.

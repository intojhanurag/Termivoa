# Architecture

Status (2026-10-04): latest release tag `v0.1.0-beta.3` (2026-10-04). It
adds multi-computer (ADR 0005), preview for sessions on peers (ADR 0006),
Found on your network (ADR 0007), Files, worktrees, project buttons, line
comments, the Bone & ink look and fixes to beta.2. Gate 0 on a real iPhone
and Android, and the real-phone checks of peer preview and Found, are still
pending. See [CHANGELOG.md](../CHANGELOG.md).

This file is the internal map of the system. The code on `main` is the source
of truth. The specification is [implementation-plan.md](implementation-plan.md).
User docs live in `site/src/content/docs/docs/`.

## 1. Big picture

```text
 phone (Home Screen web app, served by "home")
   │  HTTPS (Tailscale Serve :443 → 127.0.0.1:8787)     ┌──────────────────┐
   │  cookie __Host-pterm_session, exact Origin          │ other computers  │
   ├──────────────────────────────────────────────────► │ (peers): own     │
   │  direct credentialed CORS + WebSocket per peer      │ gateway + broker │
   ▼                                                     └──────────────────┘
 gateway  (pterm __gateway, loopback only, no shell)
   │  restricted local IPC (Unix socket / named pipe), clientKind "gateway"
   ▼
 broker   (pterm __broker) ── PTYs ── child shells and agents
   │        ├─ one Go vt emulator per session (server-authoritative screen)
   │        └─ SQLite (phone-terminal.db)
   ▲
   │  restricted local IPC, clientKind "cli"
 desktop: pterm attach / list / new / pair / … (in any terminal app)
```

Who owns what (AGENTS.md rules 7–10):

The **broker** owns PTYs, child lifetimes, terminal emulators, authentication
state, controller leases and SQLite. The **gateway** only serves the web app
and carries authenticated requests to the broker; it never owns or spawns a
shell. Clients get normalized terminal frames, never the child's ANSI bytes.
The broker is the only application-level terminal-query responder; no
browser, desktop or client reply goes to the child. All PTY input goes through
structured, authorized messages and one broker writer. Input is never queued
offline and never resent after a reconnect.

## 2. Processes and binaries

One Go binary, `pterm` (`cmd/pterm`). Node.js is a build-time tool only.

### `pterm` commands

| Purpose | Commands |
| --- | --- |
| Sessions | `list`, `new --project <id> --shell <id>`, `terminate <id>`, `attach [--control] <id>` |
| Pairing and devices | `pair [--origin] [--no-qr] [--no-wait]`, `pair pending`, `pair approve <id>`, `devices`, `devices rename`, `revoke <device>` |
| Multi-computer | `pair --via <name\|url> [--allow-tagged]` (in a terminal it also offers this computer to home for up to 10 minutes: Found on your network), `revoke --via <url>`, `computers`, `computers remove <url>` |
| Configuration | `project add`, `project list`, `profile list`, `profile detect`, `config origin [<url>]` |
| GitHub | `github status`, `github disconnect` (the GitHub login of this computer; connect is only in the app) |
| Install and run | `setup`, `setup --apply --yes` (installer mode), `doctor`, `uninstall [--dry-run] [--yes] [--delete-data]`, `launch [--no-open]` |
| Stop | `stop` (gateway only, sessions keep running), `shutdown` (ends all sessions, stops both) |
| Development | `up --dev [--listen <addr>]`; `--dev` and `--runtime-dir` on any command |
| Hidden process modes | `__broker`, `__gateway` (started by startup registration); `github-credential` (git's credential helper in a folder that Termivoa cloned; checked before any flag parsing in `main.go`) |

The help text is `commandHelp` in `cmd/pterm/management.go`. Local commands
talk to the running broker; they never start or replace it (`launch` and
`up` start processes). `pterm setup` exits with code 3 when other Tailscale
Serve handlers share the origin. It refuses (exit 1), before any change, while
Tailscale Funnel is on for 443 or 8443 (`lifecycle.ServeStatus.Funnel`), and
names `tailscale funnel --https=<port> off`; `pterm doctor` counts any Funnel
as a problem. `pterm pair` and `pterm pair approve` refuse with the same text
(`funnelProblem`), and `pterm launch` warns and starts; both read only
`tailscale serve status --json` (`termivoaFunnel`) and skip the check when it
cannot be read. `lifecycle.ServeFindings(data, host)` reads Tailscale's whole
`ipn.ServeConfig`: the saved config, each `Foreground` config (a `tailscale
serve`/`funnel` without `--bg`; Funnel there is `ForegroundFunnel`, and every
handler on 443/8443 is a problem, since it ends with its command) and
`Services` (Termivoa proxied by a Service is a problem; Tailscale has no
Funnel for Services). Only `<host>:443`/`<host>:8443` count as configured,
where host is this computer's current Tailscale DNS name; Termivoa under
another name (an old machine name) is neither configured nor a problem
(#168). `otherHandlers` also reads foreground handlers on this origin.

### Broker and gateway

| Process | Responsibilities | Entry |
| --- | --- | --- |
| Broker | Takes the data lock and runtime lock, opens SQLite, runs the session registry (default limit 8 live sessions), one actor per session, emulators, controller leases, input writer, pairing and device auth, Web Push, agent-state scans, git (Changes, worktrees), Files, project tasks, uploads, preview port scans, Tailscale status snapshots for multi-computer trust, the GitHub login and clones (`internal/github`). Serves IPC to `cli` and `gateway` clients. | `internal/host/broker.go`, `internal/broker` |
| Gateway | Loopback HTTP and WebSocket server; embedded web assets; CSP, Host, Origin and CORS checks; cookie set and refresh; forwards the raw cookie credential and the request `Origin` to the broker on every call; second loopback listener for dev-server preview; answers 404 under `/push/` (peer push scopes). Has a private control endpoint that `pterm stop` uses. Holds no Store, Registry or PTY. | `internal/gateway`, `internal/gatewayclient`, `internal/host/gateway_control.go` |

Shutdown order (broker): stop IPC accept, wait for registry cleanup, close
SQLite, release the data lock. A gateway restart does not end sessions. A
broker crash, logout or reboot ends sessions; they show as lost and are never
rerun.

Worktree session creation admits one active operation with
`worktreeCreates.TryLock`, with zero queued operations (#236). Cancellation
is checked before and immediately after admission; only admitted work uses
`context.WithoutCancel`. Concurrent duplicates receive `limit_exceeded`
without changing receipts; replay after completion returns the accepted
operation's session. Disconnect and shutdown cannot accumulate create
waiters. Shutdown still waits for the one accepted operation, including
existing Git timeout/rollback behavior. See [worktrees.md](worktrees.md).

### Startup registration

Per user, no elevation. Generated and applied by `internal/lifecycle`.

| OS | Mechanism | Names |
| --- | --- | --- |
| macOS | LaunchAgents in `~/Library/LaunchAgents` | `dev.phone-terminal.broker`, `dev.phone-terminal.gateway` |
| Linux | systemd user units in `$XDG_CONFIG_HOME/systemd/user` | `phone-terminal-broker.service`, `phone-terminal-gateway.service` |
| Windows | Task Scheduler (per-user tasks) | `Phone Terminal Broker (<user>)`, `Phone Terminal Gateway (<user>)` |

Names keep the old working name on purpose ([naming.md](naming.md)).

### Profiles

| | Production | Development (`--dev`) |
| --- | --- | --- |
| Directory name | `Phone Terminal` (Linux: `phone-terminal`) | `Phone Terminal Development` (Linux: `phone-terminal-development`) |
| Gateway listen | `127.0.0.1:8787` (fixed) | ephemeral loopback; `pterm up --dev` uses `127.0.0.1:8788` |
| Cookie | `__Host-pterm_session` (Secure) | `pterm_dev_session` (not Secure, plain HTTP) |
| Startup registration | yes | no |

Dev paths must never equal, contain or sit inside production paths
(`platform.ValidatePaths`).

### Ports

| Port | What |
| --- | --- |
| `127.0.0.1:8787` | Production gateway (app, API, WebSocket, `/healthz`) |
| `127.0.0.1:8788` | `pterm up --dev` gateway |
| `127.0.0.1:8789` | Dev-server preview listener (separate origin) |
| HTTPS 443 | `tailscale serve --bg --https=443 http://127.0.0.1:8787` |
| HTTPS 8443 | `tailscale serve --bg --https=8443 http://127.0.0.1:8789` (optional, separate consent) |

With several computers, each computer has its own 8443 with its own consent
in `pterm setup`; it proxies only that computer's sessions. The home app
frames `https://<peer>:8443` directly; nothing is relayed through home.

## 3. Code map

| Path | Purpose |
| --- | --- |
| `cmd/pterm` | CLI entry, argument parsing, `setup`/`doctor`/`launch`/`up`/`stop`, hidden process modes. `lifecycle_e2e.go` is compiled only with the `e2e` tag (section 8) |
| `cmd/pterm/github.go` | The production `github.Config` (`githubConfig`: git's install paths, then `PATH`, the home folder, this `pterm`'s resolved path; nil on Windows), `pterm github status`/`disconnect`, the GitHub line of `pterm doctor` (`inspectGitHub`), and the hidden helper `pterm github-credential` (`runGitHubCredential`: answers only `get` for `protocol=https`, `host=github.com`, with `password_expiry_utc`; asks the broker over the admin channel). `github_e2e.go`/`github_release.go` pick the helper's allowed host by build tag |
| `cmd/pterm/herdr.go` | The production `herdr.Config` (`herdrConfig`: fixed install paths, then `PATH`, the user's home; none on Windows) and the herdr line of `pterm doctor` (`inspectHerdr`: only when herdr is installed, never counted as a problem; warns on a custom prefix key) |
| `internal/agentstate` | Pure classifier: working, waiting on a question, or idle, from sanitized screen text and timing |
| `internal/auth` | Secrets, pairing grant (2 min) and browser credential lifetimes (30 days absolute, 7 days idle) |
| `internal/broker` | Sessions, registry, controller leases, input inbox and writer, viewers, RPC servers (management, admin, gateway), agent state, Changes, Files, tasks, worktrees, uploads, preview scans, live folder, multi-computer trust, Found on your network (`offer.go`: this computer's offer to its home and the pinned dial; `offers.go`: home's offer book and found list; `peerdial_e2e.go` is compiled only with the `e2e` tag) |
| `internal/broker/providers.go` | The broker's side of herdr: Integrations settings (`provider.herdr.enabled`, `provider.herdr.notify` in the `settings` table), the `gateway.providers.get/open/update/create` handlers, the per-device limiter (open 20, create 3, update 10 per minute), in-memory provider sessions (`CreateProvider` in `registry.go`, at most `MaxProviderSessions` = 4, outside the 8-session limit), the reaper (10 s after the last viewer, or when the target leaves the snapshot, or when off), and herdr changes to pushes |
| `internal/cli` | User-facing local commands (pair, devices, list, attach orchestration, QR); owns no state |
| `internal/desktop` | Desktop attach client: outer TTY modes, renderer of normalized frames, bounded input decoding |
| `internal/gateway` | HTTP/WebSocket server, static assets, CSP, Origin/CORS, cookies, preview proxy |
| `internal/gatewayapi` | Interface between gateway and broker (the gateway transports, the broker authorizes) |
| `internal/gatewayclient` | Gateway's IPC client of the broker (RPC and live streams) |
| `internal/gitchanges` | Bounded git: read changes, commit, push, worktree add/remove; never a shell, never force |
| `internal/github` | GitHub, the only package that talks to GitHub ([github-integration.md](github-integration.md)): `client.go` (outbound HTTPS client: hosts `github.com` and `api.github.com` only, no proxy, no redirects, public addresses only, 5 s dial, 10 s a request, 1 MiB an answer), `connect.go` (device flow, cancel, Disconnect with `POST /credentials/revoke`), `token.go` (the token pair in `<data>/github.json`, a `secret` type that prints `[REDACTED]`, refresh under one lock that writes the new pair before it returns it, `Credential` for the helper), `repos.go` (installations and repositories: at most 10 installations and 1000 rows, 15 s, cached 60 s, one shared run; `here` and `taken` from one `Lstat` of `~/<name>` and `git config --file` of its `origin`), `setup.go` and `git.go` (the clone job: checks, free space, temp folder `~/.termivoa-clone-<profile>-<random>`, the fixed git environment and config, the size watch, the helper lines and commit name in the clone's config, the exclusive rename in `rename_darwin.go`/`rename_linux.go`, the start-up sweep of this profile's temp folders). `ClientID` and `AppSlug` are package variables (empty: no feature); `github_e2e.go` (build tag `e2e`) points the hosts at `scripts/fakegithub` |
| `internal/broker/github.go` | The broker's side of GitHub: builds the `*github.Service` (a service that cannot start turns the feature off), the `gateway.github.*` handlers with the per-device limiter (read 60, list 10, connect 3, set up 3, cancel/disconnect/stop 10 per minute), fixed error texts, and the `admin.github.*` messages (status, disconnect, credential) |
| `internal/herdr` | herdr provider, the only package that knows herdr ([herdr-provider.md](herdr-provider.md)): finds the binary (`FindBinary`, the checks of `tailnet.CheckBinary`), the socket client (`ping`, `session.snapshot`; `worktree.create`, `agent.start`, `agent.get`, `agent.prompt` for create) with the socket checks and peer credential, the 2 s poll loop that fills a cache, mapping to the generic provider wire shape, push changes (`Change`), the attach argv (`Attach`), create (`Create`), `Inspect` for doctor. `guard.go` makes a test config that names a path outside a temp folder panic. `herdrtest` is an in-process fake socket server for Go tests |
| `internal/host` | Process bootstrap for broker and gateway, gateway control endpoint |
| `internal/ipc` | Length-prefixed local IPC, handshake, current-user peer checks, Unix socket and Windows named pipe |
| `internal/lifecycle` | LaunchAgent plist, systemd unit and Task XML generation; install/uninstall plans; doctor checks (Tailscale Serve) |
| `internal/liveclient` | Desktop observer/controller over broker IPC |
| `internal/localclient` | Bounded IPC clients for `cli` commands (management, admin, gateway control) |
| `internal/platform` | Data, runtime and log paths per OS and profile; WSL detection; safe open of a regular file under a root (`OpenRegular`) |
| `internal/projectfiles` | Read-only Files view through `os.Root`; `.git` hidden |
| `internal/projecttasks` | Lists project commands (package.json, Makefile, Cargo, Go); never runs them |
| `internal/protocol` | Generated types (`generated.go`) plus hand-written validation and bounds |
| `internal/ptyhost` | Native PTY and process trees, working folder, listening ports per OS |
| `internal/store` | SQLite (modernc.org/sqlite), migrations, auth, sessions, receipts, push, computers |
| `internal/tailnet` | Reads `tailscale status --json` into immutable snapshots; safe binary lookup |
| `internal/terminal` | Server-authoritative emulator core, frames, row patches, history |
| `internal/webassets` | Embedded web app (`-tags webdist`) or a placeholder page |
| `internal/webpush` | Web Push with the standard library (RFC 8291, RFC 8292 VAPID), push host allow-list |
| `protocol/v1` | Canonical JSON Schemas and protocol docs (section 4) |
| `web/src/screens` | React screens: Sessions, Live, Wall, Changes, Files, Preview, Settings, Computers, sheets; `ui.tsx` holds the shared icons, the busy `Button`, `Notice` and `Banner` (styles and tokens in `web/src/styles/global.css`) |
| `web/src/screens/{providers.ts,Herdr.tsx,ProviderScreen.tsx,ProviderCreate.tsx}` | herdr on the phone. `providers.ts`: one scheduler read per computer that lists a provider (own status, memory only), roll-up, count, tabs. `Herdr.tsx`: the herdr tab (projects, worktrees, state words). `ProviderScreen.tsx`: one terminal in Live (`/p/<provider>/<target>`, chips, Open, closes the stream when the page is hidden). `ProviderCreate.tsx`: the **+** sheet (new worktree and agent) |
| `web/src/screens/GitHub.tsx` | GitHub on the phone: the row of each computer in **Settings → Integrations** (`GitHubRow`), and one sheet (`GitHubSheet`) for Connect (code, step 2), the repository list with search, and a set-up (progress, **Stop**, ready, problems), drawn from `GET /api/v1/github` and polled every 2 s while it waits. Fixed GitHub addresses only. Loaded on demand from Settings and from **From GitHub** in the **New workspace** sheet (`ProviderManage.tsx`, `Sessions.tsx`) |
| `web/src/screens/Inbox.tsx` | The **Needs you** tab and its queue: one list of what waits on every computer (sessions in `waiting`, herdr items in `blocked`; then **Finished**), built from the session lists and the provider lists; the **Next** control in Live. No request of its own ([needs-you-inbox.md](needs-you-inbox.md)) |
| `web/src/terminal` | xterm display, frame renderer, snapshot assembly, input sender, composer, snippets, speech, phone-wide scheduler |
| `web/src/protocol` | Ajv validators generated at build time (no runtime `eval`) |
| `web/src/api` | HTTP client with timeouts |
| `web/src/pairing` | Pairing and add-computer flows; `scan.ts` reads the add-computer QR code in the app (`BarcodeDetector` or the lazy `qr` decoder chunk) and hands the text to the same parser and confirm sheet as paste |
| `web/src/generated` | Generated protocol TypeScript types |
| `web/src/sw.js` | Service worker: precache, network-first pages, push display (home and per-peer scopes; session and herdr terminal payloads, the one-time tap record for a session's or a herdr terminal's notification tap) |
| `web/src/storage.ts` | The one safe `localStorage`/`sessionStorage` accessor (private browsing may refuse storage) |
| `third_party` | Patched copies of `ansi`, `vt`, `xpty`, `ultraviolet` ([dependency-patches.md](dependency-patches.md)) |
| `tests` | Integration, soak, agent replay and PTY fixtures |
| `site/` | Public docs site (Astro Starlight) and landing page in `site/public/`, deployed to Vercel |
| `scripts/` | Checks, protocol generation, web embedding, Linux install, soak, Gate 0, multi-computer e2e, docs screenshots |
| `scripts/fakegithub` | A fake GitHub for the e2e harness and Go tests: one loopback server for the device flow, the API and git smart HTTP over bare fixture repositories, with `/_test/*` controls. It never contacts github.com |
| `scripts/fakeherdr` | A fake herdr binary (`server`, `status`, `terminal attach`, `agent attach`) with a control socket, for Go tests and the e2e harness. It reads only its own folder; the harness sandbox hides the real herdr |
| `packaging/` | Unsigned macOS app/DMG, Linux binaries, Windows Inno Setup installer; signing scripts |

## 4. Protocols

All schemas are JSON Schema Draft 2020-12 in `protocol/v1`.

| File | Defines |
| --- | --- |
| `ipc.schema.json` | Local IPC handshake (`ipc.hello`, `ipc.welcome`, `ipc.error`); client kind `cli` or `gateway` |
| `broker-rpc.schema.json` | Session management RPC: `session.create/list/rename/terminate/status`, worktree create on session create |
| `admin-rpc.schema.json` | Local admin RPC, `cli` only: pairing, devices, revoke, computers, projects, profiles, origin, `admin.github.status`/`disconnect`/`credential` (the credential is the only admin answer that carries a secret: the GitHub access token, never the refresh token), shutdown; `admin.gateway.stop` (gateway control endpoint only) |
| `gateway-rpc.schema.json` | Gateway → broker RPC: auth check, pairing, settings, sessions, uploads, push, preview check, computers, origin probe, live open, `gateway.github.get`/`action`/`repos` |
| `envelope.schema.json` | Live WebSocket and live IPC messages: snapshots, patches, history, input, control, session state, Changes, Files, tasks, branches, worktree remove |
| `http-api.schema.json` | Browser HTTP API bodies |
| `types.schema.json` | Index of named types for generation |
| `revision.json` | Additive protocol revision (now `revision: 7`, `minRevision: 1`; revision 2 is the preview listener rules, no schema change; revision 3 is Found on your network: the offer and found routes, `gateway.computers.offer`/`found`, `admin.pair.offer`/`offer.end`, error `home_changed`; revision 4 is providers: the `/api/v1/providers/{provider}` routes, `gateway.providers.*`, `providers` in settings; revision 5 is herdr manage; revision 6 is the agent tab, `openItem` and the `closed` list of a group; revision 7 is GitHub: `GET`/`POST /api/v1/github`, `GET /api/v1/github/repos`, `github` in settings, `gateway.github.*`, `admin.github.*`). Its `features` map (`peerPreview` 2, `providers` 4, `providerManage` 5, `providerAgentTab` 6, `github` 7) is emitted as `FEATURE_REVISION` in `web/src/generated/protocol.ts`; the app gates each feature on it |

Prose specs: [http-api.md](../protocol/v1/http-api.md) (routes, headers,
cookies, revision bump rules), [local-transport.md](../protocol/v1/local-transport.md),
[broker-management.md](../protocol/v1/broker-management.md),
[authentication-rpc.md](../protocol/v1/authentication-rpc.md),
[terminal-capabilities.md](../protocol/v1/terminal-capabilities.md).

Wire format: local IPC is four-byte big-endian length-prefixed JSON; the
WebSocket uses JSON text messages. Counters travel as decimal strings.

Code generation: `pnpm protocol:generate` runs `scripts/generate-protocol.mjs`
(quicktype) and writes `internal/protocol/generated.go` and
`web/src/generated/protocol.ts`, plus the revision constants. It fails on an
unresolved `$ref`. Drift check: `pnpm protocol:check` regenerates and runs
`git diff --exit-code` on both files; CI job `protocol-generation` and
`scripts/check` run it. The web validates with Ajv standalone code built by
`web/vite.config.ts`; Go decodes requests strictly and adds semantic bounds.

The phone reads `protocolRevision` and `minProtocolRevision` from
`GET /api/v1/settings`. It refuses a peer below the minimum, sends new
request fields only to peers that support them, and frames a peer's preview
only when that peer is at revision 2 or higher (`FEATURE_REVISION.peerPreview`). It reads a computer's providers and calls the provider routes only at revision 4 or higher (`FEATURE_REVISION.providers`). It shows herdr manage at revision 5 or higher (`providerManage`) and the agent tab, closed worktrees and the terminal menu at revision 6 or higher (`providerAgentTab`). It shows a computer's GitHub row, and **From GitHub**, only at revision 7 or higher and when that computer's settings carry `github` (`githubShown`).

## 5. Data

### SQLite tables (`phone-terminal.db`, broker only)

One file, `internal/store/migrations/001_schema.sql`, creates the whole
schema. There are no migrations: a schema change edits that file, and a
database made with another schema is refused at startup (delete it to start
fresh). `schema_migrations` keeps the file's name and SHA-256 to detect this.

| Area | Tables |
| --- | --- |
| Sessions | `projects`, `shell_profiles` (`darwin`, `windows`, `wsl`, `linux`), `session_metadata` (with `title_custom`) |
| Operation receipts | `mutation_receipts` (idempotent create/rename/terminate, with the `result_session_json` snapshot that triggers require) |
| Authentication | `devices`, `browser_sessions`, `pairing_grants`, `pending_pairings`, `settings` (grants, pending pairings and devices carry `app_origin` and app node columns) |
| Web push | `push_subscriptions`, `push_vapid_key` |
| Multi-computer | `computers` (with `node_id`: home pins each peer's node) |

Only credential hashes are stored, with one exception outside the database:
the GitHub token pair (below).
Terminal content, raw output, controller leases and process handles are never
stored.

### Locations

| OS | Data dir (DB, locks) | Runtime dir (IPC endpoint, locks) | Logs |
| --- | --- | --- | --- |
| macOS | `~/Library/Application Support/Phone Terminal` | `~/Library/Caches/Phone Terminal/runtime` | `<data>/logs` |
| Linux | `$XDG_DATA_HOME/phone-terminal` (`~/.local/share`) | `/run/user/<uid>/phone-terminal`, else `$XDG_RUNTIME_DIR/phone-terminal`, else `<data>/runtime` | `$XDG_STATE_HOME/phone-terminal/logs` |
| Windows | `%LOCALAPPDATA%\Phone Terminal` | `%LOCALAPPDATA%\Phone Terminal\runtime` (IPC is a named pipe with a current-user ACL) | `<data>\logs` |

`<data>/github.json` (mode 0600, written atomically, zeroed then removed on
Disconnect) holds this computer's GitHub login: login, account ID, access
token and refresh token with their expiries. It is a file and not a table
so that the schema stays the same (a schema change refuses every existing
database). `pterm uninstall --delete-data` removes it with the data folder.

Dev profile: same layout with the development directory name. Source:
`internal/platform/paths.go`. The Unix IPC socket is `<runtime>/broker.sock`.

## 6. Security model in short

Full model: [threat-model.md](threat-model.md). Malware already running as the
same OS user is out of scope. Nothing runs as root or SYSTEM.

| Topic | Rule |
| --- | --- |
| Pairing | `pterm pair` makes a single-use grant (2 minutes) carried in the URL fragment. The phone redeems it with one POST, then polls with a separate secret. The owner approves on the computer after comparing a verification code. A grant bound to another computer's app (`pterm pair --via`) can also reach that home as an offer (Found on your network): TLS to home's pinned Tailscale IP, at most 60 s in home's memory, readable by every device paired with home. The approval prompt names this computer, quotes the phone's code screen and gives the app's origin and owner; after a delivered offer the QR code and the link wait for Enter. |
| Cookie | `__Host-pterm_session`: Secure, HttpOnly, `SameSite=Strict`, `Path=/`. 30 days absolute, 7 days idle. `GET /api/v1/settings` resends the same value so WebKit's 7-day cap does not end it. The broker stores only hashes and re-authorizes the raw credential on every call. |
| Origin and Host | Exact Host check. Every `/api` request needs an exact allowed `Origin`, or no `Origin` plus `Sec-Fetch-Site: same-origin`. `Sec-Fetch-Mode: no-cors` is refused. Mutations need `X-Pterm-Request`. Strict CSP on every response. |
| Tailscale | Serve on 443 gives the private HTTPS origin. Funnel and public tunnels are excluded. Tailscale identity headers are ignored. `tailscale status` only narrows multi-computer access; it is never authentication. |
| Multi-computer | A peer accepts a cross-origin call only from the app origin bound to that exact credential (`devices.app_origin`), with the pinned node ID and same owner (or `--allow-tagged`). Status cached about 60 s, 10-minute grace. A changed node ID revokes devices bound to it. A compromised home compromises every peer paired through it. The phone keeps only `/sw.js` at scope `/` and at the push scope of each peer in its stored list; a registration for a host a compromised home wrote into that list lives until home's list answers. `POST /api/v1/computers/offer` is unauthenticated: any `Origin` or `Sec-Fetch-*` is `403` before CORS, its own bucket (10, then 1/s; one bucket behind Serve), always `204`, snapshot only; home keeps offers only from untagged peers of its owner, in memory, 60 s, at most 16. `GET /api/v1/computers/found` is cookie and own origin only. |
| Preview | Separate origin (8443) per computer. Termivoa cookies are never forwarded to or set by a dev server. Admission order on the preview listener: Host; then the selection (`/__termivoa/select`) needs no `Origin` and exactly one `Referer` of this computer's own app origin or of a bound app origin (a bound one is forwarded to the broker, which checks it against the credential); every other path needs rule L1 (`Origin` equal to the preview origin, or no `Origin` plus `Sec-Fetch-Site: same-origin`); then the cookie and the broker (failed attempts are not counted: no client can lock out a paired phone). The selection answers a `200` page that moves the frame to `/`, not a redirect. The broker runs the Tailscale trust check for a bound device on every preview check; open sockets are rechecked every 5 s. The app document's CSP holds its own preview origin and the `:8443` origin of each confirmed computer (never pending, never `wss:`) in `frame-src` and `connect-src`. The app probes a preview origin before it frames it, and marks a peer's frame with the computer's label and **untrusted page**: a peer chooses the page in its frame ([peer-preview.md](peer-preview.md), section 7). |
| Controller lease | One controller per session. Acquire revokes the old lease, raises the epoch and makes a new 32-byte secret. Input carries generation, lease and a rising sequence; the broker rechecks auth and lease before acceptance. |
| Input | Bounded `key`, `text`, `paste`, `mouse`, `answer` messages. Reconnect starts as observer. Lost acks show "uncertain delivery"; nothing is resent. |
| GitHub | A second outbound client (`github.com`, `api.github.com`), used after **Connect**. The token pair never leaves the broker except the access token to `pterm github-credential` (same-user peer check of the admin channel). The phone sends only actions and `owner/name`; the setup route registers one project root, `~/<name>`, that the broker cloned or checked. The clone runs with a fixed environment (no token in it) and config: https to github.com only, no hooks, templates, submodules or LFS download, `transfer.fsckObjects`; it never deletes or overwrites a folder ([github-integration.md](github-integration.md), ADR 0010) |

Decisions: [ADR 0002](decisions/0002-server-authoritative-terminal.md),
[ADR 0003](decisions/0003-private-tailscale-networking.md),
[ADR 0005](decisions/0005-multi-computer.md),
[ADR 0006](decisions/0006-peer-preview.md),
[ADR 0007](decisions/0007-found-on-network.md),
[ADR 0010](decisions/0010-github-integration.md). Pairing design:
[p4-authentication-design.md](p4-authentication-design.md).

## 7. Features

"Next" means on `main`, not in `v0.1.0-beta.3`.

| Feature | What it is | Host code | Web code | Design doc |
| --- | --- | --- | --- | --- |
| Sessions | Create (project + shell profile, optional run-first command), list, rename, terminate; idempotent operation IDs | `internal/broker/registry.go`, `internal/broker/session.go`, `internal/store/sessions.go` | `web/src/screens/Sessions.tsx`, `web/src/screens/NewSession.tsx`, `web/src/screens/SessionMenu.tsx`, `web/src/terminal/runFirst.ts` | — |
| Live terminal | Snapshot then row patches on exact base revision; history pages; display-only xterm | `internal/terminal`, `internal/broker/viewer.go`, `internal/gateway/live.go` | `web/src/screens/Live.tsx`, `web/src/terminal/FrameRenderer.ts`, `web/src/terminal/SnapshotAssembler.ts`, `web/src/terminal/Connection.ts`, `web/src/screens/History.tsx` | ADR 0002 |
| Control and observers | Explicit take control; others watch | `internal/broker/controller.go`, `internal/broker/session_dispatch.go`, `internal/broker/input_inbox.go` | `web/src/terminal/InputSender.ts`, `web/src/terminal/InputController.ts` | — |
| Wall and thumbnails | Grid of all sessions; read-only thumbnail viewers: the phone asks for 5 updates/s per tile and the broker caps any request at 30/s; at most 4 live tiles under a phone-wide budget; hold and drag to arrange; a visible never-live tile beyond the limit gets one snapshot turn (`peek`), then Paused, in a 5th connection slot | `internal/broker/viewer.go` | `web/src/screens/Wall.tsx`, `web/src/terminal/scheduler.ts` | multi-computer.md |
| Agent state and badges | Working, waiting, idle from screen; worked seconds | `internal/agentstate`, `internal/broker/agent.go` | `web/src/screens/Sessions.tsx` | [agent-state.md](agent-state.md) |
| Answer cards | Tap an option for a detected question; broker re-parses the screen and sends its own keys | `internal/broker/agent.go` | `web/src/screens/AnswerCard.tsx` | [agent-state.md](agent-state.md) |
| Notifications | Web Push "waiting" and "done"; no terminal text in payloads; from every paired computer (one `/sw.js` registration per peer at scope `/push/<host>/`) | `internal/webpush`, `internal/store/push.go` | `web/src/push.ts`, `web/src/sw.js` | [notifications.md](notifications.md) |
| Away recap | "While you were away" card: time away, time worked, changes | `internal/broker/agent.go` (worked seconds) | `web/src/screens/Recap.tsx`, `web/src/screens/activity.ts` | — |
| Changes | Diff list and file view, commit selected paths, push (never force), line comments into the composer | `internal/gitchanges`, `internal/broker/changes.go` | `web/src/screens/Changes.tsx` | [changes.md](changes.md) |
| Project buttons | Project commands in the Snips sheet; typed into the session as input | `internal/projecttasks`, `internal/broker/tasks.go` | `web/src/screens/Snippets.tsx` | [project-tasks.md](project-tasks.md) |
| Files | Browse, search and read project files; tap `path:line` in the terminal | `internal/projectfiles`, `internal/broker/files.go` | `web/src/screens/Files.tsx`, `web/src/terminal/pathLinks.ts` | — |
| Worktrees | New session in `~/termivoa/worktrees/<repo>/<name>` on branch `termivoa/<name>` (`git worktree add`); remove it; one active create, no waiting queue | `internal/broker/worktree.go`, `internal/gitchanges/worktree.go` | `web/src/screens/Worktree.tsx` | [worktrees.md](worktrees.md) |
| Dev-server preview | Proxy one port that the session's own processes listen on (OS scan only); for sessions on every computer in the list: the home app frames a peer's own preview origin, after a reachability probe, with the computer's label and **untrusted page** over a peer's page | `internal/broker/devpreview.go`, `internal/ptyhost/listen.go`, `internal/gateway/preview.go`, `internal/gateway/handler.go` (`deviceConnectSources`), `internal/broker/gateway_rpc.go` (`gateway.preview.check`) | `web/src/screens/Preview.tsx`, `web/src/screens/PreviewChip.tsx`, `web/src/screens/App.tsx` (`previewFor`), `web/src/computers.ts` (`previewOriginOf`) | [preview.md](preview.md), [peer-preview.md](peer-preview.md), ADR 0006 |
| Photo and voice | Photo upload into the project folder (controller only); dictation with the Web Speech API | `internal/broker/upload.go` | `web/src/terminal/attachments.ts`, `web/src/terminal/image.ts`, `web/src/terminal/speech.ts` | — |
| Live folder | Current working folder from the OS or OSC 7 / OSC 9;9; display only | `internal/broker/cwd.go`, `internal/broker/cwd_hook.go`, `internal/ptyhost/cwd.go` | `web/src/screens/folder.ts` | [live-folder.md](live-folder.md) |
| Snippets, composer and keys | Saved snippets, multi-line composer with drafts in memory, key row, agent keys | — | `web/src/terminal/snippets.ts`, `web/src/terminal/composer.ts`, `web/src/screens/Composer.tsx`, `web/src/screens/Dock.tsx`, `web/src/terminal/agentKeys.ts` | — |
| Multi-computer | One app on home talks directly to up to 8 peers; Settings → Computers shows each computer's `hostBuild` and last good read (memory only), **Check again** reads one computer through the scheduler | `internal/broker/computers.go`, `internal/store/computers.go`, `internal/tailnet`, `cmd/pterm/computers.go` | `web/src/computers.ts`, `web/src/screens/allSessions.ts`, `web/src/screens/Computers.tsx`, `web/src/screens/AddComputer.tsx`, `web/src/pairing/addComputer.ts` | [multi-computer.md](multi-computer.md), ADR 0005 |
| Found on your network | A new computer that waits in `pterm pair --via <home>` (or setup's answer 2) at a terminal pushes its bound grant to home every 30 s for up to 10 minutes; **Settings → Computers → Found on your network** lists it with **Add**, which runs the pasted-link flow. Routes `POST /api/v1/computers/offer` (no auth) and `GET /api/v1/computers/found` (cookie, own origin); RPCs `gateway.computers.offer`/`found`, `admin.pair.offer`/`offer.end`; protocol revision 3 | `internal/broker/offer.go` (`offerPairing`, `endOffer`, `pushOffer`, `dialHome`, `dialPinned`), `internal/broker/offers.go` (`offerComputer`, `foundComputers`, `offerBook`), `internal/gateway/handler.go` (`admitAPI` exception), `internal/gateway/api.go` (`offerComputer`, `foundComputers`), `internal/cli/admin.go` (`offerAndWait`, `waitForPhone`, `pairingPrompt`), `cmd/pterm/lifecycle.go` (`offerPairing`, `askHome`) | `web/src/computers.ts` (`watchFound`), `web/src/screens/Computers.tsx` (`FoundBlock`), `web/src/pairing/addComputer.ts` (`foundLink`, the one retry), `web/src/screens/AddComputer.tsx` | [found-on-network.md](found-on-network.md), ADR 0007 |
| herdr on your phone | A **herdr** tab in Sessions lists each computer's herdr projects, worktrees and agent state words; a tap opens that one real terminal in Live (an in-memory provider session that runs `herdr agent attach`, never `--takeover`); a push when an agent is blocked or finished; **Settings → Integrations** switches it per computer; **+** makes a worktree and starts an agent (on every git project: the host looks for `.git` itself). The **⋯** menu and **New workspace** manage herdr through one route, `POST /api/v1/providers/{provider}/manage` (`Provider.Manage` in `internal/herdr/manage.go`: close, remove without force, new tab, new workspace from a project ID, start `herdr server`; the confirmed name is checked on a fresh snapshot under the create lock). A computer without herdr lists it as `notInstalled`: no tab, a card with **Install herdr** that opens a session with the install command typed (`runFirst` with `typeOnly`). Revision 6 adds: `newTab` with `agent` and an optional `prompt` (the host starts the agent in the new tab's pane; it takes one of the device's 3 creates a minute), `openItem` (group ID plus a branch name that must be in a fresh `closed` list; `worktree.open`), and the `closed` list of each group (branch names of closed linked worktrees, from `worktree.list` in the poll in `internal/herdr/closed.go`: at most 16 names, at most 4 `worktree.list` calls per poll turn, 1 s each, one group at most every 10 s). The phone also gets the **⋯** menu inside a herdr terminal (`ProviderScreen.tsx`). Protocol revisions 5 and 6 ([herdr-manage.md](herdr-manage.md), section 14) | `internal/herdr`, `internal/broker/providers.go`, `internal/broker/registry.go` (`CreateProvider`), `internal/webpush` (`NotifyProvider`), `cmd/pterm/herdr.go`, `scripts/fakeherdr` | `web/src/screens/providers.ts`, `web/src/screens/Herdr.tsx`, `web/src/screens/ProviderScreen.tsx`, `web/src/screens/ProviderCreate.tsx`, `web/src/sw.js` | [herdr-provider.md](herdr-provider.md), ADR 0008 |
| Needs you inbox | A **Needs you** tab in Sessions (after **All**, with a count, only while something waits) lists what waits for an answer on every computer: sessions in `waiting` and herdr items in `blocked`, the longest wait first, then **Finished** ("Your turn" sessions, herdr `done` not opened here). A tap opens the item and starts a queue: **Next · 2 left ›** in Live goes to the next waiting item, **Inbox ›** on the last. A notification tap starts the queue too (the service worker's one-time tap record; never the address). Navigation only: no input, no control, no new request, no protocol or host change | — | `web/src/screens/Inbox.tsx`, `web/src/screens/Sessions.tsx` (`ComputerChips`), `web/src/screens/App.tsx` (`queueNext`, `queueOnArrival`, `queueScreen`), `web/src/screens/Live.tsx`, `web/src/screens/ProviderScreen.tsx`, `web/src/sw.js` (`SESSION_PATH`) | [needs-you-inbox.md](needs-you-inbox.md) |
| Desktop attach | `pterm attach` in any terminal app; Ctrl+] detaches | `internal/desktop`, `internal/liveclient`, `internal/cli/desktop.go`, `cmd/pterm/desktop.go` | — | — |
| Packaging | Unsigned macOS app and DMG (launcher runs `pterm launch`), Linux static binaries and installer script, Windows Inno Setup installer | `packaging/macos`, `packaging/linux`, `packaging/windows`, `scripts/install-linux.sh` | — | [install.md](install.md) |
| GitHub | **Settings → Integrations** has one row for each computer (macOS and Linux, a build with `ClientID`, revision 7): **Connect** runs the device flow of the Termivoa GitHub App in the broker (no client secret; Contents read and write, Metadata read) and keeps one token pair for each computer in `github.json`; **Set up a repository** lists the chosen repositories with `here`/`taken`, and **Set up** runs one clone job for each computer in the broker (not in a session): into a temp folder in the home folder, then an exclusive rename to `~/<name>` and `addProject`. The phone polls the job's state. In the clone's own config, a reset line and `!<pterm> github-credential` make git use Termivoa's login for that one repository (never stored by git). **Disconnect** revokes both tokens at GitHub and deletes the file. Routes `GET`/`POST /api/v1/github`, `GET /api/v1/github/repos`; protocol revision 7; no database change | `internal/github`, `internal/broker/github.go`, `cmd/pterm/github.go`, `scripts/fakegithub` | `web/src/screens/GitHub.tsx`, `web/src/screens/Settings.tsx`, `web/src/screens/ProviderManage.tsx` (**From GitHub**), `web/src/screens/Sessions.tsx` | [github-integration.md](github-integration.md), ADR 0010 |
| Docs site | Astro Starlight site with `llms.txt` and per-page Markdown | `site/` | — | AGENTS.md rules 22–23 |

## 8. Build and checks

| Command | What |
| --- | --- |
| `scripts/check` / `scripts/check.ps1` | Full local gate: protocol drift, gofmt, vet, Go tests (with `third_party`), web typecheck, tests, build |
| `pnpm build:web` then `scripts/embed-web.sh` | Build `web/dist` and copy it for `-tags webdist` |
| `.github/workflows/ci.yml` | protocol-generation, go-quality, go-race, web-quality, macOS and Windows integration, Linux release smoke |
| `.github/workflows/packaging.yml` | Unsigned macOS, Linux and Windows artifacts |
| `scripts/soak.sh` | Soak probes (build tag `soak`) |
| `scripts/gate0`, `scripts/e2e-multicomputer` | Multi-computer cookie Gate 0 page and end-to-end harness (two computers behind an HTTPS front; also covers peer preview on `:8443`, Found on your network, herdr with the fake herdr, checks `H-*`, and GitHub with `scripts/fakegithub`, `scripts/e2e-multicomputer/github.mjs`). The harness builds `pterm` with `-tags webdist,e2e` |
| Build tag `e2e` | Test builds only. `internal/broker/peerdial_e2e.go` replaces the offer dial (`dialHome`) with a `CONNECT` through the harness front when `PTERM_E2E_FRONT` is set, checking the certificate by `PTERM_E2E_SPKI` in place of the pinned-IP guard; `cmd/pterm/lifecycle_e2e.go` makes `pterm setup` check the gateway in `PTERM_E2E_GATEWAY`; `internal/github/github_e2e.go` points the GitHub hosts at the fake in `PTERM_E2E_GITHUB` (and sets the client ID and slug from `PTERM_E2E_GITHUB_CLIENT`/`_APP`), and `cmd/pterm/github_e2e.go` lets the helper answer for that host. Production builds do not compile these files; a unit test and a harness check confirm that a production binary does not contain `PTERM_E2E` |
| `scripts/docs-screenshots` | Demo-only screenshots for the docs site |

## 9. Key invariants

- The gateway never spawns a shell and never opens SQLite. It has no PTY or Store.
- Only the broker writes to a PTY, through one writer per session.
- No client-generated terminal-query replies reach the child. The web xterm has `disableStdin: true`.
- Clients get normalized frames, never raw child output. Terminal bytes stay out of React state.
- A patch applies only to its exact base revision; else the client asks for a snapshot.
- No offline input queue and no resend after reconnect. Reconnect starts as observer.
- The broker rechecks credential, Origin binding and lease on every call. The gateway caches no auth decision.
- The preview listener serves a request only from its own origin, or a selection that names an allowed app origin in `Referer`. The app never frames an origin that a peer sent; it computes `https://<host>:8443` from its own list.
- Every bound is enforced: dimensions, messages, paste, history, queues, viewers, uploads. A slow viewer is dropped, not waited on.
- Only credential hashes are stored. No terminal content is written to disk.
- Production listeners are literal IPv4 loopback addresses. Funnel is never used.
- `pterm` never changes Tailscale or startup configuration without an explicit yes.
- Read-only helpers stay read-only: `gitchanges` reads, `projectfiles`, `projecttasks`, `agentstate`.
- Screen text never names a preview port or chooses answer keys; the OS and a fresh parse do.
- Protocol changes go through `protocol/v1` and regeneration; the drift check must pass.

## 10. Where to read next

| ADR | One line |
| --- | --- |
| [0001](decisions/0001-go-typescript-stack.md) | Go host runtime, TypeScript phone app |
| [0002](decisions/0002-server-authoritative-terminal.md) | The broker's emulator is the truth; clients get frames |
| [0003](decisions/0003-private-tailscale-networking.md) | Loopback gateway, private access through Tailscale Serve |
| [0004](decisions/0004-broker-owned-sqlite.md) | Only the broker owns SQLite |
| [0005](decisions/0005-multi-computer.md) | Multi-computer through direct same-site links, per-device origin binding |
| [0006](decisions/0006-peer-preview.md) | Preview for sessions on peers: `Referer` as the app-origin proof on the selection, same-origin-only preview listener, a peer's frame is untrusted |
| [0007](decisions/0007-found-on-network.md) | Found on your network: a waiting new computer pushes its own bound grant to the home it names; the app adds it with **Add** |
| [0008](decisions/0008-herdr-provider.md) | herdr on your phone: a read-only provider seam, in-memory provider sessions that attach the real herdr client, no database change |
| [0009](decisions/0009-provider-manage.md) | Manage a provider from the phone: one manage route, never force, a close ends one workspace, the broker downloads nothing |
| [0010](decisions/0010-github-integration.md) | GitHub login in the broker (device flow, one token pair for each computer), and a phone route that clones a repository and registers one project root |

Design docs: [agent-state.md](agent-state.md), [changes.md](changes.md),
[live-folder.md](live-folder.md), [notifications.md](notifications.md),
[preview.md](preview.md), [peer-preview.md](peer-preview.md),
[project-tasks.md](project-tasks.md),
[multi-computer.md](multi-computer.md),
[found-on-network.md](found-on-network.md),
[herdr-provider.md](herdr-provider.md),
[p4-authentication-design.md](p4-authentication-design.md),
[threat-model.md](threat-model.md). Status and gaps:
[progress.md](progress.md), [pending-work.md](pending-work.md),
[owner-verification.md](owner-verification.md),
[release-checklist.md](release-checklist.md).

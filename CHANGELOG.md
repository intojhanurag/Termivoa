# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Project buttons: the Snips sheet shows a **This project** section with the
  commands the session's project folder defines, grouped as test, dev, build,
  lint and other. **Run in new tab** starts a session in the same project and
  shell that types the command once as its "run first" command; **Type here**
  types it into the current session, only with control and when no agent is
  detected. Host side: `tasks.list`/`tasks.listed` read the folder's
  `package.json` scripts (npm, pnpm, yarn or bun, picked from the lockfile),
  `Makefile` targets, and Cargo and Go defaults, with bounded file reads and
  nothing executed. Any viewer may ask; no lease is needed.
- Line comments in the Changes file view: tap a diff line, or two lines for a
  range of up to 20, then **Ask agent about lines 40–42** appends
  `@path lines 40–42:` and the quoted lines to the composer draft, never
  sending.

## [0.1.0-beta.2] - 2026-09-29

Second private beta. Updating from beta.1 restarts Termivoa on the computer,
which ends running sessions.

### Added

- Wall on the phone: a **Wall** button on the Sessions list shows every
  running session as a small, scaled-down live screen with its name, status
  badge and link dot. Waiting sessions come first. Tiles are read-only (they
  never take control, type or resize) and connect only while the Wall is
  open; a tap opens the session.
- Changes view, host side: `changes.list` and `changes.file` on the live
  stream return the uncommitted `git` changes of the session's folder (a file
  list with +/− counts) and one file's diff, read with bounded, read-only git
  commands off the session actor. Any paired device viewing a session can read
  these diffs. See [docs/changes.md](docs/changes.md).
- Commit and push from the phone, host side: `changes.commit` commits exactly
  the selected paths of a fresh listing (other staged files stay staged) and
  `changes.push` pushes the current branch, never forced. Controller only;
  the repository's hooks run. See [docs/changes.md](docs/changes.md#commit-and-push).
- `agentStatus.workedSeconds`: total time a session spent working in this
  broker's lifetime, for the phone's "while you were away" recap.
- Changes view on the phone: a **changes** action in both docks opens the file
  list (status, +/− counts) and each file's diff with line numbers. **Ask
  agent about this file** adds `@path` to the composer draft, never sending.
- Commit and push on the phone: file checkboxes, a suggested message, result
  states with git's own output, a push confirmation, and **Open pull request**
  when the remote prints a link. A write in flight is never resent after a
  reconnect.
- "While you were away" card on the live screen: time away, time the agent
  worked, changed files and waiting state, from broker data only.
- Swipe (or trackpad scroll) on the live terminal scrolls full-screen apps
  such as Claude Code, Codex, `vim` and `less`, as wheel steps. Controller
  only, off while the keyboard is open, at most 4 steps per move.
- `gateway.log` records one line per phone connection: its length and why it
  ended. No identities or terminal content.

### Changed

- Reconnect after the phone was away: a socket that still answers a screen
  resync within 2 s is kept; a connect that is not live within 5 s is dropped
  and retried; no retries while the page is hidden (the visibility, pageshow
  and online events reconnect at once); settings and session-list reads give
  up after 6 s. A reconnect under 1.5 s shows only the amber dot.

## [0.1.0-beta.1] - 2026-09-28

First **private beta**: unsigned binaries for invited testers only, published
as a prerelease in the private repository. Install steps are in
[docs/install.md](docs/install.md#private-beta). Development history and
evidence are in [docs/progress.md](docs/progress.md) and
[docs/evidence/](docs/evidence/README.md).

### Added

#### Linux host

- Linux (Ubuntu 22.04/24.04 desktop, server/VPS and WSL; x86_64 and arm64) is
  a supported host. Not yet verified on real hardware: see
  `docs/owner-verification.md`.
- `pterm setup` registers `phone-terminal-broker.service` and
  `phone-terminal-gateway.service` as systemd **user** units in
  `~/.config/systemd/user` (gateway `Wants=`/`After=` the broker,
  `Restart=on-failure`) and shows the exact files and `systemctl --user`
  commands first. `pterm uninstall` disables and removes them; `pterm stop` and
  `pterm shutdown` print the exact `disable --now` command; `pterm launch` uses
  `systemctl --user start`.
- A separate setup question runs `loginctl enable-linger <user>` so a server
  keeps sessions after logout and starts them at boot; never without `y`, never
  with `sudo`. `pterm doctor` shows the linger state.
- Without a systemd user manager (WSL without systemd, containers) setup and
  doctor say so and start the processes like `pterm launch`. In WSL they
  explain `/etc/wsl.conf` `[boot] systemd=true` and that WSL ends sessions when
  its VM stops.
- XDG paths: data in `~/.local/share/phone-terminal`, logs in
  `~/.local/state/phone-terminal/logs`, sockets in
  `/run/user/<uid>/phone-terminal`.
- Linux shell profiles: `$SHELL`, bash, zsh and fish as login shells, when
  listed in `/etc/shells`. Store migration 7 adds the `linux` platform.
- Dev-server preview on Linux reads `/proc/net/tcp{,6}` and `/proc/<pid>/fd`;
  `lsof` is no longer needed.
- `tailscale` is also found in `/usr/bin` and `/usr/sbin`. When Linux allows
  only root or the Tailscale operator to change Serve, setup and doctor name
  `sudo tailscale set --operator=$USER` instead of asking.
- `scripts/install-linux.sh` installs `~/.local/bin/pterm` and runs
  `pterm setup`; `packaging/linux/build.sh` builds `pterm-linux-amd64` and
  `pterm-linux-arm64` with the embedded web app. CI builds both and runs the
  whole Go suite on Ubuntu; the packaging workflow uploads them as private
  artifacts.

#### Live folder

- Tabs and the Sessions list show each running session's live folder name
  instead of the project name it started with; the Live status line shows the
  path (`~/src/app`). Renamed sessions keep their name.
- The broker publishes `cwd` on the session list and `session.state`: on macOS
  and Linux from the foreground process (`lsof` / `/proc`), on Windows
  PowerShell from a session-scoped prompt hook (OSC 9;9), and from OSC 7 /
  OSC 9;9 reports on any host. Session records gain `titleIsCustom`. See
  `docs/live-folder.md`.

#### Dev-server preview

- The broker finds TCP ports that a session's own processes listen on
  (macOS: process tree + `lsof`; Windows: Job Object + `GetExtendedTcpTable`)
  and publishes them as `previews` on the session list and `session.state`.
- The Live screen shows a **Preview ready** chip; **Open ↗** shows the page
  full screen in the app with back, reload, the agent status and **Tell
  agent**.
- The gateway serves previews from a separate loopback listener
  (127.0.0.1:8789; development 127.0.0.1:8790, `--preview-listen`) that never
  serves the app. It re-checks the paired-device cookie and port ownership on
  every request, rewrites Host to `localhost:<port>`, proxies WebSockets (HMR),
  and never forwards Termivoa cookies or identity headers. The app frames only
  that origin and refuses requests from it.
- `pterm setup` asks a second, separate question before it runs
  `tailscale serve --bg --https=8443 http://127.0.0.1:8789`; `pterm doctor`
  reports it. See `docs/preview.md`.


#### Sessions

- Broker-owned PTY sessions that keep running while phones and desktop
  terminals connect and disconnect.
- Several sessions at once, each started from a saved project and shell profile.
- On Windows, a separate **WSL bash** shell profile when a WSL distro is
  installed. It opens in the project folder and never replaces PowerShell.
- Server-authoritative terminal state: clients receive screen snapshots and row
  patches, and send structured input through one writer per session.
- One controller per session, with explicit **Take control** and release.
- Bounded, paged scrollback history.
- Session metadata in SQLite; after a broker restart, old live sessions are
  marked lost and their commands are never re-run.
- Agent state per running session (`working`, `waiting`, `idle`), computed by
  the broker from the screen. When Claude Code, Codex or a numbered or y/n
  prompt waits for an answer, the session list and the live `session.state`
  notice carry the question, what it approves (such as the command) and its
  options, and the controller can answer with `input.answer`. The broker
  rejects a stale answer with `question_changed`. See [docs/agent-state.md](docs/agent-state.md).

#### Pairing and devices

- `pterm pair` with a single-use, two-minute link and QR code; the owner
  approves after comparing the code shown on the phone.
- `pterm devices`, `pterm devices rename` and `pterm revoke` (takes effect at
  once, including on open connections).

#### Phone UI

- Phone web app (with a manifest for Home Screen use) served by the gateway
  from embedded assets.
- Session list with project, shell, last-output preview and a new-output dot.
- Session tabs, a key bar (Esc, Tab, Ctrl, Alt, arrows), composer for long or
  multi-line text, history with copy, and settings.
- Keyboard-up layout: tabs and the status line hide, all keys fold into one
  scrolling row, and the PTY keeps its size while the view follows the cursor,
  so full-screen agents do not redraw when the keyboard opens or closes.
- No blank screens: an unexpected error shows its message and **Reload**;
  requests stop waiting after 15 seconds; the offline screen and the offline
  page retry by themselves and name what to check (Tailscale, a sleeping
  computer).
- One terminal-style layout for the offline, error, update-required and
  session-lost screens; a small **update** pill on Sessions replaces the
  full-width update banner.
- Answer card on the Live screen: when the agent waits on a question, a
  bottom card shows the question, what it approves and one big button per
  option. One tap sends one `input.answer`; nothing answers or retries on its
  own. A read-only phone sees **Take control to answer**. **terminal** folds
  the card to an **↑ answer** pill (it also folds while the keyboard is up).
  A `question_changed` or `invalid_request` rejection keeps control and
  reuses the refused input sequence.
- Compose sheet toolbar: **Photo**, **Voice** and **Send ⏎**. Send pastes
  the draft and presses Enter in one tap, with the same 64 KiB limit and
  multi-line confirmation as before.
- **Photo** (controller only): the phone redraws the picked or taken photo
  on a canvas, which drops EXIF and GPS data, and shrinks the long edge to
  2048 px (PNG stays PNG, other photos become JPEG). The broker saves it as
  `.termivoa/uploads/<time>-<random>.<ext>` in the session's project folder,
  adds `.termivoa/.gitignore` (`*`) so uploads are never committed, and the
  draft starts with `@.termivoa/uploads/<name> ` for Claude Code or Codex.
  The ✕ removes it from the message only; the file stays on the computer.
  New route `POST /api/v1/sessions/{id}/uploads`.
- **Voice**: dictation with the browser's speech service (Web Speech API),
  with live text, a listening clock and clear error messages. **Done · edit**
  adds the words to the draft and never sends. Where the API is missing, the
  composer shows "Tip: use the keyboard's mic to dictate."

#### Notifications

- Private Web Push notifications, sent by the broker: a one-time
  "Get a buzz when an agent needs you" card on Sessions, an on/off switch and
  **Send a test notification** in Settings. Lock-screen text is only the
  session name and status ("… is waiting for you", "… is done · your turn");
  tapping opens that session. iPhone needs the Home Screen app.
- RFC 8291 encryption and RFC 8292 VAPID with the Go standard library only;
  an exact push-service host allow-list (SSRF guard); per-device and total
  subscription caps; revoke and logout delete a phone's subscriptions.
- Agent notifications are live: "… is waiting for you" once per new question,
  and "… is done · your turn" when an agent goes idle after working at least
  20 seconds. Exited or lost sessions never notify.
- Agent status carries `afterWork` on an idle that followed work, so the
  session list shows **Your turn** after a reload too.

#### Agent keys, snippets and presets

- Agent keys: one-tap Esc, Ctrl+C, Shift+Tab, Up, `1`, `2`, `y` and Enter for
  coding-agent prompts.
- Snippets: saved prompts and commands, sent with or without Enter.
- Launch presets with an optional **run first** command, typed once when the
  session opens and only if no one else has control.

#### Desktop attach

- `pterm attach` opens a session in any desktop terminal, read-only by default;
  `--control` takes control and Ctrl+] detaches without ending the session.
- `pterm list`, `pterm new`, `pterm terminate`, `pterm project` and
  `pterm profile` management commands.

#### Setup and packaging

- `pterm setup`: read-only checks, one confirmation, per-user startup
  registration (LaunchAgents on macOS, Scheduled Tasks on Windows), Tailscale
  phone address, an opt-in `tailscale serve` step, and pairing.
- `pterm doctor`, `pterm launch`, `pterm stop`, `pterm shutdown` and
  `pterm uninstall`.
- Unsigned macOS app and DMG build scripts and a Windows per-user installer
  script, with separate owner-only signing scripts.

### Changed

- The product is renamed to **Termivoa** (working name: Phone Terminal): web
  app title and home-screen name, CLI messages, docs, `Termivoa.app`, the DMG
  and the Windows installer. The `pterm` command, data directories, startup
  entries, cookies and the database file keep their old names so existing
  installs keep working (`docs/naming.md`).
- Security reports and Code of Conduct reports go to aojharaj2004@gmail.com.

### Fixed

- Linux: phone streams were held to about 2 MB/s, because the 16 KiB send
  buffer of the live socket is below two loopback segments and every write
  waited for a delayed ACK. Linux now uses 128 KiB.
- A phone that started its next photo upload as soon as the previous one
  finished could get "upload limit reached": the upload slot is now freed
  before the final reply.
- Store migrations run with foreign keys off and check every foreign key
  before they commit, as SQLite's table-rebuild procedure requires.

- The phone reconnects in seconds after a lock, backgrounding or network
  switch: the live link retries at most every 5 s and at once when the app
  comes back or goes online, and replaces a possibly dead socket after 10 s
  or more hidden (as an observer; input is never resent). The offline screen
  shows only after two failed tries and retries after 1, 2, 4, 8, then every
  10 s; the service worker offline page reloads every 5 s.
- Windows: SQLite opens with a valid `file:///C:/...` URI (UNC paths use
  `file:////server/share/...`); private directories get one current-user ACE
  that the check accepts; folders left by an elevated run are taken back for
  the current user.
- `pterm attach` on Windows: a Ctrl+] pressed during the startup handoff now
  detaches, as on macOS and Linux. Before, it was lost.
- Windows: the startup broker and gateway write `broker.log` and `gateway.log`
  in `%LOCALAPPDATA%\Phone Terminal\logs\`; the headless console discarded
  their output.
- Windows: startup tasks run at normal priority (was below normal, which
  sessions inherited).
- Windows: a disabled startup task counts as not registered, so
  `pterm setup --apply` creates it again, enabled; `pterm doctor` reports it.
- Windows: `pterm doctor` and `pterm setup` also find Tailscale in
  `%ProgramFiles%\Tailscale\tailscale.exe`.
- Windows: console close, logoff and shutdown stop pterm cleanly.
- The gateway waits up to two minutes for the broker at start instead of
  exiting at once.
- Windows: `pterm uninstall` also stops the gateway and the broker (only with
  no active session); ending a task may leave pterm running.

### Security

- Gateway bound to loopback; phone access only through private Tailscale Serve
  HTTPS. Funnel is never enabled and is reported by `pterm doctor`.
- `__Host-`, `HttpOnly`, `SameSite=Strict` session cookies, strict Origin and
  Host checks, and a content security policy.
- Bounds on message sizes, paste, history, queues and viewers.
- Photo uploads: controller-only, rechecked before the write; type from magic
  bytes (JPEG, PNG, WebP, GIF) only; at most 5 MiB; server-chosen names
  created with `O_EXCL` and mode 0600 (current-user ACL on Windows) inside the
  project folder through `os.Root`; a symlinked or non-directory `.termivoa`
  or `uploads` is refused; one upload per device at a time and 12 per minute.
- Fixes from adversarial reviews before the v0 merge, including overlapping
  polls, duplicate snippet IDs, hidden SGR-8 text in previews and an
  unannounced pairing code for screen readers.

### Known limits

- Real iPhone, Android, native Windows 11, real Tailscale Serve HTTPS and
  signing are not yet verified; see
  [docs/owner-verification.md](docs/owner-verification.md).

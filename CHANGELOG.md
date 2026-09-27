# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project will
use [Semantic Versioning](https://semver.org/) once releases begin.

## [Unreleased]

No version has been released. Development history and evidence are in
[docs/progress.md](docs/progress.md) and [docs/evidence/](docs/evidence/README.md).

### Added

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

### Fixed

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
- Fixes from adversarial reviews before the v0 merge, including overlapping
  polls, duplicate snippet IDs, hidden SGR-8 text in previews and an
  unannounced pairing code for screen readers.

### Known limits

- Real iPhone, Android, native Windows 11, real Tailscale Serve HTTPS and
  signing are not yet verified; see
  [docs/owner-verification.md](docs/owner-verification.md).

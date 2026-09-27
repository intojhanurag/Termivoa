# Owner verification checklist (v0)

Date: 2026-09-27. Host used for agent testing: Apple Silicon Mac, macOS 26.5.2,
Go 1.27.1, Node 24.11.0.

This file lists every v0 item that the agent could not test. Each item needs
your hardware, your accounts or your approval. The per-milestone evidence files
in `docs/evidence/` have the full details.

Mark an item `[x]` when you finish it. Record the result (pass or fail, device,
OS and browser version) next to it.

## 0. Run hosted CI on `main`

GitHub Actions does not start any job, because the account has a billing or
spending-limit problem. On 2026-09-27 the owner merged #27 (v0, which contains
#23–#26), #28 (first run) and #30 (phone UI v2) into `main` without hosted CI,
after the full local check passed on macOS. See `docs/progress.md`.

- [ ] Fix billing: GitHub → Settings → Billing and plans.
- [ ] Re-run CI on `main`. Native Windows tests, the Linux `/proc` path and
      the packaging workflow have never run in CI. Fix any failure in a
      follow-up PR.
- [ ] The first CI run of `.github/workflows/packaging.yml` is the first real
      build of the Windows installer (`packaging/windows/*`). Check that it
      passes.

## 1. Local demo on this Mac (5 minutes, no phone)

The agent ran this flow in a desktop browser at phone size. You can repeat it:

```sh
cd ~/phone-terminal
pnpm install --frozen-lockfile
pnpm build:web && ./scripts/embed-web.sh
go build -tags webdist -o bin/pterm ./cmd/pterm
mkdir -p /tmp/pt/data /tmp/pt/run && chmod 700 /tmp/pt/data /tmp/pt/run
./bin/pterm up --dev --data-dir /tmp/pt/data --runtime-dir /tmp/pt/run
```

In a second terminal:

```sh
./bin/pterm project add --dev --runtime-dir /tmp/pt/run --name demo ~/some-project
./bin/pterm config origin --dev --runtime-dir /tmp/pt/run http://127.0.0.1:8788
./bin/pterm pair --dev --runtime-dir /tmp/pt/run
```

- [ ] Open the printed link in a browser. Compare the code with
      `pterm pair pending`, then run `pterm pair approve <pending-id>`.
- [ ] Start a session, press **Take control**, type a command.
- [ ] In a terminal: `./bin/pterm attach --dev --runtime-dir /tmp/pt/run <session-id>`.
      You see the same screen. Ctrl+] detaches, and the session keeps running.

## 2. Real phone over Tailscale (the 3-step first run)

The agent did not change Tailscale or startup settings on this Mac. Follow
[getting-started.md](getting-started.md):

- [ ] Build and install (DMG or from source), then run `pterm setup`.
      Check the plan it prints, answer `y` once, and answer `y` to the separate
      Tailscale Serve question only if it is correct for you. Never use Funnel.
- [ ] Setup saved `https://<your-mac>.<tailnet>.ts.net` as the phone address
      (`pterm config origin` shows it) and added the project "Home".
- [ ] Confirm that Tailscale Serve keeps the original `Host`
      (`<your-mac>.<tailnet>.ts.net`) when it forwards to `127.0.0.1:8787`.
      The gateway fix assumes this.
- [ ] Scan the QR code on the phone over cellular data, compare the code, and
      answer `y` on the Mac. The phone opens the session list.
- [ ] Log out of your Mac and back in: the broker and gateway start again, and
      the phone still works without pairing again.
- [ ] Run Claude Code or Codex in a session from the phone (`command claude`
      if your shell aliases add extra flags). Check that fast output stays
      connected on cellular (bandwidth fix, `docs/evidence/p7/soak.md`).

### iPhone Safari and Home Screen app

- [ ] Pairing works, and the `__Host-pterm_session` cookie is set and sent on the
      WebSocket. Logout clears it.
- [ ] Safari allows the same-origin `wss:` WebSocket under the CSP.
- [ ] Add to Home Screen: name, icon, standalone launch, dark theme.
- [ ] With the keyboard open, the cursor row and the action bar stay visible
      (portrait and landscape).
- [ ] Keyboard-up layout: tabs and the status line hide, one key row shows,
      and the size in the status line (for example `46×35`) is the same before
      and after the keyboard opens. Claude Code does not redraw when the
      keyboard opens or closes. Tested only in a desktop browser at phone size.
- [ ] IME (Japanese, Chinese, Korean), dictation and emoji commit each word once.
      Autocorrect and capitalization stay off.
- [ ] Key bar keeps the keyboard open. Ctrl then `c` sends Ctrl+C. A hardware
      keyboard sends arrows, Tab, Shift+Tab, Esc and F-keys.
- [ ] History: swipe opens it, long-press selection and Copy work, and the
      selection stays stable while output arrives.
- [ ] Background the app and come back: it reconnects as observer, you must
      press Take control again, and no text is sent again.
- [ ] CJK, emoji, ZWJ sequences and combining marks line up with the host
      columns at 14 px and at other sizes.
- [ ] Service worker update banner and the offline "Connection unavailable" page.

### Android Chrome (Gboard and one other keyboard)

- [ ] Install prompt, icon (maskable crop), standalone launch.
- [ ] Keyboard overlap with `interactive-widget=resizes-content`.
- [ ] IME and swipe typing commit once. Backspace works on an empty field. Enter
      sends one `enter` key.
- [ ] History, backgrounding and reconnect as above. Width agreement as above.
- [ ] After rotation or font-size change, the controller resize matches the
      visible area. Observers do not resize the PTY.

### Bandwidth on cellular (soak risk P7-SOAK-1)

- [ ] Run a command that scrolls fast (for example `yes | head -n 200000`) with
      the phone on cellular. On the Mac, a scrolling screen sent about 6 MiB/s
      to each viewer. Check whether the phone is dropped as "too slow" and
      reconnects again and again. See `docs/evidence/p7/soak.md`.

### UI v2 on a real phone (tabs, agent keys, snippets, run first)

- [ ] The agent keys row fits and every key is easy to hit with a thumb (it
      wraps to two rows below 390 px wide, for example on a 375 px iPhone).
- [ ] `esc`, `⌃C`, `⇧⇥`, `↑`, `1`, `2`, `y`, `⏎` work in Claude Code and Codex.
- [ ] Session tabs switch sessions; the new-output dot appears and clears.
- [ ] Snippets send once; a multi-line snippet asks before it pastes.
- [ ] A launch preset with "run first" (for example `claude`) starts the
      agent once, and never again after reconnecting.
- [ ] VoiceOver / TalkBack read the key names ("Control C", "Shift Tab") and
      the pairing code.
- [ ] iPhone: answer a Claude Code permission prompt from the answer card.
      The card shows the command, one tap answers once, and the card closes.
- [ ] iPhone: a question that changes just before the tap refuses the old
      answer. The card says "The question changed — check the new one", and
      the phone keeps control.

### Notifications (Web Push)

Real delivery cannot be tested without a phone. See `docs/notifications.md`.

- [ ] iPhone Home Screen app (iOS 16.4+): the Sessions card
      "Get a buzz when an agent needs you" appears; **Turn on** asks for
      permission; Settings → **Send a test notification** arrives.
- [ ] iPhone, phone locked: a Claude Code permission prompt buzzes
      "`<title>` is waiting for you" (`<project> · tap to answer`). No
      terminal text is shown.
- [ ] iPhone, phone locked: a task that runs about 1 minute ends with
      "`<title>` is done · your turn" (`Worked for 1 min`). A quick shell
      command (under 20 s) does not buzz.
- [ ] After reloading the app, a finished agent session with output you have
      not opened still shows **Your turn**; opening it clears the pill.
- [ ] iPhone Safari in a normal tab shows no card, and Settings says
      "Add to Home Screen to get notifications".
- [ ] Android Chrome: the same card, test, "waiting" and "done" checks.
- [ ] Tapping a notification opens the app at that session
      (`/sessions/<id>`), both with the app closed and with it open on
      another screen.
- [ ] `pterm revoke <device-id>` (and, separately, logging out on the phone)
      stops notifications to that phone.
- [ ] Apple accepts the VAPID subject `https://github.com/intojhanurag/phone-terminal`
      (a `403 BadJwtToken` from Apple would mean it does not).

### Revocation

- [ ] `pterm devices`, then `pterm revoke <device-id>` while the phone has
      control. The phone goes back to Setup and cannot send more input.

## 3. Windows 11 x64

Only cross-compiled `go vet` and PE checks ran here. CI has a native Windows job,
but billing blocked it.

- [ ] Broker, gateway and admin commands run natively. PowerShell and pwsh
      profiles are detected.
- [ ] WSL: with a distro installed, `pterm profile detect` adds **WSL bash**
      after the PowerShell profiles. A session from it opens the distro shell
      in the project folder (`pwd` shows `/mnt/c/...`). Without a distro, or
      without WSL, no WSL profile appears.
- [ ] ConPTY: one response per terminal query; shutdown has no deadlock.
- [ ] Broker starts: run `pterm.exe __broker` in a normal (not elevated)
      prompt and leave it in the foreground; it keeps running with no SQLite
      or ACL error. Then `icacls "%LOCALAPPDATA%\Phone Terminal"` and
      `icacls "%LOCALAPPDATA%\Phone Terminal\runtime"` show only the current
      user, and the owner (`dir /q "%LOCALAPPDATA%"`) is the current user.
      Stop it, run it once from an elevated prompt, stop it, and run it again
      unelevated: it still starts.
- [ ] Session environment: set a user variable such as `JAVA_HOME` and add a
      folder to the user `Path` (System Properties, then sign out and in).
      Start pterm from Windows Terminal or VS Code, create a session, and run
      `Get-ChildItem env:`. `JAVA_HOME` and the user `Path` folder are there;
      `WT_SESSION`, `TERM_PROGRAM`, `VSCODE_*` and `CLAUDE_CODE_*` are not.
      Also run `go test ./internal/ptyhost/ -run HostEnvironment`.
- [ ] Desktop attach: console modes are restored after exit.
- [ ] Desktop attach: press Ctrl+] right as the first screen appears (the
      startup handoff). It detaches on the first press, and the session keeps
      running.
- [ ] Run the unsigned installer: per-user location, PATH opt-in (off by
      default), Start-menu shortcut, `pterm setup --apply --yes`.
- [ ] Scheduled Tasks start at logon and restart on failure. `conhost.exe
      --headless` shows no window and passes the exit code. Task Manager >
      Details shows `pterm.exe` and session shells at Normal priority.
- [ ] Logs: after logon, `%LOCALAPPDATA%\Phone Terminal\logs\broker.log` and
      `gateway.log` each get a "started" line; a failed start writes its error
      there.
- [ ] Slow broker at logon: give the broker task a 60 s logon delay in Task
      Scheduler, sign out and in. The gateway waits, then becomes healthy
      without a restart (`gateway.log` shows one start).
- [ ] Disable a task with the command `pterm stop` prints: `pterm doctor`
      reports it disabled and `pterm setup --apply` enables it again.
- [ ] Clean uninstall, with and without data deletion. With no active session,
      `pterm uninstall` leaves no `pterm.exe` running (Task Manager), and
      `--delete-data` succeeds.

## 4. macOS startup and install

- [ ] `pterm setup --apply` registers the two LaunchAgents. After logout and
      login, both run. Kill one: launchd restarts it.
- [ ] Open the unsigned DMG and run the app (`pterm launch`). Gatekeeper warns,
      because the app is not signed.
- [ ] `pterm uninstall` removes startup entries. `--delete-data` also removes data.

## 5. Signing and release (needs your credentials)

- [ ] Apple: Developer ID Application identity + notarytool keychain profile,
      then `packaging/macos/sign-and-notarize.sh`.
- [ ] Windows: code-signing certificate thumbprint + timestamp URL, then
      `packaging/windows/sign.ps1`.
- [ ] Check Gatekeeper and SmartScreen with the signed artifacts.
- [ ] Final product name, bundle IDs and publisher identity (still placeholders;
      proposed public name: Termivoa, see `docs/release-checklist.md`).
- [ ] Public release stays your manual decision (plan section 20).

## 6. Known open defects
- [ ] Approve or reject: terminate/shutdown now signal every process in the
      managed PTY session (not only the process group). It never signals
      processes outside that session; processes that call setsid stay outside.
      Plan section 6 describes the process-group boundary. See
      `docs/evidence/p6/cli-lifecycle.md`.
- Performance risks from the Mac soak (`docs/evidence/p7/soak.md`):
  scrolling bandwidth (P7-SOAK-1), CPU spent re-validating outgoing frames
  (P7-SOAK-2), higher latency while scrolling (P7-SOAK-3), and about 33 MiB of
  history per full session (P7-SOAK-4).
- Not run yet: Windows 11 soak, a 24-hour soak, the 10-minute stress run, and
  the 1,000-event key-to-frame p95 < 30 ms benchmark.

- Two earlier unexplained timeouts (real-session retirement, executable broker
  readiness). See `docs/progress.md`.
- The session list does not show which device has control. The HTTP session
  record has no field for it.
- `pterm setup` and `pterm uninstall` refuse to replace the broker while
  sessions are active, but the broker itself cannot yet refuse an update (no
  update RPC; documented in `docs/install.md` as pending). A database backup
  before migration is not implemented.

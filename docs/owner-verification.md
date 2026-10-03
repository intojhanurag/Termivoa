# Owner verification checklist (v0)

Date: 2026-09-27, updated 2026-10-02. Host used for agent testing: Apple Silicon Mac, macOS 26.5.2,
Go 1.27.1, Node 24.11.0.

This file lists every v0 item that the agent could not test. Each item needs
your hardware, your accounts or your approval. The per-milestone evidence files
in `docs/evidence/` have the full details.

Mark an item `[x]` when you finish it. Record the result (pass or fail, device,
OS and browser version) next to it.

## 0. Run hosted CI on `main`

GitHub Actions is off until the repository goes public (see
[pending-work.md](pending-work.md)); PRs merge after the local
`./scripts/check`. It first stopped on a billing or spending-limit problem: on 2026-09-27 the owner merged #27 (v0, which contains
#23–#26), #28 (first run) and #30 (phone UI v2) into `main` without hosted CI,
after the full local check passed on macOS. See `docs/progress.md`.

- [ ] Re-enable GitHub Actions when the repository goes public.
- [ ] Re-run CI on `main`. Native Windows tests, the Linux jobs
      (`go-quality` now runs the broker, CLI and `/proc` tests on Ubuntu;
      `linux-release-smoke`) and the packaging workflow have never run in
      CI. Fix any failure in a follow-up PR.
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
the [quick start](https://termivoa.vercel.app/docs/quick-start/):

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

### Compose: photo and voice

- [ ] iPhone Home Screen app, with control: Compose → **Photo** offers Take
      Photo and Photo Library. Take a photo: the row shows "uploading…", then
      "✓ on your computer · <size>", and the draft starts with
      `@.termivoa/uploads/<name> `.
- [ ] On the Mac, the file is in `<project>/.termivoa/uploads/`, mode 0600,
      at most 2048 px on the long edge, with no GPS or other EXIF data
      (`mdls` or `exiftool` show none), and `git status` in the project does
      not list `.termivoa`.
- [ ] Type "describe this image" after the path and tap **Send ⏎** in a Claude
      Code session: Claude reads the image. Repeat with Codex.
- [ ] A portrait photo is saved upright (EXIF orientation applied before the
      metadata is dropped). A PNG screenshot stays PNG.
- [ ] ✕ removes the row and the `@path` text and shows "Removed from this
      message."; the file stays on the Mac.
- [ ] Without control, **Photo** is off. After another device takes control
      during an upload, the upload fails with "take control before attaching
      a photo" and no file is written.
- [ ] **Voice** in the Home Screen app: iOS asks for microphone and speech
      permission once; words appear (unfinished words dimmed), the status
      shows "LISTENING · 0:07", **Done · edit** adds the text and sends
      nothing. **Cancel** drops it. Locking the phone stops listening.
- [ ] Deny the microphone: the panel says "Microphone access is off…".
- [ ] In a browser without the Web Speech API (for example Firefox), Voice is
      hidden and the composer says "Tip: use the keyboard's mic to dictate."
- [ ] With Reduce Motion on, the mic ring does not pulse.

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

### Dev-server preview (Tailscale Serve 8443)

- [ ] Run `pterm setup` again (or `tailscale serve --bg --https=8443
      http://127.0.0.1:8789` yourself). Check the second question shows that
      exact command and changes nothing on `n`. `pterm doctor` shows
      "HTTPS 8443 proxies dev-server previews".
- [ ] On the iPhone, in a session, ask Claude to run `npm run dev` for a Vite
      project. The **Preview ready** chip with `:5173` appears above the dock
      within a few seconds.
- [ ] Tap **Open ↗**: the page loads inside the app (this confirms that iOS
      Safari sends the `__Host-` cookie to `:8443` in the frame, and that
      Tailscale Serve passes `Host: <host>:8443`). If it shows "Unknown host"
      or "Open the preview from your paired phone", note it: the gateway
      needs a change.
- [ ] Edit a file: Vite HMR updates the page live without reload.
- [ ] Start an unrelated server in a normal Terminal window (outside pterm),
      for example `python3 -m http.server 5288`: it is never offered, and
      `https://<host>:8443/__termivoa/select?session=<id>&port=5288` shows
      "No dev server of this session listens on that port".
- [ ] **Tell agent** returns to the terminal with the composer open; **‹
      terminal** returns without it.
- [ ] Revoke the phone with `pterm revoke <device-id>` while the preview is
      open: HMR stops within 5 s and reload shows "no longer paired".

### Live folder

- [ ] Mac: open a session from the "Home" project, run `cd src` (any
      subfolder). Within about 2 seconds the tab shows `src`, the Live status
      line shows `~/src` and, on the Sessions list, the row title is `src`
      while its second line still reads "Home · zsh".
- [ ] Mac: run `vim` in another folder with `cd /tmp && vim`; the tab shows
      `tmp` while vim runs.
- [ ] Rename the session (… > Rename…) to "build", then `cd ..`: the tab and
      row keep "build".
- [ ] Linux (Ubuntu host, when available): the same `cd` check.

### Revocation

- [ ] `pterm devices`, then `pterm revoke <device-id>` while the phone has
      control. The phone goes back to Setup and cannot send more input.

## 2b. Multi-computer (Gate 0)

Multi-computer is merged (#135) but not yet tested on a real phone. Gate 0
needs a second computer and your approval for a Tailscale Serve port. Steps:
[`scripts/gate0/README.md`](../scripts/gate0/README.md). Port 8443 is already
used by the dev-server preview, so use another free port.

- [ ] iPhone Home Screen app: Gate 0 checks pass.
- [ ] Android Chrome: Gate 0 checks pass.

### Notifications from every computer (Gate 0 check 5, real app)

The agent tested this in desktop Chrome only (unit tests, and
`scripts/e2e-multicomputer/phase-b.mjs` step B3-push with a push delivered
by the DevTools protocol, not by a push service). Several scoped
service-worker registrations in an iOS Home Screen app are not verified.
Needs: the Home Screen app on home (for example `mac`) with one peer added
(for example `vps`), and an agent session on each. Do the list on iPhone,
then on Android Chrome.

- [ ] With notifications off, turn on **Settings → Notifications → Buzz when
      an agent needs you**. One permission prompt shows. No red line
      "No notifications from vps." shows under the switch.
- [ ] Tap **Send a test notification**: the note says **Sent** and two
      notifications arrive, one with the text `Notifications are on.` (home)
      and one with `vps · Notifications are on.`
- [ ] Lock the phone. Let an agent on `vps` ask a question: the notification
      "`<title>` is waiting for you" arrives and its text starts with `vps ·`.
- [ ] Tap that notification with the app closed, then again with the app
      open on another screen: both times the app opens that `vps` session
      (the address is `/c/<vps host>/sessions/<id>`).
- [ ] An agent on home still notifies, and its tap opens `/sessions/<id>`.
- [ ] Close the app, wait one day, and repeat the `vps` question: it still
      arrives (iOS did not drop the second registration).
- [ ] Add a second peer while notifications are on. No permission prompt
      shows. Open the app again, then **Send a test notification**: three
      notifications arrive.
- [ ] Stop Termivoa on `vps`, then open **Settings**: after some seconds
      "No notifications from vps." shows, and a test still arrives from home.
- [ ] **Settings → Computers → Remove** `vps`, then let the agent on `vps`
      ask again: no notification arrives.
- [ ] Turn the switch off: an agent on home and one on a peer both stay
      silent. Turn it on again: both notify again.
- [ ] If any step fails on iPhone, record the iOS version and what Settings
      showed. Home notifications must still work; that is the designed
      fallback.

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
- [ ] Live folder: in a Windows PowerShell 5.1 session and in a PowerShell 7
      session, run `cd C:\Windows`. The phone tab shows `Windows` within about
      2 seconds and the prompt looks exactly as before (no extra characters).
      A profile prompt (oh-my-posh or a custom `prompt` in `$PROFILE`) still
      shows. A renamed session keeps its name. cmd.exe and WSL sessions keep
      the project name.
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

## 3a. Linux (VPS `t3-vps` and one Ubuntu desktop)

The agent ran the Go suite as a normal user in `golang:1.27` (Debian, arm64)
and a full systemd run in a privileged `ubuntu:24.04` container (arm64):
`pterm setup --apply --yes`, doctor, a real bash session, `pterm stop` /
`launch`, `kill -9` of the broker (systemd restarted it; the session became
`lost` and its processes ended), `pterm shutdown`, a restart of the user
manager with linger (both units came back), and `pterm uninstall
--delete-data`. Not tested: real hardware, x86_64, a real SSH logout, polkit,
Tailscale, a phone, zsh and fish.

Install on each machine as your normal user (not root):

```sh
./packaging/linux/build.sh                 # on the build computer
scp dist/linux/pterm-linux-amd64 t3-vps:   # use -arm64 for an arm64 VPS
ssh t3-vps ./pterm-linux-amd64 help        # smoke test
# on the host, in a source checkout or with the script copied next to the binary:
./scripts/install-linux.sh ./pterm-linux-amd64
```

Server `t3-vps` (SSH only):

- [ ] `pterm setup` shows the two unit files and the `systemctl --user`
      commands before it asks. After `y`: `systemctl --user is-enabled
      phone-terminal-broker phone-terminal-gateway` prints `enabled` twice,
      and `pterm doctor` shows both registered.
- [ ] The linger question appears (only when linger is off). Answer `y`:
      `loginctl show-user $USER --property=Linger` shows `Linger=yes`. If it
      fails with "Access denied", setup prints the `sudo loginctl
      enable-linger` command; record which one happened.
- [ ] Start a session from the phone (or `pterm new`), run `sleep 600`, log out
      of every SSH session, wait 2 minutes, log in again: `pterm list` shows it
      still `running`.
- [ ] `sudo reboot`: after the boot, before you log in, the phone can reach
      the page (linger starts the units). Old sessions show as `lost`.
- [ ] Tailscale: with no operator set, `pterm setup` names
      `sudo tailscale set --operator=$USER` and does not ask the Serve
      questions. Run it, rerun `pterm setup`: both Serve questions appear and
      work; `https://t3-vps.<tailnet>.ts.net/` pairs a phone.
- [ ] Dev-server preview: in a session run `python3 -m http.server 3000
      --bind 127.0.0.1`; the phone shows **Preview ready** without `lsof`
      installed (`command -v lsof` prints nothing).
- [ ] `kill -9` the broker: systemd starts it again within about 5 s
      (`journalctl --user -u phone-terminal-broker`); sessions are `lost`.
- [ ] `pterm stop` prints `systemctl --user disable --now
      phone-terminal-gateway.service`; `pterm shutdown` stops both, and they
      stay stopped (`systemctl --user is-active` prints `inactive`).
- [ ] `pterm uninstall --delete-data` removes both units and
      `~/.local/share/phone-terminal`, `~/.local/state/phone-terminal` and
      `/run/user/<uid>/phone-terminal`, nothing else.

Ubuntu desktop (22.04 or 24.04):

- [ ] Same install and `pterm setup`. After logout and login both units run.
- [ ] Profiles: `pterm profile list` shows your `$SHELL` first; with zsh and
      fish installed (`sudo apt install zsh fish`, then `pterm profile
      detect`) both appear as login shells. In a session, `echo
      $XDG_RUNTIME_DIR` is set and `systemctl --user status` works.
- [ ] `pterm launch` opens the page in the default browser.
- [ ] Live folder: `cd /usr/share` in a session; the phone tab shows `share`.

WSL (optional):

- [ ] Ubuntu in WSL without systemd: `pterm setup` says startup is not
      available, explains `/etc/wsl.conf`, and starts the processes. With
      `systemd=true`, it registers the units like a desktop.

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
- [x] Final product name: Termivoa (decided 2026-09-27). Bundle IDs keep
      `dev.phone-terminal.*` for compatibility (`docs/naming.md`).
- [ ] Publisher identity for signing.
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
- The session list shows whether you, another device or no one has control,
  but not which other device.
- `pterm setup` and `pterm uninstall` refuse to replace the broker while
  sessions are active, but the broker itself cannot yet refuse an update (no
  update RPC; documented in `docs/install.md` as pending). A database backup
  before migration is not implemented.

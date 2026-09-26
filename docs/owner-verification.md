# Owner verification checklist (v0)

Date: 2026-09-27. Host used for agent testing: Apple Silicon Mac, macOS 26.5.2,
Go 1.27.1, Node 24.11.0.

This file lists every v0 item that the agent could not test. Each item needs
your hardware, your accounts or your approval. The per-milestone evidence files
in `docs/evidence/` have the full details.

Mark an item `[x]` when you finish it. Record the result (pass or fail, device,
OS and browser version) next to it.

## 0. Unblock the pull requests

GitHub Actions did not run any job, because the account has a billing or
spending-limit problem. Branch protection needs the 10 CI checks, so the PRs
cannot merge.

- [ ] Fix billing: GitHub → Settings → Billing and plans.
- [ ] Re-run CI on the PRs in this order: #23 (P3) → #24 (P4) → #25 (P5) →
      #26 (P6) → the v0 polish PR. Merge each one after its checks pass, then
      rebase the next PR on `main`.
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

## 2. Real phone over Tailscale (needs your approval)

The agent did not change Tailscale or startup settings (AGENTS.md rule 12).

- [ ] Run `pterm setup` and read the checks. It prints the Tailscale Serve command
      but does not run it. Never use Funnel.
- [ ] Run `tailscale serve --bg --https=443 http://127.0.0.1:8787` yourself.
      Confirm that Serve sends the original `Host` (for example
      `mac.tailnet.ts.net`) to the gateway.
- [ ] Start the gateway with `--origin https://<host>.ts.net`, or run
      `pterm setup --apply` to register startup.
- [ ] `pterm config origin https://<host>.ts.net`, then `pterm pair`. Scan or
      open the link on the phone over cellular data.

### iPhone Safari and Home Screen app

- [ ] Pairing works, and the `__Host-pterm_session` cookie is set and sent on the
      WebSocket. Logout clears it.
- [ ] Safari allows the same-origin `wss:` WebSocket under the CSP.
- [ ] Add to Home Screen: name, icon, standalone launch, dark theme.
- [ ] With the keyboard open, the cursor row and the action bar stay visible
      (portrait and landscape).
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

### Revocation

- [ ] `pterm devices`, then `pterm revoke <device-id>` while the phone has
      control. The phone goes back to Setup and cannot send more input.

## 3. Windows 11 x64

Only cross-compiled `go vet` and PE checks ran here. CI has a native Windows job,
but billing blocked it.

- [ ] Broker, gateway and admin commands run natively. PowerShell and pwsh
      profiles are detected.
- [ ] ConPTY: one response per terminal query; shutdown has no deadlock.
- [ ] Desktop attach: console modes are restored after exit. A Ctrl+] pressed
      exactly at the startup handoff may be lost on Windows (known limit), so
      press it again.
- [ ] Run the unsigned installer: per-user location, PATH opt-in (off by
      default), Start-menu shortcut, `pterm setup --apply --yes`.
- [ ] Scheduled Tasks start at logon and restart on failure. `conhost.exe
      --headless` shows no window, passes the exit code, and `schtasks /End`
      stops pterm.
- [ ] Clean uninstall, with and without data deletion.

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
- [ ] Final product name, bundle IDs and publisher identity (still placeholders).
- [ ] Public release stays your manual decision (plan section 20).

## 6. Known open defects

- Two earlier unexplained timeouts (real-session retirement, executable broker
  readiness). See `docs/progress.md`.
- The session list does not show which device has control. The HTTP session
  record has no field for it.
- The broker cannot yet refuse an update while sessions are active (documented
  in `docs/install.md` as pending).

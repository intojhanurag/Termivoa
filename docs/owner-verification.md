# Owner verification checklist (v0)

Date: 2026-09-27, updated 2026-10-04. Host used for agent testing: Apple Silicon Mac, macOS 26.5.2,
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
      (`pterm config origin` shows it) and added the project "Home folder".
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
      answer. The card says "The question changed. Check the new one.", and
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
      "To get notifications on iPhone, add Termivoa to your Home Screen."
      with the four steps.
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
      for example `python3 -m http.server 5288`: it is never offered. A
      typed address on `https://<host>:8443` (for example
      `/__termivoa/select?session=<id>&port=5288`) shows "Open the preview
      from the Termivoa app.": the preview listener now refuses every request
      that the app's frame did not start.
- [ ] **Tell agent** returns to the terminal with the composer open; **‹
      terminal** returns without it.
- [ ] Revoke the phone with `pterm revoke <device-id>` while the preview is
      open: HMR stops within 5 s and reload shows "no longer paired".

### Live folder

- [ ] Mac: open a session from the "Home folder" project, run `cd src` (any
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

### Scan QR in + Add a computer (real phone)

The agent tested this in desktop Chrome only, with a QR code of a real
`pterm pair --via` link as Chrome's fake camera (`phase-b.mjs`, `SCAN-*`), and
with unit tests for the camera stop paths. The camera permission of the
installed iPhone Home Screen app, and a real camera on a real terminal
window, are not tested. You need home (`mac`) and a second computer (`vps`).

- [ ] **iPhone Home Screen app, first scan.** On vps run `pterm pair --via mac`
      (it prints a QR code and the link). In the installed app open
      **Settings → + Add a computer → Scan QR**. iOS asks for the camera; the
      app says "Allow the camera to scan the QR code." Tap **Allow**. The
      picture shows inside the app (not full screen), with "Point the camera
      at the QR code that pterm pair --via printed." Point it at the code:
      **Add this computer?** opens with the Address, Machine and Owner. Tap
      **Add vps**, **Continue**, compare the code, type `y`. "vps is added".
- [ ] **The camera stops.** After the sheet opens, the green camera dot in
      the iPhone status bar goes off. Scan again and tap **Cancel**: the dot
      goes off. Scan again and go to the Home Screen: the dot goes off, and
      on return the app shows **Scan QR** and **Paste link** again.
- [ ] **Permission denied.** Tap **Scan QR**, then **Don't Allow**. The app
      says "The camera is blocked. Allow the camera for this app in your
      phone or browser settings, then tap Scan QR. Or paste the link."
      **Paste link** still works. Write down where iOS lets you allow the
      camera again for the Home Screen app (the docs do not name the exact
      iOS setting yet), and whether iOS asks again on each **Scan QR**.
- [ ] **A wrong code.** Scan any other QR code (for example a Wi-Fi code):
      "This is not an add-computer link. Copy the whole link that pterm pair
      --via printed." No sheet opens.
- [ ] **The phone's Camera app.** Scan the same QR code with the iPhone
      Camera app. Safari opens "Add this computer" with "This page cannot add
      the computer" and **Settings → + Add a computer → Scan QR**. Nothing is
      added, and vps still waits.
- [ ] **Android Chrome (installed app).** Repeat the first, second and third
      checks. Chrome on Android reads the code with its own detector.
- [ ] **Reading distance.** The code scans from a laptop screen at normal
      distance (about 30 cm), with a dark and a light terminal theme.

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
      and one with the title `vps · Termivoa` and the text
      `vps · Notifications are on.`
- [ ] Lock the phone. Let an agent on `vps` ask a question: the notification
      "vps · `<title>` is waiting for you" arrives; its title and its text
      both start with `vps ·`.
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

### Preview for sessions on peers

The agent tested this in desktop Chrome and Playwright WebKit only: unit
tests, and the multi-computer harness (`scripts/e2e-multicomputer/README.md`;
results in `docs/progress.md`). The harness runs development gateways behind
a front that stands in for Tailscale Serve, so it cannot check the installed
app, the production cookies and CSP, a real Tailscale Serve or a real tailnet
ACL. Design: `docs/peer-preview.md`.
Needs: the Home Screen app on home (for example `mac`) with one peer added
(for example `vps`), both on a Termivoa build with protocol revision 2, and a
Vite project on `vps`. Do the list on iPhone, then on Android Chrome.

- [ ] On `vps`, run `pterm setup` and answer `y` to the preview question (it
      shows `tailscale serve --bg --https=8443 http://127.0.0.1:8789`).
      `pterm doctor` on `vps` shows "HTTPS 8443 proxies dev-server previews".
- [ ] In a session on `vps`, run `npm run dev`. The **Preview ready** chip
      shows. Tap **Open ↗**: the page loads in the app. This confirms that
      the installed app sends the peer's `__Host-pterm_session` cookie and
      home's `Referer` in the frame, and that the move to `/` after the
      selection is accepted. If the frame shows a short text, record it
      ("Open the preview from the Termivoa app.", "Origin not allowed.",
      "Open the preview from your paired phone.", "Unknown host.").
- [ ] The bar over the page shows `vps` and **untrusted page**. Home's own
      preview shows neither.
- [ ] Edit a file on `vps`: Vite HMR updates the page without a reload (the
      WebSocket passes a real Tailscale Serve).
- [ ] Tap **↻**: the page loads again.
- [ ] iPhone Safari tab (not the Home Screen app): open the preview, follow
      a link to another site in the same tab, then go Back to the app. The
      preview shows again, not "Origin not allowed." (WebKit history
      restore).
- [ ] The same Back when Safari does **not** keep the app page in its page
      cache (open several heavy pages in between, or lock the phone for a
      while first, so the app page loads again from history). The app
      remounts the frame only on `pageshow` with `persisted`; this case is
      unverified. Record whether the preview shows or "Origin not allowed."
      shows until **↻**. If the text shows, file an issue (a fix needs this
      evidence first).
- [ ] Open `https://<home>/c/<peer host>/sessions/<id>/preview?port=<n>` of
      a running peer session from a link in another app (Notes, Messages)
      and, on Android, from a link that opens the installed app. The app
      shows that session's terminal, never the preview. Then tap
      **Open ↗**, and pull to reload: the preview shows again.
- [ ] Follow a link inside the framed page, then swipe Back until the frame
      is at its first page, and once more. Record what happens (known limit
      in WebKit: the whole app goes back).
- [ ] A page with a `<video>` and an `<audio>` element plays in the frame.
- [ ] A link in the framed page with `target="_top"` or `target="_blank"`
      does nothing.
- [ ] Home's own preview still works (the list "Dev-server preview" above);
      home uses the new rules too.
- [ ] Production CSP: in Safari Web Inspector (or Chrome remote debugging),
      the app document's `Content-Security-Policy` has
      `https://<vps host>:8443` in `frame-src` and in `connect-src`, and no
      `wss://<vps host>:8443`. The cookies `__Host-pterm_session` and
      `__Host-pterm_preview` on the `vps` host are `Secure`.
- [ ] Add a second peer (with its own Serve 8443) and, without closing the
      app, open a preview of a session on it. The app reloads once, by
      itself, and then shows the preview. It does not reload again and again.
- [ ] Remove Serve 8443 on `vps` (`tailscale serve --https=8443 off`), or
      block port 8443 to `vps` with a tailnet ACL, then tap **Open ↗**:
      within about 5 s the app shows "Can't open the preview on vps. If vps
      does not share dev-server previews yet, run pterm setup on vps and
      answer yes to the preview question." and no empty frame. Restore it
      after the check.
- [ ] With the preview open, run `pterm revoke <device-id>` on `vps`: HMR
      stops within 5 s, and the app removes the frame and shows
      "vps · pair again".
- [ ] Stop Termivoa on `vps` with the preview open: the app removes the
      frame and shows "vps · can't reach".
- [ ] A peer on an older Termivoa (protocol revision 1) shows no
      **Preview ready** chip.
- [ ] Use the app as usual for 8 days without opening the `vps` preview;
      then it still opens (the WebKit 7-day cookie cap, with the rolling
      refresh).

### Found on your network (real computers and phones)

The agent tested this with two test stacks behind the harness front
(`scripts/e2e-multicomputer`, checks `F-*`; Chrome, and WebKit for the phone
side of **Add**). The harness has no real Tailscale IPs, no real
certificates and no Tailscale Serve, so these checks are yours. You need
home (`mac`) and a second computer (`vps`), both set up with Termivoa from
this build. Design: [found-on-network.md](found-on-network.md), section 10.

- [ ] **vps → mac under systemd.** On `vps` (Linux, broker under systemd, no
      desktop session), run `pterm pair --via mac` in an SSH terminal. The
      screen says "Sent to mac (https://mac.<tailnet>.ts.net)." This proves
      that vps's broker dials mac's Tailscale IP on port 443 and verifies
      mac's Let's Encrypt certificate with the system roots from a background
      process. "mac did not answer." means it failed (the broker does
      not log why): record the screen line, `pterm doctor` on both
      computers, and whether `curl -sS https://mac.<tailnet>.ts.net/healthz`
      works on `vps`.
- [ ] **Serve forwards the offer.** On `mac`, Tailscale Serve passes the
      `POST /api/v1/computers/offer` with no `Origin` and no `Sec-Fetch-*`
      header to the gateway, with `Host` intact (the row shows in the app).
      Also record whether Serve sets `X-Forwarded-For` on that request (for
      the option that is rejected for now; Termivoa does not use it).
- [ ] **mac → vps under launchd.** The reverse: make `vps` the home of a
      second test app, and run `pterm pair --via vps` on `mac` (broker under
      launchd). The screen says "Sent to vps (…)".
- [ ] **The whole add on a real iPhone Home Screen app.** On `vps` run
      `pterm pair --via mac` (or `pterm setup`, answer 2). In the app:
      **Settings → Computers → Found on your network**, `vps`, **Add**, the
      **Add this computer?** sheet, **Add vps**, **Continue**. The phone shows
      a code and "If it matches, type y in the terminal on vps." The prompt
      on `vps` says "wants to add this computer (vps)" and the same code. Type `y`: `vps`
      is in the list, its sessions show, and the row is gone.
- [ ] **The same on Android** (Chrome, installed app).

## 2c. herdr on your phone (real herdr, real iPhone)

Status: **pending, not executed.** The agent tested this against the fake
herdr (`scripts/fakeherdr`, harness checks `H-*`) and, in an isolated home,
a real herdr 0.8.2 and 0.9.3 (design, section 1). Push was tested in Go only.
The harness hides your real herdr on purpose (S16), so these checks are yours.
You need a Mac with Termivoa from this build, herdr running with your own
projects, and the iPhone Home Screen app. Design:
[herdr-provider.md](herdr-provider.md), sections 6, 7 and 15.

- [ ] **Open a blocked agent and answer it.** In your herdr, let an agent
      reach `blocked`. On the phone, **Sessions → herdr** shows BLOCKED and
      the count. Tap the row: Live shows that terminal, read-only. Take
      control and answer (an answer card, or type). The agent goes on.
- [ ] **The laptop pane and its size.** While the phone shows the terminal,
      the pane in your herdr window is phone-sized and the line under the
      chips says "mac shows this terminal at phone size while it is open
      here." Lock the phone: about 10 s later the pane is back at its normal
      size. Also leave Live (back to Sessions): same result. Record the time.
- [ ] **A real push when blocked.** With the phone locked, let an agent
      become blocked. A notification `herdr · <project> · <worktree>` with
      "<agent> needs you. Tap to answer." arrives within a few seconds (blocked
      for at least 2 s). Its text has no terminal text.
- [ ] **A real push when finished.** Let an agent work for 20 s or more, then
      finish. "<agent> finished after N min." arrives (also when your herdr
      window shows that tab and the state is `idle`).
- [ ] **A tap opens the terminal at once.** Tap each notification: the app
      opens that terminal in Live with no **Open** button, and the laptop pane
      goes phone-sized. A pasted `/p/herdr/<target>` link or a reload shows the
      names and **Open** instead. Record whether it works from a closed app and
      from an open app.
- [ ] **Settings → Integrations.** Switch herdr **Off**: the herdr tab leaves
      this computer, open terminals end (the pane gets its size back), no push
      comes. Switch **On** again: it returns. **Buzz when blocked** and **Buzz
      when finished** each stop and start their pushes, and survive a reload.
- [ ] **M2: create on a real repo.** On a project row tap **+**, give a branch
      name and an agent (and a first prompt). A new worktree row appears
      under `~/.herdr/worktrees/<repo>/<branch>`, the agent starts, and
      **Open** works at once. Remove the test worktree and branch afterwards.
- [ ] **herdr stopped.** Quit herdr: the tab says herdr is not running on
      this computer within a few seconds, and Sessions stays as fast as before.
- [ ] **Each herdr version you install.** Run the opt-in lab test, which uses
      a temp HOME and its own herdr session and never your real session:
      `PTERM_HERDR_LAB=1 go test ./internal/herdr -run Lab`
      (`PTERM_HERDR_LAB_BINARY=<path>` picks another herdr than the one on
      `PATH`). Record the herdr version and the result.
- [ ] **`pterm doctor`** shows the herdr line (version, protocol, server
      running, "can make worktrees") and warns if your `config.toml` sets a
      prefix other than `ctrl+b`.

### Manage herdr (ADR 0009)

Automated with the fake herdr (harness checks `H-*`) and, in an isolated
home, a real herdr 0.8.2 (the lab test). On your live herdr and your VPS
these are yours:

- [ ] **+ on a git project.** `personal-website` (a git repository where
      herdr made no worktree yet) has **+**; a new worktree from the phone
      works.
- [ ] **Close workspace.** **⋯ → Close workspace** on a workspace you do
      not need: it asks, then the row goes away; its folder is still there.
- [ ] **Remove worktree.** On a worktree with a changed file: "…has changes
      that are not committed…" and nothing is removed. After a commit: it is
      removed and the branch is still there.
- [ ] **New tab and New workspace** open their terminal at once.
- [ ] **Install herdr on the VPS.** **Settings → Integrations → Install
      herdr** opens a session on the VPS with the install command typed and
      not run. Press Enter; after it ends, **Check again**, then **Start
      herdr**; the herdr tab shows the VPS.
- [ ] **Linux start survives a Termivoa restart.** After **Start herdr** on
      the VPS, `systemctl --user restart phone-terminal-broker.service`:
      herdr still runs (`herdr status server`). This is the `systemd-run`
      path, which no test ran on Linux.

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

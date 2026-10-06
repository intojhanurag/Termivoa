# Pending work

The single roadmap. Updated 2026-10-04. Latest tag: `v0.1.0-beta.3`.
Ask the agent to "do pending work" to start on **Next**.
Real-device test steps are in [owner-verification.md](owner-verification.md).

## Done

- Private betas `v0.1.0-beta.1`, `v0.1.0-beta.2` (#66) and `v0.1.0-beta.3`; website and docs
  live at <https://termivoa.vercel.app> and <https://termivoa.vercel.app/docs/>
  (#53, #56).
- Windows logic: WSL bash profile (#38), Ctrl+] at the attach startup handoff
  (#37), Scheduled Task launcher kept on `conhost --headless` with logs, normal
  priority, a late-broker wait and a clean stop (#40), SQLite URI and ACL fixes
  (#39). All still need a real-Windows check.
- Linux hosts: systemd user units, XDG paths, WSL notes (#50).
- Phone features: agent status and answer cards (#42–#45), Web Push (#41,
  #44), photo and voice (#46), dev-server preview (#47), live folder (#49),
  Changes view (#60), away recap and commit & push (#62), Agent wall (#64),
  project buttons and line comments (#67), Wall drag (#109), Down key (#110),
  worktrees (#113), Files view (#126).
- Multi-computer: design and ADR 0005 (#132), implementation (#135), docs
  (#137). Gate 0 on real phones is still open (Next 1).
- Faster first load: Changes, Files and Settings load on demand. The 690 kB
  TerminalView chunk stays: all of it (xterm, the envelope validator) is
  needed for the first frame. A real cut needs a smaller validator or
  terminal library.
- Wall: a paused tile that was never live shows one snapshot of its screen.
  Not yet checked on a real phone.
- Computer health in Settings: version, last seen and **Check again**.
  Follow-up: show the last known version on a "can't reach" row.
- Notifications from every computer. Open: the real-iPhone check of several
  scoped registrations (`docs/owner-verification.md`, Gate 0 check 5).
- Preview for sessions on peers: ADR 0006 and `docs/peer-preview.md`
  (design #146, approved by the owner 2026-10-03); the build also closes #143
  and #144. Open: the real-phone checks (`docs/owner-verification.md`,
  "Preview for sessions on peers").
- Scan QR inside the app for + Add a computer, and a Home badge. Open: the
  real-phone check (owner-verification) and naming the exact iOS camera
  setting in the docs after it.
- Found on your network: ADR 0007 and `docs/found-on-network.md` (owner
  approved v2 2026-10-03); implemented (a waiting `pterm pair --via` or setup
  offers the computer to home; **Settings → Computers → Found on your
  network → Add**), with unit tests and multi-computer harness checks
  `F-*` (`scripts/e2e-multicomputer/README.md`). Open: the real two-computer
  and real-phone checks (Next 3).
- herdr on your phone (ADR 0008, `docs/herdr-provider.md`): a **herdr** tab,
  open a herdr terminal in Live, buzz when blocked or finished,
  **Settings → Integrations**, new worktree with an agent. Open: the real
  device checks (Next 5).
- Session pushes are no longer dropped while earlier deliveries are on their
  way (#197), and a herdr card says "This phone gets no buzz yet." when its
  buzz cannot reach this phone. Test notifications have their own budget,
  one delivery sends to 4 subscriptions at once, and the "no buzz" line
  knows a computer by its host (#202). Open: a real push on a real iPhone
  (`docs/owner-verification.md`).
- Docs: answer-first rewrite and AI-readable docs (#118), docs rules (#72,
  #121, #133).

## Next

In this order.

1. **Gate 0 real-phone test for multi-computer** (`scripts/gate0/`). Needs the
   owner's second computer and the owner's approval for a Tailscale Serve port
   (rule 12). Port 8443 is taken by the dev-server preview, so use another
   free port.
2. **Real-phone checks for preview on peers**
   (`docs/owner-verification.md`, "Preview for sessions on peers"). Needs the
   owner's second computer with its own `tailscale serve --https=8443` (asked
   by `pterm setup` there). Can run in the same sitting as Gate 0.
3. **Real checks for Found on your network**
   (`docs/owner-verification.md`, "Found on your network"): vps's broker
   under systemd dials mac's Tailscale IP on 443 and verifies its Let's
   Encrypt certificate; mac's Tailscale Serve forwards an offer with no
   `Origin` or `Sec-Fetch-*` and `Host` intact (and whether it sets
   `X-Forwarded-For`); the reverse under launchd; the whole add on a real
   iPhone Home Screen app and on Android. Can run in the same sitting as
   Gate 0. The owner's first real add (Mac home + Ubuntu VPS, 2026-10-03)
   worked end to end with **Found on your network → Add** and `y`.
4. ~~**Docs picture** of the "Adding vps… approve on vps" bar and of
   **Found on your network** with **Add**.~~ Done: both in
   `guides/computers.md`, from `scripts/docs-screenshots/computers.mjs`.
5. ~~**herdr on your phone**~~ Done in the next beta (ADR 0008,
   `docs/herdr-provider.md`): M1a (list, push, Integrations), M1b (open,
   chips) and M2 (new worktree and agent). Open follow-ups:
   - **Owner real-device checks.** Open a blocked agent on his live herdr
     from the phone; check that the laptop pane goes phone-sized and, about
     10 s after the phone locks, back to its own size; get a real herdr push
     on an iPhone.
   - **herdr 0.9 `terminal session observe`:** watch a terminal without
     resizing the laptop pane; move to it when 0.8 support ends.
   - **Custom herdr prefix key:** today `pterm doctor` only warns; decide if
     Termivoa should double the configured key too.
   - ~~**Manage herdr from the phone**~~ Done in the next beta (ADR 0009,
     `docs/herdr-manage.md`): close, remove, new tab, new workspace, start,
     the install command, and **+** on every git project. Open: the real `systemd-run`
     start on a Linux computer; a peer computer's start failure shows only
     "That computer could not do this."; force-remove and rename are not offered; on herdr 0.9.3 two
     workspaces at one repository's main checkout close only in herdr
     (it wants `close_group`); the git check can leave more than one
     waiting goroutine for a workspace on a hung mount that leaves and
     comes back; the create route's busy text still says "another
     worktree is being made"; `SessionMenu.tsx` Terminate has the
     double-tap pattern that the herdr confirmations no longer have;
     `ProviderCreate.tsx` shows its field errors muted (`error
     hint-text`).
   - **Named herdr sessions:** only the default session shows.
   - **Windows:** herdr is not supported there.
   - **herdr installed later:** the phone shows it only the next time the
     app comes to the front (settings are read again then).
   - **"‹ Sessions" in a herdr terminal** goes to the All tab, not the herdr
     tab.
   - **Open on its own gateway connection:** the reaper holds a target's
     open lock while it ends the attach (at most 3 s); an open of that
     target, and gateway requests behind it, wait. Create already has its
     own connection.
   - **A `/p/…` address of a computer below protocol revision 4** shows a
     general failure text, not "Update Termivoa on …".
   - **Create timeout text:** the sheet tells a timeout from the elapsed
     time; give the client a `timeout` code.
   - **Binary check while herdr runs:** when the herdr program turns unsafe
     (owner or mode) while its server runs, the list stays "ready" and every
     open fails; recheck it in the poll and say why in `pterm doctor`.
   - **Test for a herdr version change between two polls** (rediscovery).

## Polish next

From the owner's first real install and add (2026-10-03). UI items are
designed in Paper first and shown to the owner before any code.

1. ~~**"connecting…" right after an add.**~~ Done: for
   5 s after an add the row says "Connecting to vps…" and retries quietly;
   `phase-b.mjs` `B2b-connecting` fails on a "can't reach" row right after
   the add. Also: a page whose own CSP blocks a listed computer
   reloads once past the service worker's cached page, then says "This app
   page is out of date. Close the app and open it again."; "vps · can't
   reach" gets the line "Last seen … Is it on, and in your tailnet?";
   **Check again** stays at least 600 ms and says "vps answered in 0.2 s.".
2. ~~**Add-computer wait screen in the terminal**~~ Done (setup answer 2 and
   `pterm pair --via`): no QR code and link up front (Enter shows them; shown
   at once only when the push failed); a short approval prompt that still
   names this computer and quotes the phone's code screen; a strong success
   line ("Done. vps is in your Termivoa app on mac. Open the app and tap
   it."); a tip to rename long machine names.
3. ~~**UI revamp (Paper first):**~~ Done: "Bone & ink" is on every screen
   and sheet (`feat/bone-and-ink`; Paper boards "V2 Bone & ink — …"), and
   the site screenshots, the landing page pictures and mock-ups, and the
   README pictures are retaken in it (`docs/bone-ink-screens`).
4. **Open issues:** #158, #161, #164 fixed; #162 not a defect (see
   `docs/progress.md`); #165 (landing page does not show adding a
   computer) waits for the owner's go on the Paper design.
5. ~~**Scan QR on iPhone:**~~ Done: a camera track that
   stays muted for 2 s ends the scan with "The camera did not start. …".
   Needs a real iPhone check (a call during Scan QR).
6. ~~**Release `v0.1.0-beta.3`**~~ Done: docs prepared for the tag
   (`docs/beta-3`); a new user gets today's features from `gh release download`.

## Later

### Smart alerts

The owner wants to plan these together first. Do not start without him.
Draft scope (honest signals only; the plan forbids labels from terminal text):

- waiting too long (agent state waiting for more than N minutes);
- looks stuck (working, no output for more than N minutes);
- dev server stopped (a session's preview port disappeared);
- session exited with a non-zero code;
- a smarter "long task done";
- settings: per-alert on/off, "remind after X min", quiet hours.

### Owner decisions

- [ ] **iOS older than 16.4.** Multi-computer refuses it; the refusal is kept
      for now.
- [ ] **PTY-session kill boundary.** Keep (recommended) or revert. See
      [owner-verification.md](owner-verification.md) section 6.
- [ ] **Owner confirms** the `SECURITY.md` response times (7 days / 30 days).

### Before the public beta (the repo goes public)

- [ ] **Real-device tests.** Linux (VPS `t3-vps`), Windows laptop, Android
      phone. Also test the update steps once
      (`pterm shutdown` → download → `pterm launch`).
- [ ] **Re-enable GitHub Actions** when the repo goes public, run CI on `main`
      (many PRs merged with local checks only) and fix any Windows or Linux
      failures.
- [ ] **24-hour soak test.** Sessions running for a full day; check memory
      and reconnects (`scripts/soak.sh`).
- [ ] **Secret scan of the full git history** before it becomes public.
- [ ] **Licence audit.** Finish the notice audit in
      `THIRD_PARTY_NOTICES.md` and `docs/dependencies.md`.
- [ ] **One-line installer.** `curl … | sh` for macOS and Linux. Needs a
      public repo or the binaries hosted on the website.
- [ ] **Owner makes the repo public.** Only the owner does this.

### Small code items

- [ ] **Preview on peers, follow-ups** (`docs/peer-preview.md`, section 13).
      Only if a real-phone check fails or a browser stops sending `Referer`
      on framed navigations: review the preview ticket (variant C). Not
      fixed: WebKit Back inside the frame moves the whole app back; cookie
      eviction by a framed dev site.

- [ ] **Staged update refusal.** The broker must refuse replacement while
      sessions are active (plan section 11; `docs/install.md` marks it
      pending).
- [ ] **Diagnostics export.** A user action with a preview before it writes a
      local file; no secrets, input, output or environment (plan section 10).
- [ ] `pterm setup` asks "Pair another phone? [y/N]" when a phone is already
      paired.
- [ ] **Desktop mouse forwarding.** Out of scope for v0; do only if asked.

### Money (owner decisions, all can wait)

- [ ] **Domain** for the website, about $10–15 a year.
- [ ] **Apple Developer Program**, $99 a year, to sign and notarize the Mac
      app. Only before v1.0; `gh`/`curl` installs need no signing.
- [ ] **Windows code-signing certificate.** Only if many Windows users come.

### Before v1.0

- [ ] Security review by an outside person.
- [ ] Signed macOS and Windows apps.

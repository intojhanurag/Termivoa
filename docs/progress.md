# Implementation progress

Last updated: 2026-10-04

## Start here

- **State on 2026-10-02:** latest tag `v0.1.0-beta.2` (private beta,
  unsigned). `main` is beta.2 plus project buttons and line comments, Wall
  drag, the Down key, worktrees, the Files view, multi-computer (#135; Gate 0
  on real phones is still open) and the docs overhaul.
- **Read first:** [`AGENTS.md`](../AGENTS.md) →
  [`docs/architecture.md`](architecture.md) →
  [`docs/pending-work.md`](pending-work.md) (the single roadmap).
- **This file** is the evidence log (rule 18). Dated entries describe the
  state on their own date; add new entries, do not rewrite old ones.

## Log

### Summary (2026-09-27)

The v0 flow is complete on `main` and verified on the macOS development
profile (Apple Silicon, macOS 26.5.2): desktop attach, phone pairing and
revocation, the authenticated gateway, the phone web app (UI v2), the 3-step
`pterm setup` first run, per-user startup registration and unsigned
packaging. It landed as #27 (v0, containing #23–#26), #28 (first run and
daily use) and #30 (phone UI v2). All three were merged without hosted CI,
because GitHub Actions jobs do not start (account billing). The full local
`./scripts/check` and `go test -race ./internal/... ./cmd/...` passed on the
merged heads. The GitHub repository is now `intojhanurag/Termivoa`. The owner
confirmed the product name **Termivoa** (2026-09-27); user-facing text uses it,
and data folders, startup entries and other stored names keep "Phone Terminal"
for compatibility (`docs/naming.md`).

Not yet verified (owner checklist in
[owner-verification.md](owner-verification.md)):

- Hosted CI on `main`, including native Windows tests and the packaging
  workflow.
- Physical iPhone Safari and Android Chrome, and private Tailscale Serve HTTPS
  over cellular.
- Native Windows 11: broker, ConPTY, desktop attach, installer, Scheduled
  Tasks.
- Startup after a real macOS logout/login, and the unsigned DMG.
- Signing and notarization (needs owner credentials).
- Windows soak, 24-hour soak, 10-minute stress run and the key-to-frame
  latency benchmark.
- Owner decisions: the Unix termination boundary, the final public name and
  identifiers.

The dated entries below are the history log; older entries describe the state
on their own date.

### No dead taps: an audit of every control (2026-10-04, branch `fix/button-feedback`)

- **Why.** After #174 (Retry on "vps · can't reach" showed nothing), every
  button, link and form in `web/src` was traced and sorted: changes the
  screen at once; network work with busy and result; or a defect (nothing
  while it waits, no result, a swallowed error, a no-op, two actions on a
  double tap, off with no reason). Phone only; host: no change.
- **Fixed.** Check again on Sessions and New session (a 0.2 s flash; now
  `checkAgain` in `allSessions.ts`, at least `CHECKING_MIN_MS`); Save title
  and Terminate session (session menu: off but no busy); New session on the
  lost screen and Run in new tab (no feedback, and a double tap made two
  sessions: `onStartNew` now returns its promise); Send a test notification
  (a second "Sent" looked like nothing); a failed Log out (its error was at
  the top of Settings, out of sight); Commit and Push that sent nothing
  (`commitChanges`/`pushChanges` returned false and nothing showed); copy
  error on the error screen (a failure was swallowed); the preview's ↻
  under a notice (a no-op, now off); the update pill after another tab
  took the update (`applyUpdate` in `hooks.ts` reloads at once); + new in
  snips off at 50 with no reason.
- **Not changed (owner's call).** Listed in the pull request: the push
  switch only greys out while it works; Take control falls back without a
  word after a `resync_required`; Files taps do nothing while the link
  reconnects (the full-screen panel hides the status line); Paste N lines
  in snips is off with no reason if control is lost while it asks; Add on
  the add sheet is a full page load with no busy state.
- **Tests.** One vitest per fix; each fails on the old code (checked by
  reverting the source files: 15 failed). The session menu and error
  screen tests set the first `useState` through `vi.mock('react')`, since
  static markup cannot tap.
- **Checks.** `web`: `tsc --noEmit`, `vitest run` (790 passed);
  `pnpm build:web`; `scripts/embed-web.sh`; `scripts/check` passed;
  e2e Phase B 79/79, Phase A 32/32 (`--no-web-build`).

### "Bone & ink" visual revamp (2026-10-04, branch `feat/bone-and-ink`)

- **What.** The owner-approved Paper boards "V2 Bone & ink — Sessions",
  "— Settings → Computers" and "— components" applied to every screen and
  sheet: pairing, Sessions, Live and the dock, New session, Settings,
  Computers, Found, + Add a computer (scan, paste, errors), the add-computer
  code page, the preview bar, the session menu, snips, worktree, compose,
  changes, history, the Wall, state screens and the offline page in
  `sw.js`. Phone only; host: no change.
- **System.** Ink `#07090C`, paper `#0F1217`, raised `#15181D`, hairline
  `#22272F`, strong line `#3A414D`, bone `#ECE8E1`, soft `#A9AFB9`, muted
  `#8C93A0`; cyan only for live/connected, amber warnings and your turn,
  red errors and destructive, green online. Serif titles (system
  `ui-serif` stack, no font download), mono caps labels. One bone pill per
  screen; ghost, quiet and red-ghost pills; busy keeps its width. No
  gradients, glows, inset shadows or tinted cards: `global.test.ts` checks
  no `gradient` and no `box-shadow` in `global.css`.
- **Markup.** `Notice` lost its icon (the 2 px rule carries the tone);
  `Banner` shows a dot; `Button` with a `busyLabel` and no icon puts the
  spinner in the busy cell. Sessions and Computers rows lost their icon
  tiles; the Home badge is `.home-badge`; the dock's quiet actions lost
  their glyphs. The terminal's pure-black default background is drawn as
  ink (`FrameRenderer` `onInk`, with a test); other colours are unchanged.
- **Contrast (WCAG AA).** Bone on ink/paper/raised 16.3/15.4/14.6; soft on
  ink/paper 9.0/8.5; muted on ink/paper/raised 6.5/6.1/5.8; cyan 11.9/11.2;
  amber 10.9/10.3; red 7.1/6.7; green on ink 11.5; ink on bone 16.3, on
  pressed bone 11.5; answer number `#55534F` on bone 6.3. Faint `#5D6470`
  (3.0–3.3) is used only for disabled labels and dots, never for text
  people must read (the Paper board's faint IDLE and "Home serves this
  app" use muted instead).
- **Evidence** (not in the repo): WebKit 2272 (Playwright 1.61.1),
  390×844, isolated two-computer dev stack (the e2e harness) with demo
  data: 23 screens before and after. No horizontal overflow at 375 or 390
  on Sessions, Live, New session and Settings → Computers.
- **Checks.** `web`: `tsc --noEmit`, `vitest run` (779 passed);
  `pnpm build:web`; `scripts/embed-web.sh`; `scripts/check` passed;
  e2e Phase A 32/32, Phase B 79/79 (incl. `B6-fit`), WebKit 8/8, all with
  `--no-web-build`.
- **Left.** The site screenshots and landing images still show the old
  look (`docs/pending-work.md`, Next item 3). Not yet seen on a real phone.

### Phone screens fit 375 and 390 px again (2026-10-04, branch `fix/phone-overflow`)

- **Report.** On a real iPhone (390 px) the page slid sideways, and in
  **Settings → Computers** the `pterm pair --via <home>` command under
  **Found on your network** ran out of its card.
- **Cause.** One rule from the lit UI (#169): `.notice code,
  .add-computer-hint code { white-space: nowrap; }`. A command with a long
  machine name (`pterm pair --via studio-workstation-02`) could not wrap, so
  it stuck out of the Found notice and the "stopped waiting" notice on the
  add page and made `.page` scroll sideways (411–420 px wide at 375/390).
  The same rule, with `overflow-x: auto` on `.notice-command`, cut the
  command in the "pair again" notice on Sessions (a hidden side scroll,
  306–313/253–268 px).
- **Fix.** `code` wraps (`overflow-wrap: anywhere`); `.notice-command` is a
  wrapping inline block with no side scroll; `PairVia` (`ui.tsx`) renders
  every `pterm pair --via` with the flag in a `.nowrap` span, because
  Chrome breaks "--via" between its dashes (WebKit does not).
- **Evidence** (not in the repo): Playwright 1.61.1, WebKit 2272 and
  Chromium, 375×812 and 390×844, isolated docs-screenshots dev stack with
  demo data and mocked peers (can't reach, pair again, Found row, Found
  empty, add sheet, add error, Continue, code, stopped waiting, paste
  error, Live with dock, preview, Settings, New session, pairing). Before:
  4 screens overflow at every size and engine; after: none. The only side
  scroll left is a terminal that another device sized wider (a viewer pans;
  by design).
- **Guard.** Phase B `B6-fit`: Sessions and **Settings → Computers** at
  390 px with "vps · pair again", once as shown and once with every `code`
  made long. `phase-b.mjs --no-web-build --only B6`: PASS on this branch,
  FAIL on the old build (`.page` 845/390). `global.test.ts` checks the CSS
  rules. `B6-pair` fails under `--only B6` (no mac rows without B3);
  unrelated to this change.
- **Checks.** `web`: `tsc --noEmit`, `vitest run` (777 passed);
  `pnpm build:web`; `scripts/embed-web.sh`; `scripts/check` passed.

### #168: Serve status for this computer's name, foreground and Services (2026-10-03, branch `fix/serve-status-168`)

- **Rename.** `lifecycle.ServeFindings(data, host)` now takes this
  computer's Tailscale DNS name (`status.Self.Host`, from the same
  `pterm doctor`/`setup` checks). Only `<host>:443` and `<host>:8443` count
  as `Configured`/`PreviewConfigured`. Termivoa under another name (left
  from an old machine name) is neither configured nor a problem: it is
  another origin (#162 note). Setup now asks to share the page at the new
  name; before, it said "already shares".
- **Foreground and Services.** Checked against Tailscale's
  `ipn/serve.go` (main, 2026-10-03): `ServeConfig` has `Foreground
  map[string]*ServeConfig` (a `tailscale serve`/`funnel` without `--bg`,
  keyed by IPN bus session ID) and `Services map[ServiceName]*ServiceConfig`
  (only `TCP`, `Web`, `Tun`; the CLI refuses Funnel with `--service`).
  `tailscale serve status --json` prints the whole `ServeConfig`.
  Foreground Funnel is now a problem and is in `ServeStatus.ForegroundFunnel`;
  setup, `pterm pair` and `pterm launch` refuse or warn with "stop the
  `tailscale serve` or `tailscale funnel` command that runs in the
  foreground (Ctrl+C)" (`tailscale funnel --https=N off` does not stop
  it). Every foreground handler on 443/8443 is a problem (Termivoa there
  too: it ends with its command), and `otherHandlers` refuses foreground
  handlers on this origin ("foreground /grafana"). A Service that proxies
  Termivoa is a problem (`tailscale serve clear svc:<name>`).
- **Tests** (all fail on the old parser): `TestServeFindings` (other host
  name, this host and an old name, foreground funnel, foreground funnel next
  to a saved config, foreground conflict, foreground TCP forward, service
  serves termivoa / another app), `TestInspectTailscaleReportsPreviewFindingsSeparately`
  ("only an old host name"), `TestOtherServeHandlersOnlyOnThisOrigin`
  (foreground gateway, path, TCP forward), `TestSetupRefusesFunnelOnTermivoaPorts`
  ("foreground 8443"), `TestPairRefusesFunnelOnTermivoaPorts` (foreground
  443). The fixtures follow the real struct shapes; no real foreground
  Funnel or Service was captured (rule 12: no Tailscale changes here).
- **Checks:** `gofmt -l`, `go vet ./...`, `CGO_ENABLED=0 GOOS=linux go vet
  ./...` (with cgo the macOS SDK cannot cross-compile runtime/cgo),
  `GOOS=windows go vet ./...`, `go test -race ./internal/lifecycle/...
  ./cmd/pterm/...`, `scripts/embed-web.sh && scripts/check`.

### Host batch: #158, #161, #162, the add-computer wait screen (2026-10-03, branch `fix/host-batch`)

- **#158 (tests only).** The systemd fake in
  `cmd/pterm/lifecycle_linux_host_test.go` answered `disabled` for a unit
  with no file, so every "first install" in those tests planned a broker
  restart ("This restarts the broker, …" in the short plan; reproduced with
  the old fake). It now answers like `systemctl --user is-enabled`: `enabled`,
  `disabled` (exit 1) only when the unit file exists, else `not-found`
  (exit 4). New test `TestLinuxFirstInstallPlanRestartsNothing` (fails with
  the old fake). No product change.
- **#161.** `pterm pair` and `pterm launch` never read the Serve settings.
  Both now read only `tailscale serve status --json` (`termivoaFunnel`,
  `lifecycle.ServeFindings`, 5 s). `pterm pair` and `pterm pair approve`
  refuse before they contact the broker while `ServeStatus.Funnel` is not
  empty (443 or 8443), with setup's text from the shared `funnelProblem`;
  `pterm pair pending` does not check (it pairs nothing). `pterm launch`
  warns and starts. No Tailscale, a failed read or bad JSON: no check, as
  before. Tests: `TestPairRefusesFunnelOnTermivoaPorts` (every pair form;
  8443; Funnel on another port and bad JSON go on; no Tailscale change),
  `TestLaunchWarnsAboutFunnelAndStarts` (real broker and gateway); both fail
  without the fix. Not run against a real Funnel.
- **#162 (no change).** Verified: `offerTailscaleServe` accepts
  `Configured` while `serve.Problems` names a handler on 443 under another
  host name (a fake Serve status with Termivoa on `vps…:443` and `/admin`
  on `old…:443`: the checks list the problem, setup plans "Then pair your
  phone"). Not a defect: Serve answers a host by its exact TLS name, and a
  handler under another name is another origin; the Termivoa cookie is
  host-only, so such a handler cannot read the app or get the cookie. The
  same-origin case (this computer's own name, and a TCP forward on 443)
  already stops setup; the doc's own fix for a handler is "its own Tailscale
  Service hostname", which a wider refusal would block. Doctor still counts
  it as a problem.
- **Add-computer wait screen.** After a delivered first offer, the N4 screen
  is five lines and the QR code and the link wait for Enter (read with the
  confirmer's `Ask`, stopped before the N5 prompt; the reader takes no byte
  past the newline). A failed first offer prints them at once, as before.
  N5 is three short lines with the app's origin and owner on one line. A
  bound approval prints "Done. vps is in your Termivoa app on mac. Open the
  app and tap it." (+ a Rename tip over 12 characters); setup ends with
  "Setup is done.". Tests: `TestPairViaEnterShowsTheLinkAfterADeliveredPush`
  (a real pipe through `FileConfirmationReader`: Enter, then `y` reaches
  the prompt), `TestPairViaFailedPushShowsTheLinkAtOnce`,
  `TestPairViaEnterAfterTheLinkExpired`, `TestPairViaSuccessHintsAShortName`,
  and the updated N4/N5 tests. The e2e harness expectations
  (`phase-b.mjs`, `webkit.mjs`) are updated but were not run here.

### Found on your network: harness, review fixes, docs (2026-10-03, branch `feat/found-on-network`)

ADR 0007 v2 is implemented (critical feature, rule 24: not merged; owner
review). This session added the e2e checks, fixed four small defects
(`pterm pair --via` with piped stdin offered; two web texts; two doc
claims) and applied the code reviews of fd0be48 (security and correctness,
both "ship after fixes"): C1 to C9 and S1 to S5, each in its own commit
with a test, except C4 with C6 and S4 with C5 (broker side), which share
hunks. Protocol revision stays 3; `admin.pair.offer` `problem` gained
`busy` (regenerated).

Commands and results (macOS 26, Apple Silicon, Chrome 154, WebKit
playwright-core 1.61.1 build 2272):

- `scripts/check`: exit 0. `go vet -tags e2e ./...`: clean.
  `go test -race` of `internal/broker`, `internal/cli`, `internal/gateway`,
  `cmd/pterm`: pass.
- `node scripts/e2e-multicomputer/phase-a.mjs`: 32/32 PASS (new A8: a
  production build has no `PTERM_E2E`; the e2e build has it).
- `node scripts/e2e-multicomputer/phase-b.mjs --no-web-build` (pterm built
  with `-tags webdist,e2e`): 77/77 PASS, 12 of them new `F-*`: F-offer,
  F-offer-owner, F-offer-origin, F-offer-preflight, F-add, F-gone, F-dead,
  F-flood (204 ×10, 429 ×40), F-flood-link, F-old (front `-old-home`, not a
  beta.2 build), F-setup-list, F-setup (real `pterm setup` on the dev stack
  with a fake `launchctl` and `PTERM_E2E_GATEWAY`).
- `webkit.mjs --no-web-build`: 8/8 PASS (new W8: the phone side of F-add).
- Site: `pnpm install --frozen-lockfile --ignore-workspace && npx astro
  build`: 25 pages, all internal links valid.

Evidence: [`docs/evidence/found-on-network/`](evidence/found-on-network/)
(result tables, the setup transcript with the grant and QR code hidden,
screenshots of Found, the sheet, the code, the stale text and WebKit).

The harness found no new product defect. It showed that the old checks
lagged the code (the N5 prompt says "shows 123-456", the in-terminal wait
says "Waiting for your phone"); they are updated. The docs pass found that
the Found texts used the name given to home on the phone, not its machine
name (fixed with C3).

Still open (owner, `docs/owner-verification.md`, "Found on your network"):
a real vps broker under systemd dialling mac's Tailscale IP with its Let's
Encrypt certificate; Tailscale Serve forwarding the offer with no `Origin`;
the reverse direction under launchd; the whole add on a real iPhone Home
Screen app and on Android. The pinned-IP dial guard is proven by unit tests
only; the harness dials through its front.

### Setup refuses Tailscale Funnel (2026-10-03, branch `fix/issue-157`)

Fixes #157. With Funnel on for 443 while Serve already proxied Termivoa,
`offerTailscaleServe` returned success and setup registered startup and
paired a phone (reproduced with the fake Serve status JSON before the fix:
the plan said "Then pair your phone" and setup reached "Registering
startup..."). `lifecycle.ServeFindings` now lists Funnel on 443/8443 in
`ServeStatus.Funnel`; `pterm setup` refuses before any change (also
`--apply --yes`) and names `tailscale funnel --https=<port> off`; doctor's
Funnel problem names the same command. Funnel on another port stays a doctor
problem and a plan warning. Tests: `TestSetupRefusesFunnelOnTermivoaPorts`,
`TestDoctorReportsFunnelAsAProblem`, `TestServeFindings`. Checks:
`go test ./cmd/pterm/... ./internal/lifecycle/...`, `go vet` (darwin, linux,
windows), `scripts/check`, site build. Not run on a real Funnel.

### Multi-computer end-to-end test (2026-10-02, branch `feat/mc-e2e`)

Two isolated dev stacks reached as `https://mac.tailx.ts.net` and `https://vps.tailx.ts.net` through `scripts/e2e-multicomputer/` (loopback CONNECT proxy with in-process TLS, fake `tailscale` status, every harness `pterm` in a `sandbox-exec` profile that hides the real Tailscale, port 8787 and launchctl). On 167eb63: Phase A 31/31, Phase B (Chrome 154, 390×844, touch) 24/24, WebKit 26.5 4/4. Commands, timings and evidence: `docs/evidence/multi-computer/results.md`.

### Multi-computer E2E fixes, web side (2026-10-02, branch `feat/mc-fix-web3`)

Two bugs from the E2E re-run on `feat/multi-computer` (1c4857c). Each fix
has a vitest test (no jsdom); the 6 new or changed tests fail on 1c4857c.

- A link tap in the terminal consumes the click of the same tap
  (`areaClick` in `Live.tsx`), so the add-computer sheet no longer closes
  at once. Path taps still open Files.
- Back from the waiting add page keeps the add. The add page stays a full
  load (home's CSP for a document only allows the pending origin from the
  next load), but that load now puts the path the add started from (`back`
  in the fragment, or `/`) before the add page in its own history, so Back
  stays in the same document. The waiting page has **Back to terminal**
  (from a session) or **Sessions** (`LeaveAdd`). The grant is still
  stripped at once, and redeemed only after Continue, after the boot
  stray-worker guard of this load.
- Evidence: `pnpm --dir web test` (47 files, 660 tests), `cd web && npx tsc
  --noEmit -p .`, `pnpm build:web` and `./scripts/check` exit 0.

### Multi-computer confirming-review fixes, web side (2026-10-02, branch `feat/mc-fix-web2`)

Fixes the confirming-review and E2E findings on the phone app of
`feat/multi-computer` (d44f1cd). The host side has its own track. Every fix
has a vitest test (no jsdom); each new or changed test was run against
d44f1cd and failed there (20 tests).

- Add flow: a Cancel wins at every step. After approval it skips the
  confirm, signs out on the new computer and deletes its entry on home; a
  computer already in the list keeps its entry and cookie
  (`AddComputerFlow.finish`, `abandon`). The add now runs outside the page
  (`startAdd`, `cancelAdd`, `currentAdd`), so the user can leave
  `/computers/add` to type y in a terminal of this app; a bar shows
  "Adding vps… approve on vps" (**Open**, **Cancel**), then "vps is added"
  or "vps was not added" (**Dismiss**). Only Cancel, expiry or Log out stop
  it; the flow, grant and poll secret are dropped when it ends.
- Start-up: a preview link reads home's preview config itself when it opens
  before home answered (`homePreview`). Home's answer is adopted again after
  a re-pair or log out in the same tab, and a failed list read is tried again
  on the next good home poll (`homeAdoption`). When home's list drops the
  computer of the open Live view, the app goes to Sessions with "vps is no
  longer in your list." (`leaveRemoved`).
- Stray-worker guard: storage that throws still shows "this link was not
  used" and keeps the grant on a safe load; a later link in the same tab is
  checked again (`guardLink`) instead of being refused until a reload.
- Log out: a peer's logout waits at most `READ_TIMEOUT_MS` (6 s), all
  peers at once; the button says "Signing out…" (`LogoutSection`); the
  stored list (`pterm.computers`) is cleared (`logOut`).
- Terminal: a wrapped add-computer link is a tap target again after a
  resize to fewer columns (`bufferRow` reads only `cols` cells).
- Colours: by place in the list, home first, so no two of 8 computers
  share one (`toComputer(…, place)`; `colorFor` removed).
- Wall: a paused tile that was never live shows the session name and
  "Paused" (`tileMark` `blank`).
- Evidence: `pnpm --dir web test` (47 files, 656 tests), `cd web && npx tsc
  --noEmit -p .`, `pnpm build:web` and `./scripts/check` exit 0.
- Docs for the host track: `docs/multi-computer.md` still says "Colour from
  a fixed palette by origin hash"; it is now by place in the list.

### Multi-computer review fixes, web side (2026-10-02, branch `feat/mc-fix-web`)

Fixes the review findings on the phone app of `feat/multi-computer`
(d826c02). The host side has its own fix track. Every fix has a vitest
test (no jsdom); each new test was run against the unfixed code and failed
there, except the "gives up after about 3 s" bound check.

- Security: no grant is redeemed on a load that a stray service worker may
  still see (`workersSafe`, `guardStartup`: drop the grant, one guarded
  reload, then "Run pterm pair again"); the service-worker allow-list
  checks every worker of a registration and keeps a fresh own-scope
  registration; `/computers/add` shows the full origin and waits for
  **Continue**; Cancel deletes the pending entry on home; "Open pull
  request" never opens a same-origin app route; a peer's WebSocket error
  text is replaced by the app's own words (`ConnectionOptions.peer`);
  composer drafts and the last Changes tab are keyed by `host/id`.
- Correctness: with peers stored the app opens at once while home sleeps,
  and adopts home's settings, list and preview origin when home first
  answers; the one-computer screen is unchanged (no computer rows, "not
  reachable" after the first failure); the first peer settings read after
  approval is retried for about 3 s; Log out signs out of each reachable
  peer first and names the rest (`pterm revoke` there); a peer without
  `protocolRevision` is "Update Termivoa on X"; "Check again" reports a
  failed read; per-origin state is dropped on remove; polls, reloads and
  route reads go through one per-computer queue (`scheduler.serial`); dead
  code removed (`useSessionList`, `latestOnly`, `SESSION_POLL_MS`,
  `parseSessionKey`, the `waitingFirst` re-export).
- Second round (correctness review): Wall tiles start offscreen until the
  IntersectionObserver reports them; a load that throws at once frees its
  read slot; each session is validated once (whole body first, item by
  item only after a failure); Ajv stops at the first error
  (`allErrors: false`); Computers and AddComputer are lazy chunks, both
  precached; the full-rate tile cost is kept as a guard only. New release
  gate test in `web/src/bundle.test.ts`: a real build's `sw.js`, installed
  and then offline, serves `/wall` and every chunk the Wall needs from its
  cache (no DOM; a phone check of the rendered Wall stays with the E2E
  track). Start-up chunk 420 kB to 392 kB.
- Not covered by a test (no DOM in tests): the Settings device-name field
  now follows home's label when home answers late (`draft ?? current`).
- Evidence: `pnpm --dir web test` (47 files, 640 tests), `cd web && npx tsc
  --noEmit -p .`, `pnpm build:web` and `./scripts/check` exit 0.
- Phone texts for the docs (track D):
  - Setup, after a refused pairing link: "For safety, this pairing link
    was not used. Run pterm pair on your computer again."
  - Add page, after a refused add link: "For safety, this add-computer link
    was not used. Run pterm pair --via <home> on the new computer again,
    then paste the new link."
  - Add page first step: **Address** with the full origin, "Continue only
    if you just ran pterm pair --via on <label>. Next, this phone shows a
    code. Check it on <label>.", buttons **Continue** and **Cancel**.
  - Pair-again row: "<label> · pair again · Run pterm pair --via <home> on
    <label>".
  - Outdated computer: "Update Termivoa on <label>" uses the renamed label;
    a computer without `protocolRevision` now shows it instead of "can't
    reach".
  - Log out: "You logged out. This phone no longer has access. Could not
    sign out of <labels>. Run pterm revoke there to remove this phone."
    With 2 or more computers the Log out section says "Signs this phone out
    of every computer. Running sessions keep running."
  - One computer: rows such as "mac · can't reach" are gone again; the
    header says "Your computer · not reachable, retrying" after the first
    failure.
  - "Check again" now shows an error when a read fails.
  - A peer's live-session errors use fixed texts (`PEER_ERRORS` in
    `web/src/terminal/Connection.ts`), for example "That computer is busy.
    Try again."
  - "Open pull request" is not shown for a link to the app's own address.

### Multi-computer host review fixes, round 2 (2026-10-02, branch `feat/mc-fix-host2`)

Host side of the confirming review of `feat/multi-computer`. Not merged;
critical feature, owner review required (rule 24). Gate 0 has not run.

- Add a computer again after its node changed: a pending add over an entry
  pinned to another node (or none) replaces it with a pending entry for the
  current node, so the CSP allows the redeem and confirm succeeds; a full
  list drops entries the app no longer shows (`PutComputer` stale callback).
  Tests: `TestComputerAddAgainAfterNodeChange` (store and broker).
- Other Serve handlers: setup advice is "another computer or its own
  Tailscale Service hostname" (another port still gets the cookie); doctor
  warns (not counted) about handlers on other ports of this host, except
  Termivoa's own 8443 preview handler; setup is not refused for them. A
  setup refused for other handlers on 443 exits 3, and the Windows installer
  shows its own message for 3 (other failures keep the generic one). Tests:
  `TestDoctorReportsOtherHandlersAsAProblem`,
  `TestDoctorWarnsAboutOtherPortsOfThisHost`,
  `TestSetupRefusesOtherServeHandlersOnItsOrigin`.
- A Changed node (home reinstalled Tailscale) keeps its origin in the CORS
  union, so peers answer 401 `revoked` and the app shows "pair again";
  every credential bound to it is revoked, every other credential and grant
  refused. Test: `TestChangedNodeOriginReadsOnlyRefusals`.
- `Status.Trust` compares tag sets (sorted), not only tagged/untagged.
  Test: `TestOwnerRuleAndTrustVerdicts` ("tag swapped", "tag added").
- `FindBinary` checks every folder up to `/` and every link of the symlink
  chain (group/other-writable without sticky, or owner not root/current
  user, is refused; group root, and admin on macOS, pass), returns each
  refusal reason, and doctor and setup print it. The broker checks again
  before every `tailscale status` run. Tests:
  `TestFindBinaryRefusesWritableOrForeignFiles`,
  `TestCommandSourceChecksTheBinaryBeforeEachRun`,
  `TestDoctorSaysWhyTailscaleWasRefused`.
- Evidence: `go test -race ./internal/... ./cmd/...`, `./scripts/check`,
  `CGO_ENABLED=0 GOOS=windows go vet ./...`, `GOOS=linux` and the site build
  exit 0 (macOS host). Each test above was run with its fix reverted and
  failed.
- Pending: the `boundOrigins` schema description in
  `protocol/v1/gateway-rpc.schema.json` still says "whose node is still
  trusted" (now also Changed nodes); regenerating touches `web/src/generated`.

### Multi-computer host review fixes (2026-10-02, branch `feat/mc-fix-host`)

Host side of the review of `feat/multi-computer` (ADR 0005). Not merged;
critical feature, owner review required (rule 24). Gate 0 has not run.

- Pairing and removal: an approved poll from an app origin refreshes the
  gateway's origin sets, so the app's first `GET /api/v1/settings` is
  admitted with CORS (was 403 until the next probe). `DELETE
  /api/v1/computers/{host}` takes the `{}` body the app sends (was always 400).
- CORS bound origins come from every device ever bound to an origin (revoked
  and expired ones too) whose pinned node is still trusted, so a revoked or
  expired phone reads its 401 and shows "pair again". The broker still
  authorizes every request; a node change, a gone name or an unreadable
  status gets no CORS.
- Tailscale trust: owners are compared by stable user ID (login names are
  display only); a node whose tagged state changed since pairing is refused
  (not revoked); a status that is not `Running` (restart, logout) keeps the
  last good snapshot under the grace rules; a new snapshot wakes the trust
  sweep at once (`tailnet.Config.OnUpdate`), so untrusted streams close in
  well under a second after the refresh instead of up to 15 s later; the
  sweep reads only devices that can still connect; `Serve` waits for it.
- Finding `tailscale`: fixed install paths first (`/usr/bin`, `/usr/sbin`,
  the macOS app bundle, `/usr/local/bin`, `/opt/homebrew/bin`), PATH last;
  a binary or folder that is group/other-writable or owned by neither root
  nor the current user is refused (Unix); a missing CLI is looked up again at
  most once a minute, so installing Tailscale later needs no restart.
- Home computer list: migration 9 pins the node of each entry; confirm needs
  the same node, and an entry whose node no longer holds its name leaves the
  list and the CSP. `gateway.computers.*` only from the own origin. Listing
  is read-only; the admin list leaves out revoked devices.
- Broker: loopback origins count as own only in the development profile;
  upload commit re-checks the origin binding; logout authenticates once.
- CLI: `pterm revoke --via` always sends the request (it also drops pairing
  links and pending pairings); `pair --via` uses the app's labels ("Add this
  computer?", "+ Add a computer"); `pterm setup` refuses, before any change,
  while other Tailscale Serve handlers share this computer's origin, and
  doctor names them as a problem with the commands to move them; doctor says
  when the tailnet is unknown (logged out).
- Code: one `gatewayapi.Broker` (no `computerBroker`, no `Origin()`), one
  `cli.Admin`, no `tailnet.Status.Peers`, `inspectTailscale` parses once.
  Settings still asks the broker twice (`Authenticate` for the expiry, then
  `Settings`): returning the expiry would change the gateway RPC result type
  for one local IPC round trip on app start and foreground.
- Evidence: `go test -race ./internal/... ./cmd/...`, `./scripts/check`,
  `CGO_ENABLED=0 GOOS=linux go vet ./...` and `GOOS=windows` all exit 0
  (macOS host). Each fix's test was run with that fix reverted and failed.
- Pending: the schema description of `boundOrigins` in
  `protocol/v1/gateway-rpc.schema.json` still says "unrevoked devices"
  (regenerating would touch `web/src/generated`, owned by the web track);
  site docs and CHANGELOG for the changed texts; Gate 0; Playwright tests.

### Multi-computer D: user and repo docs (2026-10-02, branch `feat/mc-docs`)

Track D of `docs/multi-computer.md`, written against the integrated branch
`feat/multi-computer` (384d024), every claim checked in the code. All marked
**Next beta**; Gate 0 (real iPhone and Android) has not run, and the docs
say so.

- Site: new guide `site/src/content/docs/docs/guides/computers.md` (sidebar
  entry), and multi-computer sections in `reference/security.md`,
  `reference/cli.md`, `reference/troubleshooting.md`, `pair-your-phone.md`
  and `reference/release-notes.md`; a short mention on the landing page.
- Repo: `docs/threat-model.md` (four new trust boundaries and the new
  attackers, including the accepted home-compromise risk and its recovery),
  `protocol/v1/http-api.md` (cross-origin requests, cookie refresh, CSP),
  `CHANGELOG.md`, `README.md`, and the stale offline-bundle line in
  `docs/multi-computer.md` section 3.
- The H1a blocker above (gateway forwarded-origin key) is resolved on the
  integrated branch by e118a9c (`gatewayapi.WithForwardedOrigin`).
- Found while checking: `pterm pair --via` tells the user to tap
  "Add this computer" and to paste in "Settings → Computers → Add
  computer", but the app's labels are the **Add this computer?** sheet and
  **+ Add a computer** (`internal/cli/admin.go`, `pairVia`). **Log out** in
  Settings signs out of home only; peers keep the phone until it is removed
  or revoked there (documented).
- Evidence: `cd site && npx astro build` (links valid) and `./scripts/check`
  exit 0.

### Multi-computer H2: gateway and CLI (2026-10-02, branch `feat/mc-h2`)

Track H2 of `docs/multi-computer.md`, built against fake brokers until H1
lands (not integrated, not yet usable end to end):

- Gateway: every `/api` request needs an exact own Origin, an exact bound app
  origin (pairing routes also admit pairing origins), or no Origin with
  `Sec-Fetch-Site: same-origin`; `Sec-Fetch-Mode: no-cors` is refused.
  Admitted cross-origin requests get exact CORS headers, preflight only for
  those origins, and forward their Origin to the broker on every call.
  WebSocket origins are checked per request. `GET /api/v1/settings` refreshes
  the cookie (same value, Max-Age capped by the credential's absolute
  expiry). The app document's CSP `connect-src` lists the cookie device's
  computers (at most 8). New routes `GET/POST /api/v1/computers` and
  `DELETE /api/v1/computers/{host}`.
- CLI: `pterm pair --via <name|origin> [--allow-tagged]` (add-computer link
  `https://<this-host>/add-computer#grant=<grant>&via=<url-encoded app origin>`),
  `pterm computers [remove <origin>]`, a VIA column in `pterm devices` and
  `pterm pair pending`, `pterm revoke --via <origin>`, the computers hint on
  `pterm revoke <device>`, and doctor/setup checks (protocol revision, CORS
  list, tailnet and owner mismatches, other Serve handlers on this origin).
- Evidence: `go test -race ./internal/gateway/... ./internal/cli/... ./cmd/pterm/...`
  and `./scripts/check` passed; `CGO_ENABLED=0 GOOS=linux|windows go vet` on
  the same packages passed (macOS host).
- Pending: integration with H1 (broker RPCs, origin forwarding in
  `gatewayclient`, `localclient` admin methods), Gate 0 and the Playwright
  integration tests in section 6.

### Multi-computer H1a: broker trust and storage (2026-10-02, branch `feat/mc-h1a`)

Track H1a of `docs/multi-computer.md`, sections 3 and 4 (not usable end to
end until the gateway reads `gatewayapi.ForwardedOrigin`, see below):

- Store migration 8: `app_origin` and the pinned app node (ID, owner, tags)
  on grants, pending pairings and devices (NULL = own origin only; existing
  rows keep working), and the per-device `computers` table.
- `internal/tailnet`: an immutable `tailscale status --json` snapshot of
  own-tailnet nodes, `--via` resolution (full origin, or one DNS first label;
  ambiguity refused), the owner rule (`allowTagged` when either node is
  tagged), and a monitor whose readers never wait: single-flight refresh,
  10 s hard limit, 60 s cache, 10-minute grace, retries at most every 5 s.
- Broker: per-device origin binding on every authenticated gateway request
  (including live open, logout and upload), bound grants (redeem and poll
  only from their own origin), `boundOrigins` and `pairingOrigins` on the
  origin probe, `gateway.computers.*` (8 per device, pending entries expire
  after 10 minutes), `admin.computers.*`, `admin.device.revoke.via` (also
  drops grants and pending pairings via that origin). A 15 s sweep closes
  open streams from an origin that is no longer trusted (own-origin streams
  untouched) and revokes devices whose node ID changed.
- Evidence: `go test -race` on `internal/store`, `auth`, `broker`,
  `gatewayclient`, `localclient`, `tailnet` and `lifecycle`;
  `./scripts/check`; `CGO_ENABLED=0 GOOS=linux|windows go vet ./...` all
  exit 0 (macOS host). Broker tests: `internal/broker/computers_test.go`
  (origin binding with near-miss and `null` origins, tagged and other-owner
  nodes, an ambiguous short name, node change to revocation, the grace
  period, revoke-via closing streams and releasing control, the computer
  list limit and pending expiry); store tests: migration of existing rows.
- Blocking for integration: `internal/gateway` still keeps its own
  forwarded-origin context key, so `gatewayclient` sends no origin and the
  broker treats cross-origin requests as own. The gateway must use
  `gatewayapi.ForwardedOrigin`/`WithForwardedOrigin` before this ships.
- Pending: Gate 0, the Playwright integration tests in section 6, and a real
  tailnet check of `tailscale status --json` owners and tags.

### Linux host (2026-09-27, branch `feat/linux-host`)

Linux (Ubuntu 22.04/24.04, x86_64 and arm64, including servers and WSL) is a
supported host: `linux` shell-profile platform (store migration 7), XDG paths,
systemd user units with a separate linger question, a no-systemd fallback,
WSL notes, `/proc`-based preview ports, Tailscale operator detection,
`scripts/install-linux.sh` and `packaging/linux/build.sh`. Docs:
`docs/install.md#linux-desktop-server-and-wsl`.

Evidence (commands run by the agent on the Mac and in Docker 29.2.1, arm64):

- `gofmt`, `go vet ./...` for darwin, `CGO_ENABLED=0 GOOS=linux` amd64 and
  arm64, and `GOOS=windows`; `go test ./...` on macOS; `./scripts/check`.
- `go test ./...` as uid 1000 in `golang:1.27`: all packages pass. Before the
  two fixes below, `internal/broker` (upload slot race) and
  `internal/gateway` (Linux send buffer) failed there on `main` too.
- Privileged `ubuntu:24.04` container with systemd as PID 1: setup, doctor,
  a bash session, stop/launch, broker crash restart, shutdown, user-manager
  restart with linger, uninstall with data deletion (details in
  `owner-verification.md` section 3a).

Pending owner: real VPS `t3-vps` and an Ubuntu desktop, x86_64, polkit linger,
Tailscale operator and Serve, a phone over Tailscale, zsh and fish.

### Phone UI v2 (2026-09-27)

The owner approved the Paper design "termivoa" (session tabs, agent keys,
keycap dock, snippets, launch presets with run first, redesigned Sessions,
Pair, Composer, History and Settings). It is implemented in `web/**`, with
two optional broker session-list fields (`lastOutputAt`, `preview`) for the
new-output dot and the list preview. An adversarial agent review fixed four
defects (overlapping polls, pairing code not read by screen readers, duplicate
snippet ids, hidden SGR-8 text in previews). The full local check (155 web
tests) and Go race tests pass; a real dev-stack browser run at 390×844 passed
pairing, run first, agent keys (a real `⌃C` interrupt), snippets, tab switching
with the new-output dot, and the session list preview. Real-phone checks are
listed in `docs/owner-verification.md`.

### First-run polish merge without CI (2026-09-27)

The owner asked to merge PR #28 (3-step setup, origin fix, bandwidth pacing
and compression, terminal/environment/desktop fixes, web polish) the same way:
the Actions minutes were still used up, and every CI job was blocked before it
started. "Include administrators" was turned off only for the squash merge and
restored right after. The full local `./scripts/check` (117 web tests) and
`go test -race ./internal/... ./cmd/...` passed on the merged head.

### v0 merge without CI (2026-09-27)

The owner asked to merge the v0 stack without hosted CI: the account's 3,000
included GitHub Actions minutes for September were used up, and they reset in
early October. PR #27 contains all of #23–#26 and was squash-merged alone into
`main` with administrator bypass; "include administrators" was turned off only
for that merge and turned back on right after. #23–#26 were closed as included
in #27. Before the merge, an adversarial review of all five PRs found 25 real
defects; all were fixed with failing-first tests except desktop mouse
forwarding, which is documented as out of scope for v0. The full local
`./scripts/check` and `go test -race ./internal/... ./cmd/...` passed on the
merged head (macOS arm64). Native Windows tests, the Linux `/proc` path and the
packaging workflow have not run in CI yet: run CI on `main` when minutes reset
and fix any failure in a follow-up PR.

### v0 stack (2026-09-27)

The P3–P6 work is pushed as stacked private PRs: #23 (P3 desktop attach and
live client) → #24 (P4 pairing, broker auth and gateway) → #25 (P5 phone
website) → #26 (P6 unsigned packaging and lifecycle commands) → the v0 polish
PR. (Superseded the same day: #27 replaced #23–#26 and was merged; see
"v0 merge without CI" above.) None is merged yet: GitHub Actions did not start any job because of the
account billing/spending limit, and branch protection requires the ten CI
checks. Full local `./scripts/check` passed at the head of each PR. The
configured Greptile review did not respond on these PRs; same-account agent
review passes are recorded in each evidence file instead.

A real end-to-end run on this Mac (`pterm up --dev`, desktop Chromium at
390×844) passed: pair → approve → session list → new zsh session → Take
control → typed command → real shell output; `pterm attach` observed the same
session read-only and detached with Ctrl+]; a broker restart marked old
sessions `lost` and the browser credential stayed valid. Everything that needs
a real phone, native Windows, Tailscale HTTPS, a real login, or signing
credentials is listed in `docs/owner-verification.md`.

### Earlier local integration checkpoint (2026-09-21)

The reviewed P3 process, management CLI, live IPC, and desktop-client components
are combined on `feat/p3-desktop-integration`. Main remains at the privately merged
PR #22 checkpoint `9d212651efe331f35fdbb66038905e705125072f`; local integration is
not described as merged. GitHub API DNS lookup is currently timing out, so remote
visibility/review cannot be refreshed and these checkpoints have not been pushed.
No authentication, account, or network settings were changed to work around it.

Implemented locally:

- Independent broker/gateway process modes and broker-owned session persistence.
- Real `list` and explicitly confirmed `terminate` commands over restricted IPC.
- Canonical observer/control/input/snapshot/history live IPC and a client tested
  against a real Mac broker and PTY, including same-child reconnect.
- Bounded desktop input decoding, cancellable owned terminal I/O, join-before-TTY
  restoration, and reversible Windows console setup (native execution pending).
- Exact session selection and public `new`/`attach` commands, including one-shot
  creation, read-only attachment, explicit control, detach and TTY restoration.
- Actual executable attachment in disposable Mac outer PTYs, plus isolated real
  zsh, less and Vim compatibility fixtures. No owner shell configuration is used.

The full `./scripts/check` at integrated `d227802` passed, including 47 web tests.
The full Go race suite passed at `2c1bf3c`; later command checkpoints have focused
race evidence, not a claim that this older full run covers them. The real outer-PTY
executable test additionally passed twice at `df6d8d2` (16.144s). A concurrent
earlier broad race run failed a real-session retirement test and a viewer
resnapshot test. The viewer cancellation/epoch races were reproduced and fixed;
the retirement timeout remains unclassified despite passing focused reruns.
A separate earlier
executable-broker readiness timeout remains unclassified, documented in
`docs/evidence/p3/process-fixture-readiness-investigation.md`. These failures are
not erased by green reruns. Native Windows 11, real-phone, authentication, signed
installer, performance/soak, and release gates remain pending.

Desktop startup input is now admitted only for the current connected, ready
controller epoch, and an early Ctrl+] at the startup handoff detaches instead of
being flushed (2026-09-27, `docs/evidence/p3/desktop-input-admission.md`).
Native shrink-then-grow resizing is covered by the outer-PTY executable test
(`docs/evidence/p3/desktop-native-resize.md`). The active-output termination
drain fix is integrated (`docs/evidence/p3/active-output-shutdown.md`).
Native Windows execution of these paths remains pending.

### Milestone record

The finite-output probe reproduced a broker shutdown wait/drain cycle during
active PTY output. The local lifecycle fix keeps the actor draining canonical
output while one bounded termination worker runs; cleanup joins that worker,
process Wait and PTY EOF. Deterministic tests cover Close, explicit termination,
lost state, repeated/canceled admitted requests, error identity, queued input
revocation, authoritative replies racing Done and continuously queued Status
traffic that must not starve output drain. Native continuous-output Mac
Close/Terminate regressions fail on the old runtime and pass with the fix.
Independent source review, affected race tests, vet and Windows compile-only
checks pass. Native Windows and sustained multi-session stress/soak remain
pending. See `docs/evidence/p3/active-output-shutdown.md`; this is a local,
unpushed checkpoint, not a complete P3 or release gate.

P0 — Private repository, preflight, and scaffold: **complete**.

P1 — Go terminal core: **in progress**, tracked by private issue #7. The owner
approved the documented ConPTY query-response and macOS process-group
boundaries on 2026-09-20; native Windows 11 validation remains pending.

P2 — Frame and input protocol: **in progress**. The first checkpoint was
merged from private PR #10 as `0e7b870c5ad1be1c63c99a080437cd04b8ef16ad`.
The canonical protocol, normalized frame/patch model, browser renderer/input
foundation, and minimal desktop client are implemented. P1 is not represented
as complete, and P2 remains in progress until its live connection acceptance
tests are integrated.

P3 — Persistent broker: **in progress**, tracked by private issue #11. The
controller/input-ingress checkpoint was merged from private PR #12 as
`1ac199dfea8a2e5aa6d86bcab70cce80183679e9`. The bounded local handshake
checkpoint merged from private PR #13 as
`a92d1ca1836df29c4f3a72bc772bed8b6d354b54` and the
broker-owned live-session runtime merged from private PR #14 as `d3ff4fd`. The
session registry merged from private PR #15 as
`400f48460ea7546cb815f631439b1f1933a2c645`, followed by SQLite persistence
from private PR #16 as `bc361141995f1bbf1d5a273ac38835fa9ee7cc3d`, and secure
platform IPC merged from private PR #17 as `9ab4a3e99e7f15d8634315e2e1e22eda059354d0`.
Viewer delivery merged from private PR #18 as
`1ee75ea4717562905ccb79175729b999df072346`. Durable operation receipts merged
from private PR #19 as `fe968ec303e31cd9dd34cf56540733686f1df974`, adding
broker-integrated deduplication for session create, rename, and terminate,
shutdown admission barriers, clock-rollback-safe monotonic persistence, and
retained runtime/cleanup diagnostics.
Broker-management RPC merged from private PR #20 as
`e14cc5722066f714db2c3a71a4d5442b11df4089`.
Immutable history paging merged from private PR #21 as
`f74f7620746ba1646e7a056886558f23dbbc795a`.
Broker-management dispatch merged from private PR #22 as
`9d212651efe331f35fdbb66038905e705125072f`. A local management-client
checkpoint is now implemented on `feat/p3-management-cli`: it authenticates as
the local CLI, enforces bounded and exactly correlated management RPC, exposes
only saved configuration IDs for create, and distinguishes a typed rejection
from uncertain mutation delivery without retrying. The user-facing command
layer implements bounded `list` and exact-ID, explicitly confirmed `terminate`
orchestration, including safe rendering of legacy control characters and
honest already-exiting/exited/lost output. Thin `cmd/pterm` routing for `list`
and `terminate` is integrated locally; `new` and `attach` now use the canonical
live-session stream. Focused race, full repository, and Windows
cross-build evidence is recorded in
`docs/evidence/p3/management-cli.md`.
Independent review additionally moved operation-ID validation inside the
serialized client exchange so invalid acknowledgements close the connection
before queued mutations can write. Confirmation now requires a context-aware
input reader; cancellation and late-answer regressions pass without a reader
goroutine. An owned OS-descriptor/handle input adapter is now implemented;
actual macOS pipe and pseudo-terminal cancellation, descriptor ownership,
TTY/flag restoration, and Close interruption tests pass under the race
detector. Windows tests compile to x86-64 PE, including an isolated native
console fixture, but native Windows execution remains pending. Public desktop
command routing is still an explicit integration gate.
The same owned-handle infrastructure now supports cancellable terminal
output. Actual macOS stalled-pipe and aliased input/output PTY tests pass;
`Close` joins blocked writes while preserving caller-owned stdout. Desktop
command integration and native Windows execution remain pending, documented
in `docs/evidence/p3/terminal-output.md`.

The live local-stream checkpoint is implemented on `feat/p3-session-ipc`:
canonical CLI-only desktop negotiation, observer attachment, correlated
actor-ordered control grants, structured input acknowledgements, history,
resize/resync, and a bounded cancellable writer. It also rejects malformed
Unicode and invalid mouse buttons. Full repository/race/cross-compile checks and
independent integration review pass; evidence and fixed review findings are in
`docs/evidence/p3/session-ipc.md`. This does not grant gateway/browser access,
complete desktop CLI attachment, or claim physical-device support.

Independent integrated-client review reproduced a late output-cleanup error
being hidden after `Close` initiated cancellation. The live client now retains
substantive output errors before the joined painter exits, without turning
ordinary cancellation or normal network closure into a failure. Focused race
regressions and desktop/liveclient/CLI package tests pass; synthetic failure
injection and platform limitations are recorded in
`docs/evidence/p3/live-client-cleanup.md`.

The viewer checkpoint provides broker-owned
bounded viewer subscriptions, exact snapshot/patch revision boundaries,
slow-viewer isolation, actor-owned acknowledgement expiry, reliable bounded
detach delivery, screen-only live capture, and a conservative encoded-row
transport limit. Focused race/vet and the complete repository check pass;
evidence is in `docs/evidence/p3/viewer-delivery.md`.

The broker-management RPC checkpoint defines and validates bounded local IPC
requests/results for session list, create, rename, terminate, and status.
Mutation operation IDs are distinct from request correlation IDs, and create
requests can reference only saved project and shell-profile IDs. Request
dispatch merged from private PR #22 as
`9d212651efe331f35fdbb66038905e705125072f`: accepted current-user
connections complete the handshake, execute sequential correlated RPC, resolve
process configuration inside the broker, return typed bounded errors, and
survive gateway reconnects without starting a second process. Validation
evidence is recorded in
`docs/evidence/p3/broker-management-rpc.md` and
`docs/evidence/p3/broker-management-dispatch.md`.
The protocol audit then tightened exact case-sensitive JSON field matching,
lowercase UUID canonicalization, schema-level session identity invariants, and
consistent UTF-8 title semantics while preserving stored legacy titles. The
dispatcher now enforces response correlation. Independent broker/gateway
process modes are implemented on `feat/p3-process-modes`: the broker acquires
canonical data-directory and runtime IPC locks before opening SQLite, retains
data ownership through Registry/Store cleanup, owns generation recovery and
the Registry, while the gateway owns only an exact-Host loopback health
bootstrap and restricted IPC probes. macOS runner and executable-process tests
prove a gateway restart preserves the same child/session generation and
advancing canonical output; startup recovery marks prior live metadata lost
without rerunning it. Review regressions also prove that a same-data broker with
a distinct runtime cannot overlap live ownership or shutdown cleanup, bare and
partial development modes remain isolated from canonical production paths, and
gateway timeout cleanup force-closes incomplete HTTP connections without
suppressing the timeout error. Evidence is in `docs/evidence/p3/process-modes.md`.
Authenticated HTTP/WebSocket-to-live-session dispatch and native Windows 11
execution remain pending.

The history-paging checkpoint provides actor-owned normal-history
epochs/revisions, immutable pages of at most 200 rows, stale-token
`history.changed` results, viewer-relative history-reset signaling, and strict
5,000-row/32 MiB normalized retention applied to the emulator scrollback
itself. Alternate-screen state has no hidden scrollback. Focused race tests
pass; full repository and Windows cross-compile evidence is recorded in
`docs/evidence/p3/history-paging.md`.

The follow-up boundary audit found and fixed a retained-wide-cell resize
blocker: shrinking a history row with a width-2 grapheme in the new final
column previously produced an invalid row without a continuation. The adapter
now transports a clipped single-column replacement at that width and restores
the original grapheme if a later width can represent it. Repeated resize,
alternate-screen, retention, race, and transport fixtures cover the boundary.

The final review audit also found that internal screen patches omitted the
frame's history row/byte/token metadata. Patch diff/apply now round-trips all
four fields, canonical wire patches read the token from that patch, and the
reset signal remains derived from each viewer's applied base epoch. Actor-backed
append/reset regression tests, the full Node 24 check, race tests, and Windows
amd64 compile-only checks pass.

An independent dispatch audit found that initial mutation responses were being
rebuilt from mutable saved configuration or current session metadata. The
checkpoint now persists the exact bounded response snapshot with every receipt,
checks create receipts before resolving saved configuration, and returns
rename/terminate receipt snapshots without rereading current state. Regression
tests cover disabled create configuration, rename A/B/replay A, and terminal
state change before terminate replay. Migration 003 rewrites old create
fingerprints to the stable project/profile-ID identity. A later PR review found
that v2 create receipts could not prove an original title after a later rename;
the migration now marks unprovable legacy create/terminate results unavailable
instead of manufacturing a hybrid response, and retries fail deterministically
without rerunning the operation. Store, Registry, and RPC regressions cover the
boundary. Final validation and the follow-up independent audit are recorded in
`docs/evidence/p3/broker-management-dispatch.md`.

The P1 terminal-core checkpoint is implemented and merged. No benchmark,
physical-phone test, installer, signing operation, or release has been
completed. Unexecuted later-milestone gates remain pending.

The desktop decoder-boundary checkpoint reproduces upstream Ultraviolet paste
and incomplete-sequence accumulation beyond the product limit. A documented
local patch now applies opt-in early limits, typed errors, failed-reader
invalidation, and cancellation that joins the input pump. The desktop adapter
selects 64 KiB limits. Retained upstream and adapter race suites pass on macOS;
Windows test binaries cross-compile only. Actual command wiring and native
Windows console validation remain pending. See
`docs/evidence/p3/desktop-decoder-bounds.md`.

## Verified preflight facts

| Item | Evidence observed on 2026-09-20 | Status / milestone impact |
| --- | --- | --- |
| Workspace | Isolated `phone-terminal` checkout; new Git repository on `main`, with no commit at inspection time | Approved workspace is available for P0. The machine-specific absolute path is intentionally not recorded. |
| GitHub identity | Authenticated `gh` session reports account `intojhanurag` | Intended owner identity verified without printing credentials. |
| GitHub destination | GitHub API reports `intojhanurag/phone-terminal`, visibility `PRIVATE`, and no default branch before the first push | Private-first target verified. Visibility must remain private. |
| Git author | User name and email are configured locally; values were not copied into this document. No commit-signing requirement is configured. | Bootstrap commits used the configured identity; no signing setting was disabled. |
| Development host | Apple Silicon (`arm64`) macOS 26.5.2 | Local P0 work and later macOS testing are possible; a complete P1 claim still requires native test evidence. |
| Go toolchain | System Go is 1.21.13; Go 1.27.1 was fetched and verified through `GOTOOLCHAIN=go1.27.1` without replacing the system installation | Locked P0/P1 toolchain is available per repository command; global Go remains unchanged. |
| Web toolchain | Default PATH has Node.js 25.8.1; nvm already contains Node.js 24.11.0, selected with pnpm 10.33.0 for repository checks | Locked P0 web toolchain is available without changing the global default. |
| Tailscale | CLI 1.102.4 is installed; backend reported `Stopped` | No configuration was changed. P4 production phone access requires owner authorization/login and conflict-safe Serve setup. |

Note (2026-09-27): the repository was later renamed to `intojhanurag/Termivoa`;
the old name redirects. It is still private.

Commands used for this read-only preflight included `gh api user`, `gh repo view`, `git config --get`, `uname -m`, `sw_vers`, tool version commands, and a minimal `tailscale status` query. No token, raw credential, private hostname, or Tailscale Serve configuration was recorded here.

## P0 deliverables

- [x] Verify authenticated GitHub owner and exact destination.
- [x] Verify the destination is private before first push.
- [x] Create truthful open-source preparation and repository-policy documents.
- [x] Record architecture and fixed decisions without claiming implementation.
- [x] Add the authoritative plan at `plan.md` and `docs/implementation-plan.md` (the root `plan.md` copy was removed on 2026-10-02).
- [x] Add Apache-2.0 `LICENSE` text.
- [x] Add editor, ignore, and attributes policy files.
- [x] Resolve and pin exact Go/web/tool dependencies and commit lockfiles.
- [x] Add repository-owned Unix and Windows check tasks.
- [x] Add pinned GitHub Actions workflows with required stable check names.
- [x] Run staged-file and secret review.
- [x] Create and push the private bootstrap commit; SHA `fbaeaeb382662a83f9498715896e722acf742694`.
- [x] Verify supported branch/ruleset controls and configure required PR/check/conversation/force-push/deletion rules.
- [x] Record final bootstrap CI results after the release-smoke repair is merged.

The P0 exit criterion is met: the private repository, bootstrap push, repository
controls, final CI run, and access blockers are recorded above.

## Owner-dependent and external blockers

These questions must be resolved before their dependent gates; they do not block independent documentation work.

| Need | Current state | Blocked gate |
| --- | --- | --- |
| Required toolchains | Go 1.27.1 is available through Go toolchain selection; Node 24.11.0 is available through nvm | No current P0 toolchain blocker; developers must explicitly select Node 24. |
| Windows 11 x64 access | Owner has a physical Windows laptop but cannot make it available during the current implementation session | Native P1 lifecycle and P6 installer validation remain pending; native GitHub Windows CI continues as supplemental evidence |
| Physical iPhone and Android access | Test arrangement not established | P4 private cellular connection and P5 physical-device acceptance |
| Tailscale login/configuration | Installed but stopped; no authorization to log in or alter Serve configuration | P4 private HTTPS evidence |
| Host installation consent | Not requested; no startup/PATH/install changes made | P6 installation and startup evidence |
| Coding CLI and disposable test repository | Claude Code and Codex CLI ran in a disposable empty project on the Mac development profile (`docs/evidence/p7/coding-agents.md`) | A real-phone agent run remains in `owner-verification.md` |
| Apple signing/notarization | Membership, identities, and approved credential location not established | Signed macOS P6/P8 artifacts |
| Windows code signing | Owner-approved trusted identity and key access not established | Signed Windows P6/P8 artifacts |
| Public identity | Name decided: Termivoa (2026-09-27; repository `intojhanurag/Termivoa`; kept compatibility names in `docs/naming.md`). Publisher identity for signing and publication permission not confirmed | Public-release P8 handoff |
| Hosted CI | GitHub Actions jobs do not start because of account billing; `main` was merged without hosted CI (2026-09-27) | Native Windows CI, packaging workflow, required checks on later PRs |
| Public contacts | Done (2026-09-27): owner chose aojharaj2004@gmail.com for security reports (`SECURITY.md`) and Code of Conduct reports | No current blocker |
| Branch enforcement | Supported and configured on private `main`: required PR, strict named checks, resolved conversations, linear history, admin enforcement, no force push, no deletion | No current blocker. Squash is the only enabled merge method. |
| Existing GitHub App | `Greptile Review` runs automatically on private PRs; the agent did not install it | Owner confirmed on 2026-09-20 that Greptile is intentionally authorized to review this project. It is not a required branch-protection context and its confidence threshold is configured not to block merging. |

## Milestones

| Milestone | Status | Evidence |
| --- | --- | --- |
| P0 — Private repository, preflight and scaffold | Complete | PR #5 squash-merged as `6ece2af8ea46fbf5bc7221dfba1b677ca7ea02e8`; final-head CI run `35509425646` passed all ten required checks |
| P1 — Go terminal core | Implemented on macOS; native Windows 11 pending | PR #8 merged as `0342a0058e088b4a4970eb23ec2e150aac6f0232`; architecture boundaries approved; native Windows 11 execution remains pending |
| P2 — Frame and input protocol | Implemented; physical-device evidence pending | Private issue #9; PR #10 merged as `0e7b870c5ad1be1c63c99a080437cd04b8ef16ad`; checkpoint evidence in `docs/evidence/p2/frame-protocol-validation.md`; live broker/gateway connection and physical-device evidence remain pending |
| P3 — Persistent broker | Merged in #27 (2026-09-27); native Windows pending | Private issue #11; controller/input-ingress and local IPC merged through PR #13; broker-owned runtime and bounded registry merged through PR #15; SQLite persistence merged through PR #16; platform IPC merged through PR #17; viewer delivery merged through PR #18; durable operation receipts merged through PR #19; broker-management RPC merged through PR #20; immutable history paging merged through PR #21; broker-management dispatch merged through PR #22; process modes, live-session IPC and desktop attach landed through PR #23, merged as part of #27; evidence under `docs/evidence/p3/` |
| P4 — Authentication and private access | Merged in #27 (2026-09-27) | `docs/evidence/p4/`; real phone over cellular and Tailscale Serve pending owner |
| P5 — Phone experience | Merged in #27; UI v2 in #30 (2026-09-27) | `docs/evidence/p5/phone-web.md`; physical iPhone/Android pending owner |
| P6 — Install and update | Merged in #27; first run in #28 (2026-09-27); unsigned | `docs/evidence/p6/`; Windows installer run, real startup registration and signing pending owner |
| P7 — Reliability and review | In progress | `docs/evidence/p7/` (Mac soak, coding agents, terminal fixes); Windows soak, 24-hour soak and stress run pending |
| P8 — Release preparation | Not started | None |

## Repository and CI evidence

The repository API reports private visibility, full owner admin/push/pull permissions,
`main` as the default branch, and squash-only merging. Branch protection requires
the ten stable CI contexts defined in `.github/workflows/ci.yml`.

The first bootstrap CI run (`35508287671`) passed protocol generation, Go quality,
Go race, web quality, macOS/Windows scaffold integration, secret scanning, and
vulnerability scanning. Both release-smoke matrix entries failed because the
unanchored `pterm` ignore pattern excluded `cmd/pterm/main.go` from the bootstrap
commit. Branch `fix/p0-bootstrap-ci` anchors the binary ignore rules at repository
root and adds the previously excluded scaffold command.

At repair head `b6cffa1c42d12be48229acc78c6b8900dc6714f5`, all ten
repository-owned CI checks passed in run `35508613947` (runner/check naming was
also pinned and corrected). An independent self-review found no missing Section
19 files, direct collaborators, webhooks, releases, tags, or deployments. P0 is
tracked in private issue #6 and linked from PR #5. The review also identified
the pre-existing `greptile-apps` check on PR #5. On 2026-09-20, the owner
confirmed that Greptile is intentionally authorized for this project. GitHub
branch protection requires only the ten repository-owned contexts, so Greptile
does not gate merging. Its concrete review finding was fixed before resolution.

PR #5 was squash-merged on 2026-09-20 as
`6ece2af8ea46fbf5bc7221dfba1b677ca7ea02e8`, automatically closing issue #6.
Final-head Actions run `35509425646` passed all ten required checks. The
repository remained private after merge, and `main` retained its protection.

## Known defects

Termination boundary decision (2026-09-27, pending owner approval): on Unix,
terminate and shutdown now signal every process in the managed PTY session, not
only the session shell's process group that the owner approved on 2026-09-20
(plan section 6). This ends interactive-shell background jobs. Session members
get the 500 ms grace period before SIGKILL, also when the shell exits on its own.
Processes that call `setsid` stay outside. See
`docs/evidence/p6/cli-lifecycle.md` and `docs/owner-verification.md`.

The public `new` checkpoint validates an interactive 80x24 initial viewport before
creating from saved project/shell IDs, then attaches and explicitly takes control
after the first snapshot. Native Mac command/PTY tests prove detach/cancellation
preserve the new session and restore caller terminals. Errors after creation retain
recovery guidance; no retries or destructive rollback occur. Local project
registration and release/device gates remain pending; see
`docs/evidence/p3/new-command.md`.

The controlled-resize follow-up replaces the demonstrated shrink disconnect with
a bounded Syncing/resnapshot transition. Focused race tests cover stale frames,
fixed timeout, observer/takeover boundaries, no input replay and zero-byte versus
partial/joined output failures. CLI/liveclient Windows test compilation passes;
native execution and the independent outer-PTY executable shrink fixture remain
pending. See `docs/evidence/p3/desktop-controlled-resize.md`.

Independent review of the resize candidate reproduced and fixed an old-snapshot
acknowledgement overtaking a resize publication, stale cached dimensions blocking
grow-back, and queued startup input during blocked Control status output. Setup
now drains a separate bounded decoder, joins it, discards remaining owned raw
input, then starts a fresh live decoder. Initial control waits for the requested
canonical grid. Focused race/vet and actual Mac raw-PTY startup regressions pass;
an independent working-tree executable shrink/grow run also passed. Repeated
committed executable evidence and native Windows remain separate gates. Partial
input events spanning later live readiness changes are not yet established by
these startup tests. Details and exact commands are in the same evidence record.

The public desktop attach checkpoint connects exact session generations through
restricted IPC, defaults to read-only, and accepts explicit `--control` only after
the initial snapshot is applied. Focused Mac race and disposable-PTY restoration
tests pass; Windows compile-only evidence is not a native pass. Exact-fit grids
are preserved; smooth window shrink and initial resize readiness are being fixed
in separate reviewed checkpoints. The independent executable/broker integration
suite passes on Mac, including read-only input discard, takeover, Unicode paste,
literal Ctrl+], Ctrl+] detach, SIGTERM restoration and same-child reconnect. See
`docs/evidence/p3/desktop-attach-command.md` and
`docs/evidence/p3/desktop-outer-pty.md`.

An explicit Mac `nativecompat` suite now exercises installed zsh 5.9, less 668,
and Vim 9.1 through broker-owned PTYs and structured input. Editing, Ctrl+C,
Ctrl+D, held multiline paste, Unicode text, pager navigation, actual editor
save, and alternate-to-normal buffer transitions pass on the available Mac.
The headless normalized-frame consumer does not establish outer-terminal or
phone glyph layout. Native Windows and owner login profiles remain separate
gates; evidence and the explicit command are
in `docs/evidence/p3/native-mac-applications.md`.

The CLI selection checkpoint centralizes exact session-generation resolution and
adds one-shot creation from saved project/shell IDs. It preserves uncertain-delivery
warnings and never retries or restarts a lost session. Focused Mac race tests and
vet pass; public `new`/`attach` wiring is now integrated locally. See
`docs/evidence/p3/cli-session-selection.md`.

The live-client viewport checkpoint provides a pre-paint check for the actual
desktop grid, including snapshot transfer and observer-resize changes. Focused
race tests prove rejected drawings are not written or acknowledged and exact-fit
grids remain valid. Public desktop command integration and native resize behavior
are pending; see `docs/evidence/p3/live-client-viewport.md`.

The real-session registry retirement fixture once exceeded its eight-second
deadline under concurrent full race/repository checks. Eleven further focused
race invocations, two complete ordinary Go suites, and one full race suite
passed with overlapping runs. Failure-only synthetic-child/lifecycle probes
never fired, so the cause remains unclassified; no deadline, retry, warmup, or
runtime change was made. Evidence is in
`docs/evidence/p3/retirement-readiness-investigation.md`.

The command integration checkpoint wires `pterm list` and confirmed
`pterm terminate` through the real restricted IPC client. Native Mac tests use a
disposable PTY child and verify decline, confirmed exit, and idempotent reporting
of an already-exited session. Focused race/vet, Windows cross-build, and frontend
checks pass. Its initial full repository check failed the existing PTY fixture
READY timeout, which remains under investigation rather than being counted as
green. See `docs/evidence/p3/management-command-integration.md`. Desktop attach,
phone access, and release readiness are not claimed by this checkpoint.

The Windows desktop console checkpoint adds the missing reversible UTF-8 code
pages and output VT-processing setup around the outer terminal. Injected policy
tests pass on Mac, and Windows cross-compilation is supplemental only. Native
Windows console/Unicode execution and complete desktop command wiring remain
pending. See `docs/evidence/p3/windows-console-modes.md`.

The desktop shutdown checkpoint reproduced restoration of the enclosing terminal
before the input decoder had stopped. The coordinator now connects after terminal
entry, joins input and display workers before restoration, and observes connection
loss. Focused race tests and Windows cross-compilation pass; native terminal/client
integration remains a separate pending gate. Evidence is recorded in
`docs/evidence/p3/desktop-shutdown.md`.

Independent command review reproduced blocked stdout preventing cancellation
of listing and termination confirmation. Management output now uses the
owned, context-aware pipe/terminal adapter and joins its cleanup. Actual Mac
command regressions verify cancellation with saturated pipes, preserved caller
descriptors/flags, no consumed confirmation, no termination, and no duplicate
leak; both regressions fail with the previous command implementation. Regular
file/custom-writer output retains explicitly documented synchronous limits.
Focused race/vet and Windows cross-compilation pass after the fix; native
Windows and the earlier full-check PTY fixture issue remain pending.

The initial release-smoke failure above was fixed and merged. P1 found two
defects in pinned dependencies and carries narrow reviewed source patches:
arbitrary `vt.Write` boundaries split grapheme clusters and xpty leaked the
original Windows process handle after reopening it for `exec.Cmd`. The patch
record and removal criteria are in `docs/dependency-patches.md`.

Native Windows 11 lifecycle execution is still blocked on machine/VM access;
cross-compilation is not a platform pass. Missing implementation and unexecuted
gates are not counted as passing behavior. Native Windows Server 2025 CI also
demonstrated that ConPTY consumes child DSR/CPR queries before they reach the Go
emulator and supplies the response itself. On 2026-09-20 the owner approved
treating broker-owned ConPTY as the Windows platform responder for queries it
consumes, while retaining the exactly-one-response rule and prohibiting viewer
responses. The owner also approved the macOS PTY process group as the managed
termination boundary; a descendant that deliberately creates a new session is
outside that boundary. These boundaries are resolved design decisions, not
claims that native Windows 11 validation has passed.

P2 exposed a width mismatch in the unmodified pinned
`@xterm/addon-unicode-graphemes` provider for required Extended Pictographic
ZWJ fixtures. The implemented server-cell adapter retains that provider,
repairs its GB11 classification, and applies each validated canonical cell's
explicit width while painting. Automated xterm-buffer fixtures pass; physical
Safari and Chrome font/layout agreement is still pending and is not inferred
from these tests.

The P3 viewer-delivery design audit found that a maximally escaped normalized
row can exceed the 1 MiB outbound-message limit even though snapshot chunks
cannot split a row. The viewer checkpoint now applies a
conservative encoded-row budget, clipping marker, persistent per-row clipping
state, and boundary fixtures. The gateway adapter is still unimplemented and
must honor the viewer sink's 2 MiB drain-accounting contract before this can be
claimed end to end.

The P3 session-registry checkpoint retains a bounded in-memory create-operation
cache as a concurrency optimization. SQLite now owns the durable result for
session create, rename, and terminate mutations, including fingerprint conflict
detection across broker/store reopen. Active-session receipts are never pruned;
terminal history is bounded, and new unique mutations are rejected if active
receipts alone fill the hard cap. SQLite also records project, shell-profile,
and lifecycle metadata and marks prior live rows lost when a new broker
generation begins. It does not restore dead processes or terminal contents.

The history-paging checkpoint's broad ten-repeat race sweep observed one
timeout in the existing viewer-resync test while waiting for a third snapshot.
The test passed 20 isolated race repetitions afterward; the full affected
race run and 50 history-specific repetitions also passed without a race
report. This remains recorded as a non-reproduced timing flake in
`docs/evidence/p3/history-paging.md`, not as native-platform evidence.

The recurring full-suite PTY fixture readiness failure was reproduced with the
fresh child still at macOS `_dyld_start`, before the Go fixture could emit
`READY`. Test setup now separately validates and times one bounded native
activation probe; the original two-second PTY I/O deadlines remain unchanged.
The fixture directory cleanup also now runs before `TestMain` exits. Full-suite
race tests and twenty focused PTY race repetitions passed; native Windows is
still pending. See `docs/evidence/p3/pty-fixture-activation.md` for the sampled
failure, exact checks, and distinct cold-launch versus PTY validation scopes.

- **Preview proxy kept a Termivoa cookie name in odd spellings** (#150,
  `fix/issue-150`, 2026-10-03; from a code read, then tested): one function,
  `namesTermivoaCookie` in `internal/gateway/preview.go`, now decides for
  `Set-Cookie` (dev server to browser) and `Cookie` (browser to dev server).
  Before the fix these passed through in both directions: empty name
  (`=__Host-pterm_preview=x`), other letter case (`__HOST-pterm_session`,
  `PTERM_DEV_SESSION`), quoted name, and a Termivoa cookie folded after
  `;` or `,` in a `Set-Cookie` line. A space or tab before the name was
  already dropped. Tests `TestPreviewDropsTermivoaSetCookieShapes` and
  `TestPreviewDoesNotForwardTermivoaCookieShapes` fail before the fix. Not
  run: a real browser (the report says Chrome and Firefox reject such
  cookies; WebKit unchecked).

## P4 pairing and authentication (broker side)

On 2026-09-27 branch `feat/p4-pairing-broker` implemented the broker half of
P4: gateway and local administration RPC schemas, migration 004 with hashed
secrets, the pairing/credential/revocation service, credential-bound browser
live streams with access rechecks in the session actor, shell-profile
discovery, `internal/gatewayclient`, and the `pair`, `devices`, `revoke`,
`project`, `profile` and `config origin` commands. A gateway IPC peer can no
longer reach unauthenticated session management. Race tests, Windows
cross-vet, web checks and the real-broker pairing integration test pass on
macOS. `TestPtermDesktopAttachNativeOuterTTY` fails identically on the base
commit; this was later traced to a lost Ctrl+] at the attach startup handoff
and fixed (#68, see below). Native Windows, the HTTP gateway/web pairing path and
a real phone remain pending. See `docs/evidence/p4/pairing-broker.md`.

## Private beta and post-beta features (2026-09-28 to 2026-09-29)

- **Private beta `v0.1.0-beta.1`** (2026-09-28): GitHub prerelease in the
  private repository with unsigned macOS, Linux and Windows binaries and
  `SHA256SUMS` (#52). Downloaded with `gh` and checksum-verified; the macOS
  binary carries no quarantine attribute.
- **Website and docs** (#53–#57): landing page and Astro Starlight docs at
  <https://termivoa.vercel.app> and `/docs`, sources in `site/`.
- **Swipe to scroll full-screen apps** (#58): Claude Code enables the alternate
  screen and SGR mouse tracking (captured in a pty), so a swipe now sends
  wheel steps. Unit tests; owner confirmation on a real iPhone.
- **Reconnect** (#59, #61): after a long hide the socket is checked with a
  screen resync instead of replaced; 5 s attach timeout; no retries while
  hidden; one `gateway.log` line per phone connection. The log showed
  connections of 1–5 s in bursts before #61. The owner confirmed on the
  iPhone after #61.
- **Changes view** (#60): `changes.list/file` with bounded read-only git in
  `internal/gitchanges`. End-to-end check on an isolated `pterm up --dev`
  stack with a paired browser at 390×844.
- **Recap and commit & push** (#62): `agentStatus.workedSeconds`,
  controller-only `changes.commit/push`. End-to-end check on an isolated dev
  stack driven through Chrome DevTools: a commit of 2 of 3 changed files
  (`git show` confirmed the third stayed uncommitted) and a push to a local
  bare remote. The recap was checked by unit tests only.
- **Agent wall** (web only, `feat/agent-wall`): `/wall` shows each running
  session as a tile with its own read-only `Connection` and `TerminalView`,
  scaled with CSS `transform: scale()` to the tile's width and moved up to keep
  the cursor row in view (fitting the whole grid made a phone-shaped grid a
  narrow column with unreadable text; fixed in review); waiting first, then
  working. Tiles never take control, send input or resize, and detach on
  leave. Checked with `npx tsc --noEmit -p .`, `npx vitest run` (ordering,
  scale, tiles, empty state, tap to open) and `./scripts/check`. On an
  isolated `pterm up --dev` stack at 390×844 with 4 shells: 2 columns, readable
  tiles after the fix (checked again with 3 sessions printing output), tap opens the session, browser back returns to `/wall`, and
  each session's `viewerCount` fell from 1 to 0 on leaving; 4 columns at 1024 px.

- **Private beta `v0.1.0-beta.2`** (2026-09-29, #65): GitHub prerelease at
  `4421f2c` with unsigned macOS, Linux (amd64, arm64) and Windows binaries,
  `install-linux.sh` and `SHA256SUMS`. Each binary carries the build ID and no
  personal paths; the macOS and Linux arm64 binaries start. Downloaded with
  `gh`: all checksums match and the macOS file has no quarantine attribute.

- **Project buttons and line comments** (`feat/tasks-and-comments`):
  `tasks.list`/`tasks.listed` read the project folder's package.json scripts
  (package manager from the lockfile), Makefile targets, and Cargo/Go defaults
  through `os.Root`, never executing anything; the Snips sheet shows them with
  **Run in new tab** (run-first in a new session) and **Type here**. Changes
  lines can be selected to draft "Ask agent about lines N–M". In review,
  **Type here** was always off (every session has an agent status), so it now
  keys on the alternate screen (full-screen apps such as Claude Code) and a
  working or waiting state. End to end on an isolated `pterm up --dev` stack
  driven through Chrome DevTools at 390×844: the pnpm project showed
  test/dev/build/lint/db:seed; Run in new tab printed `TESTS-RAN-OK` in a new
  tab; Type here printed `BUILD-OK`; selecting 2 diff lines drafted
  `@cart.js lines 3–4:` with both lines quoted, without sending.

- **Worktrees, host side** (`feat/worktrees-host`, 2026-10-01): protocol
  `branches.list`/`branches.listed`, `worktree.remove`/`worktree.removed`, an
  optional `worktree` object on session create (HTTP and broker RPC) and on
  session records, and the error codes `worktree_exists` and
  `not_a_repository`. `internal/gitchanges/worktree.go` lists branches and
  runs `git worktree add --no-track -b termivoa/<name>
  ~/termivoa/worktrees/<repo>/<name> <base commit>` and `git worktree remove`,
  with timeouts. The base must be in a fresh listing. Limit: 50 per
  repository. A create whose session fails to start is rolled back. Branches
  are never deleted. Checks: `go test -race` on `internal/{protocol,gitchanges,broker,gateway}`,
  `./scripts/check`, and `go vet ./...` with `GOOS=linux` and `GOOS=windows`
  (`CGO_ENABLED=0`). The end-to-end check on an isolated `pterm up --dev`
  stack with a temporary HOME is in
  [evidence/worktrees/host-e2e.md](evidence/worktrees/host-e2e.md). The
  phone UI is pending. Host review fixes: ignored files (such as `.env`)
  count as changes, so remove without force never deletes them; a failed
  `git worktree add` (a failing `post-checkout` hook or the timeout) is rolled
  back and returns `worktree_failed` (HTTP 422) with the end of git's output;
  a repository folder name of `.`, `..` or one with a slash is refused, and
  the path must stay `<root>/<repo>/<name>`; a reused operation ID with a
  different worktree is `operation_conflict`. On Windows the session ends
  before `git worktree remove`, because the shell holds its folder open; that
  order is tested on macOS through a switch, not on Windows itself.

- **Lost Ctrl+] at desktop attach startup** (#68, `fix/issue-68`): the
  flaky `TestPtermDesktopAttachNativeOuterTTY` failure (`attach executable did
  not exit` at line 102) reproduced 11 times in 400 runs (8 copies of the test
  binary in parallel, `-test.count=50`). Debug logging in a scratch copy showed
  every hang was the observer's `IGNORED\r\x1d` read by the startup decoder and
  then dropped when that decoder was canceled at the handoff (bounded
  Ultraviolet drops undelivered reads and events on cancellation), so the
  handoff drain saw nothing; a second Ctrl+] always detached. The startup
  reader now records a raw Ctrl+] it reads, and the handoff detaches on it.
  After the fix: 0 failures in 480 runs of the same stress.
- **Finished session shown as lost under load** (#92, `fix/issue-92`,
  2026-09-30): after PTY EOF the broker waited `processExitGrace` (1 s) for
  the process wait, but ptyhost returns Wait only after the session cleanup
  (`closeProcessTree`: up to the 500 ms member grace plus full process-table
  scans). Temporary logs showed that delay alone reached 878 ms at p100 in 400
  `-race` runs, and 2.3 s under extra CPU load. ptyhost now reports `Exited()`
  once the child is reaped; when the grace ends and the child has exited, the
  session shows `exiting` and keeps waiting, so it ends `exited` with its code.
  A child that is not reaped is still `lost` and terminated. Stress (8 copies
  of each `go test -c -race` binary, `-test.count=50`, one cold-`GOCACHE`
  `go test ./... -p 16`, 12 CPU burners for 4 minutes, same load for both):
  before 8 wrong `lost` results in 1,200 test runs, after 0 in 1,200 (13
  sessions waited past the grace and ended `exited`). New test
  `TestSessionExitGraceWaitsOnlyForExitedChild` fails before the fix.

- **Background jobs lost their grace period under load** (#88, `fix/issue-88`):
  `TestSessionJobsGetGracePeriod` failed 52 times in 3200 subtests (8 copies of
  the ptyhost test binary, `-test.run 'TestSessionJobsGetGracePeriod'
  -test.count=200`, with parallel cold-cache `go test ./...` load). Two causes.
  Test: the shell printed the job PID before the job's subshell had set its
  HUP trap, so the hangup could arrive first (all 25 of 25 failures in a
  diagnostic run had no trap yet). Product: `terminateProcessTree` stamped the
  grace start before `signalSession` listed the session and sent SIGHUP, which
  took up to 597 ms under load; each of the 10 remaining failures in two
  diagnostic runs had its handler start 436–628 ms after that stamp and was
  killed at the 500 ms deadline. The job now reports readiness through a FIFO before the shell prints its PID, and
  the grace period runs from the time the signals were sent (a concurrent
  Terminate and leader exit wait for one shared hangup). After the fix: 0
  failures in 4800 subtests of the same stress (8 × `-test.count=300`).
- **Ctrl+] between the handoff drain and flush** (#90, `fix/issue-90`): the
  handoff drained queued input for Ctrl+] and then flushed the terminal input
  queue (`TIOCFLUSH`/`TCFLSH`/`FlushConsoleInputBuffer`), so a key that
  arrived between the two was dropped unscanned. The drain now reads until the
  queue is empty and no longer flushes; the flush remains only as a last
  resort for input that never stops (256 reads). A key that arrives after the
  drain goes to the live decoder, which detaches on Ctrl+] even before input
  is admitted. `TestDesktopHandoffDrainKeepsLateDetach` injects a Ctrl+]
  through a test hook exactly when the drain finds the queue empty; with the
  old drain-then-flush order it fails (`""`, deadline exceeded), and it passes
  200/200 with the fix.
- **Desktop attach flake closed** (#68, `fix/issue-68`, 2026-09-30): with the
  readable failure logs from #91 and the #90 fix, 8 parallel copies of the
  test binary (`-test.count=40`) plus a looped `go test ./... -p 16` failed
  1 of 320: the observer exited with status 1 and printed `pterm: desktop:
  connect terminal: context canceled` / `liveclient: connection closed`
  after its Ctrl+] arrived while it was still connecting. Cause: canceling
  the connection ends the client context at once, but `stop` records the
  reason later from an `AfterFunc` goroutine; a snapshot acknowledgement
  written in between got `ErrClosed` from `closedError()`, the painter
  recorded that as the close reason, and `Close` joined it to the
  cancellation. `closedError()` now returns the parent's cancellation; the new
  `TestClosedErrorReportsParentCancellationBeforeStop` fails before the fix.
  The test also runs the freshly built `pterm` once before its deadline (as
  #94 did): that first run took 0.27–9.2 s (median 0.8 s, p99 8.3 s) in 320
  stressed runs. The activate-before-admit gap was checked and is not a cause
  here: widening it by 50 ms fails the test 5/5 at the `CONTROL` receipt, a
  failure never seen in 800 stressed runs. After the fix: 0 failures in 320
  (8 × 40) and 480 (8 × 60) runs of the same stress.
- **Desktop attach input admitted at the Control footer** (#104,
  `fix/issue-104`, 2026-09-30): `desktopDisplay.activate` wrote the Control
  footer and only then admitted input, so a key read in between was tagged
  epoch 0 and dropped. The mirror case also existed: the live decoder tags a
  read after `ReadContext` returns, so a key read while the footer still said
  "Syncing" was admitted if `admit()` ran before the tag. Fix: a context hook
  (`withTerminalIOHook`) runs at the exact read or write syscall. Activate
  admits at the footer's last written byte, and each live read records the
  admission it saw at its own syscall. On Unix both run under `terminalIOMu`,
  so the order is exact; a read during a blocked footer write stays dropped;
  nothing is buffered. Two new tests fail before the fix:
  `TestDesktopKeyAfterReadyFooterIsAdmitted` (key injected through the new
  `desktopReadyFooterWritten` hook, after the footer write and before
  `admit()`: epoch 0) and `TestDesktopKeyReadBeforeReadyFooterIsDropped`
  (footer written between the read and its tag: epoch 1). Evidence:
  `go test -race -count=50` of the desktop tests in `internal/cli` and all of
  `internal/desktop` pass; `TestPtermDesktopAttachNativeOuterTTY` passes
  160/160 (4 parallel copies × `-test.count=40`); with a 50 ms sleep before
  `admit()` it fails 5/5 at the `CONTROL` receipt without the fix and passes
  5/5 with it; `./scripts/check` passes; `go vet ./internal/cli/` passes for
  `GOOS=linux` and `GOOS=windows`. Windows runs the hook right after
  `ReadFile`/`WriteFile` (there is no shared lock there), which is no weaker
  than before; native Windows validation stays pending.
- **Blocked Control footer covered through the real attach path** (#107,
  `fix/issue-107`, 2026-09-30, test only): new
  `TestAttachDesktopDropsKeyReadDuringBlockedControlFooter` in
  `internal/cli/desktop_unix_test.go` runs `AttachDesktop` with `--control`
  semantics against a disposable in-process broker (`/bin/cat` child) and a
  real pty as the outer terminal. At the handoff drain (`desktopDrainEmpty`)
  it stops the outer terminal's output (`tcflow` TCOOFF; macOS also needs
  IXANY cleared and the queue filled) so the Control footer write blocks;
  goroutine stacks confirm the write is in progress. A key typed then is read
  during the block and never reaches the child; after output restarts and
  "Control |" shows, a second key reaches the child; Ctrl+] detaches with no
  error. No product code changed. Fail-proof: admitting at the start of the
  footer write (`admit()` before `statusLocked`) fails it 10/10 on macOS and
  10/10 on Linux (`key typed during the blocked Control footer reached the
  child`). Evidence: `go test -race -count=50` of the test passes on macOS;
  `-count=30` plus `go test -race ./internal/cli ./internal/desktop` pass on
  Linux (`golang:1.27.1` in Docker, non-root user; as root the broker
  correctly refuses the world-writable `/tmp`); `go test -race ./internal/cli
  ./internal/desktop` and `./scripts/check` pass on macOS; `go vet
  ./internal/cli/` passes for `GOOS=linux` and `GOOS=windows`.

- **New PTY revoked by an older session's exit on macOS** (#100,
  `fix/issue-100`, 2026-09-30): `TestConcurrentResizeAndSize` failed twice in
  the #89 stress with `fork/exec .../ptychild: bad file descriptor`. Cause: a
  macOS session leader revokes its controlling terminal (`VNOP_REVOKE`,
  `REVOKEALL`) at the end of its exit, and before that it blocks while the
  terminal has unread output and the master is open (seen as `E` state).
  `Process.Close` closed master and slave together, so when it ran before the
  child was reaped (in the tests, a cleanup whose 1 s `Terminate` ran out,
  in this or another parallel test binary), the PTY number was freed, the
  next `Start` got the same `/dev/ttysNNN`, and the late revoke killed it:
  `TIOCSCTTY` in the child failed with `EBADF`, or
  the new session started with a dead terminal (the parent's slave copy
  answered `TIOCGWINSZ` with `ENOTTY`). `Close` now closes the master at once
  and the slave only after the child is reaped, so the PTY stays allocated
  until the revoke is done. A repro that runs the flaky test's cleanup in a
  loop (sleep fixture with unread `READY`, `Terminate` with a 30 ms context,
  `Close`, next `Start`; 3 copies x 4 workers, 3 cold-cache `go build -a ./...`
  loops and 6 CPU burners): before 59 `EBADF` starts and 18 dead terminals in
  13,417 starts, after 0 and 0 in 47,171. The flaky test itself did not fail
  in 3,060 runs before or after the fix (6 x `-test.count=100` and 4 x
  full-package `-test.count=3` binaries, 5 rounds, with `go test ./...`
  load). New `TestCloseKeepsSlaveOpenUntilChildIsReaped` fails before the fix.
  Only tests hit this: the broker, the one caller of `ptyhost.Start`, calls
  `Close` after the reap. Review: the new test first failed 28/6,000 under
  load when the hung-up child was reaped before its check; it now accepts a
  closed slave after `Exited()`, 0/6,000.

- **Arrange the Wall** (`feat/wall-drag`, 2026-09-30): hold a tile for 300 ms,
  then drag it; the grid reorders live, the page scrolls at the top and bottom
  edges (stopped at the page's end measured when the drag starts, because the
  moving tile would stretch it), and the order is saved per device in
  `localStorage` (`pterm.wallOrder`, up to 100 session ids). A move before the
  hold ends is a scroll; a drop does not open the tile; Alt+arrow keys move the
  focused tile; **Reset order** clears the saved order. Web only; no protocol
  or host change. Evidence: `pnpm --dir web test` (Wall tests for the order
  helpers and storage), `./scripts/check` exit 0, and a headless Chrome run
  against an isolated `pterm up --dev` stack (390x844 touch emulation, four
  sessions): hold-and-drag touch and mouse moves, drop stays on `/wall`, quick
  swipe and quick tap unchanged, order kept after reload, Alt+Left, Reset
  order, and an edge drag on a 390x380 screen that scrolled to the page end
  (116 px) and no further. The first edge run found the tile stretching the
  page (it scrolled 800 px into empty space); fixed before merge.
  Review fixes: the window (not the tile) now follows the pointer, because
  moving the tile in the list dropped its pointer capture and a release off
  the tiles left the drag stuck; a drag ends when its session ends; a hold
  without a move saves nothing; every Alt+arrow is kept from the browser
  (Alt+Left is Back); a screen reader hears the new place; a tap right after
  the page loads opens the tile. The browser run now also checks a hold
  without a move, Alt+Left on the first tile and a release off the tiles
  (18/18 pass).

- **Down next to Up in the keyboard-up key row** (`feat/compact-down-key`,
  2026-09-30): the owner had to hide the keyboard to reach `↓`, because the
  scrolling row put it at about key 13. The compact row now inserts
  `DOWN_KEY` (an agent key, no latch, like `↑`) after `↑` and drops the arrow
  row's `↓`; the full dock is unchanged. Evidence: `pnpm --dir web test`
  (new Live test: order and a single Down), `./scripts/check`, and a headless
  Chrome run on an isolated `pterm up --dev` stack (390x844, viewport cut to
  480 px to open the keyboard row, session running `cat -v`): `↓` is key 5,
  on screen (right edge 245 px), once, and ↑ ↓ ↓ Enter printed
  `^[[A^[[B^[[B`.

- **Worktrees, phone side and end to end** (`feat/worktrees`, 2026-10-01):
  **⋯ → New worktree…** sheet (random editable name, base branch picker with
  the default branch first, start with Shell/Claude/Codex), the `⎇ name`
  badge on session rows and the status line, and **Remove worktree…** with a
  confirm step and a second confirm for uncommitted changes. Evidence:
  `pnpm --dir web test`, `./scripts/check` exit 0, and a headless Chrome run
  (`~/phone-terminal-worktrees/e2e/worktrees.mjs`, not in the repo) against
  `pterm up --dev` with `HOME=/tmp/wth`, a repository with `main` and
  `feat/x` and a bare `origin`: 19/19 checks pass. The sheet opens with base
  `main` and lists `main`, `feat/x`, `origin/main`, `origin/feat/x` (no
  `origin/HEAD`); Create makes the folder and `termivoa/<name>` at `main`'s
  commit with no upstream and opens the new session with its badge; a second
  worktree made from inside the first uses base `feat/x` and the main
  repository's name; a duplicate name is refused with a clear message; a
  folder outside git shows the `cd` hint; a dirty worktree asks before
  **Remove anyway**, then the folder is gone and the branch stays.

- **Project buttons never block on a FIFO** (#125, `fix/issue-125`,
  2026-10-01): `projecttasks.readFile` checked the file with `Lstat` and
  then called `root.Open`, which waits forever for a writer when a FIFO is
  swapped in between. The open is now `openRegular` (the same approach as
  `internal/projectfiles`): `O_RDONLY|syscall.O_NONBLOCK`, then `Stat` on
  the open file must be regular; the `SameFile` check stays, because the
  open follows a symlink swapped in after `Lstat`. Evidence:
  `TestAFIFONeverBlocksARead` (`mkfifo` at `go.mod`, 3 s timeout) failed
  with "opening a FIFO blocked" before the fix and passes after;
  `go test -race ./internal/projecttasks ./internal/projectfiles
  ./internal/broker`, Linux and Windows cross-vet, `./scripts/check`.

- **`pterm uninstall` leftover message** (#116, `fix/issue-116`,
  2026-10-01): macOS and Windows printed "move Termivoa.app to the Trash, or
  remove it in Windows Settings > Apps", but the beta installs one `pterm`
  file. `programLeftover` now says `delete <path> yourself` everywhere, and
  names the bundle only when `pterm` runs inside a macOS `.app`. Evidence:
  `TestProgramLeftoverNamesWhatToDelete`, the Linux host test that already
  checked `delete /opt/pterm/bin/pterm yourself`, Linux and Windows
  cross-vet, `./scripts/check`.

- **Live tab bar shows the open tab** (`fix/issue-114`, 2026-10-01, #114):
  `TabBar` scrolls the open tab into view with `scrollIntoView({ inline:
  'nearest', block: 'nearest' })` (smooth, or `auto` with reduced motion)
  when the open session or its place in the strip changes. Evidence: `pnpm
  --dir web test` (new `revealTab` tests), `./scripts/check`, and a
  headless Chrome run (390x844) on an isolated `pterm up --dev` stack
  (`HOME=/tmp/fixdemo`, port 8798) with 6 sessions: opening the last one
  from Sessions put its tab at x 1042-1151 in a 12-272 strip before the fix
  and at 163-272 (fully visible, `scrollLeft` 879) after; `scrollY` 0 and
  focus on `BODY` in both. A direct page load with reduced motion also
  shows the open tab (`scrollLeft` 779).
- **Status line keeps the folder name** (`fix/issue-115`, 2026-10-01,
  #115): the path is split by a new pure `splitPath` helper into its parent
  and last folder, and CSS cuts them in order: the parent (from its start,
  `direction: rtl` with a `dir="ltr"` isolate inside so `~/` and `/`
  keep their order), then `· shell · size` (from its end), then the last
  folder. Evidence: `pnpm --dir web test` (new `splitPath` tests),
  `./scripts/check`, and a headless Chrome run (390x844) on an isolated
  `pterm up --dev` stack (`HOME=/tmp/fixdemo`, port 8798). Before:
  `…/tmp/fixdemo/code/billing-api/serv` with no folder, shell or size.
  After: `…es/payments · zsh (login) · 46×35`; a 23-character folder shows
  as `…/payments-reconciliation · zsh (…`. At widths of 390, 320, 250 and
  150 px and with a long worktree badge the path part never overflows.
- **Files view, host side** (`feat/files-host`, 2026-10-01): protocol v1
  gains `files.list`/`files.listed`, `files.read`/`files.content` and
  `files.search`/`files.found` (read-only, any viewer, no lease). The new
  `internal/projectfiles` reads the files root (git top level of the
  session's working folder, else that folder) only through an `os.Root`;
  `.git` is never listed or read (case, trailing dots and `GIT~1` too).
  Bounds: 1000 entries per folder (20000 scanned), 256 KiB of text cut at a
  line break and at 768 KiB of JSON, files over 50 MiB not opened, binary
  on a NUL in the first 8 KiB or over 1% invalid UTF-8, 200 search results
  from `git ls-files -co --exclude-standard -z` (5 s, 4 MiB) or a walk of
  20000 entries without `.git` and `node_modules`; one files request per
  viewer, 10 s. Bad paths (absolute, `..`) get `invalid_request` and the
  stream stays open. quicktype renamed two Go constants
  (`PatchStateBinary`, `TaskKindOther`); wire values are unchanged.
  Evidence: `go test -race` on `internal/projectfiles` (traversal, symlink
  escape, sorting, `.git`, 1000-entry cut, binary, invalid UTF-8, line cut,
  JSON budget, sparse 50 MiB file, repo and walk search, subfolder root),
  `internal/protocol`, `internal/gitchanges`, `internal/projecttasks` and
  `internal/broker` (live-socket list/read/search, `invalid_request`,
  `limit_exceeded`, cancel on detach); Linux and Windows cross-vet;
  `./scripts/check`. The phone UI is separate work.
  Host review fixes (2026-10-01): a symlink into `.git` (`gl -> .git`,
  `cfg -> .git/config`) is resolved and refused (`not_found`, listed as
  `link`); `files.listed` entries and `files.found` paths stop at 900 KiB
  of JSON (truncated), so 1000 names of 200 `<` no longer make a 1.2 MiB
  message that closed the stream; `files.read` opens with `O_NONBLOCK` and
  rechecks the open file, so a FIFO cannot block it; search drops submodule
  entries and deleted files. Evidence: new `TestSymlinksCannotReachGit`,
  `TestListAndSearchKeepRepliesUnderTheMessageLimit` (fails without the fix
  at 1239384 bytes), `TestAFIFONeverBlocksARead` (mkfifo),
  `TestWorktreeRootWithAGitFile`, `TestSearchDropsSubmodulesAndDeletedFiles`;
  `go test -race`, cross-vet, `./scripts/check`. The same FIFO problem in
  projecttasks is #125.

- **Files view, phone side and end to end** (`feat/files`, 2026-10-01): the
  Changes screen gains a **Changes | Files** switch; Files lists folders,
  searches paths, reads text files in a virtualized viewer with line numbers,
  and a terminal link provider opens `path:line` tokens. Evidence: `pnpm
  --dir web test` (482 tests), `./scripts/check` exit 0, the web helper's
  touch harness (27/27: tap opens, swipe, pull-to-history and selection do
  not), and a headless Chrome run against `pterm up --dev` with `HOME=/tmp/fh`
  and a repository with nested folders, a binary file, a 300 KB file, an
  ignored `node_modules` and a symlink to `/etc/hosts`: 12/12 checks pass. A
  touch tap on `src/app.ts:42` printed in a real session opens the file with
  line 42 centred; a tap on plain text does nothing; the root lists folders
  first and hides `.git`; the outside link is dimmed; the binary file says it
  is not text; the big file is cut with a note; search finds
  `src/lib/util.ts`, opens it, and skips the ignored `node_modules`; the
  Changes tab still works.
- **Vite dev server loads the protocol validators** (`fix/issue-123`,
  2026-10-01, #123, developer tooling only): even with `code.esm`, Ajv's
  standalone output calls its runtime helper as
  `require("ajv/dist/runtime/ucs2length").default` (for `maxLength`). The
  build bundler resolved that; the dev server sent it to the browser as-is.
  The `virtual:protocol-validators` plugin in `web/vite.config.ts` now hoists
  each `require` into an ES import that gives what `require` gave in dev and
  build, and fails the build if a `require(` is left. Evidence: headless
  Chrome on `pnpm --dir web dev --port 5197` with `/api/*` answered 401.
  Before: importing `src/screens/Live.tsx` threw `ReferenceError: require is
  not defined at …virtual:protocol-validators:1:46206`. After: it loads, the
  pairing screen renders ("Pair this phone"), and bad messages are still
  rejected. The same `maxLength` cases (512 and 513 ASCII characters and
  emoji) give equal results in the dev browser and in production bundles of
  `origin/main` and this branch. New code-point `maxLength` test in
  `validate.test.ts`; `pnpm build:web`, `pnpm --dir web test` (498 tests),
  `tsc --noEmit -p web`, `./scripts/check` exit 0.

- **Docs screenshots clean step stops only its own stack** (`fix/issue-124`,
  2026-10-01, fixes #124): `capture.mjs` saves the PID of the `pterm up --dev`
  it starts in `/tmp/demo-home/.capture-pid`; `node setup.mjs clean` (and the
  next capture run) sends SIGINT to that PID only, waits up to 15 s, then
  SIGKILL, and checks the PID's command line first. `pterm up --dev` stops
  its own gateway and broker on SIGINT (`runUpCommand`). The `pkill -f`
  pattern is gone. Evidence: a dummy-process test (not the real capture run,
  port 8788 untouched): the recorded PID stops, a second process with the
  same command line keeps running, a PID with a different command line is
  left alone, a process that ignores SIGINT is killed after the bounded
  wait. A bare `node setup.mjs` (no `clean`) also stops the recorded stack
  before it wipes `/tmp/demo-home`; checked with two dummy processes that
  carry `/tmp/pterm-demo up --dev` in their command line (only the recorded one
  stopped). `node --check` on all scripts; `./scripts/check` exit 0.

- **Linux XDG paths no longer depend on the build host** (`fix/issue-130`,
  2026-10-01, fixes #130, no behavior change on Linux):
  `TestLinuxPathsFollowXDG` failed in all four cases on Windows because
  `linuxPaths` (`internal/platform/paths.go`) modelled Linux paths with the
  host's `filepath`: on Windows, `filepath.IsAbs("/data/")` is false (no
  volume), so XDG values were ignored, and `filepath.Join` gave
  `\home\ada\.local\share\phone-terminal`. It now uses the slash-only `path`
  package. Production only calls it on Linux, where `filepath` and `path`
  agree, so Linux output is unchanged; the test stays on for every OS.
  Evidence: `linuxPaths` now calls only `path` (slash-only on every OS) and
  `strconv`, so its output cannot depend on the host. The Windows test
  binary was not run: Wine in Docker on Apple Silicon failed (Wine 8 lacks
  `bcryptprimitives.dll`; amd64 under Rosetta: `invalid gdt selector`; arm64
  Wine 10 hung in `wineboot`). Native Windows stays pending (CI is off).
  `go test -race ./internal/platform/...`, `CGO_ENABLED=0 GOOS=windows go vet
  ./...`, `GOOS=windows go test -c ./internal/platform` and `./scripts/check`
  exit 0.

- **Multi-computer H1b: broker thumbnail viewers** (`feat/mc-h1b`,
  2026-10-02, ADR 0005, `docs/multi-computer.md` "Performance budget"; not
  user-visible until the Wall uses it): `session.attach` with
  `thumbnail.maxUpdatesPerSecond` (1–30) makes a read-only viewer. The actor
  refuses its `control.acquire` with `not_controller`
  (`ErrViewerReadOnly`), and without a lease it cannot send input, answer,
  resize, upload or commit. `reserveViewerDelivery`
  (`internal/broker/viewer.go`) spaces its screen frames at least
  `1s/rate` apart; a skipped frame arms the viewer's single lazy
  `time.AfterFunc` timer, which only wakes the worker when the slot opens,
  and the next patch is built from the viewer's last delivered frame to the
  newest one. Notices (state, control, history reset) are not throttled.
  Detach stops the timer. Normal viewers and canonical PTY processing are
  unchanged. Evidence (`internal/broker/viewer_thumbnail_test.go`): at 5/s
  under a 1.3 s flood the thumbnail got 7 patches and a normal viewer 74,
  both ending on the canonical revision with an unbroken patch chain; a
  burst that stops inside a slot paints its final frame; the timer test
  (no actor, so nothing else wakes the viewer) fails when the timer is
  removed and when detach does not stop it; stream test refuses
  control, input, answer and resize with `not_controller`, also with another
  connection's live lease; goroutines return to baseline after detach and
  close. `go test -race -count=5` on the viewer, thumbnail, stream, writer,
  snapshot and control tests, `./scripts/check`, and `CGO_ENABLED=0
  GOOS=linux|windows go vet ./...` exit 0.

- **Faster first load** (`perf/split-terminal-chunk`, 2026-10-03): Changes
  and Files (one `ChangesScreen` chunk) and Settings are lazy chunks. Live:
  107.7 → 84.0 kB raw (31.9 → 25.2 kB gzip). First Live open: 166.4 → 159.7
  kB gzip. First Sessions load: 106.2 → 104.9 kB gzip. The 689.7 kB
  `TerminalView` chunk is xterm (~331 kB), the generated envelope validator
  (~284 kB), the graphemes addon (28 kB) and the connection code; all of it
  is needed for the first frame, so it was not split. Evidence:
  `scripts/check` exit 0; every new chunk is in the service worker's precache
  list. Device timing not measured.

- **Wall snapshot for never-live paused tiles** (`feat/wall-paused-snapshot`,
  2026-10-03): a paused tile that was never live gets one short snapshot turn
  (read-only thumbnail attach, first snapshot, detach, Paused). One at a
  time, 1 s after the connection gate is quiet, in the 5th connection slot,
  at most 8 s and 1 MiB, never for a computer that is backing off, never
  offscreen or in the background, one try. Evidence: `scripts/check` exit 0,
  site build. Not tested on a real phone.

- **Computer health in Settings** (`feat/computer-health`, 2026-10-03): each
  row in Settings → Computers shows `hostBuild` and the time of the last good
  read (memory only); **Check again** calls `reload(origin)` through the
  scheduler's one-read-per-computer queue. `hostBuild` gains a pattern in the
  schema and `protocol.ValidHostBuild`; no revision bump (no new field).
  Evidence: `scripts/check` exit 0, site build. Not run on a real phone.

- **Notifications from every computer** (`feat/peer-push`, 2026-10-03): one
  `/sw.js` registration per peer at scope `/push/<host>/` on home's origin,
  subscribed with that peer's VAPID key (`web/src/push.ts`,
  `web/src/sw.js`). Host: the gateway answers 404 under `/push/`. No
  protocol change. Reviews: security (4 findings: a held peer call could undo
  "off"; the app could load under a peer scope; the finding 22 text; a peer
  controlled the whole title) and correctness (5 findings: a peer tap with
  the app open; sync when a peer comes back; the switch waited for peers;
  unregister after a failed unsubscribe; tests), all fixed. Evidence:
  `scripts/check` exit 0; site build;
  `node scripts/e2e-multicomputer/phase-b.mjs` 30/30 (new B3-push-cors,
  B3-push-show, B3-push-scope, B3-push-open with a synthetic
  notificationclick in the peer worker, B3-push-cold, B3-push-off);
  `phase-a.mjs` 31/31; `webkit.mjs` 4/4. Pending (owner): real iPhone and
  Android checks in `docs/owner-verification.md`, "Notifications from every
  computer" (Gate 0 check 5). No real push service was used in any test.

- **Preview for sessions on peers** (`feat/peer-preview`, 2026-10-03; ADR
  0006, `docs/peer-preview.md` version 3; closes #143 and #144). Host
  (`96d50f7`): preview listener rules L1 and L2, the broker trust check on
  every preview check, text pages readable in the frame, confirmed
  computers' `:8443` in `frame-src` and `connect-src`, protocol revision 2.
  Web (`84e2abc`): the chip and the Preview screen for a peer, the bar
  `<label> · untrusted page`, the probe, ↻. Harness
  (`scripts/e2e-multicomputer/`): the front answers `CONNECT <host>:8443`,
  stacks start with a preview listener, a `devserver` mode is the dev site.
  Commands and results (macOS, Chrome 154.0.8037.93, Playwright WebKit 26.5):
  - `scripts/check`: exit 0 (708 web tests).
  - `go test ./scripts/e2e-multicomputer/`: ok (5 tests).
  - `node scripts/e2e-multicomputer/phase-a.mjs`: 31/31.
  - `node scripts/e2e-multicomputer/phase-b.mjs --no-web-build`: 63/63 (30
    earlier checks, 33 new `PV-*`). `revoke --via` → a held preview socket
    closed by vps after 3.3 s and the frame gone after 3.3 s; node ID change
    → the held socket closed after 24 s.
  - `PW=… WEBKIT=… node scripts/e2e-multicomputer/webkit.mjs --no-web-build`:
    7/7 (new W5 to W7).
  - `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build`:
    25 pages, all internal links valid.
  Evidence: [evidence/peer-preview/](evidence/peer-preview/) (the three
  result tables and six screenshots).
  Defects the harness found, fixed in this branch (all in
  `web/src/screens/Preview.tsx`):
  1. A computer added in this visit was not in the loaded page's
     `frame-src`/`connect-src`, so the app's own CSP blocked its preview and
     the app showed the `pterm setup` text. Fix: on a
     `securitypolicyviolation` for the preview origin the app reloads once
     (`reloadForPolicy`, loop guard in `sessionStorage`; unit test). Check
     `PV-added`.
  2. ↻ (and the WebKit history restore) mounted the frame twice: once with
     the old probe answer, then again after the new probe. The first select
     request was cancelled. Fix: the old answer is dropped with the old
     frame. Check `PV-vps-reload` (exactly one select).
  3. After a revoke the screen showed "Dev server stopped" above
     "vps · pair again". Fix: no "Dev server stopped" while a computer notice
     shows. Check `PV-revoke-app`.
  Fixes 2 and 3 have no unit test: the web tests render without effects;
  the harness checks cover them.
  Two adversarial reviews of the joined build (security: one required fix;
  correctness: ship after fixes), all applied:
  4. Security: a link to `/c/<host>/sessions/<id>/preview?port=<n>` opened
     a peer's frame with no tap. Fix: `navigate` marks the entry it makes
     (`PREVIEW_TAP` in `history.state`); `route()` opens a peer preview only
     with that mark and a port in the session's `previews`
     (`peerPreviewOnLoad`, unit tests), else the terminal. Checks
     `PV-link-reload`, `PV-link-cold`, `PV-link-port`.
  5. `fail` in `internal/gateway/preview.go` showed a broker
     `protocol_mismatch` as "This app may not use this computer."; it now
     matches the code `invalid_request` (Go test).
  6. A home preview without an origin showed "Opening preview…" without
     end; it now shows the "Can't open the preview on …" text (unit test).
  7. Unverified, no fix guessed: a WebKit history restore without the page
     cache may show "Origin not allowed." until ↻; it is on the owner list.
  Not run here (development profile; owner list in
  `docs/owner-verification.md`, "Preview for sessions on peers"): `__Host-`
  and `Secure` cookies in the frame on a real iPhone (installed app) and
  Android; the production gateway's own `https://<host>:8443` text in its
  CSP and its Host rule for `:8443`; a real `tailscale serve --https=8443`
  on a peer; a tailnet ACL that blocks 8443; video; HMR of a real dev
  server; WebKit's history restore of the app page (difference A).
- **Switch on, nothing sent after re-pair** (`fix/issue-145`, 2026-10-03,
  #145): `syncHomePush` (`web/src/push.ts`) re-posts the browser's push
  subscription to home when home answers (`homeAdoption` in `App.tsx`, so also
  after Log out, `pterm revoke` and a new pairing); the host upserts by
  endpoint. Evidence: `web/src/push.test.ts`, `scripts/check`.
- **Notification tap picks the visible window** (`fix/issue-148`, 2026-10-03):
  with several app windows, `notificationclick` in `web/src/sw.js` now prefers
  a focused visible window, then a visible one, then the first (#148); tests in
  `web/src/sw.test.ts`. Evidence: `scripts/check` exit 0; the new test fails without the fix.
- **No lockout by another client's failed requests** (`fix/issue-149`,
  2026-10-03; fixes #149). Confirmed with Go tests on `main` before the fix:
  the gateway's count of failed credential attempts (20, then 1 per 6 s) is
  one bucket for all clients behind Tailscale Serve, so a few forged requests
  made the API, the live socket and the preview listener answer 429 to a
  paired phone, and `index.html` lost its per-device CSP entries. The issue
  named only previews. Fix: the count is deleted (`internal/gateway/api.go`,
  `preview.go`, `handler.go`); a well-formed credential always reaches the
  broker. A first fix that remembered accepted credentials was dropped after
  a security review: it did not hold after a gateway restart. Guessing is
  prevented by the 256-bit credential, not by a count. The pairing limits are
  unchanged; their shared bucket is a known limit in `docs/threat-model.md`.
  Tests: `TestFailuresOfAnotherClientDoNotLockOutAPairedDevice` and
  `TestFailuresOfAnotherClientDoNotLockOutTheLiveSocket`
  (`internal/gateway/lockout_test.go`); both fail on `main`. Commands, all
  pass: `gofmt -l internal`, `go vet ./...` (also `CGO_ENABLED=0 GOOS=linux`
  and `GOOS=windows`), `go test -race ./internal/gateway/...`,
  `scripts/check`, `node scripts/e2e-multicomputer/phase-a.mjs` (31 PASS),
  `node scripts/e2e-multicomputer/phase-b.mjs --no-web-build` (63 PASS), the
  site build. Not measured: a real Tailscale Serve.

- **Ubuntu umask 002 stopped setup** (`fix/ubuntu-umask`, 2026-10-03): found
  on the owner's new Ubuntu 24.04 VPS. `scripts/install-linux.sh` made
  `~/.local` and `~/.local/bin` mode 775 (the default umask of a new Ubuntu
  user is 002), and `validateUnixAncestors` (`internal/ipc/transport_unix.go`)
  refuses a group-writable ancestor. The script now runs `chmod go-w` on both
  folders and the error names that command. Evidence: after `chmod go-w` on
  the VPS, `pterm doctor` runs; `internal/ipc` test for the message.

- **Mac home could not add a computer** (`fix/macos-tailscale-cli`,
  2026-10-03): found in the owner's first real two-computer test (Mac home,
  Ubuntu VPS peer): the app showed "service unavailable" after **Check
  link**. Under launchd the broker's `tailscale status --json` ran
  `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, which prints "The
  Tailscale GUI failed to start" (exit 0) unless it has a terminal or
  `TAILSCALE_BE_CLI=1`. `tailnet.CLIEnv` now sets it for the broker's status
  reads and for `pterm`'s own Tailscale calls. Evidence: on the owner's Mac,
  with `env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin XPC_SERVICE_NAME=x`, the
  status read fails before the change and parses "Running" after it;
  `TestCommandSourceRunsTheCLIAsACLI`. The e2e harness uses a fake Tailscale
  CLI, so it could not see this.

- **Setup for a second computer** (`feat/setup-ux`, 2026-10-03): from the
  owner's first real install (Mac home, new Ubuntu VPS): a 60-line setup
  wall, a first-computer QR code at the end of setup on the second computer
  (scanned, it made a separate app), a long `--via` link to copy, `pterm:
  command not found`, root refused without help, and "Home" naming. Setup now
  prints a short plan (`--verbose` for the checks, unit files and commands)
  with the checks' warnings under `Warnings:`; asks "Is Termivoa already on
  your phone?" and runs the `pterm pair --via` flow for answer 2 (a tagged
  computer needs its own yes, as `--allow-tagged`; a bad number or unknown
  name asks again); pairs nothing and names the fix when port 443 is not
  shared; names `pterm pair --via <name>` when the phone was not approved or
  the link expired. `pterm pair --via` prints a QR code for the app's **Scan
  QR** (added by `feat/scan-add-computer`, same release), after the scan
  text. On Linux, root gets the commands that make a user (`sudo` or `wheel`,
  `authorized_keys`, a copy of the files; with `SUDO_USER` set: "run it
  without sudo"), and the last lines give `export PATH=…` when `exec.LookPath`
  does not find this `pterm`. An adversarial review's findings (M1–M5,
  L1–L8) were fixed on the same branch. Evidence: `go test ./cmd/pterm
  ./internal/cli ./internal/tailnet`; `gofmt`; `go vet ./...` for darwin,
  `CGO_ENABLED=0 GOOS=linux` and `GOOS=windows`; `scripts/check`; the site
  build. A privileged `ubuntu:24.04` container with systemd as PID 1 and a
  fake `tailscale` CLI ran `sh install-linux.sh ./pterm-linux-arm64` under
  `expect` for three users: no Tailscale operator (setup ended with "Setup is
  done, but your phone cannot reach this computer yet: … sudo tailscale set
  --operator=$USER, then run `pterm setup` again."), the normal case (Serve
  `y`, answer `2`, then `1` → `pterm pair --via mac` link and QR code), and a
  tagged VPS (`tag:server`: the tag question, then `pterm pair --via mac
  --allow-tagged`). As root the script and `pterm setup` printed the user
  commands, which then worked in that container. Not tested: the e2e
  multi-computer run (the coordinator runs it), a real VPS, a real phone,
  and the linger question (the container's users had no login session).

- **Scan QR for + Add a computer and the Home badge**
  (`feat/scan-add-computer`, 2026-10-03): `web/src/pairing/scan.ts` reads the
  QR code with `BarcodeDetector` or the lazy `qr` 0.7.0 decoder chunk (34.8 kB,
  14.3 kB gzip); the text goes through `parseAddComputerLink` and the same
  confirm sheet as paste. Review: 6 low findings, all fixed (reader-load text,
  ended track, two codes in one frame, `object-fit: contain`, empty Home line,
  canvas reuse). Evidence: `scripts/check` (728 web tests);
  `phase-b.mjs` 65/65 with `SCAN-native` and `SCAN-js` (Chrome fake camera
  showing a QR code of a real `pterm pair --via` link). Pending (owner):
  real iPhone and Android, `docs/owner-verification.md` "Scan QR in + Add a
  computer (real phone)".

- **Web polish from the first real use** (`fix/web-polish`, 2026-10-03),
  one commit per item, each with a test that fails without it:
  - "Connecting to vps…" for 5 s after an add, with quiet retries at 0.5, 1
    and 2 s and at 5 s (`connectingRetry`, `nextStatus` in
    `web/src/screens/allSessions.ts`; tests in `allSessions.test.ts` and
    `Sessions.test.tsx`). `phase-b.mjs` gets `B2b-connecting`: every problem
    row right after "vps is added" is recorded and "can't reach" fails it.
    Not run here (the coordinator runs the harness). Note: the add flow's
    own first settings read already retried for 3 s and the gateway
    refreshes its origins at approval, so the exact cause on the iPhone is
    not proven; item 2 below may be the same report.
  - The app's own CSP blocking a listed computer: `securitypolicyviolation`
    filter (`blockedPeer`), one reload per computer per tab
    (`reloadOnce`, `pterm.policyReload`), `pterm-fresh=1` makes `sw.js` wait
    for the network with no 2.5 s timeout, then status `blocked` with "This
    app page is out of date. Close the app and open it again." Tests:
    `allSessions.test.ts` (filter, guard, URL, status) and `sw.test.ts`
    (fresh navigation waits past 2.5 s; cached shell only on an error).
  - "vps · can't reach" detail line "Last seen 3 h ago. Is it on, and in
    your tailnet?" (`problemDetail` in `Sessions.tsx`; the question only for
    a network error, `network` in the `unreachable` status). Tests in
    `Sessions.test.tsx`, `allSessions.test.ts`, `AddComputer.test.tsx`.
  - **Check again**: `atLeast` (600 ms) and `answeredText` ("vps answered
    in 0.2 s.") in `Computers.tsx`; tests in `AddComputer.test.tsx`.
  - #164: the + Add a computer command uses `firstLabel(homeOrigin())`;
    test in `AddComputer.test.tsx`.
  - Scan QR: a camera track muted for `MUTED_MS` (2 s) ends the scan with
    `failed` (`web/src/pairing/scan.ts`); tests in `scan.test.ts`. Needs a
    real iPhone check.
  Evidence: `npx tsc --noEmit -p .` in `web`, `pnpm --dir web test`,
  `pnpm build:web`, `scripts/check`, the site build. Not run: the e2e
  multi-computer harness, a real phone.
- **UI polish: lit buttons and notices** (`feat/ui-lit`, 2026-10-03): the
  owner-approved Paper design ("Buttons that feel lit", "Errors that tell you
  what to do", Settings → Computers) in `web/src/styles/global.css` (tokens,
  the button family, notices, banners, computer cards) and
  `web/src/screens/ui.tsx` (`Icon`, `Button` with a busy state, `Notice`,
  `Banner`). Texts unchanged except the design's two notice lines ("This
  phone lost access on vps. On vps, run:", "It runs an older version than
  this app. Your other computers still work."). Evidence:
  `docs/evidence/ui-lit/before/` and `after/` (390×844, demo data, isolated
  dev stack; the multi-computer shots load the app as a demo tailnet name
  with mocked peers); `npx tsc --noEmit -p .`, `pnpm --dir web test`,
  `pnpm build:web`, `scripts/check`, site build. Index CSS 41,748 →
  52,089 bytes (gzip 8,506 → 10,791). Not reached in the screenshots: the
  green "vps is added" toast (needs a real approval). Site screenshots are a
  follow-up.
- **Bone & ink pictures** (`docs/bone-ink-screens`, 2026-10-04): every app
  picture on the docs site, the landing page and the README retaken in the
  new look, from isolated dev stacks with demo data only. Docs: 16 phone
  screens and `key-row.webp` retaken with `scripts/docs-screenshots/capture.mjs`
  (now also Files, the pairing code and a dev-server preview) and the
  multi-computer ones with the new `scripts/docs-screenshots/computers.mjs`
  (two dev stacks through the e2e harness library, demo tailnet only); two
  new pictures in `guides/computers.md`: **Found on your network** with
  **Add**, and the bar **Adding vps… approve on vps**. Landing: `answer`,
  `sessions` and `preview` (was `preview-nightdrive`) are real app screens,
  `lock.webp` and `og.png` are rendered from `site/public/_lock.html` and
  `_og.html` (`capture.mjs landing`); the hand-built mock-ups (the phone in
  "How it works", the Found row in "More computers?") restyled to Bone & ink,
  glows removed. README: the four PNGs in `docs/images/`. Evidence:
  `cd site && pnpm install --frozen-lockfile --ignore-workspace && pnpm build`;
  proof screenshots of the built docs and landing at 390 and 1280 px (not
  committed). Not changed: `docs/images/multi-computer-concept.png` (internal
  concept drawing).

Still pending: real Linux, Windows and Android devices; CI after the Actions
minutes reset; the 24-hour soak; the history secret scan and licence audit
(see `docs/pending-work.md`). Features from #60 and #62 need a broker restart
on the owner's Mac, which ends running sessions.

# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **GitHub: set up a repository on a computer from the phone** (ADR 0010,
  `docs/github-integration.md`; docs: `guides/github.md`). Phone:
  **Settings → Integrations** has one row for each computer, "GitHub on
  mac", also with no herdr. **Connect** shows an 8-character code and
  **Copy code and open GitHub**; one time for each GitHub account,
  **Choose on GitHub** picks the repositories. **Set up a repository** lists
  them with search and the tags "on mac" and "name taken"; **Set up** clones
  into `~/<name>` with a percent and **Stop**, then "Ready on mac" with
  **New session** and **New workspace in herdr**. **From GitHub** sits next
  to **Add a folder** in herdr's **New workspace** sheet. **Disconnect** asks
  first; when GitHub did not confirm the revoke, the row links "Also remove
  Termivoa on GitHub.". Host: the broker logs in with the device flow of the
  Termivoa GitHub App (no client secret; Contents read and write, Metadata
  read), keeps one token pair for each computer in `github.json` (mode 0600)
  in its data folder, refreshes it, and revokes it on Disconnect. It clones
  with a fixed git environment (https to github.com only, no hooks, no
  templates, no submodules, no LFS download, objects checked) into a temp
  folder, renamed only when the name is still free; it never deletes or
  overwrites a folder. In a folder it cloned, git uses Termivoa's login for
  that repository through the hidden helper `pterm github-credential`. New:
  `GET` and `POST /api/v1/github`, `GET /api/v1/github/repos`,
  `HTTPSettingsResponse.github`, protocol revision 7; `pterm github status`,
  `pterm github disconnect`, and a GitHub line in `pterm doctor`. No
  database change. Hidden until the build carries the app's client ID.
- **Needs you** (`docs/needs-you-inbox.md`; docs: `guides/needs-you.md`).
  Phone: a **Needs you** tab in Sessions, right after **All**, with a count
  (`Needs you 3`), while something waits. It lists what waits for an answer
  on every computer, the longest wait first: sessions that are waiting
  (with the question and **Answer ›**) and herdr worktrees that are blocked;
  then, under **Finished**, sessions with **Your turn** and herdr worktrees
  that are done. A tap opens the item, and Live then shows
  **Next · 2 left ›** to go to the next one that waits (**Inbox ›** on the
  last one). Nothing moves by itself and Next sends nothing. A tap on a
  notification opens the item with Next too. While a computer does not
  answer, the tab says "Some computers did not answer. See All." and not
  "Nothing needs you."; **‹ Sessions** and Back from an item go back to the
  tab. Host: no change (no new request, no protocol change).
- **More herdr from the phone** (`docs/herdr-manage.md`, section 14).
  Phone: **New tab** in the **⋯** menu offers **Shell** or an agent, with
  **First prompt (optional)** and **Start**; a worktree whose workspace was
  closed shows under its project as **CLOSED** with **Open**; a herdr
  terminal has the **⋯** menu of its workspace; **‹ Sessions** in a herdr
  terminal returns to the **herdr** tab. Host: `newTab` may carry `agent`
  and `prompt` (create's rules and its limit of 3 per minute); the group
  field `closed` and the action `openItem`; protocol revision 6. An agent's
  herdr name is now its kind and pane (herdr takes each name once, so a
  second agent of one kind failed before).
- Phone: the **New workspace** sheet has **Add a folder**. It opens a session
  on that computer with `pterm project add ` typed and not run; type the
  folder, press Enter, then **Check again**. Host: no change.
- **Manage herdr from the phone** (ADR 0009, `docs/herdr-manage.md`; docs:
  `guides/herdr.md`). Phone: each row of the **herdr** tab has a **⋯** menu
  with **New tab**, **Close tab** (never a workspace's last tab), **Close
  workspace** ("Close silver-meadow? Its terminals end. Files stay.") and, for a worktree, **Remove worktree** ("Its folder is
  deleted, also files that git ignores (such as .env). The branch stays.");
  a worktree with changes that are not committed is never removed. **New
  workspace** opens one of the computer's Termivoa projects in herdr. **+**
  shows on every git project, also before herdr made a worktree there.
  **Settings → Integrations** has a herdr card on every macOS and Linux
  computer: **Start herdr** when it does not run (also on the tab's "herdr is
  not running" notice), and **Not installed** with **Install herdr**, which
  opens a session with herdr's install command typed and not run.
  Host: `POST /api/v1/providers/{provider}/manage` (`closeItem`,
  `removeItem` without force, `newTab`, `closeTab`, `newWorkspace` with a
  project ID,
  `startServer`), 30 per minute for each phone and one at a time; the
  confirmed name is checked on a fresh read; the provider state
  `notInstalled` with `canInstall`, `canStart` and `linked`; protocol
  revision 5. The broker downloads nothing. No database change.
- Landing page: a **Providers** section with a tile for each provider (herdr
  first) that opens its guide, and a **Providers** link in the navigation.
- **herdr on your phone** (ADR 0008, `docs/herdr-provider.md`; docs:
  `providers.md`, `guides/herdr.md`). Needs herdr 0.8.2 or later on the computer, macOS or
  Linux, and no setting: Termivoa finds herdr within about 30 s.
  Phone: Sessions gets a **herdr** tab (next to **All** or with the computer
  chips) with a count of blocked and done terminals. It shows one row per
  project, its worktrees under it, state words (BLOCKED, DONE, WORKING,
  IDLE), and the status line "1 blocked · 1 done · 2 working" or "herdr ·
  nothing running". **Open the blocked agent** opens the first blocked
  agent. The **All** tab lists worktrees that have an agent state under
  **HERDR**. A worktree opens as one herdr terminal in Live, read-only until
  **Take control**, with the other tabs of its workspace as chips and the
  line "mac shows this terminal at phone size while it is open here."
  Problems show on the herdr tab only: "herdr is not running on mac",
  "Update herdr on mac", "herdr on vps · can't reach" with **Retry**.
  **Settings → Integrations** has a card for each computer with **On**,
  **Buzz when blocked** and **Buzz when finished**. A buzz says "<agent>
  needs you. Tap to answer." or "<agent> finished after N min."; a tap opens
  the terminal. **+** on a git project opens **New worktree** (**Branch
  name**, **Start an agent in it**, **First prompt (optional)**, **Create and
  start**).
  Host: reads herdr through its own socket (no `--takeover`, never terminal
  text), at most 4 open herdr terminals for each computer, push limit 6 per
  minute apart from session notifications, limits per phone (open 20,
  create 3, settings 10 per minute). `pterm doctor` gets herdr lines (path,
  version, server, "can make worktrees", the agents the phone can start, and
  a warning for a herdr prefix key other than ctrl+b). No database change.
  Not yet tested on a real iPhone.

### Changed

- Phone, UI consistency (no feature removed; less text): every
  confirmation of a destructive or multi-line action (Terminate session,
  Send N lines, Paste N lines, Remove worktree, Remove anyway, Remove a
  computer) has the way out first and ignores a confirm tap in its first
  400 ms, so a double tap cannot confirm; tap areas that were under 44 px
  (computer tabs, the push card, the answer pill, "terminal", the
  Changes/Files switch, preview Open, Tell agent, file search) are 44 px;
  screen titles, text actions, hint and error sizes, and button case follow
  one rule ("Edit", "+ New", "Save", "Save as preset", "Sessions"); shorter
  lines on the Wall, pairing and Settings → Computers. In an iPhone Safari
  tab, Sessions shows one line, "For notifications, add Termivoa to your
  Home Screen", with **See how** and **Not now**. Host: no change.
- Phone: in a Safari tab on iPhone, **Settings → Notifications** now says why
  there is no switch and what to do: "To get notifications on iPhone, add
  Termivoa to your Home Screen." with four steps (before: one line, "Add to
  Home Screen to get notifications"). A browser without notifications gets
  "This browser cannot show notifications. Try Chrome, Edge, Firefox or
  Safari." Host: no change.

### Fixed

- Host: slow session/worktree creation, termination, preview scans,
  notification tests and herdr terminal opens use bounded independent
  exchanges, so they no longer hold up session lists or new Live
  authentication (#218). Phone: other sessions can be opened during a slow
  worktree create; excess simultaneous slow actions report a limit instead
  of claiming that the repository has too many worktrees. Authorization,
  request correlation and uncertain-delivery rules remain in place.
- herdr: the first prompt of a new agent now reaches it (#192). It was
  refused on every herdr (0.8.2 and 0.9.3): herdr answers `agent.start` at
  once (`launch_pending`) and takes no prompt until the agent is ready.
  Host: after the start, the host asks herdr for the agent (`agent.get`)
  until herdr takes input for it, then sends the prompt; the start and this
  wait share the 20 s step limit. An agent that is not ready in time still
  answers `promptFailed`. With no first prompt, an agent that waits at a
  question counts as started. Phone: it waits 45 s, as before; with no
  first prompt the notice is "The agent started and waits at a question."
  (it does not say "Your prompt was not sent.").
- herdr: "The worktree is ready, but the agent did not start." (or "The tab
  is ready, …") now also shows when the agent's program is not installed
  (#193). herdr says "started" in that case too. Host: when herdr drops
  the launch (no program showed within herdr's own launch limit of 15 s,
  or the program ended), the answer is `agentFailed`; a missing program is
  reported after about 15 s. A herdr that cannot answer `agent.get` counts
  the agent as started, as before. Phone: no change.
- Phone: a herdr terminal outside git no longer shows "this herdr terminal's
  folder cannot be read" right after it opens (#194). The "while you were
  away" line read the changes by itself, and Live read the files folder
  ahead for tapped paths; both reads are now quiet. The text shows only
  after a tap on **Changes**. Host: no change.
- Phone: the **New worktree** sheet and the **New tab** chooser read that
  computer's settings again when they open, so they offer the agent kinds
  that the computer offers now (#195). Before, a kind whose last agent had
  left herdr was still offered, and **Start** answered "herdr cannot start
  that agent here". A picked agent that left is never changed to another
  agent: the sheet goes to **No agent** (the chooser: no agent picked) and
  says, for example, "codex is not running in herdr now." Host: no change.
- Host: a slow herdr check no longer turns off tabs without an agent until
  the next restart (#196). A `herdr terminal attach --help` or `herdr agent
  start --help` run that the 3 s limit stopped, or that a signal ended, is
  no answer: it runs again after 30 s (then 1, 2, 4 and 8 minutes), never
  on every poll; the wait starts again for another herdr version. Phone:
  no change.
- herdr, two workspaces at the main folder of one repository (#200): **Close
  workspace** is off for each, with "herdr closes the workspaces at this
  project's main folder only together. Close them in herdr on mac." Host: a
  close of one of them answers `409 operation_conflict` with its own text,
  and herdr is not asked. herdr 0.9.3 refuses such a close, and herdr 0.8.2
  closed **both** workspaces for one tap; Termivoa never closes two.
- Small herdr fixes (#200). Host: a create that waits for another herdr
  change says "herdr is busy with another change; try again" (before:
  "another worktree is being made; try again"). Phone: a wrong branch name
  or prompt in **New worktree** and **New tab** shows in the error colour
  (it was grey).
- herdr, "busy" and the create limit. Host: a "herdr is busy with another
  change; try again" answer no longer takes one of the phone's 3 creates a
  minute (create and an agent tab). Phone: for that answer the **New
  worktree** sheet now shows the computer's own words ("herdr is busy with
  another change; try again", or "too many requests; wait a minute and try
  again" for the limit), not "Too many new worktrees just now. Wait a
  minute, then try again." for both. For a computer that you added it says
  "herdr on vps is busy, or this phone asked for too many worktrees just
  now. Wait a minute, then try again."
- Phone: the **Changes** tab of a herdr terminal outside git no longer
  stays on "loading" when you open the panel on **Files** right after the
  terminal opens and then switch to **Changes**. Host: no change.
- Host: **Send a test notification** can no longer use up the budget of
  session notifications. Test notifications have their own budget: 6 per
  minute for all phones together, and one per phone per 5 s as before
  (before: tests used the session budget of 12 per minute, so a phone that
  sent a test every 5 s stopped "needs you" notifications for that minute).
  Up to 3 subscriptions whose push service hangs no longer hold back the
  phones after them: one
  delivery now sends to 4 subscriptions at once (before: one after another,
  so six hung subscriptions used the whole 60 s). At most 192 push requests
  are open at once (#202). Phone: a herdr card in **Settings →
  Integrations** shows "This phone gets no buzz yet." only for the computer
  that sends no notifications, also when two computers have the same name
  (before: both cards showed it).
- Host: a session notification is no longer dropped when two deliveries are
  still on their way (three sessions turn `waiting` together while the push
  service is slow). Session pushes have 24 delivery slots, two minutes of
  the budget of 12 per minute (#197; provider pushes had this fix in #185).
  Phone: no change.
- Phone: a herdr card in **Settings → Integrations** says "This phone gets
  no buzz yet." under the buzz switches while a buzz switch is on and the
  app can tell that this phone has no notifications from that computer (not
  turned on, an iPhone
  Safari tab, a browser without push, or on at home and not at that
  computer). Its link (**Turn on notifications**, **Add to Home Screen** or
  **See why**) goes to **Settings → Notifications**. Docs: troubleshooting
  has "I hear a sound but see no notification" (with herdr, the sound is most likely herdr's own).
  Host: no change.

## [0.1.0-beta.3] - 2026-10-04

Third private beta. Updating from beta.2 restarts Termivoa on the computer,
which ends running sessions. It also needs a fresh database (run
`pterm uninstall --delete-data`, then `pterm setup`, and pair again), iOS
16.4 or later on iPhone, and no other Tailscale Serve handlers on port 443.

### Added

- **Found on your network** (ADR 0007, `docs/found-on-network.md`; not yet
  tested on real computers or a real phone). Add a computer with no link.
  Phone: **Settings → Computers** has a **Found on your network** block. A
  computer that waits in `pterm setup` or `pterm pair --via mac` shows there
  with its name, its address and **Add**. **Add** opens the same **Add this
  computer?** sheet as a pasted link: **Add vps**, **Continue**, then the
  code and "If it matches, type y in the terminal on vps." With no row, the
  block says "To add another computer, run pterm setup on it, or pterm pair
  --via mac if it is already set up. It shows here while it waits." When
  home cannot read Tailscale (two reads in a row), it says "mac cannot read
  its Tailscale status. Check Tailscale on mac." The app reads the list
  every 5 s while **Settings → Computers** is open. A row goes when its
  computer stops waiting or changes its Tailscale machine, and is hidden
  while that computer is in your list and answers; a computer that lost
  your phone's access can be added again from **Found**. If the new
  computer stopped waiting, the add page says "vps stopped waiting. On vps
  run pterm pair --via mac, then tap Add again." The sheet now says "Add it
  only if you just ran pterm setup or pterm pair --via on vps.", and an
  expired code says "…then tap Add again, or scan or paste the new link."
  Host: `pterm pair --via mac` in a terminal now waits up to 10 minutes and
  sends this computer's single-use add-computer secret to mac (over
  Tailscale's encrypted connection to mac's Tailscale address, re-sent
  every 30 s), then prints "Sent to mac (https://…)." and "On your phone,
  open Termivoa → Settings → Computers and tap Add next to "vps"." The QR
  code and the link stay as the other ways (on Enter, or at once when
  nothing reached mac; see Changed), for 2 minutes; when they
  expire and nothing reached mac, the command ends as before; else it says
  "The QR code and link above expired. Add in the app still works." and
  keeps waiting. With piped input or `--no-wait` it sends nothing; with a
  tagged computer it sends nothing and says "Add does not work with tagged
  computers: mac is tagged. Use the QR code or the link below." (or "this
  computer is tagged"). Other lines: "mac did not answer.", "mac is busy
  right now.", "Termivoa on mac cannot receive this yet. Update it, …",
  "This computer cannot read its Tailscale status.", "This computer could
  not offer itself to mac.", "Add needs this computer's address: run pterm
  setup, or pterm config origin https://<this computer>.", and, when an
  older broker still runs, "Restart Termivoa on this computer (pterm
  shutdown, then pterm launch), then try again.",
  "mac is now a different Tailscale machine. Run pterm pair --via mac
  again." and "Nobody added this computer in 10 minutes. Run pterm pair
  --via mac again." Ctrl+C or a closed SSH session deletes the unused
  secrets. `pterm setup`'s answer 2 ("Yes, add this computer to it") does
  the same; its list of homes shows each full address and OS ("Pick the
  computer whose address your Termivoa app opens."); after 10 minutes it
  ends with "Setup is done, but your phone is not connected yet. To
  connect it, run `pterm pair --via mac`." The approval prompt for an
  added computer now names it: `Phone "iPhone" wants to add this computer
  (vps) to your Termivoa app on mac.`, then mac's address and owner, and
  says to approve only if the phone shows the same code and says "type y
  in the terminal on vps".
  Plain `pterm pair` no longer offers pairings that belong to another
  computer's app. Home accepts offers on a new route without login,
  `POST /api/v1/computers/offer` (no browser can call it; always `204`;
  own rate limit, 10 then 1 per second; offers in memory only, 60 s), and
  lists them on `GET /api/v1/computers/found` (cookie, home's own address
  only). Protocol revision 3 (`minRevision` stays 1; no database change).
  Trust: every phone paired with home can see the waiting computers and
  their one-time secrets, and anyone who can reach home in your tailnet can
  make **Add** fail for a while; the QR code and the link still work.
- **Scan QR in + Add a computer.** Phone: **Settings → + Add a computer**
  now offers **Scan QR** and **Paste link**. Scan opens the camera inside the
  app and reads the QR code that `pterm pair --via` prints. A scanned link is
  checked like a pasted one and opens the same **Add this computer?** sheet.
  Camera pictures stay on the phone; nothing is stored. The QR reader (`qr`
  0.7.0, MIT OR Apache-2.0) loads only when you tap Scan QR. The page Safari
  shows for an add-computer link now sends you to **Settings → + Add a
  computer → Scan QR**. Host: no change.
- **Home badge.** Phone: **Settings → Computers** marks the home computer
  with a **Home** badge and says "Home serves this app to your phone."
- **Preview for sessions on other computers** (ADR 0006,
  `docs/peer-preview.md`; not yet tested on a real phone). Phone: **Preview
  ready** and **Open ↗** now work for a session on another computer in your
  list. That computer must run a Termivoa build with protocol revision 2 and
  must share dev-server previews (`pterm setup`). Over a preview from
  another computer, the bar always shows the computer's label and
  **untrusted page**: that computer chooses the page in the frame. The bar
  has **↻** to load the preview again. When a computer shows "pair again",
  "can't reach" or "Update Termivoa on …", its open preview is removed and
  that text shows. A link alone never opens a preview from another computer:
  its address shows the preview only in the tab where you tapped **Open ↗**
  (a reload keeps it), and only for a port that session lists; otherwise it
  opens the terminal. After you add a computer, the first preview from it
  reloads the app once, so the app's security policy includes it. Host: the
  app's CSP frames the preview origin (`https://<host>:8443`) of confirmed
  computers only, never a pending one, and allows a reachability probe of
  preview origins (`connect-src`, never `wss:`). A device bound to another
  computer's app is checked against Tailscale trust on every preview
  request; open preview sockets close within the 5 s recheck. Protocol
  revision 2 (`minRevision` stays 1; no schema change). Trust: a framed page
  from another computer can show false content and can sign the phone out
  of all computers by filling the cookie jar; pair again to recover.
- **Notifications from every computer.** Phone: each paired computer now
  sends "waiting" and "done" notifications. One switch (Sessions card or
  **Settings → Notifications**) covers all computers. The title and text of
  a notification from another computer start with its name (`vps · …`) and
  a tap opens that session. With the app already open, the tap moves it to
  that session. A computer that was offline when you turned notifications on
  is added when it is back. Settings shows "No notifications from vps." for
  a computer that could not be subscribed; home and the other computers
  keep working. **Send a test notification** asks every computer. Removing
  a computer, **Log out**, "pair again" and turning the switch off stop
  that computer's notifications, also while that computer is slow to
  answer. The service-worker cleanup now keeps the app's worker and one
  push registration per listed computer, and removes everything else.
  Host: every path under `/push/` now answers 404. Not yet tested on a
  real iPhone.
- Computer health. Phone: **Settings → Computers** shows the Termivoa
  version each computer runs and when your phone last got an answer from it,
  and **Check again** re-checks one computer now (it replaces **Retry**
  there). Host: the settings answer's `hostBuild` (the version) now allows
  only ASCII letters, digits and `. _ + -`.
- Wall: a paused tile that was never live now shows a small picture of its
  session's screen. Phone: Termivoa connects once, for a moment, takes one
  picture and marks the tile Paused. One tile at a time, only while the Wall
  is on screen and the app is in the foreground; a failed try leaves the tile
  as before. Host: no change.

- Multi-computer (ADR 0005, `docs/multi-computer.md`; not yet verified on a
  real iPhone or Android phone, Gate 0 pending). Phone side: one Home Screen
  app, served by a "home" computer, talks directly to up to 8 other computers
  in the same tailnet. **Settings → Computers** lists each computer
  (**online**, **checking**, **offline**, **needs you**; home is **this
  computer**) with **Retry**, **Rename** ("Name on this phone") and
  **Remove** (signs this phone out there first). **+ Add a computer** takes
  the link from `pterm pair --via`, or the link is tapped in a terminal; both
  open the **Add this computer?** sheet (Address, Machine, Owner or Tags)
  before **Add vps**; the add page shows the full **Address** again and
  waits for **Continue**, and **Cancel** deletes the pending entry on home;
  then the code check. The Sessions list merges all
  computers with **All** and per-computer chips, a colour dot and label per
  row, "2 computers · 1 waiting · 1 working", **❯ new session on…**, and per
  computer "vps · can't reach" (**Retry**), "vps · pair again · Run pterm
  pair --via mac on vps" (also for a revoked or expired phone) and "Update
  Termivoa on vps" (the phone's label; also for a peer that reports no
  `protocolRevision`). **Log out** signs out of every reachable computer and
  names the rest ("Could not sign out of vps. Run pterm revoke there to
  remove this phone."). New session picks the computer first ("Runs on vps").
  The Wall shows tiles from all computers, at most 4 live at 5 updates per
  second under one phone-wide budget; the rest show **Paused**. Live, the
  composer, uploads, Changes and Files work on every computer (previews too,
  see "Preview for sessions on other computers" above). Peer answers are schema-checked leniently
  and peer error text is never shown. The service worker now precaches the
  complete bundle of its build, loads pages network-first with a 2.5 s
  fallback to that copy, and removes any service worker that is not the
  app's own on every load. Host side: `pterm pair --via <name|origin>
  [--allow-tagged]` prints a two-minute add-computer link bound to that app
  origin; the peer pins the app's Tailscale node ID and requires the same
  owner, compared by stable Tailscale user ID, never login name (or
  `--allow-tagged` for tagged nodes; a node whose tags changed since pairing
  is refused). The command says to tap the link and confirm "Add this
  computer?", or to tap "+ Add a computer" in Settings → Computers and
  paste it. Per-device origin binding:
  a peer accepts a cross-origin request only from the app origin bound to
  that request's own credential, grant or pending pairing, and only while
  its Tailscale snapshot (60 s cache, 10-minute grace) still shows the pinned
  node; a changed node ID revokes the devices bound to it, a new snapshot
  closes open streams that lost trust at once, and a Tailscale status that
  is not `Running` (restart, logout) keeps the last good snapshot. Home pins
  each peer's node too: a peer whose node changed leaves
  the list and the CSP, and confirm fails with "this name now belongs to
  another Tailscale machine; add the computer again". The gateway sends
  exact credentialed CORS headers to bound origins (devices whose node is
  still trusted, revoked and expired ones included) only, refuses `Sec-Fetch-Mode: no-cors` and
  Origin-less requests without `Sec-Fetch-Site: same-origin` on `/api`,
  refreshes the cookie (same value, never past its expiry) on
  `GET /api/v1/settings`, and adds the device's computers to the app's CSP
  `connect-src`. New routes `GET/POST /api/v1/computers` and
  `DELETE /api/v1/computers/{host}`; `protocolRevision` and
  `minProtocolRevision` in settings; read-only `session.attach` thumbnail
  viewers whose frame rate the broker enforces. New commands
  `pterm computers`, `pterm computers remove <origin>` and
  `pterm revoke --via <origin>` (always sent, also with no device: it also
  deletes that app's pairing links and pending pairings); a **VIA** column in `pterm devices` and
  `pterm pair pending`; `pterm revoke <device>` on home lists the phone's
  other computers; `pterm doctor` shows the protocol revision, the CORS
  lists, tailnet and owner mismatches, "tags changed since pairing", an
  unknown tailnet (Tailscale logged out), and other Tailscale Serve handlers
  on this computer's origin as a problem with the commands to move them.
  The store schema changed (one schema file, no migrations: start with a
  fresh database). Known limit: a cached offline shell keeps the
  CSP from when it was cached. A compromised home compromises every
  computer paired through it; see the threat model and the recovery steps.
- Files view. Phone side: the Changes screen has a **Changes | Files** switch;
  **Files** shows the session's project as folders (folders first, sizes,
  links that leave the project dimmed), searches file paths, and reads text
  files with line numbers, **Copy path** and **Ask agent about this file**.
  Tapping a path such as `src/app.ts:42` in the terminal opens that file at
  that line; swipes, pull-to-history and selection are unchanged. Host side:
  new read-only `files.list`/`files.listed`, `files.read`/`files.content` and
  `files.search`/`files.found` messages for any viewer (no lease). The root is
  the git top level of the session folder, or the folder itself; every access
  goes through `os.Root`, so `..` and symlinks cannot leave it, and `.git` is
  never listed or read. Limits: 1000 entries per folder, 256 KiB of text per
  file (cut at a line break), no files over 50 MiB, binary files detected,
  200 search results from `git ls-files` (ignored files skipped) or a bounded
  walk, one request at a time per viewer.
- Worktrees. Phone side: in a running session whose folder is in a git
  repository, **⋯ → New worktree…** opens a sheet with a random name that can
  be edited (🎲 for another), a base branch picker (default: the repository's
  default branch; any local or remote branch), and **start with** Shell,
  Claude or Codex; **Create** opens the new session as a tab and types the
  agent command once. Sessions in a worktree show a `⎇ name` badge on the
  Sessions list and in the status line. **Remove worktree…** (with control)
  asks first, and asks again before it deletes uncommitted changes. Host
  side: new `branches.list`/`branches.listed` (any viewer) and
  `worktree.remove`/`worktree.removed` (controller) messages, an optional
  `worktree { sourceSessionId, name, base }` on session create, and an
  optional `worktree { name, repo }` on session records. The broker runs
  `git worktree add --no-track -b termivoa/<name>
  ~/termivoa/worktrees/<repo>/<name> <base>` after checking the name and
  that the base is a real branch, with time limits and at most 50 worktrees
  per repository; the phone never sends a path. If the session cannot start,
  the worktree is removed again. Remove runs `git worktree remove` (with
  `--force` only after the second confirmation), keeps the branch, and ends
  the session (on Windows the session ends first, because a running shell
  keeps its folder open). Ignored files such as `node_modules` and `.env`
  count as changes, so they are never deleted without the second
  confirmation. A failed `git worktree add` (for example a failing hook)
  leaves nothing behind. New error codes `worktree_exists` and
  `not_a_repository` (HTTP 409) and `worktree_failed` (HTTP 422, with the
  end of git's message).
- Down arrow next to Up while typing (phone side): with the keyboard open, the
  one-row key bar now shows `↓` right after `↑`, so both arrows are in reach
  without scrolling the row or hiding the keyboard. The row's second `↓`
  (after `←`) is gone. Like `↑`, it ignores the Ctrl/Alt latch. No host-side
  change.
- Arrange the Wall on the phone: hold a tile for a moment, then drag it to a
  new place. The other tiles move out of the way, and dragging to the top or
  bottom edge scrolls the page. The order is saved on this device only
  (`pterm.wallOrder` in browser storage, session ids only); new sessions go
  after the arranged ones. A quick swipe still scrolls, a tap still opens the
  session, and **Reset order** brings back the default order (waiting agents
  first). With a keyboard, Alt+arrow keys move the focused tile. No host-side
  change.
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

### Changed

- A calmer add-computer screen (host side). After `pterm pair --via mac` (or
  `pterm setup`, answer 2) reached mac, the terminal shows five short
  lines: "Adding this computer (vps) to your Termivoa app on mac.", "Sent
  to mac (https://…).", "On your phone, open Termivoa → Settings →
  Computers and tap Add next to "vps".", "Waiting for your phone (up to 10
  minutes; Ctrl+C stops)…" and "No Add button? Press Enter to show a QR
  code and a link." The QR code and the link come only on Enter; when
  nothing reached mac they print at once, as before. The approval prompt
  is shorter and keeps its checks: `Phone "iPhone" wants to add this
  computer (vps) to your Termivoa app on mac.` / `mac is https://… (owner
  …).` / `Approve only if your phone shows 123-456 and says "type y in the
  terminal on vps". [y/N]`. Success now says "Done. vps is in your
  Termivoa app on mac. Open the app and tap it.", with "Tip: give it a
  short name in the app: Settings → Computers → Rename." for a machine
  name longer than 12 characters; setup then ends with "Setup is done."
  Phone: no change.
- Phone: **Check again** in **Settings → Computers** shows **Checking…** for
  at least 600 ms and then says how fast the computer answered: "vps
  answered in 0.2 s." (seconds, one decimal, at least 0.1). Failure texts
  are unchanged. Host: no change.
- Phone: "vps · can't reach" has a second line, "Last seen 3 h ago. Is it
  on, and in your tailnet?" (the time moved there from the first line). The
  question shows only after a network error, when nothing answered; after
  an HTTP error the line only says when vps was last seen. Host: no change.
- Phone: a new look, "Bone & ink", on every screen and sheet. Flat: an
  ink background, paper surfaces and hairlines; no gradients, glows or
  tinted cards. Screen and sheet titles (**Sessions**, **Settings**,
  **Compose**, …) are set in a serif; labels, status words and meta lines
  in small mono capitals. One bone pill per screen is the main action (for
  example **❯ new session**); the others are outlined pills, quiet text or
  red (**Log out**, **Remove vps**). A busy button shows a spinner and a
  muted label and keeps its width (**Check again** → **Checking…**,
  **start session** → **starting…**). Cyan now only means live or
  connected (the host line dot, **Working**, **● CONTROL**, the spinner
  while a computer is added); amber says warning or your turn (**Waiting**
  and **Your turn**); red is errors and destructive actions; green is
  online. Sessions and **Settings → Computers** are rows between hairlines
  with the state on the right (**online** with a green dot, **offline** in
  amber, **needs you**), and a small bone **Home** badge. The computer
  filter is a row of underline tabs. Notices have a 2 px rule in their
  colour and an underlined action: "vps · can't reach" (amber, **Retry**),
  "vps · pair again" (red, with the `pterm pair --via mac` command),
  "Update Termivoa on vps" (grey). The adding banner is an outlined pill
  with a cyan spinner while it waits, a bone pill when "vps is added" and
  a red-ruled pill when "vps was not added". The terminal keys are flat;
  Enter and **❯ type** are bone, **^C** is red. The terminal's black
  background is drawn in the app's ink, so it sits flush with the app.
  Commands show without font ligatures and never break inside `--via`.
  Motion is off with Reduce Motion. Host: no change.
- A shorter `pterm setup` for a new computer. Host: setup prints a short
  plan, one line for each change (startup at login, where data and logs go,
  the phone address, each Tailscale Serve question, linger, the project
  **Home folder**), and asks as before. `pterm setup --verbose` adds the
  checks, the startup files and every command. Warnings from the checks
  (other ports that get the Termivoa cookie, Funnel, Serve conflicts,
  multi-computer findings) still show, under **Warnings:**, before the plan.
  When the phone cannot reach this computer over port 443 (no Tailscale,
  the Tailscale operator is missing on Linux, or you said no to sharing),
  setup pairs nothing and ends with the fix and "then run `pterm setup`
  again". When Tailscale shows other computers of yours, setup asks "Is
  Termivoa already on your phone?": **1** pairs this computer (QR code);
  **2** asks which computer you first paired the phone with and adds this
  computer to that app (the same as `pterm pair --via <name>`, with a
  separate yes for a tagged Tailscale computer, as `--allow-tagged`). A
  wrong number or an unknown name asks again; Enter skips. If you do not
  approve the phone, setup says so and names the command to try again. The
  project setup adds is now named **Home folder** (was **Home**). On Linux,
  setup and `install-linux.sh` refuse root with the commands that make a
  normal user (group `sudo` or `wheel`, your SSH key, a copy of the files),
  or, under `sudo`, say to run without it; and when the shell does not find
  this `pterm` yet, setup's last lines give the `export PATH=…` command.
  Phone: no change.
- `pterm pair --via` prints a QR code on a terminal, after a line that says
  to scan it in the app (**Settings → + Add a computer → Scan QR**), not with
  the phone's Camera app. A terminal too narrow for it gets the link only.
  The standalone `pterm pair` first says that it is for a FIRST computer.
- Breaking (host): preview listener rules. Only the preview origin itself
  may send requests to it (an `Origin` equal to the preview origin, or no
  `Origin` and `Sec-Fetch-Site: same-origin`). The selection
  (`/__termivoa/select`) needs the app's `Referer` and answers a page, not a
  redirect. Per-device origin binding now applies on the preview listener
  (#143). A `curl` or a typed URL on the preview listener is now 403.
  Phone: the preview frame now sends the app's origin as referrer (origin
  only, no path). Reload the app after you update: an app that was open
  during the update has the old script and shows "Open the preview from the
  Termivoa app." in the frame until it reloads.
- Preview reachability. Phone: the app asks the preview address before it
  shows the page. If nothing answers in 5 seconds, it shows "Can't open the
  preview on <computer>…" and no empty frame. Host: `GET /api/v1/preview` is
  unchanged (it cannot know whether Tailscale Serve 8443 is set up) (#144).
- Faster first load. Phone: the Changes and Files screens and Settings now
  load only when you open them, so the terminal screen downloads about 7 kB
  (gzip) less on first open. Nothing changes in what you see; the offline
  bundle still contains everything. Host: no change.

- Breaking (host): the database is one fresh schema; an older database stops
  the broker with "delete the database file to start fresh". Delete the data
  folder and pair again.
- Phone: photo sizes in the composer use the same format as Files ("900 B",
  "1.5 KB").
- Breaking (all users, one computer too): every `/api` request needs an
  exact `Origin`, or no `Origin` and `Sec-Fetch-Site: same-origin`. Safari
  before iOS 16.4 sends neither on `GET`, so iPhone and iPad need iOS 16.4
  or later.
- Breaking (all users, one computer too): `pterm setup`, also
  `--apply --yes` from the Windows installer, refuses before any change
  while other Tailscale Serve handlers share this computer's Termivoa
  origin (port 443), and prints the commands to move them; `pterm doctor`
  reports them as a problem. It then exits with code 3, and the Windows
  installer names that cause. The advice is another computer or the
  handler's own Tailscale Service hostname: another port on the same
  computer still receives the Termivoa cookie, so `pterm doctor` also warns
  (not a problem) about handlers on other ports of this computer, except
  Termivoa's own preview port 8443.
- Multi-computer, phone side: adding a computer shows a banner, "Adding
  vps… approve on vps", that keeps running when you go back to the terminal
  to type `y`. **Log out** shows "Signing out…" and gives each other
  computer at most 6 s.
- Multi-computer, host side: a computer whose Tailscale node changed (for
  example Tailscale reinstalled) can be added again, and home pins the new
  node; entries the app no longer shows do not fill the list of 8. After
  home's node changes, peers answer the app with "revoked", so it shows
  "pair again" instead of "can't reach". A peer refuses a home whose
  Tailscale tag set changed, not only one that gained or lost all tags.
  `pterm` runs the Tailscale CLI only when no other user can replace it
  (file, symlinks and every folder up to `/`), checks again before each
  run, and `pterm doctor` says why it refused one.
- An app tab left open across a host update may fail to read settings until
  it is reloaded.

### Fixed

- Fix (host side): on a busy computer `pterm launch` said "broker did not
  start" or "gateway did not start" after 10 s, and the service started a
  moment later. It now waits up to 30 s, as `pterm setup` does (#175).
  Phone: no change.
- Fix (phone side): no more taps that look dead. Phone: **Check again** on
  Sessions and on New session stays on "Checking…" for at least 600 ms, as
  in **Settings → Computers**. **Save title** and **Terminate session** in
  the session menu say "Saving…" and "Terminating…" while the computer
  answers. **New session** on "This session is gone" and **Run in new tab**
  in snips say "Starting…" and are off until the new session opens, so a
  second tap no longer starts a second session. **Send a test
  notification** says "Sending…". A failed **Log out** shows its error next
  to the button, not at the top of the page out of sight. **Commit** and
  **Push** that send nothing (control or the link was lost) say "Not sent.
  Take control, then try again." **copy error** on "This screen broke" says
  "copy failed" when the copy does not work. The preview's **↻** is off
  while a notice says why there is nothing to load. The **update** pill
  reloads at once when another tab of the app already took the update.
  **+ new** in snips, off at 50 snippets, says "You can keep 50 snippets at
  most. Delete one to add another." Host: no change.
- Fix (phone side): **Retry** on "vps · can't reach" in Sessions showed
  nothing when tapped, so it looked dead. It now shows a spinner and
  "Retrying…" for at least 600 ms while it asks the computer again. Host: no
  change.
- Fix (phone side): screens fit a narrow phone (375 and 390 px) again. A
  `pterm pair --via <home>` command with a long machine name, in
  **Settings → Computers** (under **Found on your network**), in the
  "pair again" notice on Sessions and on the add page ("stopped waiting"),
  ran out of its box and let the page slide sideways. Commands and other
  code now wrap inside their box; `--via` stays whole. Host side: no change.

- Fix (host side): after you renamed a computer in Tailscale, `pterm setup`
  said "Tailscale Serve already shares the page at https://<new name>/"
  while only the old name shared it, so the phone could not reach the new
  name. Now only this computer's current name counts: setup asks to share
  the page there, and `pterm doctor` says "not configured for Termivoa". A
  handler that is left under the old name is not a problem. Setup, doctor,
  `pterm pair` and `pterm launch` now also read Tailscale Serve and Funnel
  that run in the foreground (`tailscale serve` or `tailscale funnel`
  without `--bg`): Funnel there stops setup and pairing like Funnel in the
  saved settings, and the text says "stop the `tailscale serve` or
  `tailscale funnel` command that runs in the foreground (Ctrl+C)". A
  foreground handler on port 443 or 8443 is a problem, and one on this
  computer's address stops setup like any other handler there. `pterm
  doctor` reports a Tailscale Service that serves Termivoa, with `tailscale
  serve clear <service>` to remove it (#168). Phone: no change.
- Fix (host side): when the phone's terminal got fewer rows (for example
  when the "Adding vps… approve on vps" banner opens) while the cursor was
  on one of the cut rows, the bottom rows were lost, so the last line of a
  prompt (such as "Approve only if your phone shows …") did not show. Now
  the rows above the cursor move into the history, as in other terminals.
  Phone: no change.
- Fix (host side): `pterm pair` and `pterm launch` did not check Tailscale
  Funnel; only `pterm setup` and `pterm doctor` did. Now every `pterm pair`
  that pairs (`pterm pair`, `--via`, `--no-wait`, `pterm pair approve`)
  refuses before it makes a link while Funnel is on for port 443 or 8443,
  with setup's text: "refusing to pair: Tailscale Funnel is on for …, which
  puts Termivoa on the public internet; turn it off with `tailscale funnel
  --https=443 off`, then run `pterm pair …` again". `pterm launch` still
  starts, but first prints "Warning: Tailscale Funnel is on for …; turn it
  off with `…`. Pairing a phone is refused until it is off." Termivoa never
  runs the command. When the Serve settings cannot be read, both go on as
  before (#161). Phone: no change.
- Fix (phone side): right after "vps is added", the first read of vps could
  fail while its gateway did not yet allow the app (it refreshes that list
  once per second), and the row said "vps · can't reach" until a refresh.
  For 5 s after an add the row now says "Connecting to vps…" and "Just
  added. Its sessions show in a moment.", and the app retries quietly after
  0.5, 1 and 2 s and when the 5 s end; only then does it say "can't
  reach". Host: no change.
- Fix (phone side): an app page whose own security policy did not list a
  computer in the list (a page from before the add, or an old copy that the
  offline cache served when home answered slowly) blocked every request to
  that computer: "vps · can't reach" for hours while vps saw nothing. The
  app now notices the block and, when home still lists that computer (one
  removed elsewhere is just dropped), reloads once per computer, past the cached
  copy (it waits up to 12 s for home, then opens the cached copy). If the
  page still blocks it, the row says "This app page is out of date. Close
  the app and open it again." The preview's own reload also goes past the
  cached copy now. Host: no change.
- Fix (phone side): **+ Add a computer** showed `pterm pair --via` with the
  name given to home on the phone; it now shows home's machine name, which
  the command needs (#164). Host: no change.
- Fix (phone side, iPhone): when a call takes the camera during **Scan QR**,
  iOS mutes the camera instead of stopping it, and the picture froze until
  Cancel. A camera that stays muted for 2 s now ends the scan with "The
  camera did not start. Close other apps that use it, then tap Scan QR. Or
  paste the link." Host: no change.
- Fix (host side): `pterm setup` went on to start Termivoa and pair a phone
  when Tailscale Funnel was on for port 443 that already served Termivoa; it
  only printed a warning. Funnel puts the terminal on the public internet.
  Setup now refuses before any change while Funnel is on for port 443 or the
  preview port 8443 (also with `--apply --yes`), and prints the command that
  turns it off, for example `tailscale funnel --https=443 off`; it never runs
  it. `pterm doctor` reports Funnel as a problem with the same command
  (#157). Phone: no change.
- Fix (host side, macOS): adding a computer to a Mac home failed with
  "service unavailable". The broker runs `tailscale status` in the
  background, and there the Tailscale app's own program answered "The
  Tailscale GUI failed to start" instead of the status; `pterm doctor` in a
  terminal saw no problem. Termivoa now runs the Tailscale CLI with
  `TAILSCALE_BE_CLI=1` everywhere.
- Fix (host side, Linux): on Ubuntu a new user has umask 002, so the install
  script made `~/.local` group-writable and `pterm setup` then stopped with
  "runtime ancestor … is writable by another user". The script now removes
  group and other write from `~/.local` and `~/.local/bin`, and the error
  names the command that fixes it (`chmod go-w <folder>`).
- Fix (host side): another machine in the tailnet could stop the app and
  previews for every phone (429, "Too many failed attempts.") with about 20
  requests that carried a bad cookie, because all clients behind Tailscale
  Serve shared one count of failed attempts. The host no longer counts
  failed attempts; the 256-bit credential is what prevents guessing. The
  pairing limits are unchanged. No phone-side change (#149).
- Fix (host side): the preview proxy now also drops a dev server's
  `Set-Cookie` that sets a Termivoa cookie with an empty name
  (`=__Host-pterm_preview=x`), in other letter case (`__HOST-pterm_session`),
  with a quoted name, or folded after another cookie in one line; the same
  spellings are removed from the browser's `Cookie` before it reaches the dev
  server. The dev site's own cookies still pass unchanged. No phone-side
  change (#150).
- Fix (host side): the preview listener's short text pages (for example
  "No dev server of this session listens on that port.") were blank inside
  the app's frame, because they allowed framing only by the preview origin
  itself. They are now readable in the frame. No phone-side change (#144).
- Fix (phone side): after browser Back to the app (Safari), the preview
  loads again and does not show a refusal. No host-side change.
- Fix (phone side): after **Log out** or `pterm revoke` and a new pairing in
  the same browser, **Settings → Notifications** showed on but home sent
  nothing, and **Send a test notification** said "No subscription on this
  phone". The switch reads the browser's subscription, which survives; the
  computer had deleted its copy. Home now gets the browser's subscription
  again whenever it answers, so the switch is true. Other computers already
  did this.
- Fix (phone side): with several app windows or tabs open, a tap on a
  notification went to the first one, which could be hidden. It now goes to
  the window you see (focused first, then visible, then any). No host-side
  change (#148).
- Fix (host side): project buttons could hang. Reading `package.json`, a
  `Makefile`, `Cargo.toml` or `go.mod` checked the file and then opened it;
  a FIFO (named pipe) put at that name between the two steps made the open
  wait forever for a writer, so `tasks.list` never answered. The host now
  opens the file without blocking and refuses anything that is not a
  regular file. No phone-side change (#125).
- Fix (host side): `pterm uninstall` on macOS and Windows told users to
  remove `Termivoa.app` or a Windows app entry, which the beta never
  installs. It now names the `pterm` file to delete, as on Linux; only a
  real macOS app bundle is still moved to the Trash. No phone-side change
  (#116).
- Desktop attach (host side): Ctrl+] pressed just as `pterm attach` finished
  connecting could be ignored, so the attachment stayed open until Ctrl+] was
  pressed again. The startup input reader could read the key and then drop it
  when it stopped at the handoff to live input. A Ctrl+] it reads now always
  detaches. No phone-side change (#68).
- A session whose program ended normally is no longer shown as lost when the
  computer is busy. Host side: after the terminal closes, the broker kept
  waiting only 1 second for the process result, and the cleanup of the
  program's session can take longer under load. It now keeps waiting while
  the program has exited and its result is still coming (the session shows
  as exiting), so it ends as exited with its exit code. A program that
  closes its terminal but keeps running is still marked lost and stopped
  after 1 second (#92). Phone side: no change.
- Ending a session (host side): background jobs could lose most of their
  500 ms cleanup time on a busy computer and be killed before their hangup
  handler finished. The grace period started before the host had listed the
  session and sent the hangup, which took up to about 0.5 s under heavy load.
  It now starts after the signals are sent. No phone-side change (#88).
- Desktop attach (host side): a Ctrl+] that arrived in the short moment
  between the handoff's scan of queued input and its flush was discarded
  unscanned. The handoff now reads the queued input until it is empty and
  scans all of it, instead of flushing; a key that arrives later goes to live
  input, which detaches on Ctrl+]. No phone-side change (#90).
- Desktop attach (host side): Ctrl+] pressed while `pterm attach` was still
  connecting could print `pterm: desktop: connect terminal: context canceled`
  and `liveclient: connection closed` and exit with status 1, although the
  detach worked and the session kept running. When the attach canceled its
  connection, a screen acknowledgement still being sent reported a false
  "connection closed" before the cancellation was recorded. It now reports
  the cancellation, so the detach exits cleanly. No phone-side change (#68).
- Desktop attach with `--control` (host side): a key typed just as the footer
  changed to "Control" could be lost, because input was admitted only after
  the footer write returned. The other way round, a key typed while the
  footer still said "Syncing" could reach the program if the input reader was
  slow to tag it. Input is now admitted at the footer's last written byte,
  and each key is checked at the moment it is read. On macOS and Linux both
  steps run under the same terminal I/O lock, so a key read before the footer
  is always dropped and a key read after it is always sent. Nothing is queued.
  No phone-side change (#104).
- PTY hardening on macOS (host side): `ptyhost` now keeps its end of a
  session's terminal open until the program has fully exited. macOS takes the
  terminal away from an exiting program at the very end of its exit; if both
  ends were already closed, macOS could give the same terminal to the next
  session first, which then failed with `fork/exec ...: bad file descriptor`
  or started with a dead terminal. Termivoa's broker already closed a
  terminal only after its program exited, so this mainly fixes a flaky test
  and guards future callers. No phone-side change (#100).
- Live tab bar (phone side): opening a session whose tab was past the
  visible part of the tab strip left that tab hidden, so the open session
  could not be seen at a glance. The open tab now scrolls into view when the
  open session changes, when the screen opens, and when the session list
  arrives later and moves it; smoothly, or at once with reduced motion. The
  page does not scroll and focus does not move. No host-side change (#114).
- Live status line (phone side): a long folder path was cut off at its end
  with no `…`, so the folder name and the `· shell · size` part were lost.
  The path is now cut first, from its start with a `…`, then the shell and
  size, and only then the last folder (from its start). The badges and
  CONTROL stay whole, and the worktree badge rules are unchanged. No
  host-side change (#115).

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

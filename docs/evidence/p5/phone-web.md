# P5 phone web experience

Date: 2026-09-27. Branch `feat/p5-phone-web`, base `eb8421a`. Apple Silicon
macOS 26.5.2 (25F84), Node 24.11.0, pnpm 10.33.0. No physical phone was used.
Every phone-specific behavior below is listed under "Owner verification needed"
and is **not** claimed as passed.

## What was built

All code is under `web/`. Protocol files, Go code, and packaging were not
changed. No dependency was added (the jsdom option was not needed; tests use
Node's built-in `EventTarget` and a fake WebSocket).

Controllers (no React, unit tested):

- `src/terminal/Connection.ts` owns one session's WebSocket, the reconnect
  state machine, frame application, control lease, heartbeat, and history
  reads. Order: `hello` → `welcome` → `session.attach` as observer →
  `snapshot.begin/rows/end` → validated paint → `snapshot.applied` → patches →
  Take control is offered. Reconnect uses 0.5/1/2/4/8/15 s backoff. It never
  re-acquires control and never replays input. Unacknowledged input at
  disconnect or control loss shows "Delivery uncertain—check the terminal
  before sending again." A changed session generation, a changed broker
  generation after reconnect, `session.state lost`, or the
  `generation_changed`/`session_lost`/`session_missing` errors stop the
  connection in `lost`. `access.revoked`, `unauthorized`, and `revoked` stop it
  in `revoked`. Heartbeat every 5 s (lease included only while controller).
  Patches are coalesced so at most one paint is queued per animation frame; a
  non-contiguous patch requests `screen.resync`. Resize is controller-only,
  clamped to 20–400 × 5–200, and deduplicated until the resize snapshot lands.
  History reads use the frozen epoch/revision token, pages of 200 rows, at
  most 25 pages, restart on `history.changed`, and never change after load.
- `src/terminal/InputController.ts` owns the dedicated textarea. Hardware keys
  map to the protocol key enum; plain text arrives once through `beforeinput`;
  `compositionend` is the only IME commit path and an immediate `insertText`
  echo of the same commit is dropped. Enter and Backspace become keys. Command
  (meta) shortcuts are left to the browser. Clipboard paste is routed to the
  Composer, never sent directly. Sticky Ctrl/Alt from the key bar apply to the
  next key or character.
- `src/terminal/composer.ts`: in-memory drafts keyed by session/generation;
  paste checks (disabled when not sendable, 64 KiB limit, multiline needs
  confirmation).
- `src/pairing/pairing.ts`: reads and strips the `/pair` fragment with
  `history.replaceState` before first render, redeems, polls every 2 s until
  approved or expired, and clears the poll secret when polling ends.
- `src/api/http.ts`: same-origin fetch; every POST/PATCH sends
  `Content-Type: application/json` and `X-Pterm-Request: 1`.
- `src/terminal/TerminalView.ts`: display-only xterm (`disableStdin`,
  `scrollback: 0`, xterm helper textarea made read-only with
  `inputmode="none"`), measured cell grid for resize.

Screens (`src/screens/`): Setup, Sessions (create from saved project + shell
IDs, rename, terminate behind an explicit confirmation that states it is not
Ctrl+C and not Detach), Live (status bar, xterm, Esc/Tab/Ctrl/Alt/arrow keys,
Type, Compose, History, Sessions, Take control/Release, application wheel
scroll buttons on the alternate screen, lost-session screen with a separate
Start new session action), History (virtualized selectable DOM text, "New
output available", Return to live), Composer (Paste into terminal, separate
Enter, multiline confirmation, disabled with draft kept while not sendable),
Settings (device name, host/web build, font size 10–24 px default 14,
logout). Release control on `pagehide` and `visibilitychange` hidden.
Terminal-derived text is rendered only as React text nodes; there is no
`innerHTML` or `dangerouslySetInnerHTML`. Only the font-size preference is
written to `localStorage`.

PWA: `public/manifest.webmanifest` ("Phone Terminal", theme `#0B0E14`),
`icon.svg`, `icon-192.png`, `icon-512.png`. `src/sw.js` is emitted as `/sw.js`
by a small Vite plugin with a cache name derived from the hashed asset list.
It caches only same-origin `GET /assets/*`, answers failed navigations with a
"Connection unavailable" page, never touches `/api`, and never calls
`skipWaiting` unless the user presses "Reload to update". Registration happens
only in production builds on secure contexts.

## Automated checks

```
pnpm typecheck       # pass, 0 errors
pnpm test:web        # 10 files, 81 tests passed (47 existing + 34 new)
pnpm build:web       # pass; one 814 kB JS chunk (Vite size warning only)
./scripts/check      # see result below
```

`./scripts/check` exited 0: protocol generation had no drift, the plan copy
matched, gofmt was clean, `go vet` passed, all 22 Go test packages (repository,
`third_party/vt`, `third_party/xpty`, ultraviolet) passed, and the web
typecheck, 81 tests, and build passed.

New test files: `src/terminal/Connection.test.ts` (13: observer first,
acknowledged paint before control, explicit control grant, uncertain delivery
and observer-only reconnect with no input or `control.acquire` replay,
acknowledged input not uncertain, generation change → lost, broker generation
change → lost, error/revoke mapping, heartbeat, patch coalescing and resync,
resize bounds; history paging/freeze/new-output/return-to-live,
`history.changed` restart, alternate screen), `src/terminal/InputController.test.ts`
(11: key mapping, Enter prevents default, text once, Chrome-style and
Safari-style IME commit once, repeated commit after the echo window, line
break/backspace, sticky Ctrl, paste routing, field clearing),
`src/terminal/composer.test.ts` (4), `src/pairing/pairing.test.ts` (6:
fragment removal, poll every 2 s until approved, headers, secret never in URL
or storage and cleared after polling, server and local expiry).

## Desktop browser smoke test (not a phone)

The built `web/dist` was served by a throwaway Node mock gateway outside the
repository (static files, mock HTTP API, minimal WebSocket speaking protocol
v1 envelopes). It was driven in a desktop Chromium preview at a 390×844 CSS px
viewport. Observed:

- Sessions list, create, rename/terminate confirmation, Settings, font size,
  logout → Setup.
- Live: observer first; Take control → controller; measured resize sent
  46×41; typed text sent once as `input.text`; Ctrl+C sent as
  `input.key character c ctrl`; Enter as `input.key enter`; all 13 bar buttons
  are 44 px tall in two rows.
- Multiline paste shows the confirmation before sending.
- History: 50 history rows plus screen rows, frozen while a new snapshot
  painted, "New output available", Return to live.
- Dropping the socket → "Reconnecting… input is off" → reconnect sent only
  `hello`, `session.attach`, `snapshot.applied` (no `control.acquire`, no
  input).
- `session.state lost` → lost screen → Start new session opened a new session
  as observer; `access.revoked` → Setup with the reason.
- `/pair#<grant>`: URL became `/pair`, `localStorage` and `sessionStorage`
  empty, code and `pterm pair approve <pendingId>` shown, approval → Sessions.
  Mutations carried `X-Pterm-Request: 1` and `application/json`.
- Service worker: cache contained only `/assets/*`; a rebuild produced a
  waiting worker and the "Reload to update" banner; reload activated it and
  deleted the old cache; with the server stopped, navigation showed
  "Connection unavailable".

The mock is not the Go gateway. No end-to-end run against the real broker or
gateway was possible here; the gateway does not yet serve `web/dist`.

## Known gaps

- The HTTP session record has no controller or connection fields, so the
  Sessions list cannot show controller/connection state. The Live bar shows
  both for the open session. Adding them needs a protocol change owned
  elsewhere.
- There is no HTTP route to rename a device, so Settings shows the device name
  read-only.
- CSP and other response headers belong to the gateway and were not added
  here.
- Hidden or background tabs throttle `requestAnimationFrame`, so a snapshot is
  painted and acknowledged only when the page is visible again.
- A virtualized history list can drop a selection that spans rows scrolled far
  outside the rendered window.

## Owner verification needed

None of these were run. Each needs a real device over the private HTTPS
hostname.

iPhone Safari and Home Screen app:

1. Add to Home Screen: name, icon, standalone launch, dark theme color.
2. Keyboard overlap: with the keyboard open, the cursor row and the action bar
   stay visible above the keyboard (visualViewport + safe-area insets), in
   portrait and landscape, including devices with a home indicator.
3. IME: Japanese, Chinese, and Korean input commit each word exactly once;
   dictation; emoji keyboard; autocorrect, smart punctuation, and
   capitalization stay off in the terminal input.
4. Key bar: buttons do not close the keyboard; sticky Ctrl then `c` sends
   Ctrl+C; hardware keyboard (iPad or Bluetooth) arrows, Tab, Shift+Tab, Esc,
   F-keys; Cmd+C/Cmd+V still work.
5. Selection/copy: swipe down on a normal-screen terminal opens History;
   long-press selection and Copy work; selection stays stable while new output
   arrives.
6. Backgrounding: switching apps releases control (or expiry does); returning
   reconnects as observer and needs an explicit Take control; no text is sent
   again.
7. Width agreement: CJK, emoji, ZWJ sequences, and combining marks line up
   with the host's columns at 14 px and at other font sizes.
8. Service worker update banner and offline "Connection unavailable" page.

Android Chrome (Gboard and at least one other keyboard):

1. Install prompt, icon (including maskable crop), standalone launch.
2. Keyboard overlap with `interactive-widget=resizes-content`.
3. IME and swipe typing commit exactly once; Backspace works when the hidden
   field is empty; Enter sends one `enter` key.
4. Selection/copy in History, backgrounding, and reconnect behavior as above.
5. Width agreement as above.
6. Controller-only resize: the measured grid matches the visible area after
   rotation and font-size changes; observers do not resize the PTY.

## Review pass 2026-09-27

An adversarial review of `git diff eb8421a..HEAD -- web docs/evidence/p5`
against AGENTS.md, plan sections 5 and 7–11, and `protocol/v1`. This was a
same-account agent review, not an independent human review. Each fix has a
vitest test that failed before the fix.

Fixed:

| Severity | Finding | Fix |
| --- | --- | --- |
| Blocker | Ajv compiled validators with `Function()` at runtime. The gateway CSP has no `'unsafe-eval'`, so the served page was blank. | A Vite virtual module (`virtual:protocol-validators`) emits standalone Ajv code at build and test time. The custom keywords are now code generators. The bundle contains no `Function(`, `eval(`, or `sourceMappingURL`. Test: `src/protocol/noEval.test.ts` blocks `Function` and validates. |
| Medium | After any `input.rejected` (for example `limit_exceeded`), the page kept the lease. The broker does not advance its high-water mark for a rejected sequence, so every later input was rejected as `input_gap` while the page still showed "You have control". | Any rejection releases the lease, stops sending, and shows the message. The user must press Take control for a new lease (sequence restarts at 1). |
| Medium | Multiline text from keyboard clipboard chips, text replacement, or a composition commit arrived as `insertText`, not a `paste` event. It was sent at once with Enter keys and skipped the multiline confirmation. | Multiline inserted text goes to the Composer. A lone line break is still one `enter` key. |
| Low | The pairing fragment was removed only on exactly `/pair`. At `/pair/` or another path the grant stayed in the URL and history. | Every fragment is removed at startup. The grant is accepted only on `/pair`. |
| Low | Under continuous output each `history.read` got `history.changed`, and the page restarted without end ("Loading history…" forever). | After five restarts the view becomes unavailable and asks the user to return to live. |
| Build | Built JS pointed to a source map that the gateway refuses to serve. | `build.sourcemap: 'hidden'`. |

Checked, no defect found: input is gated on a live link, the controller
role, a current lease, and a running session. Reconnect attaches as an
observer and sends only `hello`, `session.attach`, and `snapshot.applied`,
with no replay. Sequences are decimal strings from a `bigint` counter per
lease. Patches are applied only on a matching base revision, after the
serialized snapshot paint. `snapshot.applied` is sent only after the paint
promise resolves. Observers never send resize, and resize is clamped to
20–400 × 5–200. IME commits are sent once. No Enter comes from a form
submit. The composer never sends on reconnect. The poll secret is never in
React state or storage. There is no `innerHTML` and no
`dangerouslySetInnerHTML`. Every WebSocket message goes through
`parseHostMessage`. Terminate, Ctrl+C, and Detach use separate handlers.
History pages are keyed by epoch, revision, and offset. The service worker
caches only `/assets/*` and activates only after the user chooses to reload.
Listeners, timers, and sockets are released on unmount. Controls are at
least 44 px.

Not fixed (recorded):

- Lazy loading the Live route was not done. The service worker does not
  precache, so after a host upgrade an old page could request a lazy chunk
  that no longer exists and fail. Safe splitting needs a chunk-load error
  path. The single chunk is now 866 kB raw (203 kB gzip, down from 231 kB).
- A lease heartbeat that is in flight when the shell exits can get
  `session_lost` from the broker (`ErrSessionNotRunning` maps to
  `session_lost`), so the page can show "Session lost" for an exited shell.
  This is a small race. The mapping is in Go and is outside this branch's
  scope.
- `control.changed` without a lease drops local control without comparing
  `leaseEpoch`. The current broker never sends the holder a lease-less
  notice for its own epoch, so this is only a robustness gap.

Checks after the fixes: `pnpm typecheck` passed. `pnpm test:web` passed:
11 files, 86 tests. `pnpm build:web` passed. `./scripts/check` exited 0: no protocol
generation drift, gofmt and `go vet` clean, 22 Go test packages passed,
and the web typecheck, 86 tests, and build passed.

## PR #25 adversarial review 2026-09-27

| Severity | Defect | Fix |
| --- | --- | --- |
| High | `FrameRenderer.paint()` awaited one xterm write per style run and width group. Each write costs a browser timer task (about 4 ms when clamped). A colorful 80x24 screen with 20 runs per row took 4.8 s in Chromium, 120x50 took 9.9 s. This is longer than the broker's 5 s `snapshot.applied` deadline, so the viewer was dropped and reconnected without end. | The frame is built as one drawing string per server cell width. A frame is 1 write (2 writes when it has wide cells). The sequences are unchanged: the same allowlist, rows erased before repaint, autowrap off while painting, cursor restored at the end. Width 1 and width 2 cells are still separate writes, because `ServerUnicodeAddon` applies the width while xterm parses. |
| Medium | After `screen.resync` the broker answers `control.acquire` with `resync_required`. The page sent a resync but stayed in `acquiring`, so Take control stayed disabled. | On `resync_required` while acquiring, control goes back to `observer`. The user can tap again after the new snapshot. |
| Low | A late heartbeat or resize after `session.state` `exiting`/`exited` gets `session_lost`. The page showed "Session lost … host restarted". | If the session already reported exiting or exited, `session_lost`/`session_missing` closes the link and keeps the exit screen ("Shell exited (code N)") and the last screen. No reconnect. |
| Low | Frames were scheduled only with `requestAnimationFrame`. Hidden tabs do not run it, so `snapshot.applied` was not sent and the broker dropped the viewer every 5 s. | Each scheduled frame also has a 100 ms timer. The first of the two to run paints the frame. |

xterm writes per frame (counted on a real xterm `Terminal` in Node):

| Frame | Before | After |
| --- | --- | --- |
| 200x60, 20 runs per row, narrow cells | 2403 | 1 |
| 80x24, 20 runs per row, narrow cells | 963 (computed: 2 + 24 x 20 x 2 + 1) | 1 |
| Snapshot or patch with wide cells | per run | 2 |

The 200x60 frame paints in about 10 ms in Node. The regression test
requires at most 2 writes and less than 1 s. A real-browser timing run was
not done: Playwright is not installed. The write-count test is the gate.

New tests: `FrameRenderer.test.ts` (large colorful snapshot write count and
time; mixed narrow/wide snapshot and patch with correct widths),
`Connection.test.ts` (resync_required during Take control, session_lost
after exit, session_lost while running still lost, snapshot acknowledged
when animation frames never run). Each test failed before its fix.

Checks after the fixes: `pnpm typecheck` passed. `pnpm test:web` passed:
11 files, 92 tests. `pnpm build:web` passed. The first `./scripts/check`
run failed two `internal/liveclient` tests
(`TestWrongInputAcknowledgementDoesNotAdvanceSequence`,
`TestStaleCorrelatedGrantCannotRestoreReleasedEpoch`: "read/write on closed
pipe"). This branch did not change Go code. `go test -count=5
./internal/liveclient/` then passed, and a second `./scripts/check` exited 0
(24 Go packages ok, web typecheck, 92 tests, and build passed). The
liveclient tests are flaky under parallel load; this is recorded, not fixed
here (Go is outside this task's scope).

## First-user polish 2026-09-29

Branch `feat/hu-web`, base `12b8dc7`. Apple Silicon macOS 26.5.2, Node
24.11.0, pnpm 10.33.0. Only `web/` and this file changed. No dependency was
added. `web/src/generated/protocol.ts` was not edited. The server side of
`PATCH /api/v1/device` is built in parallel on another branch; the page was
tested with mocked `fetch` and a throwaway mock gateway, not the Go gateway.

### Faster first load

Setup, Sessions and Settings no longer load the terminal code. `Live` (with
xterm, its CSS, the protocol validators, `Connection`, `InputController`,
History and Composer) is a lazy chunk (`React.lazy`). The Sessions screen
fetches that chunk in the background after it shows, so Open and Start
session still go straight to the live terminal.

| Initial load (`pnpm build:web`) | Before | After |
| --- | --- | --- |
| Entry JS | 867.37 kB (203.93 kB gzip) | 244.23 kB (76.00 kB gzip) |
| Entry CSS | 8.55 kB (2.37 kB gzip) | 4.75 kB (1.58 kB gzip) |
| Lazy `Live` chunk | – | 629.28 kB JS (130.08 kB gzip) + 3.93 kB CSS |

Initial JS is 63% smaller after gzip. The `Live` chunk is still over Vite's
500 kB warning size; xterm is most of it.

Chunk failure: after a host upgrade an old page can ask for a `Live-*.js`
that the gateway no longer serves (the gateway returns 404 for missing
`/assets/*`, never HTML). The import is wrapped (`loadChunk`) so only that
failure becomes `ChunkLoadError`. `ChunkErrorBoundary` shows "A new version
is available" with a Reload button instead of a blank page. Other render
errors are not hidden. Reload is a normal page load: it does not tell a
waiting service worker to skip waiting. `sw.js` is unchanged: it still has no
`skipWaiting()` on install and does not cache a failed (404) asset. The build
has no inline script, no `eval(`/`Function(`, and no `sourceMappingURL`, so it
works with the gateway CSP (`script-src 'self'`).

### Device rename in Settings

Settings has a "Device name" field with the current label and a "Save name"
button. It calls `PATCH /api/v1/device` with `{label}` (same-origin,
`Content-Type: application/json`, `X-Pterm-Request: 1`), and shows the
server's new label. Validation mirrors the `deviceLabel` schema: 1–64
characters, at most 64 UTF-8 bytes, no control characters, no edge
whitespace (input is trimmed first). The same check now drives the pairing
name field. States: invalid (message under the field, button off),
unchanged (button off), "Saving…", "Name saved.", and the server's error
message. A 401 goes back to Setup; no network goes to "Connection
unavailable". The field has a label, `aria-describedby` help and a
`role="status"` line.

### First-use fixes

- Sessions with no saved project: shows "Add a project on your computer
  first:" with `pterm project add <folder>` and a Check again button,
  instead of an empty picker. No shell profile: points to `pterm doctor`.
- Refresh and Check again now reload settings too, so a project added on
  the computer shows up without reloading the page. The pickers fall back to
  the first option when the list changes.
- Start session opens the live terminal right away ("Starting…" while
  waiting) and no longer reloads the list behind it.
- Pairing errors say what happened and what to do: expired or used link
  (410/401) → "This pairing link expired or was already used. Run
  `pterm pair` again on your computer."; 429 → wait a minute; no network →
  "This phone cannot reach your computer."; raw server text is no longer
  shown. Opening `/pair` without a usable grant (reloaded or cut link) says
  so, instead of the generic text.
- Setup steps now include "Check the code on both screens, then approve it
  on your computer." Logout and revocation reasons are plain ("You logged
  out. This phone no longer has access.", "This phone is no longer paired
  with your computer.") above the same steps.
- A reloaded `/pair` (or any unknown path) after pairing is replaced by `/`.
- Live, read-only: a line above the controls says "Read-only. Tap Take
  control to type." (or "Tap Take control to move control to this phone."
  when another connection has control). Not shown while connecting, while
  controlling, or after the shell ended. Reconnect text is now "Reconnecting…
  typing is paused".
- 320 px: selects and inputs are full width (a long project name no longer
  widens the page); command blocks wrap at spaces first and have no extra
  margin.

Not done (outside the plan or needs a protocol decision): automatic Take
control after Start session. The live contract is observer first with an
explicit `control.acquire`, so the page still waits for the tap.

### Tests

New files: `src/api/http.test.ts` (label rules incl. 16×4-byte emoji ok,
66-byte CJK rejected; PATCH method, path, headers, same-origin, body; error
code/message), `src/screens/Settings.test.tsx` (save trims and PATCHes,
invalid/unchanged send nothing, API error thrown; labelled field markup),
`src/screens/chunks.test.tsx` (static import graph from `main.tsx` has no
`@xterm`, `Live`, `Connection` or react-virtual; `ChunkLoadError` wrapping;
boundary shows Reload for chunk errors and rethrows others),
`src/sw.test.ts` (no `skipWaiting` on install, only on the reload message; a
404 asset is not cached), `src/screens/Setup.test.tsx` (error wording,
logout reason with steps, `/pair` without grant),
`src/screens/Live.test.tsx` (read-only hint). `Sessions.test.tsx` gained 3
empty-state tests.

```
pnpm typecheck    # pass
pnpm test:web     # 18 files, 117 tests passed (was 12 files, 94 tests)
pnpm build:web    # pass (Live chunk size warning only)
./scripts/check   # exit 0: no generation drift, plan copy ok, gofmt/go vet
                  # clean, all Go packages ok, web typecheck/117 tests/build
```

Desktop Chromium smoke test at 320×640 CSS px against `web/dist` served by a
throwaway mock gateway outside the repository (no WebSocket): the first load
fetched only `index-*.js`/`index-*.css`, then `Live-*` in the background
after Sessions showed; no element wider than the viewport and every button
at least 44×44 px on Sessions, Settings and Live; the empty-project card;
Start session went straight to `/sessions/<id>`; Settings rename showed
"Enter a name." for a blank name and "Name saved." after a save. With the
service worker and caches cleared and the mock returning 404 for `Live-*`,
opening a session showed "A new version is available" and Reload.

### Owner verification needed

Not run; each needs a real phone over the private HTTPS hostname and the Go
gateway with the rename route.

1. First load on a slow mobile connection: Sessions appears before the
   terminal code finishes; opening a session right after shows "Opening
   terminal…" briefly, then the terminal.
2. Upgrade path: open the app, install a newer host build, then open a
   session without reloading. Expect "A new version is available" → Reload
   → the session opens. A live session already open keeps working, and the
   "Reload to update" banner still waits for a tap.
3. Rename in Settings on iPhone Safari and Android Chrome: the name saves,
   `pterm devices` shows it on the computer, emoji and CJK names within 64
   bytes save, and a 65th byte is refused on the phone.
4. Fresh install with no project: the card shows the command; after
   `pterm project add <folder>` on the computer, Check again shows the
   pickers.
5. Pairing: an expired link, a used link, and a reloaded `/pair` page each
   say to run `pterm pair` again.
6. Read-only hint and Take control on a 320 px wide phone (for example
   iPhone SE 1st gen) in portrait, with the keyboard open and closed.

## UI v2 (2026-09-27)

Branch `feat/ui-v2-web`. The phone web client now follows the approved
"Termivoa v2" design (Paper file "termivoa"; exact colors, sizes, spacing
and radii were read with the Paper tools, not from screenshots). Fonts stay
local: system-ui for the UI, the local monospace stack for the terminal,
keycaps, tabs and the status line. No downloaded fonts, gradients or
animations. Protocol and security behavior is unchanged; the client only
reads the two new optional session fields `lastOutputAt` and `preview`.

### What changed

- Live (in control): session tabs (open tab with `❯` and accent border,
  new-output dot, `+` new session, `…` menu), status line
  (`project · shell · cols×rows`, link dot, `● CONTROL`), and the cockpit dock:
  agent keys `esc` `⌃C` `⇧⇥` `↑` `1` `2` `y` `⏎`, modifier row `tab`
  `ctrl` `alt` `←` `↓` `→` (plus application scroll `▲` `▼` on the alternate
  screen), and actions `❯ type`, `¶ compose`, `⌘ snips`, `↺ history`.
- Live (read-only): same tabs and status line with `READ-ONLY`; the dock says
  who has control ("<device> has control" / "No one has control"), "You are
  watching. The agent keeps working.", Take control, History and Sessions.
- `…` menu: Release control, Rename, Terminate (same confirmation text as
  before), All sessions, Settings. Rename and Terminate moved here from the
  Sessions list.
- Composer and Snippets are bottom sheets over the dimmed terminal. They
  overlay the dock, so opening them does not resize the PTY.
- Sessions: title, host line, settings button; rows with glyph tile, name,
  controller text (You = accent, Other device = amber, No one = muted),
  `project · shell · age`, new-output dot and preview line (plain text only);
  exited rows faded and not openable; `❯ new session`.
- New session (new screen, `/new`): saved launch presets, custom project /
  shell (first profile, the login shell, is the default) / optional "run
  first", "save as preset", `❯ start session`.
- Pair: wordmark, step label, big title, six digit tiles, a preview of the
  computer's `Approve? [y/N]` prompt, the `pterm pair approve` fallback,
  static waiting dots and "Codes don't match? Cancel".
- History: header with Done, "Paused at … · N lines", older lines dimmed,
  accent selection, "New output · Return to live" pill, and a Copy button
  that copies the current selection with the user's tap.
- Settings restyled (device name, text size, build, log out).
- Fix found during the browser check: the controller's fit loop could
  oscillate with desktop scrollbars (grid overflows → scrollbar narrows the
  box → smaller grid → scrollbar goes → larger grid). The terminal area now
  hides scrollbars from layout (panning still works) and measures its
  offset size.

### Feature rules

- Tabs: running sessions from the session list, polled every 5 s while
  Sessions or Live is open and the page is visible. Switching tabs unmounts
  Live (release control, `session.detach`, close) and mounts a new
  connection that attaches read-only. No lease, sequence or input carries
  over.
- New-output dot: `lastOutputAt` newer than this device's last view of that
  session id + generation. Last-view times live in localStorage
  (`pterm.lastViewed`, at most 100 entries, not secret). A view records
  max(phone time, the session's own `lastOutputAt`) so clock skew cannot
  flag output already seen.
- Agent keys send fixed messages (exact envelopes tested), ignore the armed
  Ctrl/Alt latch, and are disabled without control. The modifier row keeps
  the existing latch behavior.
- Snippets (localStorage `pterm.snippets`): three generic defaults; title
  ≤ 80 characters, text ≤ 64 KiB and no control characters except tab and
  line breaks, at most 50. Tap sends `input.paste`, then Enter only when the
  snippet asks. Multi-line text shows the composer's confirmation first.
  Disabled without control. `+ new`, edit and delete via an edit toggle.
- Launch presets (localStorage `pterm.launchPresets`, at most 20, none by
  default): label, project, shell, optional run first (one line, ≤ 1000
  characters).
- Run first: after the first snapshot is applied, ask for control only if no
  one holds it, then send the command as text and Enter once, and keep
  control. If someone else holds control, the grant fails, the link drops or
  the shell ends, nothing is sent and a banner says why. The request is
  in-memory only and marked settled, so reconnects, reloads and returning to
  the tab never send it again.

### Tests

```
pnpm typecheck    # pass
pnpm test:web     # 22 files, 151 tests passed (was 18 files, 117 tests)
pnpm build:web    # pass (Live chunk size warning only, as before)
./scripts/check   # exit 0 (Node 24.11.0): no generation drift, plan copy ok,
                  # gofmt/go vet clean, all Go packages ok, web checks as above
```

New or changed tests: exact agent-key envelopes and nothing sent without
control (`terminal/agentKeys.test.ts`); snippet defaults, bounds, tampered
storage, multi-line confirmation and sendEnter (`terminal/snippets.test.ts`);
run first: free control only, sent once, not after reconnect or remount, not
when control is taken or the link drops, not after exit
(`terminal/runFirst.test.ts`); new-output dot against last view, generation
keying, clock skew, storage bound, and launch preset bounds
(`screens/activity.test.ts`); tab switching detaches and reattaches
read-only with no control or input carried (`terminal/Connection.test.ts`);
dock accessible names ("Control C", "Shift Tab", "Enter", …), keys disabled
without control, armed modifier, tab dot and active tab
(`screens/Live.test.tsx`); controller text, ages, preview and title rendered
as text with no HTML injection, exited rows, first use and New session
(`screens/Sessions.test.tsx`); the import graph still keeps xterm, Live and
the new dock/sheet/run-first code out of the first-load chunk
(`screens/chunks.test.tsx`).

### Sizes (gzip)

| Asset | Before | After |
| --- | --- | --- |
| First-load JS (`index-*.js`) | 76.00 kB | 78.56 kB |
| First-load CSS (`index-*.css`) | 1.58 kB | 3.88 kB |
| `index.html` | 0.46 kB | 0.46 kB |
| Live JS (lazy) | 130.08 kB | 135.83 kB |
| Live CSS (lazy, xterm) | 1.01 kB | 1.01 kB |

### Desktop browser check (not a phone)

Desktop Chromium at 390×844 and 320×568 CSS px against `web/dist` served by
a throwaway mock gateway outside the repository (hand-written WebSocket that
speaks the v1 envelopes). Checked: Sessions rows, dots and a preview holding
`<img …>` shown as literal text; read-only dock with "MacBook has control";
Take control → cockpit; `⌃C`, `⇧⇥`, `y`, `⏎` produced exactly
`character c + ctrl`, `tab + shift`, text `y`, `enter`; snippet `git status`
→ `input.paste` + `enter`; a two-line snippet asked first, then pasted;
switching to another tab sent `control.release`, `session.detach`, then a
new `hello`/`session.attach` and showed READ-ONLY; New session with run
first `claude` → one `control.acquire`, `input.text "claude"`, `enter`;
reloading that session sent nothing. At 320 px every button was at least
44×44 px and nothing overflowed the viewport; the agent-key row wraps to two
lines there.

### Deviations from the design

- Keys and tabs keep 44 px hit targets. The design's agent keys are 34–46 px
  wide, so the row needs about 390 px; narrower phones wrap it to two lines.
  Tabs keep the 36 px face inside a 44 px button.
- The design's "edit" next to AGENT KEYS was dropped (not implemented).
- The session list does not include other devices' labels, so the list says
  "Other device" (amber) instead of a device name. Live shows the label from
  `control.changed` when the broker sends it.
- "12 new lines" in History is "New output": the client knows output
  arrived, not how many lines.
- Pairing says "your computer" (hosts can be Windows) and shows the real
  `Approve? [y/N]` prompt plus the `pterm pair approve` fallback.
- "hold to edit" on snippets and presets is an explicit edit toggle
  (long-press is not accessible). Presets can be removed this way.
- The Sessions host line says "Your computer", as the API has no host name.
- The read-only "Details" link was not in the brief and was left out.
- Small hint text uses the muted color instead of the design's `#5B6578`,
  which is below 4.5:1 contrast; section labels keep `#5B6578`.

### Owner verification needed

Not run; each needs a real phone over the private HTTPS hostname.

1. Cockpit dock on iPhone Safari and Android Chrome with the soft keyboard
   open: keys stay above the keyboard, tapping a key does not close the
   keyboard, the armed `ctrl` then a typed letter sends Ctrl+letter.
2. Agent keys against a real agent (for example Claude Code): `esc` stops,
   `⌃C` interrupts, `⇧⇥` cycles the mode, `1`/`2`/`y`/`⏎` answer prompts.
3. Tabs: switching between two running sessions never carries control
   (the new tab says READ-ONLY until Take control), the new-output dot
   appears for a background session that prints, and clears after viewing.
4. Snippets: defaults present on a fresh phone; add, edit, delete survive a
   reload; a multi-line snippet asks first; nothing is sent without control.
5. New session with run first on a fresh session: the command runs once;
   reconnecting (toggle airplane mode) does not run it again.
6. 320–375 px wide phones: the agent-key row wraps cleanly and the terminal
   keeps a usable height; Composer and Snippets sheets scroll above the
   keyboard.
7. History Copy on iOS and Android: select lines, tap Copy, paste elsewhere.
8. VoiceOver/TalkBack read "Control C", "Shift Tab", "Enter", tab names with
   ", new output", and "Take control".

## UI v2 review pass 2026-09-27

Same-account agent review (not an independent reviewer) of
`git diff origin/main...feat/ui-v2` on branch `fix/ui-v2-review`. Each fix
has a test that failed before the fix.

### Fixed

- Medium: the session list poll could stack requests (a new one every 5 s
  while an older one hung) and a slow older response could overwrite a newer
  list. `latestOnly` in `screens/hooks.ts` now skips a tick while a request is
  open and applies only the newest response (`screens/hooks.test.ts`).
- Medium (accessibility): the pairing code tiles are `aria-hidden` and the
  `aria-label` sat on a `<p>`, where screen readers do not read it, so the
  verification code had no spoken text. It is now visually hidden text
  (`screens/Setup.test.tsx`).
- Low: a stored snippet list with a repeated id made editing one snippet
  overwrite the other (edit and delete match by id). Loading keeps the first
  entry per id (`terminal/snippets.test.ts`).
- Low (privacy): the session list preview printed text the terminal marks as
  concealed (SGR 8), which the live view hides. Concealed cells now count as
  blank (`internal/broker/preview_test.go`).
- Docs: `http-api.md` said `lastOutputAt` moves when the screen changes; it
  moves on any applied child output (bell, title change), as the broker
  evidence already says.

### Checked, no defect found

- Run first: sent once (the request is in-memory and marked settled before
  sending); nothing after reload, back/forward, tab return or reconnect
  (every new view has no request); asks for control only when the first
  snapshot is live and `control.changed` (sent before the snapshot) shows no
  other controller; gives up if control is taken while acquiring.
- Tabs: the Live screen is keyed by session id + generation, so switching
  releases control, sends `session.detach`, closes the socket and clears the
  reconnect timer; lease, sequence, latch, panel and composer draft do not
  carry over.
- Agent keys: sent only through the Connection, which refuses without a live
  lease. Real encodings through the broker codec and emulator: `esc` →
  `\x1b`, `⌃C` → `\x03`, `⇧⇥` → `\x1b[Z`, `↑` → `\x1b[A`, `⏎` → `\r`.
- Snippets: every send goes through `checkPaste` (64 KiB bound, multi-line
  confirmation, control required); stored data is type-checked and bounded;
  a lone surrogate from tampered storage is refused by the sender without a
  crash.
- localStorage (`pterm.snippets`, `pterm.launchPresets`, `pterm.lastViewed`):
  corrupt JSON, wrong types and oversize arrays fall back or are filtered;
  none holds a secret.
- No `innerHTML`/`dangerouslySetInnerHTML`/links; titles, previews, labels
  and snippet text render as React text only.
- Polling stops when the page is hidden and when Sessions/Live unmount; an
  auth failure returns to Setup and stops it.
- Broker: `lastOutput` is set only in the PTY output case of the actor; CLI
  list/status pass no device, so they never carry `lastOutputAt`/`preview`;
  the screen copy is taken in the actor and the text is built after it
  replies.
- Preview validator parity: 200,031 strings (random from a hostile pool of
  controls, NEL, NBSP, U+180E, U+2028, U+200B, U+FEFF, bidi, combining marks,
  CJK, emoji, private-use, unassigned, plus 160/161-rune and 480/481-byte
  edges) gave the same verdict from Go `ValidatePreviewText` and the schema
  rule used by the web Ajv keywords (11,690 valid on both sides, 0
  disagreements). All 19,985 non-empty `screenPreview` outputs from 20,000
  random screens passed the schema rule. Scratch harness only, outside the
  repository.

### Known limits (not changed)

- Run first and a manual Take control can still take over another device
  that acquired within one round trip before our `control.acquire`: the
  protocol's acquire is a takeover and has no "only if free" form.
- Closing a sheet does not return focus to the button that opened it.

### Commands

```
pnpm typecheck && pnpm test:web && pnpm build:web   # pass, 23 files, 155 tests
GOTOOLCHAIN=go1.27.1 go test -race ./internal/...   # pass
GOTOOLCHAIN=go1.27.1 ./scripts/check                # exit 0 (Node 24.11.0)
```

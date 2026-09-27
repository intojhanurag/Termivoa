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

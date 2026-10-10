# Touch gestures on the Live terminal

Termivoa's Live terminal takes five touch gestures on the phone: hold and
slide for arrow keys, tap to place the cursor, swipe on the status line to
switch session, pinch for text size, and a two-finger tap for Esc. This is
the internal design; how to use them is in the Live terminal guide on the
docs site. Every gesture also has a button, so none is required.

## What did the owner decide?

| Decision | Choice |
| --- | --- |
| Which gestures | All five |
| Off switch | **Settings → This phone → Terminal gestures**, per phone, on by default (`localStorage` key `pterm.gestures`, `on`/`off`) |
| Two-finger tap | Esc (kept) |

With the switch off, every touch behaves exactly as before: one-finger
swipes scroll a full-screen app or pull down to History, and the page may
zoom.

## Where is the code?

| Part | File |
| --- | --- |
| Pure rules and the state machine (`TouchGestures`) | `web/src/terminal/gestures.ts`, tests in `gestures.test.ts` |
| Wiring: native touch listeners, the host, tap to place, status-line swipe, the pill | `web/src/screens/Live.tsx` |
| Cell geometry (`cellSize`, `cellAt`, `rowCells`) | `web/src/terminal/TerminalView.ts` |
| Frame cursor (`Connection.cursor()`: metadata only) | `web/src/terminal/Connection.ts` |
| The switch (`useGestures`) | `web/src/screens/hooks.ts`, `web/src/screens/Settings.tsx`, threaded by `App.tsx` and `ProviderScreen.tsx` |
| End-to-end checks GE1–GE7 | `scripts/e2e-multicomputer/gestures.mjs` |

## How does one touch become a gesture?

One `useEffect` in Live adds native `touchstart`, `touchmove`, `touchend`
and `touchcancel` listeners on `.terminal-area` (move and end with
`passive: false`, so they may prevent the default) plus `gesturestart` and
`gesturechange` (Safari's page zoom). They replace the old React touch
handlers; the old app-scroll and pull-to-History code runs unchanged as the
machine's `drag` callback.

```text
idle ─1 finger─▶ pending ─moved > 8 px─▶ scroll (drag: app scroll / History pull)
                    │
                    ├─ held 400 ms, can send ─▶ trackpad ─lift─▶ idle
                    │
                    └─ 2nd finger ─▶ two ─moved ≥ 10 px or scale ≥ 5 %─▶ pinch ─lift─▶ done
                                       └─ both lift: quick and still ─▶ Esc
done: ignore everything until every finger lifts (a new single touch resets)
```

The machine lives in a ref and reads a `GestureHost` that Live rebuilds
each render, so it always sees the latest state. Only the pill text
(`← → ↑ ↓` or `20 px`) reaches React state. No terminal bytes pass through
the machine (rule 8).

A gesture that took its touch prevents the default of `touchend` (no
synthetic click) and also stamps `tookTap`; a click within 600 ms is then
treated like a link tap in `areaClick` and does nothing.

## How is input sent?

Every key goes through `connection.sendKey(name, NO_MODIFIERS)`, the same
path as the agent keys, not `InputController.pressKey`, so a latched
Ctrl or Alt is not consumed. `sendKey` refuses (returns false) without a
live link, control and a running session; nothing is queued (rule 10).
The first refusal ends the gesture; there is no retry. The broker applies
DECCKM, so the phone sends `arrow-*` names.

Input gestures need `canInput`: control of a live, running session, and no
sheet, question card or full panel. A read-only phone never arms the
trackpad and never sends Esc. Pinch and the status-line swipe are not
input and work without control. Wall tiles have no gestures.

## What are the rules and bounds of each gesture?

| Gesture | Starts when | Sends | Bounds (rule 11) |
| --- | --- | --- | --- |
| Hold and slide | One finger held 400 ms within 8 px, and `canInput` | `arrow-left/right` or `arrow-up/down` | Axis locks after 12 px until lift. One key per `max(cell width, 10 px)` across or `max(1.5 × cell height, 24 px)` down (`wheelSteps`); at most 4 per move, 30 per second (excess dropped, never buffered), 120 per gesture |
| Tap to place | A plain tap with the keyboard already up, gestures on, `canInput` | one `arrow-left/right` per cell | Only when `tapArrows` agrees (below); at most 80 |
| Swipe on the status line | One finger on `.status-line`: at least 60 px across, more than twice its vertical travel, under 500 ms, starting more than 24 px from the screen edges | Nothing; opens the tab before or after (`onOpen`) | Not on a provider screen (chips, not tabs) |
| Pinch | Two fingers on the terminal, moved ≥ 10 px or distance changed ≥ 5 % | Nothing; sets the text size on lift | `clamp(start + round(log2(scale) × 6), 10, 24)`; one `useFontSize` update, so the PTY resizes once |
| Two-finger tap | Second finger within 60 ms, both lift within 250 ms, each moves < 10 px, distance changes < 5 % | One `escape` | Never during pinch, trackpad or a sheet |

A second finger, `touchcancel`, a hidden page (`visibilitychange`), a lost
`canSend` or a cap ends the trackpad. A move before arming is a scroll and
never arms.

## When does a tap move the cursor?

`tapArrows(screen, tap, cells)` returns the signed number of arrows, or 0
(just focus, as before). All must hold:

- the normal screen (not the alternate one), a visible cursor, and the
  tapped row equals the frame cursor row (left and right only, never up or
  down);
- every cell between the tap and the cursor has width 1 and one code point
  (no wide character, continuation cell or joined emoji);
- a move to the right only crosses cells styled like the cell left of the
  cursor, and never the blank tail after the last non-blank cell. This keeps
  grey autosuggestions (fish, zsh-autosuggestions) and a right prompt from
  being "typed over";
- at most 80 cells away.

`TerminalView.cellAt` maps a viewport point to a frame row and column.
xterm can show its buffer from a line other than its base after a resize
(`viewportY ≠ baseY`); the cell is mapped through both, and `rowCells`
reads the frame row's cells from `baseY + row`.

## What about accessibility?

VoiceOver uses its own gestures, so these do not fire under it. Every
gesture keeps its button: the dock arrows and `esc`, the tabs, **A−**/**A+**
in Settings. The pill is `aria-hidden` and does not animate, so reduced
motion needs nothing. Page zoom is blocked only on the terminal area and
only while gestures are on; `-webkit-touch-callout: none` there keeps the
hold from opening the iOS callout.

## How is it tested?

- `web/src/terminal/gestures.test.ts`: axis lock, pinch mapping and clamps,
  two-finger timing, swipe rules, every `tapArrows` refusal (wide character,
  ZWJ, style change, blank tail, other row, hidden cursor, alternate
  screen, row start, far tap), and the machine with a fake clock: carry and
  the 4/30/120 caps, a refused key ends the gesture, read-only never arms,
  a scroll never arms, cancel, pinch on lift, Esc, the switch off.
- `scripts/e2e-multicomputer/gestures.mjs` (WebKit 26.4 with touch, real
  dev stack): GE1 hold and slide sends three `←` (`echo 12345` → `12X345`);
  GE2 two-finger tap sends one Esc (`cat -v` shows `^[`); GE3 pinch shows
  `20 px` and sends exactly one `terminal.resize`; GE4 a tap on `a` of
  `echo abc` gives `Xabc`; GE5 a tap on dim text right of the cursor sends
  nothing; GE6 a status-line swipe opens the other session; GE7 with the
  switch off nothing fires. WebKit has `TouchEvent` but no `Touch`
  constructor, so the script sends plain events that carry the touch lists.

## What is left?

In [pending-work.md](pending-work.md): auto-repeat at the screen edge for
the trackpad, tap to place in `vim` and other full-screen apps, and checks
on a real iPhone (iOS Safari and the home-screen app).

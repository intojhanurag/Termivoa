# Sheet focus

Termivoa's shared `Sheet` owns focus entry and restoration for its bottom
sheets. This internal design records the accessibility correction in #214.
User instructions live in the Live terminal guide.

## What was wrong?

`Sheet` focused its section on mount but had no unmount cleanup. Removing
the focused section put browser focus on `document.body`, so keyboard users
lost their place after Close or Escape. `ProviderManage` had two caller-local
restoration hooks, but the other sheets did not. Restoration now belongs
to the shared component; those duplicate hooks are removed.

## How is the opener retained and restored?

`useState` captures `globalThis.document?.activeElement` once when the Sheet
first renders, before React commit and child focus effects or `autoFocus`.
The optional document keeps server/static rendering unchanged. Re-renders
retain the original element. The existing mount effect still focuses the
sheet section, and captures that section for later cleanup (the ref is null
after React removes the DOM node).

Cleanup restores only a connected `HTMLElement`, and only when current
focus is on body or still inside that sheet. If navigation or another
control deliberately placed focus elsewhere, cleanup leaves it there.
Native focus behavior skips hidden, disabled or inert controls; a removed
trigger is explicitly skipped. Restoration uses `preventScroll` so it does
not change the page's scroll position.

Each nested Sheet remembers its own trigger. Closing the inner one restores
the trigger inside its parent; removing both restores the outer trigger,
while the disconnected inner trigger cannot override it. The same lifecycle
works in React StrictMode's effect setup/cleanup check. Nothing adds a focus
trap or changes which busy actions allow Close/Escape.

## What are the bounds and side effects?

| Resource or behavior | Result |
| --- | --- |
| Retained data | One opener DOM reference per mounted Sheet; released on unmount |
| Cleanup | One connectedness check, containment check and at most one focus call |
| Extra timers, global listeners or queues | None |
| Nested lifetime | Existing mounted React sheet hierarchy; no global stack |
| Terminal input or control | Unchanged; restoration sends no input or command |
| Host, network, authentication, stored settings and protocol | Unchanged |

## How are real effects tested?

`web/tests/sheet-focus/` mounts the real Sheet/SheetHeader in React StrictMode
with demo controls. `scripts/test-sheet-focus.mjs` uses the existing
repository Browser/CDP helper and a loopback Vite test server. It checks a
real Enter/Escape keyboard sequence plus ten browser DOM/effect cases:
Close, Escape, direct unmount, nested close, removing both, deliberate focus
elsewhere, removed/disabled/hidden triggers and child focus effects. No
broker, credential, shell, real session or new dependency is involved.
The fixture is typechecked but absent from the production Vite entry.

Evidence: [issue-214/verification.md](evidence/issue-214/verification.md).
Real VoiceOver, iPhone and Android checks remain pending.

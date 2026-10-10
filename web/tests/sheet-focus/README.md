# Sheet focus browser regression

This development-only fixture mounts Termivoa's real `Sheet` and
`SheetHeader` in React StrictMode. It uses only demo controls, no app API,
session, pairing, terminal or real user data. Vite's production entry does
not import it, so it is excluded from the shipped app.

From the repository root, with Node 24 and an installed Chromium executable:

```sh
pnpm install --frozen-lockfile
CHROME=<installed-chromium-executable> node scripts/test-sheet-focus.mjs
```

The script starts one loopback Vite server on a free port, uses the existing
repository CDP harness with a temporary browser profile, and closes/removes
both on completion. It installs nothing. It first sends real Enter/Escape
keyboard events, then runs ten DOM/effect checks for Close, Escape, unmount,
nested sheets, unavailable triggers and deliberate external focus. A failed
check exits nonzero and names the active element.

For manual inspection, run `pnpm --filter @phone-terminal/web dev`, open
`/tests/sheet-focus/index.html`, and call `window.runSheetFocusChecks()` in
the browser. The results also appear on the page. The normal web typecheck
includes this fixture; the production web build should contain none of its
`runSheetFocusChecks`/fixture title text.

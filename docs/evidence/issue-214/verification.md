# Issue #214: sheet focus restoration

Date: 2026-10-08. Branch: `fix/issue-214`, base `08f0332`.
Issue #214 had no comments. Its user-facing claim is real: the focused
Sheet was removed without returning focus to the opening control.

## What browser evidence reproduces the user effect?

The permanent fixture mounts the real `Sheet` and `SheetHeader` with React
StrictMode, in Chromium 151.0.7922.34. Its controls are synthetic and no
broker, real terminal, API, device, pairing or credential is involved.

```sh
pnpm install --frozen-lockfile
CHROME=<installed-chromium-executable> node scripts/test-sheet-focus.mjs
```

On the unmodified Sheet, a real browser Enter key opens the sheet and its
section gains focus. A real Escape key closes it, then
`document.activeElement` is `BODY`. The check exits 1. Six effect regressions
failed, each observing `BODY`; four safety cases passed.

| Browser case | Baseline | Fixed |
| --- | --- | --- |
| Real keyboard Enter → Escape | `BODY`, failure | `opener`, passed |
| Close returns to opener | Failed | Passed |
| Escape returns to opener | Failed | Passed |
| Direct unmount returns to opener | Failed | Passed |
| Nested close returns to its own trigger, then outer opener | Failed | Passed |
| Removing both nested sheets returns to the outer opener | Failed | Passed |
| Deliberate external focus is retained | Passed | Passed |
| Removed trigger is not focused | Passed | Passed |
| Disabled trigger is not focused | Passed | Passed |
| Hidden trigger is not focused | Passed | Passed |
| Child focus effects do not become the opener | Failed | Passed |

The fixed command exited 0 on both runs, including after simplifying the
cleanup guard. Each run passed all ten fixture checks and the real keyboard
sequence. These are actual React DOM/effect tests, not static HTML checks.

## Why was the local browser harness used?

Preferred preview tools were attempted first: `preview_status` reported
closed, then `preview_open` created a tab for the loopback Vite fixture.
`preview_snapshot`/`preview_evaluate` reported `chrome-error://chromewebdata/`
while local curl returned HTTP 200. The preferred environment-port
`preview_navigate` then returned an explicit automation error. Following
that unavailable path, the test used the repository's existing Browser/CDP
helper with an already installed Chromium executable, verified read-only.
No browser/dependency was installed outside the worktree. The temporary
browser profiles and script-owned Vite servers were cleaned up; the manual
Vite preview process was stopped as well.

## Which other checks passed?

| Command/check | Actual result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Local workspace install passed; lockfile unchanged |
| `pnpm test:web` | 57 test files, 1,048 tests passed |
| `pnpm build:web` | TypeScript build and production Vite build passed |
| `pnpm typecheck` | Passed, including the browser fixture |
| Production fixture exclusion | Scanned every file in `web/dist`, including source maps: no `runSheetFocusChecks`, fixture title or nested demo title |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Passed, 29 pages, all internal links valid, generated `.md`/`llms.txt` retained |
| `git diff --check` | Passed |

Commands use Node 24.11.0 on PATH. The React best-practices skill review
checked hook lifecycle, one stable opener capture, effect cleanup ownership,
StrictMode, accessibility and test-only bundle isolation. The shared Sheet
owns restoration; the two duplicate provider-only hooks are removed.
No timer, global listener, queue, focus trap, input send or dependency is
added to production code.

Architecture, [sheet-focus.md](../../sheet-focus.md), roadmap, README,
changelog, the existing Live terminal guide/sidebar, Next beta notes and
troubleshooting are covered together. No access, data or command change
requires a security or CLI guide change. No new pairing or database is
required. No unrelated defect was fixed.

## What remains pending?

Real VoiceOver, iPhone and Android checks are pending; Chromium DOM focus is
not evidence of a real screen reader's cursor. A removed, disabled or hidden
trigger cannot be restored, and deliberate focus elsewhere is retained.
Busy-action Close/Escape behavior and the existing absence of a focus trap
are unchanged. No host/Go or protocol code changed, so host suites were not
rerun for this web-only fix. Full repository checks, CI (currently disabled),
adversarial PR review, push, merge and docs deployment remain coordinator
gates; this helper performs only a local commit.

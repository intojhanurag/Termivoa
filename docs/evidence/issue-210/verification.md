# How was issue #210 verified?

Date: 2026-10-08. Branch: `fix/issue-210`, isolated from `origin/main`
`08f0332`. No stack/copy of #208 backend availability or #214 Sheet focus
changes. No real agent, terminal transcript, provider credit or user data.

## What was reproduced?

The real React fixture ran on baseline with only test files added. Chromium
151.0.7922.34 reported seven failures and one pass across eight scenarios:
home/peer worktree creation and New tab accepted cached agent choices during
pending settings reads; an injected rejecting settings callback was
unhandled (a new promise-contract robustness case, not a claim about App's
existing catch); a shared main
folder's last-tab hint pointed at disabled Close workspace; stale close
conflicts hid actionable main-folder guidance. No agent was already usable.

The fixture also explicitly checks `Sessions`' origin callback and newest
provider entry, so removing the open effects or substituting the kept entry
fails independently of the busy controls. The old pure `readsKinds` rule
test has been replaced by actual effect evidence, not labeled a DOM Vitest
unit test.

## Why was the local browser fallback used?

T3 `preview_status` then `preview_open` initialized `tab_2` for the fixture.
`preview_snapshot` returned `chrome-error://chromewebdata/`, despite the
loopback fixture responding HTTP 200. `preview_navigate` with the
environment-port target returned an explicit automation error. The existing
repository Chromium/CDP helper therefore ran the fixture. No browser or
package was installed outside the workspace.

## Which commands passed?

Use Node 24.11.0 on PATH and set CHROME to an existing executable. No actual
personal executable path is stored here.

```sh
pnpm install --frozen-lockfile
pnpm typecheck
pnpm test:web
CHROME='<installed Chromium executable>' node scripts/test-provider-kinds.mjs
pnpm build:web
cd site
pnpm install --frozen-lockfile --ignore-workspace
npx astro build
```

| Check | Actual result |
| --- | --- |
| Web typecheck | Passed, including the browser fixture |
| Vitest SSR/helper suite | 57 files, 1048 tests passed |
| Focused real React browser suite | Eight scenarios passed; home and peer submissions sent exactly one selected-agent request, and pending starts sent none |
| Web production build | Passed; fixture-only names/error markers absent from generated assets and source maps |
| Frozen docs install and Astro build | Passed; 29 pages, all internal links valid; existing herdr sidebar entry retained |
| React checklist | Checked effect cleanup, StrictMode setup replay, latest callback refs, no new DOM dependency or unnecessary derived state |

One focused rerun passed all scenarios but failed profile removal with
`ENOTEMPTY`: the shared browser helper does not wait for process exit.
The new runner now waits for its own process exit and uses bounded deletion
retries; the shared helper is untouched. A subsequent focused run passed
including cleanup.

Existing build notices about the large terminal chunk and absent docs i18n/
404 content remain. They did not fail these gates and were not changed.

## What remains pending?

Private PR, independent adversarial review, root integration of #208/#214,
required CI access, merge and production docs deployment belong to the
coordinator. Real iPhone/Android/VoiceOver and real herdr checks remain
pending. The successful settings read is a snapshot: a kind can disappear
afterward and the host can still refuse it. Quiet read failure keeps cached
choices; no mutation is automatically retried. No trust, protocol, host
runtime, database, stored-secret or pairing change is made.

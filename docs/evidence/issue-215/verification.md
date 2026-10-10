# How was issue #215 verified?

Date: 2026-10-08. Branch: `fix/issue-215`, isolated from `origin/main`
`08f0332`. Synthetic dates/demo repository only; no real GitHub data,
credentials, agents or provider credits.

## What did the baseline show?

Added formatter boundaries and the real GitHub repository-row regression,
then ran:

```sh
pnpm --filter @phone-terminal/web exec vitest run src/screens/Sessions.test.tsx src/screens/GitHub.test.tsx
```

Baseline: ten failures, 79 passes. The three-year repository push returned
`1095 d ago` instead of `3 yr ago`; weeks/months/years were absent and a
non-finite clock produced `NaN d`. After the fix the focused 89 tests
passed. An additional rendered SessionRow regression was then added and
passed in the full suite.

## What changed?

Only `Sessions.age` changes application code: seven-day weeks, 30-day months
and 365-day years, rounded down; current smaller units preserved. Future
dates still clamp to now. Missing/invalid ISO timestamps and non-finite
clocks yield no age. GitHub, session and last-seen wrappers share the helper;
the repository list still formats once per read, not per search keystroke.
No host, protocol, database, secret, auth or timer change.

## Which final checks passed?

Node 24.11.0 on PATH; all installs were frozen and inside the worktree.

```sh
pnpm install --frozen-lockfile
pnpm typecheck
pnpm test:web
pnpm build:web
cd site
pnpm install --frozen-lockfile --ignore-workspace
npx astro build
```

| Check | Actual result |
| --- | --- |
| Web types | Passed |
| Vitest | 57 files, 1068 tests passed |
| Boundary/invalid regressions | Each transition and preceding millisecond; 366-day, three-/ten-year spans; future dates, unknown/invalid ISO, non-finite clock, equivalent timezone offsets |
| Rendered UI | SSR GitHub RepoList shows `3 yr ago`; SessionRow shows `3 yr` with its project/shell metadata preserved |
| Web production build | Passed; existing large terminal chunk notice retained |
| Frozen docs install/build | 29 pages and all internal links valid; existing GitHub/Sessions sidebar entries retained |

No browser effect test is needed for this pure formatter/text rendering
change. Existing docs i18n/404 warnings remain; unrelated defects were
reported to the coordinator rather than changed here.

## What remains pending?

Private PR, independent adversarial review, required CI access, merge and
docs deployment belong to the coordinator. Real-phone checks remain
pending. Month/year labels describe approximate elapsed units, not exact
calendar anniversaries. No fresh database or pairing is required.

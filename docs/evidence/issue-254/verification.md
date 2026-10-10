# How was issue #254 verified?

Date: 2026-10-08. Branch: `fix/issue-254`, fresh from `origin/main`
`08f0332`, without #215's age-unit changes. Synthetic demo repositories
only; no real GitHub data, credentials, providers or credits.

## What failed before the fix?

`protocol/v1/http-api.schema.json` accepts a nonnegative integer optional
`pushedAt`. The integer `8640000000000001` is schema-valid but outside the
JavaScript Date range. `pushedText` converted it to ISO before checking its
date; both the helper and actual RepoList rendering threw `RangeError:
Invalid time value`.

Added two regressions and ran:

```sh
pnpm --filter @phone-terminal/web exec vitest run src/screens/GitHub.test.tsx
```

Baseline: two failures, 35 passes. The rendering regression runs the real
`parseHttpBody('githubReposResponse', ...)` and then SSR-renders RepoList,
so it proves the accepted data reaches the failing UI, not just a helper.

## What does the fix preserve?

`pushedText` constructs Date once and checks `getTime()` is finite before
ISO conversion. An unrenderable optional date omits only the age. The parsed
value is untouched, and both the invalid-date repository and another
valid-date row retain names, metadata and available **Set up** actions.
Valid hour/yesterday/future labels, the representable Date boundary and
epoch remain usable. Missing dates and non-finite helper inputs omit age.

No schema bounds, generated types, protocol revision, host code, access,
database or stored data changes. #215 remains a separate branch. This adds
constant per-row display work, with the existing memoized list calculation.

## Which final commands passed?

Node 24.11.0 on PATH; frozen installs only inside this worktree.

```sh
pnpm install --frozen-lockfile
pnpm --filter @phone-terminal/web exec vitest run src/screens/GitHub.test.tsx
pnpm typecheck
pnpm test:web
pnpm build:web
cd site
pnpm install --frozen-lockfile --ignore-workspace
npx astro build
```

| Check | Actual result |
| --- | --- |
| Focused GitHub Vitest | 37 tests passed |
| Full web Vitest | 57 files, 1050 tests passed |
| Web typecheck/build | Passed; existing large terminal chunk notice retained |
| Parsed-response SSR | Invalid optional date does not throw; names/metadata/actions and the other row's valid age remain |
| Frozen docs install/build | 29 pages, all internal links valid; existing GitHub sidebar entry retained |

Existing docs i18n/404 warnings remain. No browser effect test is required
for this pure date conversion/rendering fix. No new CLI, access or stored
data change requires CLI/security edits; the public GitHub guide, release
notes and troubleshooting explain an omitted age without internals.

## What remains pending?

Private PR, independent adversarial review, deployment and real-phone
checks belong to the coordinator. CI remains disabled by the owner's
explicit denial; it was not enabled and no merge was attempted. No fresh
database or pairing is required.

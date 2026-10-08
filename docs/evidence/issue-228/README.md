# Separate docs audit evidence

Date: 2026-10-08. Branch `fix/issue-228`, stacked on `fix/issue-226`.
No owner sessions, credentials or terminal history were used.

| Check | Actual evidence |
| --- | --- |
| Baseline separate audit | `cd site && pnpm audit --ignore-workspace --json` exits 1 with three high and one moderate on main 08f0332; root audit never included this lockfile |
| Source-map dependency | #226 / PR #234 supplies source-map-js 1.2.2; this PR does not repeat that fix |
| Compatible parser change | Site-only `postcss-nested@6.2.0>postcss-selector-parser` override to 7.1.6, locked integrity matches installed registry metadata |
| Frozen install and static build | `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` passes 29 pages and all internal links |
| CSS parity | SHA-256 maps of all eight emitted CSS files match the source-map-only branch's original parser output exactly |
| Raw audit after patch | Exits 1: two high, zero moderate, zero critical. No claim of zero advisories |
| Reviewed gate | `node scripts/audit-site.mjs` exits 0 and prints both retained advisory IDs/counts; exact findings and exposure documented in dependencies.md |
| Fail-closed regressions | `node --test scripts/audit-site.test.mjs` passes four tests: errors/report drift, new severity/ID/path/version, available patch and new moderate/high/critical findings block |

Primary advisory pages and locked dependency source were inspected directly.
Astro's remote-image module creates its own requests and calculates cache TTLs;
it does not invoke the vulnerable incoming max-stale method. The project's
passthrough service/local images/static output do not form a shared user cache.
The llms plugin accepts match patterns only from trusted build configuration,
not site visitors. Short literal globs in this repository have no recursive
brace patterns. These are scoped reachability decisions, not repaired upstream
packages. See `docs/dependencies.md` for exact IDs, versions and paths.

The existing root high-severity audit threshold is unchanged. The new site gate
fails on all unreviewed moderate/high/critical findings; it does not globally
ignore either package or advisory in the root workspace. Required hosted checks
remain unexecuted because CI is manually disabled and permission to spend
Actions minutes is pending. Independent adversarial review and merge are pending.
No deployed site or host runtime was changed by these local checks.

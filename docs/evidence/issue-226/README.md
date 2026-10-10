# Issue #226: patched source-map build dependency

Date: 2026-10-08. Base: `08f0332`. Node 24.11.0, pnpm 10.33.0.

The issue is real: the starting root lockfile and independent website
lockfile resolve `source-map-js` 1.2.1. `pnpm audit --audit-level high`
fails with GHSA-68fv-2mgg-jv7q. The registry identifies 1.2.2 as patched.
No shipped-runtime exploit was demonstrated.

The website lockfile was updated with
`pnpm update source-map-js@1.2.2 --depth Infinity --lockfile-only --ignore-workspace`.
The root transitive update did not change its lockfile, so a narrowly scoped
workspace override maps `source-map-js@<1.2.2` to 1.2.2; pnpm regenerated the
lockfile. Only this dependency resolution changes. No direct dependencies,
runtime code, protocol, security thresholds or CI settings change.

| Executed command | Result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Pass |
| `pnpm protocol:check` | Pass; generated files unchanged |
| `pnpm typecheck` | Pass |
| `pnpm test:web` | All 1,048 tests pass |
| `pnpm build:web` | Pass; existing chunk-size warning |
| `pnpm audit --audit-level high` | Exit 0; two moderate stream-json advisories remain (#227) |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Pass; 29 pages, all internal links valid |
| `cd site && pnpm audit --ignore-workspace --audit-level high` | Exit 1; source-map advisory absent; two high and one moderate advisories remain (#228) |

The remaining site high advisories concern `http-cache-semantics` and
`braces`; their remediation belongs to a separate issue and PR. Independent
adversarial review, required hosted checks and merge remain pending.

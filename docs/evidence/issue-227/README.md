# Issue #227: patched stream-json for protocol generation

Date: 2026-10-08. Base: `08f0332`. Node 24.11.0, pnpm 10.33.0,
Go 1.27.1. Only checked-in schema inputs were generated.

Registry metadata confirms quicktype 26.0.0 pins stream-json exactly to
3.5.0; the lockfile and audit reproduce two moderate advisories,
GHSA-hqr4-qq8f-hg3x and GHSA-mjw6-4jj6-33hc. Patched 3.6.0 is available.
No exploitation through Termivoa's trusted schema inputs was demonstrated.

The override `quicktype@26.0.0>stream-json: 3.6.0` affects only that parent
edge. pnpm regenerated the lockfile; stream-chain remains 4.2.5. All other
package resolutions and direct package declarations are unchanged.

| Command | Actual result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Pass |
| `scripts/check` | Pass: protocol drift, Go formatting/vet/tests, vendored modules, TypeScript, 1,048 web tests and production build |
| `pnpm audit --json` | Two stream-json advisories absent; zero moderate, one unrelated source-map high advisory (#226); exit 1 |

Generated Go and TypeScript remain byte-for-byte unchanged. The high audit
failure is preserved rather than weakened or bundled into this PR. Independent
adversarial review, required hosted checks, site build before merge and merge
remain pending. No runtime, pairing, database or secret change.

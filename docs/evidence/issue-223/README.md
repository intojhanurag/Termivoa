# Issue #223: production static asset compression

Date: 2026-10-08. Base: `08f0332`. Go 1.27.1, Node 24.11.0,
pnpm 10.33.0. Synthetic fixtures and public built app code only.

## Reproduction

An additive fixture test with the original handler restored via Go source
overlay requests a JS asset with `Accept-Encoding: gzip, br`. It fails:
status 200, 19,000 raw bytes, no Content-Encoding. This is a real missing
serving behavior, independent of any external reverse proxy.

## Fix and regressions

Build gzip sidecars from final immutable files, not API data. Initial
`generateBundle` compression failed decoded-equality tests because Vite
later rewrites preload tokens; `closeBundle` compresses the final bytes.
The existing embed and macOS/Linux/Windows packaging copies preserve those
generated files. Sidecars are gitignored and cannot be requested directly.

The real bundle test verifies every generated gzip file decompresses to its
corresponding final asset and is smaller. Sidecars are excluded from the
service-worker manifest; existing offline Wall coverage still passes.
Gateway tests cover quality negotiation, q=0/wildcards, HEAD lengths,
representation ETags/304, raw single/multipart ranges, identity-forbidden
encoded requests, unsupported encodings and map/dotfile/API exclusions.

## Executed verification

| Command | Actual result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Pass |
| `pnpm --dir web exec vitest run src/bundle.test.ts` | Two bundle/offline tests pass |
| `go test -race ./internal/gateway -count=1 && go vet ./internal/gateway` | Pass, including final range/identity behavior |
| `scripts/check` | Pass: generation drift, Go formatting/vet/tests, vendored modules, TypeScript, 1,048 phone tests and production build |
| `pnpm build:web && sh scripts/embed-web.sh && go test -tags webdist ./internal/gateway -run '^TestProductionAssetCompression$' -count=1 -v` | Pass; embedded TerminalView identity 690,359 bytes, gzip 132,274 bytes, decoded bytes identical |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | Pass; 29 pages, all links valid |

These are direct Go gateway transfer sizes, not a real mobile latency
measurement. External proxy and physical-phone/cellular gates remain
pending. Independent adversarial PR review, required hosted checks and merge
remain pending.

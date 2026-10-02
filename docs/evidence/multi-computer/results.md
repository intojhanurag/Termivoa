# Multi-computer end-to-end results

Run on 2026-10-02, macOS, base `origin/feat/multi-computer` 167eb63, harness `scripts/e2e-multicomputer/` (see its README). Headless Chrome 154.0.8037.93 at 390×844 with touch emulation, and Playwright WebKit 26.5. Two isolated dev stacks reached as `https://mac.tailx.ts.net` (home) and `https://vps.tailx.ts.net`, with a fake Tailscale status (mac and vps: `ada@example.com`; evil: `mallory@example.com`). Demo data only.

```sh
node scripts/e2e-multicomputer/phase-a.mjs --out docs/evidence/multi-computer/phase-a
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build --out docs/evidence/multi-computer/phase-b
PW=<playwright-core 1.61.1> WEBKIT=~/Library/Caches/ms-playwright/webkit-2272/pw_run.sh \
  node scripts/e2e-multicomputer/webkit.mjs --no-web-build --out docs/evidence/multi-computer/phase-b
```

| Part | Checks | PASS | FAIL | Table |
| --- | --- | --- | --- | --- |
| Phase A: no screens (CORS, Sec-Fetch, near-miss and `null` origins, bound grants, doctor) | 31 | 31 | 0 | [phase-a/phase-a.md](phase-a/phase-a.md) |
| Phase B: the phone flow in Chrome | 24 | 24 | 0 | [phase-b/phase-b.md](phase-b/phase-b.md) |
| Phase B core in WebKit | 4 | 4 | 0 | [phase-b/webkit.md](phase-b/webkit.md) |

Fixed during these runs (each found by this harness): the first settings read right after approval (A3-lag); wrapped add-computer links after a resize; the tap's own click closing the sheet (B2b-tap); an add that did not survive leaving the waiting page (B2b-back, B2b-same, now with Back and "Back to terminal"); the colour clash; the never-live Paused tile; re-adding vps after a node change (B7-readd).

## Timings (Chrome, loopback, one run)

| Step | ms |
| --- | --- |
| Pairing with home (link opened → Sessions) | 1154 |
| Add vps by paste (`pterm pair --via` → "vps is added", includes the harness steps) | 4008 |
| Sessions list load (navigation → rows from both computers) | 215 |
| Open a vps session → live prompt | 520 |
| Echo round-trip on vps (Enter → output painted) | 210 |
| Cold open with home offline (navigation → vps rows) | 220 |
| Add from a terminal link with Back, y in the same app (tap → "vps is added") | 3152 |
| Add from a terminal link with "Back to terminal" (tap → "vps is added") | 3059 |
| Node ID change → bound device revoked | 34144 |
| Re-add vps after the node change | 3998 |
| WebKit: open a vps session → live prompt | 420 |

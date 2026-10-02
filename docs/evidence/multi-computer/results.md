# Multi-computer end-to-end results

Run on 2026-10-02, macOS, base `origin/feat/multi-computer` 88742e3, harness `scripts/e2e-multicomputer/` (see its README). Headless Chrome 154.0.8037.93 at 390×844 with touch emulation, and Playwright WebKit 26.5. Two isolated dev stacks reached as `https://mac.tailx.ts.net` (home) and `https://vps.tailx.ts.net`, with a fake Tailscale status (mac and vps: `ada@example.com`; evil: `mallory@example.com`). Demo data only.

```sh
node scripts/e2e-multicomputer/phase-a.mjs --out docs/evidence/multi-computer/phase-a
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build --out docs/evidence/multi-computer/phase-b
PW=<playwright-core 1.61.1> WEBKIT=~/Library/Caches/ms-playwright/webkit-2272/pw_run.sh \
  node scripts/e2e-multicomputer/webkit.mjs --no-web-build --out docs/evidence/multi-computer/phase-b
```

| Part | Checks | PASS | FAIL | Table |
| --- | --- | --- | --- | --- |
| Phase A: no screens (CORS, Sec-Fetch, near-miss and `null` origins, bound grants, doctor) | 31 | 31 | 0 | [phase-a/phase-a.md](phase-a/phase-a.md) |
| Phase B: the phone flow in Chrome | 24 | 22 | 2 | [phase-b/phase-b.md](phase-b/phase-b.md) |
| Phase B core in WebKit | 4 | 4 | 0 | [phase-b/webkit.md](phase-b/webkit.md) |

Fixed since the first run: the first settings read right after approval (A3-lag, B2), wrapped-link detection after a resize (the link now underlines on hover), approval in the same app's terminal through in-app navigation (B2b-same), the colour clash, the never-live Paused tile, and re-adding vps after a node change (B7-readd).

## Failures

| Check | What happens | Where |
| --- | --- | --- |
| B2b-tap | A tap on the add-computer link in a terminal opens the sheet on mouseup, and the click that follows the same tap closes it again. Event log in the page: `mousedown:false`, `mouseup:false`, `click:true` (capture, sheet open), then sheet closed after the bubbling click. With `clickCount: 0` (no click event) the sheet stays open. A file path tap is not affected because Files is not a "sheet" panel. The terminal-area `onClick` runs `if (sheet) setPanel('none')` even when `linkTapped` is set. | `web/src/screens/Live.tsx:639` |
| B2b-back | On the waiting add page, the only way back to a terminal is Back. The add page is its own page load (`window.location.assign`), so Back loads `/settings` and the add is gone: no banner, the code page is lost, and vps stays "pair again". The add survives in-app navigation (B2b-same passes), but the page offers no in-app way out while it waits. An iPhone Home Screen app has no Back at all. | `web/src/screens/AddComputer.tsx:102`, `:177` |

## Timings (Chrome, loopback, one run)

| Step | ms |
| --- | --- |
| Pairing with home (link opened → Sessions) | 1164 |
| Add vps by paste (`pterm pair --via` → "vps is added", includes the harness steps) | 4024 |
| Sessions list load (navigation → rows from both computers) | 217 |
| Open a vps session → live prompt | 522 |
| Echo round-trip on vps (Enter → output painted) | 208 |
| Cold open with home offline (navigation → vps rows) | 221 |
| Add from a terminal, approved in the same app (link → "vps is added") | 7315 |
| Node ID change → bound device revoked | 24089 |
| Re-add vps after the node change | 3975 |
| WebKit: open a vps session → live prompt | 423 |

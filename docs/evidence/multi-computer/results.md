# Multi-computer end-to-end results

Run on 2026-10-02, macOS, base `origin/feat/multi-computer` 7ad31e7, harness `scripts/e2e-multicomputer/` (see its README). Headless Chrome 154.0.8037.93 at 390×844 with touch emulation, and Playwright WebKit 26.5. Two isolated dev stacks reached as `https://mac.tailx.ts.net` (home) and `https://vps.tailx.ts.net`, with a fake Tailscale status (mac and vps: `ada@example.com`; evil: `mallory@example.com`). Demo data only.

| Part | Checks | PASS | FAIL | Table |
| --- | --- | --- | --- | --- |
| Phase A: no screens (CORS, Sec-Fetch, near-miss and `null` origins, bound grants, doctor) | 31 | 31 | 0 | [phase-a/phase-a.md](phase-a/phase-a.md) |
| Phase B: the phone flow in Chrome | 23 | 21 | 2 | [phase-b/phase-b.md](phase-b/phase-b.md) |
| Phase B core in WebKit | 4 | 4 | 0 | [phase-b/webkit.md](phase-b/webkit.md) |

## Failures

| Check | What happens | Where |
| --- | --- | --- |
| B2b-tap | A tap on the add-computer link that `pterm pair --via` prints in a mac session does nothing. The link wraps over 3 rows at 46 columns. A tap on a file path in the same terminal does open Files, and `joinedRowLinks` finds the link when it gets 46-cell rows. Likely cause: `rowCellsAt` reads `IBufferLine.length` cells, which xterm documents may be more than `terminal.cols` after a resize (the session opens at 80 columns, then fits to 46). The trailing blank cells make `full()` false, so the rows are never joined. | `web/src/terminal/TerminalView.ts:137`, `web/src/terminal/pathLinks.ts:201` |
| B2b-same | The in-terminal path cannot finish when you approve with `y` in the same app's terminal, which is the "best" path in the design (`ssh vps` inside a Termivoa session). Going back to the terminal leaves `/computers/add`, which stops the poll (cancel on unmount). The link works only once. vps prints "Approved", but the phone never gets the credential, and vps stays "vps · pair again". Approving from another vps terminal (`pterm pair approve`) works (B2b). | `web/src/screens/AddComputer.tsx:91`, `:150`; `docs/multi-computer.md` section 3, step 2 |

## Observations (not failures)

- mac and vps get the same colour (`#4FA3FF`): `colorFor` hashes each origin into 8 colours on its own, so two computers clash 1 time in 8 (`web/src/computers.ts:45`). The label is always shown too.
- A Wall tile that was never live shows an empty Paused tile, not a last frame (b4-wall.png).
- In the first second after `pterm pair --via`, the peer refuses the preflight, because the gateway learns new pairing origins on its 1 s broker probe. A person never acts that fast.

## Timings (Chrome, loopback, one run)

| Step | ms |
| --- | --- |
| Pairing with home (link opened → Sessions) | 1124 |
| Add vps by paste (`pterm pair --via` → "vps is added", includes the harness steps) | 4643 |
| Sessions list load (navigation → rows from both computers) | 219 |
| Open a vps session → live prompt | 517 |
| Echo round-trip on vps (Enter → output painted) | 207 |
| Cold open with home offline (navigation → vps rows) | 217 |
| Node ID change → bound device revoked (whole seconds) | 24000 |
| WebKit: open a vps session → live prompt | see phase-b/webkit.md |

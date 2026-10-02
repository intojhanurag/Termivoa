| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| B1 | Pair the phone with mac (home) | PASS | code 118-487 matched, 1124 ms |
| B2-sheet | Confirm sheet shows the full origin, machine and owner | PASS | Add this computer? Close Address https://vps.tailx.ts.net Machine vps Owner ada@example.com Add it only if you just ran pterm pair --via on vps. Next, this phone shows a code. Check it on vps. Add vps Cancel |
| B2-code | Same code on the app and on the vps CLI | PASS | app 023-070, CLI 023-070 |
| B2 | Add vps by paste ends with "vps is added" | PASS | outcome: added |
| B3-list | Sessions list shows sessions from both computers with labels and chips All/mac/vps | PASS | chips ["All","mac","vps"]; rows [{"title":"mac-1","meta":"mac · demo · zsh (login) · now"},{"title":"vps-1","meta":"vps · demo · zsh (login) · now"}] |
| B3-echo | Open a vps session from the mac app and echo round-trip | PASS | /c/vps.tailx.ts.net/sessions/9bea52d3-58c0-413e-9e19-271ade25bfb0; output e2e-2004-42 |
| B3-new | Start a new session on vps from the mac app ("Runs on vps") | PASS | new session a8cb3713-f289-44e7-a493-936a5b57d75d is in vps pterm list |
| B8-shell | The cached app opens while home is offline | PASS | service workers ["https://mac.tailx.ts.net/sw.js"]; load 12 ms; problems [] |
| B8-vps | vps sessions still listed and reachable with home offline | PASS | [{"title":"demo","meta":"vps · demo · zsh (login) · now · No one"},{"title":"vps-1","meta":"vps · demo · zsh (login) · now"}] |
| B8-wall | An unvisited Wall opens from the cache with home offline | PASS | tiles ["demo","vps-1"] |
| B4-both | Wall shows tiles from both computers | PASS | mac-4@mac, mac-3@mac, mac-2@mac (Paused), vps-2@vps, demo@vps, vps-1@vps (Paused) |
| B4-paused | With more than 4 sessions, tiles beyond 4 show Paused | PASS | 6 tiles, 2 Paused |
| B4-thumb | Thumbnails stay correct: a live tile follows its session, a paused tile stays still | PASS | vps-2 live: tick 5 → 8 in 3 s; vps-1 Paused |
| B5-down | With vps stopped, mac sessions work and "vps · can't reach" shows | PASS | ["vps · can't reach Retry"]; mac echo ok |
| B5-up | After vps restarts, Retry recovers it | PASS | problems [] |
| B6-stream | The open vps stream closes on revoke --via (Live leaves the session) | PASS | path now /; revoke said: Revoke browser access for 1 device(s) that use this computer from the Termivoa app at https://mac.tailx.ts.net? Their live connections close now. [y/N] Revoked 1 device(s) bound to https://mac.tailx.ts.net; closed 1 live connection(s). |
| B6-pair | "vps · pair again" shows for vps only; mac sessions still listed | PASS | ["vps · pair again · Run pterm pair --via mac on vps Computers"]; mac rows 4; first vps answer: GET /api/v1/settings 401 ACAO=https://mac.tailx.ts.net Set-Cookie pterm_dev_session cleared; Path=/; Max-Age=0; HttpOnly; SameSite=Strict |
| B6-reload | "vps · pair again" still shows after the app reloads | PASS | ["vps · pair again · Run pterm pair --via mac on vps Computers"]; vps answers: GET /api/v1/settings cookie=false 401 ACAO=https://mac.tailx.ts.net |
| B2b-tap | Tapping the add-computer link in the terminal opens the confirm sheet | FAIL | a tap on the wrapped link (46 columns) did nothing; a tap on a file path in the same terminal opens Files. Continued by pasting the link |
| B2b-same | Approved by typing y in the same app's terminal: vps is usable afterwards | FAIL | problems ["vps · pair again · Run pterm pair --via mac on vps Computers"]; vps rows 0; vps devices: no active device for mac |
| B2b | Terminal link, approved on vps, ends with "vps is added" | PASS | code 649-228; outcome: added |
| B7-direct | A device paired directly with vps cannot be driven from the mac app | PASS | own page 200; from mac origin: settings 400, sessions 400, WebSocket error; GET /api/v1/settings 400, GET /api/v1/sessions 400, GET /api/v1/ws 400 |
| B7-node | Changing mac's node ID on vps revokes the bound devices | PASS | refused (TypeError: Failed to fetch) 24 s and revoked 54 s after the change; before: 4b958e68-9832-4ea1-a977-f1fcae7f08cf "Demo phone" active 2026-10-02T10:07:40.748Z https://mac.tailx.ts.net / 1a7f867c-2117-47de-96c2-cc7f502321a1 "Demo phone" revoked 2026-10-02T10:06:40.645Z https://mac.tailx.ts.net; after: 4b958e68-9832-4ea1-a977-f1fcae7f08cf "Demo phone" revoked 2026-10-02T10:07:40.748Z https://mac.tailx.ts.net / 1a7f867c-2117-47de-96c2-cc7f502321a1 "Demo phone" revoked 2026-10-02T10:06:40.645Z https://mac.tailx.ts.net; app: ["vps · can't reach Retry"] |

| Timing | ms |
| --- | --- |
| pairing with home (link opened → Sessions) | 1124 |
| add computer by paste (pterm pair --via → outcome) | 4643 |
| Sessions list load (navigation → rows from both computers) | 219 |
| open vps session → live prompt | 517 |
| echo round-trip on vps (Enter → output painted) | 207 |
| cold open with home offline (navigation → vps rows) | 217 |
| vps recovery (Retry → no problem row) | 1 |
| add through the terminal, approved in the same app (link → list) | 12203 |
| add from the terminal link, approved on vps (link → outcome) | 7027 |
| node ID change → bound device revoked (whole seconds) | 24000 |

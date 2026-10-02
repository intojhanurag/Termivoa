| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| B1 | Pair the phone with mac (home) | PASS | code 284-743 matched, 1164 ms |
| B2-sheet | Confirm sheet shows the full origin, machine and owner | PASS | Add this computer? Close Address https://vps.tailx.ts.net Machine vps Owner ada@example.com Add it only if you just ran pterm pair --via on vps. Next, this phone shows a code. Check it on vps. Add vps Cancel |
| B2-code | Same code on the app and on the vps CLI | PASS | app 138-502, CLI 138-502 |
| B2 | Add vps by paste ends with "vps is added" | PASS | outcome: added |
| B3-list | Sessions list shows sessions from both computers with labels and chips All/mac/vps | PASS | chips ["All","mac","vps"]; rows [{"title":"mac-1","meta":"mac · demo · zsh (login) · now"},{"title":"vps-1","meta":"vps · demo · zsh (login) · now"}] |
| B3-echo | Open a vps session from the mac app and echo round-trip | PASS | /c/vps.tailx.ts.net/sessions/4a5a4bd3-4be8-435b-8784-d384da5f8907; output e2e-52405-42 |
| B3-new | Start a new session on vps from the mac app ("Runs on vps") | PASS | new session 8f9bcae9-11cd-4e20-96ea-40d2095ec4d0 is in vps pterm list |
| B8-shell | The cached app opens while home is offline | PASS | service workers ["https://mac.tailx.ts.net/sw.js"]; load 13 ms; problems [] |
| B8-vps | vps sessions still listed and reachable with home offline | PASS | [{"title":"demo","meta":"vps · demo · zsh (login) · now · No one"},{"title":"vps-1","meta":"vps · demo · zsh (login) · now"}] |
| B8-wall | An unvisited Wall opens from the cache with home offline | PASS | tiles ["demo","vps-1"] |
| B4-both | Wall shows tiles from both computers | PASS | mac-4@mac (Paused), mac-3@mac (Paused), mac-2@mac, vps-2@vps, demo@vps, vps-1@vps |
| B4-paused | With more than 4 sessions, tiles beyond 4 show Paused | PASS | 6 tiles, 2 Paused |
| B4-thumb | Thumbnails stay correct: a live tile follows its session, a paused tile stays still | PASS | vps-2 live: tick 5 → 8 in 3 s; vps-1 shows e2e-52405-42: true |
| B5-down | With vps stopped, mac sessions work and "vps · can't reach" shows | PASS | ["vps · can't reach Retry"]; mac echo ok |
| B5-up | After vps restarts, Retry recovers it | PASS | problems [] |
| B6-stream | The open vps stream closes on revoke --via (Live leaves the session) | PASS | path now /; revoke said: Revoke browser access for 1 device(s) that use this computer from the Termivoa app at https://mac.tailx.ts.net? Their live connections close now. [y/N] Revoked 1 device(s) bound to https://mac.tailx.ts.net; closed 1 live connection(s). |
| B6-pair | "vps · pair again" shows for vps only; mac sessions still listed | PASS | ["vps · pair again · Run pterm pair --via mac on vps Computers"]; mac rows 4; first vps answer: GET /api/v1/settings 401 ACAO=https://mac.tailx.ts.net Set-Cookie pterm_dev_session cleared; Path=/; Max-Age=0; HttpOnly; SameSite=Strict |
| B6-reload | "vps · pair again" still shows after the app reloads | PASS | ["vps · pair again · Run pterm pair --via mac on vps Computers"]; vps answers: GET /api/v1/settings cookie=false 401 ACAO=https://mac.tailx.ts.net |
| B2b-tap | Tapping the add-computer link in the terminal opens the confirm sheet | FAIL | the sheet opens on mouseup, then the click that follows the tap closes it again; continued by pasting the link |
| B2b-back | After Back from the waiting add page, the add keeps running (banner "Adding vps… approve on vps") | FAIL | Back went to /settings (the add page was a separate page load); banner: none; code was 281-508; terminal /sessions/1446dcc4-4174-406d-8542-217effe2f3a5 |
| B2b-same | Approved by typing y in the same app's terminal (in-app navigation): vps is added and usable | PASS | banner while waiting: Adding vps… approve on vps Open Cancel; after y: vps is added Dismiss; problems []; vps rows 4 |
| B7-direct | A device paired directly with vps cannot be driven from the mac app | PASS | own page 200; from mac origin: settings 400, sessions 400, WebSocket error; GET /api/v1/settings 400, GET /api/v1/sessions 400, GET /api/v1/ws 400 |
| B7-node | Changing mac's node ID on vps revokes the bound devices | PASS | refused (401) 24 s and revoked 24 s after the change; before: 85e5a28f-0762-4730-8d69-a37ac06d699a "Demo phone" active 2026-10-02T10:35:07.222Z https://mac.tailx.ts.net / c2d6599b-d6e9-4aff-a4fa-74aefdbdc3c1 "Demo phone" revoked 2026-10-02T10:34:11.022Z https://mac.tailx.ts.net; after: 85e5a28f-0762-4730-8d69-a37ac06d699a "Demo phone" revoked 2026-10-02T10:35:07.222Z https://mac.tailx.ts.net / c2d6599b-d6e9-4aff-a4fa-74aefdbdc3c1 "Demo phone" revoked 2026-10-02T10:34:11.022Z https://mac.tailx.ts.net; app: ["vps · pair again · Run pterm pair --via mac on vps Computers"] |
| B7-readd | Re-adding vps after the node ID change works (new node pinned, sessions back) | PASS | outcome added; problems []; vps rows 4; active bound devices on vps 1 |

| Timing | ms |
| --- | --- |
| pairing with home (link opened → Sessions) | 1164 |
| add computer by paste (pterm pair --via → outcome) | 4024 |
| Sessions list load (navigation → rows from both computers) | 217 |
| open vps session → live prompt | 522 |
| echo round-trip on vps (Enter → output painted) | 208 |
| cold open with home offline (navigation → vps rows) | 221 |
| vps recovery (Retry → no problem row) | 1 |
| add from the terminal, approved in the same app (link → "vps is added") | 7315 |
| node ID change → bound device revoked | 24089 |
| re-add vps after a node change (pterm pair --via → outcome) | 3975 |

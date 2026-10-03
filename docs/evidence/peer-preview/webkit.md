WebKit 26.5

| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| W1 | WebKit: pair with mac (home) | PASS | code 289-964, 2924 ms |
| W2 | WebKit: add vps by paste ends with "vps is added" | PASS | code 536-624; added; 3923 ms |
| W3 | WebKit: the Sessions list shows the vps session with its label | PASS | create 200; chips All,mac,vps |
| W4 | WebKit: open a vps session and echo round-trip | PASS | /c/vps.tailx.ts.net/sessions/aa6a6c44-307a-4781-a18b-cb4dd6076589; open-to-live 420 ms |
| W5 | WebKit: Preview ready, Open, the vps dev site in the frame (sub-resource, fetch, WebSocket) under "vps · untrusted page" | PASS | bar: ‹ terminal localhost:55688 ↻ vps · untrusted page ; select 200 referer=https://mac.tailx.ts.net/ site=same-site cookie=true; / 200 site=same-origin; frame: dev site on vps load 1 sub ok fetch pong ws echo |
| W6 | WebKit: ↻ loads the preview again | PASS | dev site load 1 → 2 |
| W7 | WebKit: evil.tailx.ts.net framing the vps select address is refused | PASS | 403 referer=https://evil.tailx.ts.net/ site=same-site cookie=true; the frame shows: Open the preview from the Termivoa app. |

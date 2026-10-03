WebKit 26.5

| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| W1 | WebKit: pair with mac (home) | PASS | code 790-409, 2481 ms |
| W2 | WebKit: add vps by paste ends with "vps is added" | PASS | code 572-474; added; 3527 ms |
| W3 | WebKit: the Sessions list shows the vps session with its label | PASS | create 200; chips All,mac,vps |
| W4 | WebKit: open a vps session and echo round-trip | PASS | /c/vps.tailx.ts.net/sessions/248475ab-e8f9-4b02-a42a-489d7f40e124; open-to-live 427 ms |
| W5 | WebKit: Preview ready, Open, the vps dev site in the frame (sub-resource, fetch, WebSocket) under "vps · untrusted page" | PASS | bar: ‹ terminal localhost:49569 ↻ vps · untrusted page ; select 200 referer=https://mac.tailx.ts.net/ site=same-site cookie=true; / 200 site=same-origin; frame: dev site on vps load 1 sub ok fetch pong ws echo |
| W6 | WebKit: ↻ loads the preview again | PASS | dev site load 1 → 2 |
| W7 | WebKit: evil.tailx.ts.net framing the vps select address is refused | PASS | 403 referer=https://evil.tailx.ts.net/ site=same-site cookie=true; the frame shows: Open the preview from the Termivoa app. |
| W8 | WebKit: Found on your network: vps — Add → "Add this computer?" → code; vps's prompt names THIS computer and the code; y → "vps is added" | PASS | remove 204; Sent to mac (https://mac.tailx.ts.net).; code 929-187/929-187; phone says "type y in the terminal on vps": true; added; 3262 ms |

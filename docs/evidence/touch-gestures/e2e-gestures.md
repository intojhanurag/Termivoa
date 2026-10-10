WebKit 26.4

| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| GE1 | Hold and slide: the pill shows, three ← go out, the click is taken, the pill goes | PASS | Event with touch lists; step 10.0 px; pill "← → ↑ ↓"; keys arrow-left arrow-left arrow-left; touchend prevented true; output 12X345: ok |
| GE2 | Two-finger tap sends exactly one Esc (cat -v shows ^[) | PASS | keys escape enter; ^[ shown true; touchend prevented true |
| GE3 | Pinch: a live "20 px" pill, the size set on lift, exactly one resize | PASS | font 14 → 20; pill "20 px"; terminal.resize messages 1 |
| GE4 | Tap to place: a tap on "a" moves the cursor there; X makes Xabc | PASS | live keyboard; tap at 93,173; cell 13.0 px wide; keys arrow-left arrow-left arrow-left; output Xabc: ok |
| GE5 | A tap on dim text right of the cursor sends nothing; a tap on the text to its left does | PASS | on the dim "d": keys []; on "b": keys [arrow-left] |
| GE8 | A tap on a line without a shell prompt (Continue? [y/N]) sends nothing | PASS | keys [] |
| GE6 | A swipe right on the status line opens the session before (the tab to the left) | PASS | tabs tab/tab active; /sessions/ec2bb331-60ca-43e5-9999-5e9e99143458 → /sessions/9f38e00f-f8d9-426d-bc40-77bf709db6a5 |
| GE7 | Switch off: no pill, no keys, no text size change, no switch | PASS | switch true → false (stored off); pill null; keys []; font 20 → 20; path /sessions/ec2bb331-60ca-43e5-9999-5e9e99143458 → /sessions/ec2bb331-60ca-43e5-9999-5e9e99143458 |

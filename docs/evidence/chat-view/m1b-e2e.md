Chromium 151.0.7922.34, real host and phone, scripts/fakeagent

Latency (agent write to phone screen): 40 turns: p50 62 ms, p95 84 ms, min 35 ms, max 88 ms

| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| CH1 | No switch while Chat is off; on, the real host links Chat and shows the turns | PASS | switch at the prompt 0; chat messages chat.open, chat.read; bubbles 2 |
| CH2 | Live turns arrive by chat.changed and chat.read | PASS | 41 chat.read |
| CH3 | Write to phone: p50 ≤ 400 ms, p95 ≤ 750 ms | PASS | 40 turns: p50 62 ms, p95 84 ms, min 35 ms, max 88 ms |
| CH4 | Real blocks render (Ran, Edit app.go, markdown) and the list stays at the bottom | PASS | Ran true; app.go true; bold 5; 0 px from the bottom |
| CH5 | Edit hunk, then Open in Changes shows the file | PASS | banner "" |
| CH6 | Nothing wider than 390 px | PASS | none |
| CH7 | Not available: the host reason, Why?, Show terminal, then no switch | PASS | Termivoa can’t read this agent’s conversation, so it shows nothing rather than a guess. The terminal has everything. \| Chat works today with Claude Code on macOS and Linux. This session runs another agent, or no agent Termivoa can read. |
| CH8 | Leaving Chat sends chat.close | PASS | chat.open, chat.close |
| CH9 | Settings turns Chat off on the computer | PASS | Chat view: off (turn it on with pterm chat on) |

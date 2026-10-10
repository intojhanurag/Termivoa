Chromium 151.0.7922.34 (fake host chat)

| ID | Check | Result | Evidence |
| --- | --- | --- | --- |
| CH1 | No switch at a plain prompt; with an agent it shows, and Chat opens with one chat.open | PASS | switch at the prompt 0; bubbles 1; chat messages: chat.open |
| CH2 | chat.changed pulls one chat.read after, and the block shows | PASS | reads 1 |
| CH3 | Edit row shows its hunk; Open in Changes opens Changes, with no refusal | PASS | hunk shown; changes true; banner "" |
| CH4 | Link asks first and shows the full address | PASS | https://example.com/tax |
| CH5 | Sticky at the bottom; N new ↓ shows while scrolled up | PASS | 0 px from the bottom after 20 new; pill "2 new ↓" |
| CH6 | Nothing wider than 390 px | PASS | none |
| CH7 | Not available: Why? explains, Show terminal returns | PASS | After /clear the agent writes a new conversation file. Termivoa links a file only when it is sure. Use the terminal for this conversation. |
| CH8 | Leaving Chat sends chat.close | PASS | closes 3 |

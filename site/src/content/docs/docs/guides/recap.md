---
title: While you were away
description: When you open a session, a short card tells you what the agent did while your phone was away.
---

This page helps you see, at a glance, what happened in a session while you were away.

## What the card shows

When you open a session, a small card can show at the top of the terminal. For example:

**Since you left 32 min ago · worked 14 min · 5 files changed +120 −34 · waiting for you**

The card shows only when one of these is true:

- the agent worked for one minute or more while this phone was away;
- the agent waits for you now, or it finished and it is your turn.

Each part comes from facts that Termivoa knows:

- **Since you left**: the last time that this phone showed the session.
- **worked**: the time that the agent was busy, from your computer.
- **files changed**: the uncommitted changes in the session's git project.
- **waiting for you** or **your turn**: the agent's state.

If a fact is not known, the card leaves that part out. For example, a folder that is not a git project shows no file count. Termivoa never reads the terminal text to write the card.

## Use the card

1. Open the session.
2. Read the card.
3. Tap **See changes** to open [Changes](/docs/guides/changes/). Or tap **✕** to close the card.

The card also closes when you tap somewhere else, or after 12 seconds. It shows one time for each visit.

Each phone keeps its own "last visit". A second phone gets its own card.

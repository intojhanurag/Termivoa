---
title: Sessions
description: Start, read, rename and close terminal sessions from the Sessions list on your phone.
---

This page helps you start, follow and close sessions from your phone.

## What a session is

A session is one terminal on your computer, such as a shell or a coding agent.
It runs on your computer, not on the phone. It keeps running when you close the app or lose signal.

## The Sessions list

The Sessions list is the first screen after pairing. Each row shows:

- the session name;
- the project and the shell, for example "Home · zsh";
- the last line of output;
- a dot when there is new output.

Sessions that wait for you are at the top.

## Start a session

1. Tap **New session**.
2. Pick a project. A project is a folder where new sessions start.
3. Pick a shell.
4. Optional: in **run first**, type one command, such as `claude` or `codex`.
5. Tap **start session**.

The **run first** command is typed once, with Enter, when the session opens. It is typed only if no one else has control.

To reuse the same choices, tap **save as preset**. The preset shows under **Launch**, and one tap starts it.

To add more projects, run this on your computer:

```sh
pterm project add <folder>
```

## Status badges

| Badge | Meaning |
| --- | --- |
| **Working** | The program is busy, or it printed output in the last few seconds. |
| **Waiting** | A coding agent or a `[y/N]` prompt waits for your answer. The row shows the question. |
| **Your turn** | An agent finished its work and you have not looked yet. |
| **Exited** | The shell ended. The exit code shows when there is one. |

A plain shell that waits at its prompt shows no badge.
Waiting sessions can also [notify your phone](/docs/guides/notifications/).

## Live folder name

When you `cd` to another folder, the session name changes to that folder name.
The live terminal also shows the full path, for example `~/src/app`.
The row still shows the project on its second line.

If you rename a session, it keeps your name.

## Rename a session

1. Open the session.
2. Tap the session menu (the button next to the tabs).
3. Tap **Rename…**, type a new title and tap **Save title**.

## Close a session

1. Open the session.
2. Tap the session menu.
3. Tap **Terminate…**.
4. Tap **Terminate session**. Tap **Keep running** to cancel.

This ends the program and everything it started.

On your computer, you can also run:

```sh
pterm terminate <session-id>
```

Get the session ID from `pterm list`.

## When a session is lost

A session ends if your computer restarts, you log out, or the broker (the background process that owns sessions) crashes.
The phone then says "This session is gone". A lost session cannot come back. Tap **New session** to start again.
Termivoa never re-runs old commands on its own.

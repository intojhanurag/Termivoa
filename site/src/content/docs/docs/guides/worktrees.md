---
title: Worktrees
description: Termivoa worktrees give each new task its own folder and branch from your phone, so several coding agents can work in one repository at once.
lastUpdated: 2026-10-10
---

Termivoa can start a new task in a git worktree: a second folder of your repository on its own branch, with a new session in it. Two agents in two worktrees do not change each other's files. All worktrees share one history, so you commit and push as usual.

![main stays untouched while two worktrees branch off it: brave-otter, where Claude fixes the cache headers, and clever-marten, where Codex writes the tests, each in its own folder under …/worktrees/acme-web; a hand-written note says: one task, one branch. nobody steps on anybody's files.](../../../../assets/docs/illo-worktrees.webp)

## Make a worktree

1. In a running session, `cd` into your repository, such as `~/code/myapp`.
2. Open the session menu (**⋯**), then tap **New worktree…**.
3. Check the fields:
   - **name**: random at first, such as `brave-otter`. Tap 🎲 for another. Use lowercase letters, digits and single hyphens, up to 40 characters.
   - **from**: the branch to start from. The default is the repository's default branch, such as `main`. You can pick any local or remote branch.
   - **start with**: **Shell**, **Claude** or **Codex**.
4. Tap **Create**.

Termivoa makes the folder `~/termivoa/worktrees/<repository>/<name>` and the branch `termivoa/<name>` from the branch you picked. A new session, named after the worktree, opens in that folder as a new tab.

The new branch does not track a remote. A push from [Changes](/docs/guides/changes/#commit-and-push) sends it to `origin` as a new branch, ready for a pull request.

![The New worktree sheet shows the name fix-cache-headers, the base branch main, Shell as the start command, and the folder and branch it creates.](../../../../assets/docs/phone-worktree-sheet.webp)

:::tip
Start from `main` for a new task. To continue another task, pick its branch in **from**.
:::

## What happens if creation times out?

Termivoa stops git's ordinary hook children before cleaning up
a failed creation. This prevents a stopped hook from continuing to change the
new worktree while it is removed. Check the sheet's error and your worktrees
before trying again. Hooks are trusted programs on your computer; deliberately
detached programs on macOS or Linux are outside this cleanup.

## Can I use another session while Create waits?

Termivoa keeps the Sessions list and other session opens
available while a slow worktree checkout or hook finishes. Creating the
worktree still needs the computer to finish its own git operation.

| What happens | What to do |
| --- | --- |
| **Create** takes time | Open another session while it finishes. |
| **Termivoa reached a limit on this computer.** | Wait for pending actions to finish. If this repository has 50 worktrees, remove an unused one; if eight sessions are running, close an unused session. |
| A creation times out or the connection breaks | Check Sessions before creating again; the computer may have completed it. |

## See your worktrees

A worktree session shows a **⎇ name** badge on the Sessions list and in the line above the terminal. The folders in `~/termivoa/worktrees` open in your editor like any other folder.

## Remove a worktree

Remove a worktree when its task is done and pushed.

1. Open the worktree's session and take control.
2. Open the session menu (**⋯**), tap **Remove worktree…**, then **Remove worktree**.

Termivoa deletes the folder and ends the session. The branch `termivoa/<name>` stays, so no commit is lost.

If the folder has uncommitted changes, new files, or files that git ignores (such as `node_modules` or `.env`), Termivoa asks first. **Remove anyway** deletes them for good. **Keep it** stops.

## What are the limits?

| Limit | What to do |
| --- | --- |
| 50 Termivoa worktrees per repository | Remove a finished worktree to make another. |
| One worktree creation at a time per computer | Wait for the current create to finish, then tap **Create** again. Extra requests receive a limit message immediately. |

Closing the sheet or app after a create started can leave the computer
finishing it. Check Sessions for that worktree before creating it again.

## Common questions

### Why can I not make a worktree?

**New worktree…** shows only for a running session. Its folder must also be in a git repository. If not, the sheet says so: `cd` into the repository first.

### Why does Create say the name already exists?

A name can be used once per repository. The folder or the branch `termivoa/<name>` already exists, so pick another name or tap 🎲.
